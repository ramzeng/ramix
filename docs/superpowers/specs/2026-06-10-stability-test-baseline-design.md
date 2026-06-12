# Stability and Test Baseline Design

## Summary

Ramix currently has a small, understandable codebase, but its service lifecycle and
connection concurrency model are not strong enough for production use. Standard
repository verification also fails because each example package contains two `main`
functions. This change establishes a production baseline for both TCP and WebSocket
without preserving API compatibility with the current release.

The work will replace implicit process-signal ownership with an explicit,
context-driven server lifecycle; make connection shutdown and task submission safe
under concurrency; convert protocol panics into typed errors; and add integration
tests for the critical runtime paths.

The binary message format remains unchanged:

```text
4-byte little-endian event | 4-byte little-endian body length | body
```

## Goals

- Give callers explicit control over server startup and shutdown.
- Start TCP and WebSocket atomically: either every configured listener starts, or
  startup rolls back completely.
- Make connection close, message send, and task submission deterministic under
  concurrency.
- Prevent malformed remote input from panicking the process.
- Preserve message ordering within a connection while allowing different
  connections to run concurrently.
- Make `go test ./...`, `go test -race ./...`, and `go vet ./...` pass.
- Test real TCP and WebSocket startup, routing, protocol errors, and shutdown.

## Non-Goals

- Changing the on-wire message format.
- Replacing the worker model with a new event-loop architecture.
- Adding automatic reconnection, authentication, observability backends, or
  application-level acknowledgements.
- Maximizing statement coverage as an independent target.
- Supporting restart of a stopped `Server` instance.

## Public API Direction

This is an intentionally breaking release. The implementation may remove or rename
existing lifecycle and send APIs rather than retaining compatibility wrappers.

The primary lifecycle API will be:

```go
func NewServer(options ...ServerOption) (*Server, error)
func (s *Server) Run(ctx context.Context) error
func (s *Server) Shutdown(ctx context.Context) error
```

Transport selection uses an explicit enum option:

```go
type Transport uint8

const (
    TransportTCP Transport = iota + 1
    TransportWebSocket
)

func WithTransports(transports ...Transport) ServerOption
func WithShutdownTimeout(timeout time.Duration) ServerOption
func WithWorkerCount(count uint32) ServerOption
func WithWorkerQueueCapacity(capacity uint32) ServerOption
func WithServerMaxFrameLength(length uint64) ServerOption
```

Both TCP and WebSocket are enabled by default, matching the current server behavior.
`WithTransports` replaces `OnlyTCP` and `OnlyWebSocket`; an explicitly empty set is
invalid, duplicate values are deduplicated, and unknown values are rejected. Port `0`
is valid for either transport so the operating system can select an ephemeral port.
The default shutdown timeout is 10 seconds. The default worker count is
`runtime.GOMAXPROCS(0)`, the default per-worker queue capacity is 1024, and the default
maximum frame length is 1 MiB. All are validated as positive values.

`NewServer` validates configuration before returning. Validation includes transport
selection, ports, WebSocket path, connection limits, connection group count, buffer
sizes, heartbeat durations, frame limits, and worker counts and queue sizes. Enabling
neither transport or using non-positive capacities is invalid.

Scalar options, including worker count and queue capacity, are fixed at construction.
Route, middleware, hook, and error-handler registration remains available while the
server is in `new`; these mutating methods return `ErrServerRunning` after startup has
been claimed and `ErrServerStopped` after termination. `Run` performs a final
validation and freezes a routing snapshot before binding listeners.

`Run` is synchronous. It binds all enabled listeners before publishing the running
state. If any bind or server initialization fails, it closes resources already
created and returns the startup error. After successful startup, it blocks until the
server stops.

Canceling the `Run` context is a normal stop request. It triggers the same shared
shutdown sequence as `Shutdown`. The sequence always uses the server's configured
shutdown timeout; caller contexts only limit how long an individual `Shutdown` call
waits for that sequence. `Run` returns `nil` if cleanup completes, or the shared
shutdown error if forced cleanup was required. Unexpected listener or internal
component failure causes shutdown and is returned from `Run` as a wrapped error. If
that failure is followed by a shutdown failure, `Run` returns `errors.Join` of the
runtime and shutdown errors so neither cause is lost.

`Shutdown` is safe to call concurrently and repeatedly. All callers observe the same
shutdown completion. A caller whose context expires returns its context error without
canceling or replacing the shared shutdown sequence, which continues in the
background. If graceful draining exceeds the configured server deadline, remaining
workers and connections are force-canceled and the shared terminal result wraps
`ErrShutdownTimeout`.

The server has a single lifecycle. `starting` is externally treated as running for
error reporting:

