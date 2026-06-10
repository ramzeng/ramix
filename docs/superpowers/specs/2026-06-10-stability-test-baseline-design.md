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

`NewServer` validates configuration before returning. Validation includes transport
selection, ports, WebSocket path, connection limits, connection group count, buffer
sizes, heartbeat durations, frame limits, and worker counts and queue sizes. Enabling
neither transport, enabling mutually exclusive transport options together, or using
non-positive capacities is invalid.

`Run` is synchronous. It binds all enabled listeners before publishing the running
state. If any bind or server initialization fails, it closes resources already
created and returns the startup error. After successful startup, it blocks until the
server stops.

Canceling the `Run` context is a normal stop request. It triggers the same shutdown
sequence as `Shutdown`, using a configurable default shutdown timeout, and `Run`
returns `nil` if cleanup completes. Unexpected listener or internal component failure
causes shutdown and is returned from `Run` as a wrapped error.

`Shutdown` is safe to call concurrently and repeatedly. All callers observe the same
shutdown completion. A caller whose context expires returns its context error without
corrupting the shared shutdown sequence. If graceful worker draining exceeds the
server's shutdown deadline, remaining workers are canceled and the timeout is
reported.

The server has a single lifecycle:

```text
new -> running -> stopping -> stopped
```

Calling `Run` outside `new` returns `ErrServerRunning` or `ErrServerStopped` as
appropriate. Calling `Shutdown` on a new or already stopped server succeeds without
side effects.

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

Worker-pool operations will return errors and accept contexts where waiting is
possible. The built-in round-robin pool remains the supported default. Its interface
may be revised as part of this breaking release so custom implementations do not rely
on unexported methods.

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

1. Atomically transition from `new` into an internal starting state.
2. Validate or initialize the worker pool.
3. Bind every enabled listener.
4. If any bind fails, close previous listeners and return the error.
5. Publish the `running` state.
6. Start accept and HTTP serving goroutines.
7. Wait for context cancellation, an unexpected serving error, or shutdown.

Normal listener-closed errors produced by shutdown are filtered and are not reported
as runtime failures.

## Shutdown Sequence

Shutdown has one owner and a completion channel observed by concurrent callers. The
ordered sequence is:

1. Transition from `running` to `stopping` and reject new task submissions.
2. Close TCP and WebSocket listeners so no new connections are accepted.
3. Snapshot and close all active connections.
4. Wait for each connection reader, writer, and heartbeat goroutine to exit.
5. Drain tasks already accepted by workers.
6. Stop workers and wait for service goroutines.
7. Transition to `stopped`, store the terminal result, and notify waiters.

Closing a socket is what releases blocked reads and writes. Connection manager
shutdown must close actual connections; replacing its internal maps is not sufficient.

If a shutdown deadline expires, the server cancels remaining workers, records the
deadline error, and completes the transition to `stopped`. No server-owned goroutine
is intentionally left running after shutdown completion.

## Connection Concurrency Model

Each connection owns:

- its socket
- connection context and cancellation
- bounded outgoing message queue
- frame decoder
- heartbeat checker
- connection-level goroutine wait group
- atomic last-active timestamp
- atomic state `open`, `closing`, or `closed`
- a `sync.Once` guarded close sequence

The outgoing message queue is not closed. Both `Send` and the writer select on the
connection context, eliminating the race between queue closure and concurrent sends.
Once closing starts, new sends return `ErrConnectionClosed`.

The connection close sequence is idempotent:

1. Mark the connection `closing` so new work is rejected.
2. Cancel the connection context and close the socket.
3. Wait for reader, writer, and heartbeat goroutines.
4. Remove the connection from the manager exactly once.
5. Run the close hook exactly once.
6. Mark the connection `closed`.

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

Task submission does not block indefinitely. Submission returns one of:

- success when the task is queued
- `ErrWorkerQueueFull` when capacity is exhausted
- `ErrConnectionClosed` or a shutdown error when intake has stopped

When the reader encounters `ErrWorkerQueueFull`, it reports the error and closes that
connection. Continuing to read while dropping or reordering requests would leave the
application protocol in an undefined state.

During graceful shutdown, workers stop accepting new tasks, process tasks already in
their queues, then exit. If the shutdown deadline expires, worker contexts are
canceled and remaining queued work may be abandoned.

The same behavior applies to the built-in shared pool and any per-connection worker
mode retained by the implementation.

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
- `ErrServerStopped`

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
- frame fragmentation, coalescing, invalid fields, oversized frames, and short input
- message body-length mismatch
- worker ordering, queue saturation, drain, cancellation, and repeated stop
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
- shutdown releasing listeners, clients, workers, and goroutines

### WebSocket Integration Tests

WebSocket integration tests use a loopback ephemeral listener and real Gorilla
clients. Tests cover:

- binary route request and response
- text message rejection
- malformed binary frame rejection
- ping/pong activity refresh
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
