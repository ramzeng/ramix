# Server Statistics Snapshot Design

## Summary

Ramix has explicit server lifecycle management, bounded worker queues, transport-level
connection handling, and graceful shutdown, but applications cannot inspect basic
runtime behavior without maintaining their own counters. This change adds a
zero-dependency statistics snapshot API for production monitoring.

The API reports aggregate and per-transport values for TCP and WebSocket. It includes
connection and queue gauges, message and byte counters, task rejection and connection
error counters, and basic request processing duration totals. Statistics exist for the
entire lifetime of a `Server`: they are readable before startup, while running, and
after shutdown, and they cannot be reset.

Snapshots favor low overhead over transactional consistency. Every field is read
atomically and is individually valid, but fields in one snapshot may represent
slightly different instants when the server is active.

## Goals

- Expose enough runtime state to answer basic production questions without adding an
  exporter or third-party dependency.
- Report both an all-transport total and separate TCP and WebSocket statistics.
- Keep updates non-blocking and safe under concurrent connection, worker, send, and
  snapshot activity.
- Preserve statistics after the server stops.
- Give applications stable cumulative values from which they can calculate rates and
  average request duration.
- Keep the public API independent of worker pool internals.

## Non-Goals

- Prometheus, OpenTelemetry, or another monitoring backend.
- Event subscriptions or a general event bus.
- Histograms, percentiles, configurable duration buckets, or sampling.
- Per-route, per-connection, or per-worker statistics.
- A statistics reset operation.
- Persisting statistics outside the `Server` process.
- Changing overload policy, connection policy, routing behavior, protocol framing, or
  shutdown behavior.

## Public API

The package adds two exported snapshot types and one server method:

```go
type ServerStats struct {
    Total     TransportStats
    TCP       TransportStats
    WebSocket TransportStats
}

type TransportStats struct {
    ActiveConnections uint64
    QueuedTasks        uint64

    ReceivedMessages uint64
    ReceivedBytes    uint64
    SentMessages     uint64
    SentBytes        uint64
    RejectedTasks    uint64
    ConnectionErrors uint64

    CompletedRequests      uint64
    TotalRequestDuration   time.Duration
    MaximumRequestDuration time.Duration
}

func (s *Server) Stats() ServerStats
```

`Stats` returns a value snapshot and never returns an error. It is valid immediately
after `NewServer` succeeds, throughout `Run`, and after `Run` and `Shutdown` complete.
Calling it does not mutate the server. Modifying the returned value cannot affect
future snapshots.

All counters start at zero when `NewServer` creates the server. A server cannot be
restarted, and the counters are never reset. The API does not promise that statistics
from two distinct `Server` instances can be combined without application-level
identity or timestamps.

## Snapshot Semantics

`ServerStats.TCP` and `ServerStats.WebSocket` are independently loaded from private
atomic counters. `ServerStats.Total` is calculated by combining those two snapshots;
it is not maintained as a third update target.

Each field is individually atomic and internally valid. The complete snapshot is not
a transactional view: concurrent activity may occur between field loads or between
the TCP and WebSocket loads. Consequently, relationships such as total received
messages equaling completed plus queued work are not guaranteed at an arbitrary
instant. This is an explicit low-overhead contract.

When combining TCP and WebSocket values into `Total`, counters, gauges, and
`TotalRequestDuration` use saturated addition. Unsigned values saturate at
`math.MaxUint64`, and the duration total saturates at the largest positive
`time.Duration`. `Total.MaximumRequestDuration` is the larger of the TCP and WebSocket
maximums; maximum durations are never added. Values never wrap around to zero or
become negative.

## Metric Definitions

### Connections

`ActiveConnections` is a gauge. It increases after a transport connection has been
accepted, successfully wrapped as a Ramix connection, and added to the connection
manager. It decreases when the connection supervisor removes that connection from
the manager during finalization.

The gauge includes connections whose open hook is executing and connections draining
during shutdown until they leave the manager. Failed upgrades, rejected connections,
and sockets that fail before Ramix registers a connection are not counted.

### Queued and Rejected Tasks