```text
new -> starting -> running -> stopping -> stopped
             \-----------------> stopped  (startup rollback)
```

The first `Run` call atomically claims `new` before performing final validation or
binding. Concurrent `Run` calls during `starting` or `running` return
`ErrServerRunning`; calls after rollback or shutdown return `ErrServerStopped`.

`Shutdown` on a new server succeeds without side effects. On a stopped server it
returns the stored shutdown result, which is `nil` after normal cleanup and may wrap
`ErrShutdownTimeout` after forced cleanup. During `starting`, it records a stop request
and waits for startup to finish, subject only to the caller's wait context. Startup
checks both the `Run` context and that stop request between resource-creation steps.
If cancellation or shutdown is requested, startup rolls back every created resource,
transitions to `stopped`, and `Run` returns `nil`. If startup itself fails, rollback
also transitions to `stopped`, while `Run` returns the startup error and waiting
`Shutdown` callers return `nil`; startup errors are not stored as shutdown results.

The connection send API becomes context-aware:

```go
type Connection interface {
    ID() uint64
    RemoteAddress() net.Addr
    Send(ctx context.Context, event uint32, body []byte) error
}
```

`Send` returns when the encoded message enters the write queue, the caller context is
canceled, or the connection closes. A full write queue therefore applies bounded,
caller-controlled backpressure instead of blocking forever.

This round supports one task model: the built-in fixed-size round-robin worker pool.
The exported custom `WorkerPool` interface and per-connection worker mode are removed.
Worker count and per-worker queue capacity are constructor options. Removing the
ambiguous extension surface keeps lifecycle and drain behavior consistent; a new
public worker extension point can be designed separately if a concrete need appears.

## Server Ownership and Startup

`Server` owns all process-independent runtime resources:

- TCP listener
- dedicated WebSocket `http.Server`, `ServeMux`, and listener
- server context and cancellation
- connection manager
- worker pool
- service-level goroutine wait group
- lifecycle state and terminal error

The library no longer registers operating-system signal handlers or calls `os.Exit`.
Applications translate signals into context cancellation or explicit `Shutdown`
calls.

WebSocket routing uses a private `http.ServeMux`; Ramix does not register handlers on
the global HTTP mux. TLS configuration is applied independently to each enabled
transport while continuing to use the configured certificate and key files.

Startup follows this sequence:

1. Atomically transition from `new` to `starting` and create `startupDone`.
2. Freeze and validate routes, hooks, and runtime configuration.
3. Initialize the built-in worker pool without starting intake.
4. Check the `Run` context and pending shutdown request.
5. Bind each enabled listener, checking for cancellation between binds.
6. If validation, binding, cancellation, or a stop request occurs, close resources,
   transition to `stopped`, and close `startupDone`.
7. Start workers and transport-serving goroutines behind a closed startup gate.
8. Publish `running`, open the startup gate, and close `startupDone` so no connection
   can be accepted while the externally visible state is still `starting`.
9. Wait for context cancellation, an unexpected serving error, or shutdown.

Normal listener-closed errors produced by shutdown are filtered and are not reported
as runtime failures.

## Shutdown Sequence

Shutdown has one owner and a completion channel observed by concurrent callers. It
creates an internal timeout context from the configured shutdown duration. A caller's
context controls only that caller's wait on the completion channel. The ordered
sequence is:

1. Transition from `running` to `stopping` and reject new task submissions.
2. Close TCP and WebSocket listeners so no new connections are accepted.
3. Snapshot active connections and quiesce their readers and heartbeat checkers while
   keeping their writers available to already accepted handlers.
4. Drain tasks already accepted by workers. Handlers may still call `Send` during
   this phase, so accepted requests can finish their responses.
5. Stop new sends, wait for sends already in progress, and drain each outgoing queue.
6. Fully close connections and wait for connection supervisors to finish.
7. Stop workers, wait for serving goroutines, transition to `stopped`, store the
   terminal result, and notify waiters.

Quiescing uses transport read deadlines to wake blocked TCP, TLS, and WebSocket reads
without immediately closing the write side. Readers recognize the server's draining
state and exit without initiating full connection closure. Final connection closure
closes the socket to release any remaining blocked I/O. Connection manager shutdown
must operate on actual connections; replacing its internal maps is not sufficient.

If the internal shutdown deadline expires at any phase, the server rejects sends,
cancels remaining task contexts, force-closes every connection, records an
`ErrShutdownTimeout` result, and completes the transition to `stopped`. Framework
goroutines exit after their owned resources are released; a currently executing
application handler can outlive shutdown only if it ignores its canceled task context.

## Connection Concurrency Model

Each connection owns:

- its socket
- connection context and cancellation
- bounded outgoing message queue
- frame decoder
- heartbeat checker
- connection-level goroutine wait group
- atomic last-active timestamp
- atomic state `open`, `draining`, `closing`, or `closed`
- a `sync.Once` guarded close sequence
- a completion channel owned by a connection supervisor

The outgoing message queue is not closed. Both `Send` and the writer observe a
dedicated send gate, eliminating the race between queue closure and concurrent sends.
`Send` is allowed in `open` and `draining`, because draining workers may still need to
write responses. Once send draining or immediate close starts, new sends return
`ErrConnectionClosed`; blocked sends are released by cancellation of the send gate.

The send gate tracks in-progress `Send` calls. Graceful shutdown first rejects new
sends, releases blocked sends, waits for active send calls to finish, and only then
asks the writer to drain the queue. The writer can therefore determine that no future
enqueue is possible without closing the channel.

Connection shutdown separates a non-blocking close request from finalization so a
reader or writer never waits for itself. The close request is idempotent:

1. Mark the connection `closing` so new work is rejected.
2. Cancel read, heartbeat, and send activity and close the socket.
3. Return immediately to the requesting goroutine.

A separate connection supervisor, started with the connection, performs finalization:

1. Wait for reader, writer, and heartbeat child goroutines.
2. Remove the connection from the manager exactly once.
3. Run the close hook exactly once.
4. Mark the connection `closed` and close its completion channel.

Server shutdown requests quiescing or closure and waits on the completion channel; it
does not call a child-goroutine wait from inside that same connection child.

The open hook runs once after the connection has been registered and its goroutines
are ready. If the open hook panics, Ramix recovers, reports the error, and closes that
connection because application initialization did not complete. A close-hook panic is
reported but cleanup continues.

TCP writes use full-write semantics and treat zero-progress or partial-write failures
as connection errors. WebSocket writes use one Gorilla writer at a time and treat any
`WriteMessage` failure as a connection error.

## Task Dispatch and Backpressure

A connection is assigned deterministically to one worker for its lifetime, preserving
message order within that connection. Different connections may execute on different
workers.

Task submission is internal and non-blocking. It attempts one immediate channel send
and returns one of:

- success when the task is queued
- `ErrWorkerQueueFull` when capacity is exhausted
- `ErrServerStopping` when intake has stopped

When the reader encounters `ErrWorkerQueueFull`, it reports the error and closes that
connection. Continuing to read while dropping or reordering requests would leave the
application protocol in an undefined state.

During graceful shutdown, workers stop accepting new tasks, process tasks already in
their queues, then exit. If the shutdown deadline expires, worker contexts are
canceled and remaining queued work may be abandoned. Each Ramix `Context` exposes a
task cancellation signal derived from its connection and worker. Handlers that perform
blocking work are expected to observe that signal.

Go cannot forcibly terminate an arbitrary handler goroutine. After a shutdown timeout,
Ramix cancels task contexts, force-closes transport resources, and returns
`ErrShutdownTimeout`; a handler that ignores cancellation and never returns may keep
its worker goroutine alive. This is treated as non-cooperative application code rather
than a framework-cleanup guarantee.

The worker pool has only internal lifecycle methods. Public configuration is limited
to worker count and queue capacity, avoiding partially compatible custom shutdown
implementations in this round.

## Framing and Message Validation

The frame decoder API becomes error-returning:

```go
func NewFrameDecoder(options ...FrameDecoderOption) (*FrameDecoder, error)
func (d *FrameDecoder) Decode(input []byte) ([][]byte, error)
```

Frame decoder construction rejects unsupported length-field widths, invalid offsets,
negative strip values, impossible adjustments, and maximum lengths too small for the
configured header.

Runtime decoding returns typed errors rather than panicking. The default maximum frame
length remains 1 MiB. A frame must contain at least the eight-byte Ramix message
header. The message decoder checks input length before reading either field and
requires the declared body size to match the complete frame.

TCP decoding continues to support fragmented frames, multiple frames in one read, and
frames spanning arbitrary read boundaries. Decoder state remains connection-local.

WebSocket accepts binary data messages only. Binary payloads use the same Ramix frame
decoder as TCP. Text data messages and unsupported data message types are connection-
level protocol errors. Gorilla ping and pong handlers refresh heartbeat activity;
control frames are not passed to the Ramix message decoder.

An invalid frame, oversized frame, or message-length mismatch is terminal for that
connection. Ramix reports the error and closes only the offending connection.

## Error Model

Public sentinel errors support `errors.Is` while wrapped errors retain operational
detail:

- `ErrInvalidConfiguration`
- `ErrInvalidFrame`
- `ErrFrameTooLarge`
- `ErrConnectionClosed`
- `ErrWorkerQueueFull`
- `ErrServerRunning`
- `ErrServerStopping`
- `ErrServerStopped`
- `ErrShutdownTimeout`