`QueuedTasks` is the number of successfully accepted request contexts waiting in a
worker channel. It increases as part of successful queue submission and decreases when
a worker receives the task, before the handler chain begins. A task currently running
is therefore not queued.

Queue accounting must increase before the task can become receivable from the worker
channel. If the non-blocking channel submission fails, submission rolls that increase
back before returning `ErrWorkerQueueFull`. A worker decreases the gauge immediately
after receiving the task. This ordering prevents a worker from decrementing an
unaccounted task and avoids unsigned underflow; the approximate snapshot may briefly
include an in-progress submission.

`RejectedTasks` increases only when submission fails with `ErrWorkerQueueFull`. A task
rejected because the server is stopping is not an overload rejection and is not
counted here. Each rejected request increments the transport associated with its
connection exactly once.

### Received Messages and Bytes

`ReceivedMessages` increases once after a complete frame has passed frame decoding and
the fixed Ramix message decoder has successfully validated and decoded it. It is
counted before worker submission, so a valid message later rejected by a full worker
queue is still a received message.

`ReceivedBytes` increases by the decoded message body length at the same point. It
does not include the fixed eight-byte Ramix event and length header, TCP framing
fragments, WebSocket framing, TLS overhead, or malformed and partial input.

Multiple valid Ramix messages returned by one successful frame-decoder call are
counted separately. If a frame-decoder call encounters malformed data, its current
behavior returns an error without returning any frames from that call; consequently,
none of the otherwise valid frames preceding the malformed data in that same call are
counted. Messages returned by earlier successful decoder calls remain counted.

### Sent Messages and Bytes

`SentMessages` increases only after the connection writer successfully writes one
complete encoded Ramix message to the underlying TCP or WebSocket transport.

`SentBytes` increases by that message's body length and excludes the eight-byte Ramix
header and transport overhead. A message accepted by `Connection.Send` but discarded
during forced shutdown is not sent. A failed or partial transport write does not
increase either sent field.

The writer already receives one encoded Ramix message per queue item. It may derive
the body length from the known encoded message length after validating that the item
contains at least the fixed header; metrics must not introduce a second body copy.

### Connection Errors

`ConnectionErrors` increases whenever `reportConnectionError` receives a non-nil error
for a registered connection. This includes read, write, protocol, task, open-hook, and
close-hook operations. It counts reported error occurrences rather than unique
connections: one connection may contribute more than one error if distinct failures
are reported during its lifecycle.

Whether an application installed `OnConnectionError` does not change counting. A
panic in the application error callback does not add another connection error because
the callback panic is only written to debug output by existing behavior.

### Completed Requests and Duration

Request timing begins after a worker receives a non-canceled task and immediately
before it enters the middleware and route handler chain. `QueuedTasks` has already
been decremented at this point.

`CompletedRequests` increases after that handler chain returns normally.
`TotalRequestDuration` increases by the elapsed wall-clock duration for the same
request, and `MaximumRequestDuration` records the largest completed duration observed.
Tasks canceled before execution and messages rejected before execution do not affect
these fields.

The duration total uses saturating addition. The maximum is updated with a compare-and-
swap loop and never decreases. External monitoring can calculate mean processing time
as `TotalRequestDuration / CompletedRequests` when the request count is non-zero.

This timing covers middleware and the selected route or built-in not-found handler. It
does not include socket reads, frame decoding, queue wait time, or asynchronous socket
write completion after `Connection.Send` returns.

## Internal Architecture

`Server` owns one private `serverMetrics` value created during `NewServer`. It contains
two `transportMetrics` groups, one for TCP and one for WebSocket. Each group contains
private atomic unsigned integer storage for all fields. Durations are stored as
non-negative nanoseconds and converted to `time.Duration` only while building a
snapshot.

`netConnection` stores its existing transport identity as a private `Transport` field.
TCP and WebSocket connection construction supply the appropriate value. This does not
change the exported `Connection` interface. Internal request and worker accounting
obtain transport identity from the Ramix-owned connection through a private interface
or the concrete base connection.

The metrics component exposes narrow internal operations rather than its atomic fields,
for example:

```go
connectionOpened(transport Transport)
connectionClosed(transport Transport)
messageReceived(transport Transport, bodyBytes uint64)
messageSent(transport Transport, bodyBytes uint64)
taskQueued(transport Transport)
taskDequeued(transport Transport)
taskRejected(transport Transport)
connectionError(transport Transport)
requestCompleted(transport Transport, duration time.Duration)
snapshot() ServerStats
```

Unknown transport values are an internal programming error. Metric update helpers must
ignore them rather than panic or alter request processing. All Ramix-created network
connections have a known transport, so this fallback exists only to keep observability
incapable of disrupting runtime behavior.

## Data Flow

### Connection Lifecycle

1. TCP accept or WebSocket upgrade succeeds.
2. Ramix creates the concrete connection with its transport identity.
3. The connection manager registers it.
4. The transport's active connection gauge increases.
5. Existing open, read, write, heartbeat, and shutdown behavior continues.
6. The supervisor removes the connection from the manager exactly once.
7. The transport's active connection gauge decreases exactly once.

### Incoming Request

1. The transport obtains a complete frame.
2. `Decoder.Decode` validates and returns a `Message`.
3. Received message and body byte counters increase.
4. `handleRequest` creates a Ramix `Context` and submits it to the worker pool.
5. Successful submission increases the queued task gauge.
6. Queue-full submission increases rejected tasks and follows the existing connection
   error and close behavior.
7. A worker receives the task and decreases the queued task gauge.
8. If the task is already canceled, existing behavior skips execution and no request
   completion is recorded.
9. Otherwise, timing starts, the handler chain runs, and normal return records request
   completion and duration.

### Outgoing Message

1. `Connection.Send` encodes and enqueues one message using existing behavior.
2. The connection writer removes the encoded message from its outgoing queue.
3. The transport write succeeds completely.
4. Sent message and body byte counters increase.
5. Write failure follows existing error and connection-close behavior without counting
   the message as sent.

## Concurrency and Overflow

Metric updates use `sync/atomic` and do not take server lifecycle, connection manager,
worker pool, or connection locks. `Stats` does not block runtime work.

Monotonic counters use saturating compare-and-swap addition. Gauges use balanced
increment and decrement operations with an underflow guard, preserving zero if an
internal lifecycle bug attempts an extra decrement. Duration input is clamped to the
non-negative `time.Duration` range before accumulation. Saturation is effectively an
exceptional lifetime condition but makes long-running behavior deterministic.

Observability must never change an existing control-flow result. Metric helpers do not
return errors, allocate per event, invoke user callbacks, log, or panic.

## Documentation

README documentation will add a short example showing that callers may poll
`server.Stats()` and export the result through their own logging or monitoring stack.
Public types, fields, and the `Stats` method will have Go documentation describing the
lifetime and approximate-snapshot semantics.

No monitoring endpoint is added to either configured Ramix transport.

## Testing

### Unit Tests

- A new metrics test verifies zero-value snapshots and independent TCP/WebSocket
  updates.
- Total counters, gauges, and duration totals equal saturated sums of the two
  transport snapshots; total maximum request duration equals the larger transport
  maximum.
- Counters and duration totals saturate instead of wrapping.
- Maximum request duration remains monotonic under competing updates.
- Gauge decrement cannot underflow.
- Unknown internal transport values are ignored.
- `Stats` returns detached values and is callable before startup and after shutdown.

### Runtime and Integration Tests

- TCP and WebSocket tests each verify connection gauge increase and eventual decrease.
- A valid inbound request increments received messages and body bytes for the correct
  transport and for `Total`.
- A successful response increments sent messages and body bytes only after transport
  write success.
- A deliberately full worker queue increments rejected tasks and leaves queued task
  accounting balanced after drain or shutdown.
- Completed handler execution records count, total duration, and a non-decreasing
  maximum; queue waiting time is excluded.
- Protocol or transport errors increment connection errors for the correct transport.
- Concurrent traffic while repeatedly calling `Stats` produces no race reports and no
  impossible unsigned wraparound.

### Repository Verification

The completed implementation must pass:

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
```

## Compatibility

This is an additive public API change. Existing server construction, transport,
connection, middleware, routing, and shutdown APIs retain their behavior. The binary
message format is unchanged, and the feature adds no module dependency.