Startup and server-level failures are returned from `NewServer`, `Run`, or `Shutdown`.
Connection-level read, write, protocol, heartbeat, task, and hook failures are reported
through a configurable connection error callback. The callback receives the
connection, operation, and wrapped error. The default behavior logs through Ramix's
logger.

The error callback is protected from panics. A callback panic is recovered and logged
without recursively invoking the callback.

Expected shutdown errors, context cancellation, and normal remote EOF are not logged
as server failures. They may still be available as connection close reasons for hooks
and tests.

## Heartbeat Behavior

Heartbeat state uses an atomic timestamp or equivalent synchronization so reads and
updates are race-free. Successful TCP reads refresh activity. WebSocket binary reads,
ping frames, and pong frames refresh activity.

Each heartbeat checker uses a ticker owned by the connection and stops when the
connection context is canceled. An expired heartbeat closes the connection once and
reports a timeout-classified connection error.

Invalid heartbeat intervals or timeouts are rejected during server construction. The
timeout must not be shorter than the check interval.

## Example and Documentation Layout

Each runnable example becomes its own buildable package, for example:

```text
examples/startup/server/main.go
examples/startup/client/main.go
examples/websocket/server/main.go
examples/websocket/client/main.go
```

TLS and barrage examples follow the same structure. This lets standard Go package
discovery compile every example without duplicate `main` declarations.

The English and Chinese READMEs will be updated to show:

- error handling from `NewServer`
- signal-to-context wiring in the application
- synchronous `Run(ctx)`
- explicit deadline-bound `Shutdown(ctx)` where needed
- context-aware `Connection.Send`
- the breaking-release migration notes

The stale TODO claiming unit tests are absent will be removed.

## Test Strategy

### Unit Tests

Unit tests cover:

- valid and invalid server configurations
- all server state transitions and repeated lifecycle calls
- concurrent `Run`, startup cancellation, and `Shutdown` during `starting`
- caller wait cancellation without cancellation of the shared shutdown sequence
- frame fragmentation, coalescing, invalid fields, oversized frames, and short input
- message body-length mismatch
- worker ordering, queue saturation, drain, cancellation, and repeated stop
- task context cancellation reaching a cooperative blocking handler
- concurrent connection close and send
- close-after-send and send-after-close behavior
- hook and error-callback panic recovery
- heartbeat activity and timeout using bounded test clocks or short deterministic
  intervals

Tests must assert errors with `errors.Is` rather than matching complete strings.

### TCP Integration Tests

TCP integration tests bind loopback port `0`, discover the selected address from the
server's internal listener, and use real `net.Conn` clients. Tests cover:

- startup and route response
- split frame delivery
- multiple frames in one write
- oversized and malformed frames closing only the offending client
- concurrent clients preserving per-connection order
- an accepted handler completing its response while shutdown drains workers
- forced cleanup when a handler exceeds the configured shutdown timeout
- a non-cooperative handler causing `ErrShutdownTimeout` without blocking `Run`
- shutdown releasing listeners, clients, workers, and goroutines

### WebSocket Integration Tests

WebSocket integration tests use a loopback ephemeral listener and real Gorilla
clients. Tests cover:

- binary route request and response
- text message rejection
- malformed binary frame rejection
- ping/pong activity refresh
- an accepted handler completing its response while shutdown drains workers
- client cleanup and server shutdown

### Async Test Discipline

Asynchronous tests use completion channels, contexts, or eventually assertions with
hard deadlines. Fixed sleeps are not used as the primary synchronization mechanism.
Tests verify goroutine termination through owned wait groups and observable component
completion, avoiding brittle assertions against the process-wide goroutine count.

## CI and Acceptance Criteria

CI runs these commands from the repository root:

```sh
go test ./...
go test -race ./...
go vet ./...
```

The first optimization round is complete when:

- all three commands pass
- both transports satisfy the same lifecycle and protocol guarantees
- malformed client input cannot panic the server in covered decoding paths
- shutdown is idempotent and releases all server-owned resources in tests
- queue saturation has explicit, tested behavior
- examples compile under standard package discovery
- English and Chinese documentation describe the new API

## Implementation Boundaries

The implementation should favor focused units rather than expanding `server.go` and
the connection files indefinitely. Expected responsibilities are:

- server lifecycle and state transitions
- transport-specific listener setup
- shared connection lifecycle primitives
- transport-specific reading and writing
- frame and message decoding
- task dispatch and worker shutdown
- connection registry
- hooks and error reporting

Internal boundaries may differ from the file layout above, but each component must
have one clear owner, an explicit shutdown contract, and focused tests.
