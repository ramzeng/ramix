# Stability and Test Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Ramix's implicit, panic-prone runtime with an explicit TCP/WebSocket lifecycle that is safe under concurrency and fully verified by the standard Go test, race, and vet commands.

**Architecture:** Keep the wire protocol and router model, but split runtime ownership into validated options, a server state machine, an internal fixed-size worker pool, shared connection lifecycle primitives, and transport-specific readers/writers. Shutdown is server-owned and phased: stop intake, quiesce reads, drain accepted tasks and writes, then force-close on the configured deadline.

**Tech Stack:** Go 1.20, standard library `context`/`net`/`net/http`/`crypto/tls`/`sync`, Gorilla WebSocket v1.5.0, Go unit and integration tests.

---

## File Map

The implementation should converge on these responsibilities:

- `errors.go`: public sentinel errors, connection operation names, and error callback types.
- `option.go`: transport enum, immutable `ServerOptions`, defaults, option functions, and validation.
- `server_state.go`: lifecycle states and synchronized transitions.
- `server.go`: construction, `Run`, `Shutdown`, routing dispatch, hooks, and shared shutdown orchestration.
- `tcp_server.go`: TCP/TLS listener creation and accept loop.
- `websocket_server.go`: private `ServeMux`, HTTP server, WebSocket upgrade, and listener serving.
- `connection.go`: public `Connection`, shared send gate, connection states, close requests, supervisor, and task-context creation.
- `tcp_connection.go`: TCP read/write loops and read quiescing.
- `ws_connection.go`: WebSocket read/write loops, control-frame activity, and protocol filtering.
- `manager.go`: sharded connection registry, snapshots, counts, quiesce, and force-close.
- `heartbeat.go`: race-free activity timestamps and connection-owned checker.
- `context.go`: middleware state plus cooperative task cancellation.
- `worker.go`: one ordered worker and its drain/cancel loop.
- `worker_pool.go`: fixed round-robin pool, non-blocking submission, drain, and force cancellation.
- `frame_decoder.go`, `decoder.go`, `encoder.go`: panic-free framing and exact message validation.
- `*_test.go`: focused unit tests next to each responsibility.
- `server_integration_test.go`: real TCP lifecycle and shutdown tests.
- `websocket_integration_test.go`: real WebSocket lifecycle and shutdown tests.
- `examples/**/{client,server}/main.go`: independently buildable examples using the new API.
- `.github/workflows/test.yml`: test, race, and vet verification.
- `README.md`, `README-CN.md`: new lifecycle API and migration notes.

Do not preserve `Serve`, `SendMessage`, `OnlyTCP`, `OnlyWebSocket`, `UseWorkerPool`, the exported `WorkerPool` interface, or per-connection workers in the final API.

For every task, format all changed Go files with `gofmt`, run `git diff --check`, and inspect `git status --short` before the task's single commit. Do not carry uncommitted changes from one task into the next.

### Task 1: Restore the Repository Build Baseline

**Files:**
- Modify: `go.mod`
- Move: `examples/startup/client.go` -> `examples/startup/client/main.go`
- Move: `examples/startup/server.go` -> `examples/startup/server/main.go`
- Move: `examples/barrage/client.go` -> `examples/barrage/client/main.go`
- Move: `examples/barrage/server.go` -> `examples/barrage/server/main.go`
- Move: `examples/tls/client.go` -> `examples/tls/client/main.go`
- Move: `examples/tls/server.go` -> `examples/tls/server/main.go`
- Move: `examples/websocket/client.go` -> `examples/websocket/client/main.go`
- Move: `examples/websocket/server.go` -> `examples/websocket/server/main.go`

- [ ] **Step 1: Reproduce the known package-discovery failure**

Run: `go test ./...`

Expected: FAIL with `main redeclared in this block` in the four example directories.

- [ ] **Step 2: Raise the module language baseline required by `errors.Join`**

Change `go.mod` to:

```go
module github.com/ramzeng/ramix

go 1.20

require github.com/gorilla/websocket v1.5.0
```

- [ ] **Step 3: Split every example into independently buildable packages**

Use `mkdir -p` and `git mv` for the paths listed above. Keep package names as `main`.

In `examples/tls/server/main.go`, change certificate paths so running from the repository root works:

```go
ramix.WithCertFile("examples/tls/public_certificate.pem"),
ramix.WithPrivateKeyFile("examples/tls/private_key.pem"),
```

- [ ] **Step 4: Verify standard package discovery now succeeds**

Run: `go test ./...`

Expected: PASS for the root package and all eight example packages.

Run: `go vet ./...`

Expected: PASS with no duplicate-main diagnostics.

- [ ] **Step 5: Commit Task 1**

```bash
git add go.mod examples
git commit -m "chore: restore full repository build baseline"
```

### Task 2: Add Typed Errors and Validated Immutable Options

**Files:**
- Create: `errors.go`
- Modify: `option.go`
- Rewrite: `option_test.go`
- Modify: `server.go`
- Modify: `server_test.go`
- Modify: `examples/**/main.go`

- [ ] **Step 1: Write failing option and construction tests**

Replace repetitive option setter tests with table-driven behavior tests. Include at least:

```go
func TestDefaultServerOptions(t *testing.T) {
    opts := defaultServerOptions()
    if len(opts.Transports) != 2 || opts.Transports[0] != TransportTCP || opts.Transports[1] != TransportWebSocket {
        t.Fatalf("transports = %v", opts.Transports)
    }
    if opts.ShutdownTimeout != 10*time.Second {
        t.Fatalf("shutdown timeout = %s", opts.ShutdownTimeout)
    }
    if opts.WorkerCount != uint32(runtime.GOMAXPROCS(0)) {
        t.Fatalf("worker count = %d", opts.WorkerCount)
    }
    if opts.WorkerQueueCapacity != 1024 || opts.MaxFrameLength != 1<<20 {
        t.Fatalf("unexpected queue/frame defaults: %+v", opts)
    }
}

func TestValidateServerOptions(t *testing.T) {
    tests := []struct {
        name   string
        mutate func(*ServerOptions)
    }{
        {"no transports", func(o *ServerOptions) { o.Transports = nil }},
        {"unknown transport", func(o *ServerOptions) { o.Transports = []Transport{99} }},
        {"negative port", func(o *ServerOptions) { o.Port = -1 }},
        {"empty websocket path", func(o *ServerOptions) { o.WebSocketPath = "" }},
        {"zero connection groups", func(o *ServerOptions) { o.ConnectionGroupsCount = 0 }},
        {"zero read buffer", func(o *ServerOptions) { o.ConnectionReadBufferSize = 0 }},
        {"zero worker count", func(o *ServerOptions) { o.WorkerCount = 0 }},
        {"zero worker queue", func(o *ServerOptions) { o.WorkerQueueCapacity = 0 }},
        {"zero max frame", func(o *ServerOptions) { o.MaxFrameLength = 0 }},
        {"heartbeat timeout too short", func(o *ServerOptions) { o.HeartbeatTimeout = o.HeartbeatInterval / 2 }},
        {"certificate without key", func(o *ServerOptions) { o.CertFile = "cert.pem"; o.PrivateKeyFile = "" }},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            opts := defaultServerOptions()
            tt.mutate(&opts)
            if err := validateServerOptions(opts); !errors.Is(err, ErrInvalidConfiguration) {
                t.Fatalf("validateServerOptions() error = %v", err)
            }
        })
    }
}

func TestNewServerRejectsInvalidConfiguration(t *testing.T) {
    _, err := NewServer(WithWorkerCount(0))
    if !errors.Is(err, ErrInvalidConfiguration) {
        t.Fatalf("NewServer() error = %v", err)
    }
}
```

Do not add a new assertion dependency; keep direct standard-library assertions.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test . -run 'Test(DefaultServerOptions|ValidateServerOptions|NewServerRejectsInvalidConfiguration)'`

Expected: FAIL because `Transport`, new option fields, sentinel errors, validation, and the error-returning constructor do not exist.

- [ ] **Step 3: Define the public error model**

Create `errors.go` with:

```go
package ramix

import "errors"

var (
    ErrInvalidConfiguration = errors.New("invalid configuration")
    ErrInvalidFrame         = errors.New("invalid frame")
    ErrFrameTooLarge        = errors.New("frame too large")
    ErrConnectionClosed     = errors.New("connection closed")
    ErrWorkerQueueFull      = errors.New("worker queue full")
    ErrServerRunning        = errors.New("server running")
    ErrServerStopping       = errors.New("server stopping")
    ErrServerStopped        = errors.New("server stopped")
    ErrShutdownTimeout      = errors.New("shutdown timeout")
)

type ConnectionOperation string

const (
    OperationRead      ConnectionOperation = "read"
    OperationWrite     ConnectionOperation = "write"
    OperationProtocol  ConnectionOperation = "protocol"
    OperationHeartbeat ConnectionOperation = "heartbeat"
    OperationTask      ConnectionOperation = "task"
    OperationOpenHook  ConnectionOperation = "open_hook"
    OperationCloseHook ConnectionOperation = "close_hook"
)

type ConnectionErrorHandler func(Connection, ConnectionOperation, error)
```

- [ ] **Step 4: Replace mutable global defaults with constructor defaults and validation**

Implement:

```go
type Transport uint8

const (
    TransportTCP Transport = iota + 1
    TransportWebSocket
)

type ServerOptions struct {
    Transports                []Transport
    Name                      string
    IPVersion                 string
    IP                        string
    Port                      int
    WebSocketPort             int
    WebSocketPath             string
    CertFile                  string
    PrivateKeyFile            string
    MaxConnectionsCount       int
    ConnectionGroupsCount     int
    ConnectionReadBufferSize  uint32
    ConnectionWriteBufferSize uint32
    HeartbeatInterval         time.Duration
    HeartbeatTimeout          time.Duration
    ShutdownTimeout           time.Duration
    WorkerCount               uint32
    WorkerQueueCapacity       uint32
    MaxFrameLength            uint64
}
```

Add `WithTransports`, `WithShutdownTimeout`, `WithWorkerCount`, `WithWorkerQueueCapacity`, and `WithServerMaxFrameLength`. The server-specific name avoids colliding with the existing `WithMaxFrameLength` `FrameDecoderOption`. `WithTransports` must copy and deduplicate input. Keep existing scalar options that remain meaningful; remove `OnlyTCP`, `OnlyWebSocket`, and `WithMaxWorkerTasksCount`.

`validateServerOptions` wraps every failure with `%w` and enough field detail to diagnose it. Allow port `0`; reject ports outside `0..65535`. Require certificate and key to be both empty or both non-empty.

- [ ] **Step 5: Change construction to return validation errors**

Change the signature to:

```go
func NewServer(serverOptions ...ServerOption) (*Server, error)
```

For this task, keep the existing runtime fields and behavior otherwise unchanged. Update `server_test.go` and the examples only enough to handle the constructor error so the repository stays buildable:

```go
server, err := ramix.NewServer(...)
if err != nil {
    log.Fatal(err)
}
```

- [ ] **Step 6: Run options, root, and full-package tests**

Run: `go test . -run 'Test(DefaultServerOptions|ValidateServerOptions|NewServer)'`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add errors.go option.go option_test.go server.go server_test.go examples
git commit -m "feat: validate server construction options"
```

### Task 3: Make Message and Frame Decoding Panic-Free

**Files:**
- Modify: `encoder.go`
- Modify: `encoder_test.go`
- Modify: `decoder.go`
- Rewrite: `decoder_test.go`
- Rewrite: `frame_decoder.go`
- Rewrite: `frame_decoder_test.go`
- Modify: `server.go`
- Modify: `connection.go`
- Modify: `tcp_connection.go`
- Modify: `ws_connection.go`

- [ ] **Step 1: Write failing message codec tests**

Add table tests for a short header, body mismatch, trailing bytes, and valid empty body:

```go
func TestDecoderRejectsInvalidMessages(t *testing.T) {
    tests := []struct {
        name string
        data []byte
    }{
        {"empty", nil},
        {"short header", []byte{1, 0, 0, 0}},
        {"body shorter than declared", []byte{1, 0, 0, 0, 2, 0, 0, 0, 'a'},
        {"body longer than declared", []byte{1, 0, 0, 0, 1, 0, 0, 0, 'a', 'b'},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := (&Decoder{}).Decode(tt.data)
            if !errors.Is(err, ErrInvalidFrame) {
                t.Fatalf("Decode() error = %v", err)
            }
        })
    }
}

func TestEncoderDerivesBodySize(t *testing.T) {
    encoded, err := (&Encoder{}).Encode(Message{Event: 7, BodySize: 999, Body: []byte("ok")})
    if err != nil { t.Fatal(err) }
    if got := binary.LittleEndian.Uint32(encoded[4:8]); got != 2 {
        t.Fatalf("body size = %d", got)
    }
}
```

- [ ] **Step 2: Write failing frame decoder tests**

Cover constructor validation, split frames, coalesced frames, unsupported length widths, maximum length, and negative/overflowing adjusted lengths. The primary behavior test should be:

```go
func TestFrameDecoderSplitAndCoalescedFrames(t *testing.T) {
    decoder, err := NewFrameDecoder(
        WithLengthFieldOffset(4),
        WithLengthFieldLength(4),
        WithMaxFrameLength(64),
    )
    if err != nil { t.Fatal(err) }

    first := mustEncode(t, Message{Event: 1, Body: []byte("one")})
    second := mustEncode(t, Message{Event: 2, Body: []byte("two")})

    frames, err := decoder.Decode(first[:5])
    if err != nil || len(frames) != 0 { t.Fatalf("first Decode() = %d, %v", len(frames), err) }

    frames, err = decoder.Decode(append(first[5:], second...))
    if err != nil { t.Fatal(err) }
    if len(frames) != 2 { t.Fatalf("frames = %d", len(frames)) }
}
```

For oversized input, assert `errors.Is(err, ErrFrameTooLarge)`. For every former panic path, call the API normally and assert an error.

- [ ] **Step 3: Run codec tests and confirm failure**

Run: `go test . -run 'Test(Decoder|Encoder|FrameDecoder)'`

Expected: FAIL, including at least one panic from the current short-input decoder or frame decoder.

- [ ] **Step 4: Implement exact message validation**

`Decoder.Decode` must check `len(data) >= 8` before field reads and require:

```go
declared := int(binary.LittleEndian.Uint32(data[4:8]))
if declared != len(data)-8 {
    return Message{}, fmt.Errorf("%w: declared body %d, actual %d", ErrInvalidFrame, declared, len(data)-8)
}
```

`Encoder.Encode` must derive `BodySize` from `len(message.Body)` and reject bodies larger than `math.MaxUint32`.

- [ ] **Step 5: Replace panic paths in `FrameDecoder` with validated errors**

Change signatures to:

```go
func NewFrameDecoder(options ...FrameDecoderOption) (*FrameDecoder, error)
func (d *FrameDecoder) Decode(input []byte) ([][]byte, error)
```

Validate options once in the constructor. In `Decode`, append to connection-local buffered bytes, parse only when the complete length field is present, detect arithmetic overflow before converting to `int`, enforce the configured maximum, copy complete frames into owned slices, and retain incomplete bytes. On any invalid or oversized frame, clear buffered state and return the typed error because the caller will close the connection.

- [ ] **Step 6: Update constructor and decode call sites for the new APIs**

Temporarily propagate impossible default-construction errors from `NewServer`; connection-specific construction must use the validated server `MaxFrameLength`. Update both transport readers from `frames := decoder.Decode(data)` to `frames, err := decoder.Decode(data)` and close/report on error so the repository compiles before the transport-hardening tasks.

- [ ] **Step 7: Run codec tests and the full suite**

Run: `go test . -run 'Test(Decoder|Encoder|FrameDecoder)'`

Expected: PASS with no panic.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

```bash
git add encoder.go encoder_test.go decoder.go decoder_test.go frame_decoder.go frame_decoder_test.go server.go connection.go tcp_connection.go ws_connection.go
git commit -m "fix: return errors for malformed protocol frames"
```

### Task 4: Add Cooperative Task Contexts and Ordered Worker Draining

**Files:**
- Modify: `context.go`
- Rewrite: `context_test.go`
- Rewrite: `worker.go`
- Rewrite: `worker_pool.go`
- Create: `worker_pool_test.go`
- Modify: `server.go`
- Modify: `examples/**/server/main.go`

- [ ] **Step 1: Write failing context cancellation tests**

Extend `Context` with standard cancellation behavior while preserving middleware `Next`, `Set`, and `Get`:

```go
func TestContextCancellation(t *testing.T) {
    parent, cancel := context.WithCancel(context.Background())
    ctx := newContext(parent, nil, nil)
    cancel()

    select {
    case <-ctx.Done():
    case <-time.After(time.Second):
        t.Fatal("context was not canceled")
    }
    if !errors.Is(ctx.Err(), context.Canceled) {
        t.Fatalf("Err() = %v", ctx.Err())
    }
}
```

- [ ] **Step 2: Write failing worker behavior tests**

Tests must cover:

1. Same connection's tasks execute in submission order.
2. Submission to a full queue returns `ErrWorkerQueueFull` immediately.
3. `stopAcceptingAndDrain` runs queued tasks before returning.
4. `forceCancel` closes `Context.Done()` for a cooperative blocking handler.
5. Repeated drain/cancel calls are safe.

Use a one-worker pool and channels rather than sleeps. The queue-full test must block the worker on a channel, fill its one-slot queue, then assert the third submit fails.

- [ ] **Step 3: Run focused tests and confirm failure**

Run: `go test . -run 'Test(ContextCancellation|WorkerPool)'`

Expected: FAIL because cancellation methods and the new internal pool lifecycle do not exist.

- [ ] **Step 4: Add task cancellation to `Context`**

Use:

```go
type Context struct {
    context.Context
    Connection Connection
    Request    *Request
    handlers   []Handler
    step       int
    keys       map[string]any
    lock       sync.RWMutex
    cancel     context.CancelFunc
}
```

`newContext(parent, connection, request)` creates a child context. `cancelTask()` calls the stored cancellation function. `finish()` cancels the task context after handler execution. Do not use `context.AfterFunc`, which is not available at the Go 1.20 baseline.

- [ ] **Step 5: Implement the internal fixed round-robin worker pool**

The pool is not exported:

```go
type workerPool struct {
    workers   []*worker
    submitMu  sync.RWMutex
    accepting bool
    tasksMu   sync.Mutex
    tasks     map[*Context]struct{}
    drainOnce sync.Once
    done      chan struct{}
}

func newWorkerPool(count, capacity uint32) *workerPool
func (p *workerPool) start()
func (p *workerPool) submit(ctx *Context) error
func (p *workerPool) stopAcceptingAndDrain(ctx context.Context) error
func (p *workerPool) forceCancel()
```

`submit` holds `submitMu.RLock`, checks `accepting`, registers the task in `tasks`, selects one worker by connection ID, and performs one non-blocking channel send. If queueing fails, unregister it before returning. Drain takes `submitMu.Lock`, marks intake closed, closes worker task channels, releases the lock, and waits for workers to range over queued tasks. If the drain context expires, call `forceCancel` and return its context error without waiting forever for a non-cooperative handler.

Each worker skips middleware execution when `task.Err()` is already non-nil; otherwise it calls `task.Next()`. In both cases it then calls `task.finish()` and unregisters the task exactly once. `forceCancel` snapshots every registered queued/running task under `tasksMu` and calls `cancelTask()`; this gives Go 1.20-compatible cooperative cancellation without one merge goroutine per task.

- [ ] **Step 6: Adapt the current server to use the internal pool**

Use `ServerOptions.WorkerCount` and `WorkerQueueCapacity`. Remove the exported `WorkerPool`, `RoundRobinWorkerPool`, `NewRoundRobinWorkerPool`, `UseWorkerPool`, and per-connection worker selection. Update examples to remove `UseWorkerPool` calls.

- [ ] **Step 7: Run worker tests, race tests, and full tests**

Run: `go test . -run 'Test(Context|WorkerPool)'`

Expected: PASS.

Run: `go test -race . -run 'Test(Context|WorkerPool)'`

Expected: PASS with no race report.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

```bash
git add context.go context_test.go worker.go worker_pool.go worker_pool_test.go server.go examples
git commit -m "feat: add cancellable ordered worker processing"
```

### Task 5: Build the Shared Connection Lifecycle and Registry

**Files:**
- Rewrite: `connection.go`
- Create: `connection_test.go`
- Rewrite: `manager.go`
- Create: `manager_test.go`
- Rewrite: `heartbeat.go`
- Create: `heartbeat_test.go`
- Modify: `tcp_connection.go`
- Modify: `ws_connection.go`
- Modify: `server.go`
- Modify: `recovery.go`
- Modify: `examples/**/server/main.go`

- [ ] **Step 1: Write failing registry and heartbeat tests**

Registry tests must prove add/remove/count/snapshot across shards and that a snapshot is stable while the registry changes. Heartbeat tests use an injected `now func() time.Time` or direct atomic timestamps; do not rely on process-wide sleeps.

```go
func TestHeartbeatAliveBoundary(t *testing.T) {
    now := time.Unix(100, 0)
    activity := newActivityClock(func() time.Time { return now })
    activity.refresh()
    now = now.Add(9 * time.Second)
    if !activity.alive(10 * time.Second) { t.Fatal("connection should be alive") }
    now = now.Add(2 * time.Second)
    if activity.alive(10 * time.Second) { t.Fatal("connection should be expired") }
}
```

- [ ] **Step 2: Write failing shared connection concurrency tests**

Use a `fakeTransport` that records writes, blocks reads, exposes `SetReadDeadline`, and counts closes. Cover:

- 50 concurrent close requests call transport close once.
- `Send` after close returns `ErrConnectionClosed`.
- a blocked `Send` is released when sends stop.
- send draining writes every message accepted before the gate closes.
- child-triggered close returns immediately and supervisor completion does not deadlock.
- open and close hooks execute once; panic recovery is added in Task 8, so use non-panicking hooks here.

- [ ] **Step 3: Run focused tests and confirm failure**

Run: `go test . -run 'Test(Connection|ConnectionManager|Heartbeat)'`

Expected: FAIL because the shared state machine, send gate, snapshots, and supervisor do not exist.

- [ ] **Step 4: Implement the public connection contract and shared states**

Final public interface:

```go
type Connection interface {
    ID() uint64
    RemoteAddress() net.Addr
    Send(context.Context, uint32, []byte) error
}
```

Internal states are `connectionOpen`, `connectionDraining`, `connectionClosing`, and `connectionClosed`. The shared connection owns separate read, send, and force cancellation, a send mutex plus `sendWG`, an outgoing queue, child `WaitGroup`, `writerDone`, supervisor `done`, and `sync.Once` close/finalization guards.

Do not close the outgoing queue. `Send` must register under the send mutex before selecting on queue, caller context, send cancellation, or force cancellation.

Remove `SendMessage`. Update `recovery.go` and every server example to pass the Ramix handler context into the new API:

```go
_ = ctx.Connection.Send(ctx, 500, []byte("Server Error"))
```

Use the same form for normal route responses.
Update the server's built-in 404 response to use `ctx.Connection.Send(ctx, 404, ...)`. In the barrage example, pass the current handler context to each broadcast send and protect the shared connection map with a mutex so the example remains race-safe.

- [ ] **Step 5: Implement non-blocking close request and supervisor finalization**

Required internal methods:

```go
func (c *netConnection) requestClose(op ConnectionOperation, err error)
func (c *netConnection) quiesceReads() error
func (c *netConnection) stopSendsAndDrain(ctx context.Context) error
func (c *netConnection) wait(ctx context.Context) error
func (c *netConnection) supervise()
```

`requestClose` changes state, cancels all activity, closes the transport, records the first close reason, and returns without waiting. `supervise` waits for reader/writer/heartbeat children, removes the connection, invokes the close hook, marks closed, and closes `done`.

- [ ] **Step 6: Rewrite the manager around snapshots of actual connections**

Keep sharded maps and locks. Add:

```go
func (m *connectionManager) snapshot() []managedConnection
func (m *connectionManager) quiesceAll() []error
func (m *connectionManager) forceCloseAll()
func (m *connectionManager) waitAll(ctx context.Context) error
```

Never replace maps as a substitute for closing their values.

- [ ] **Step 7: Replace heartbeat sharing with connection-owned state**

Each connection gets its own ticker and atomic last-active time. `refreshActivity` is race-free. Heartbeat expiration calls `requestClose(OperationHeartbeat, context.DeadlineExceeded)` once. The ticker stops on read quiesce or force cancellation.

- [ ] **Step 8: Adapt TCP/WebSocket files minimally to compile against the shared primitive**

Keep transport-specific behavior functionally equivalent for now. Full error and protocol behavior is covered in Tasks 6 and 7.

- [ ] **Step 9: Run focused and race tests**

Run: `go test . -run 'Test(Connection|ConnectionManager|Heartbeat)'`

Expected: PASS.

Run: `go test -race . -run 'Test(Connection|ConnectionManager|Heartbeat)'`

Expected: PASS with no race report.

Run: `go test ./...`

Expected: PASS; no `SendMessage` call sites remain in buildable Go packages.

- [ ] **Step 10: Commit Task 5**

```bash
git add connection.go connection_test.go manager.go manager_test.go heartbeat.go heartbeat_test.go tcp_connection.go ws_connection.go server.go recovery.go examples
git commit -m "feat: supervise connection lifecycle safely"
```

### Task 6: Harden TCP Reading, Writing, and Protocol Failure

**Files:**
- Rewrite: `tcp_connection.go`
- Create: `tcp_connection_test.go`
- Modify: `connection.go`
- Modify: `server.go`

- [ ] **Step 1: Write failing TCP tests with `net.Pipe` and scripted connections**

Cover:

- split frame across reads routes exactly once.
- two frames in one read route in order.
- short/oversized/mismatched frames report `OperationProtocol` and close only that connection.
- writer retries short writes until the full encoded message is written.
- a zero-byte write without error is treated as `io.ErrNoProgress`.
- quiescing sets a read deadline and lets the reader exit without closing the writer first.
- a saturated worker queue reports `ErrWorkerQueueFull` as `OperationTask` and closes only the offending connection.

Use channels in a fake server dispatch function to observe messages; no fixed sleeps.

- [ ] **Step 2: Run TCP tests and confirm failure**

Run: `go test . -run 'TestTCPConnection'`

Expected: FAIL because current code allocates each loop, ignores write errors and short writes, and does not propagate decoder errors.

- [ ] **Step 3: Implement a reusable read buffer and error-returning frame loop**

Allocate one `[]byte` per reader goroutine. For each successful read, refresh activity, call `frameDecoder.Decode(buffer[:n])`, then message decode and submit. Any frame/message error reports `OperationProtocol`, requests close, and exits. If pool submission returns `ErrWorkerQueueFull`, report it as `OperationTask`, request close, and exit; if it returns `ErrServerStopping`, exit quietly because shutdown already owns cleanup.

Normal remote `io.EOF`, `net.ErrClosed`, and deadline errors caused by server quiescing are not reported as unexpected connection errors.

- [ ] **Step 4: Implement full-write semantics**

Use a helper:

```go
func writeFull(w io.Writer, data []byte) error {
    for len(data) > 0 {
        n, err := w.Write(data)
        if err != nil { return err }
        if n == 0 { return io.ErrNoProgress }
        data = data[n:]
    }
    return nil
}
```

The writer handles normal messages, the graceful drain signal, and force cancellation. A write error reports `OperationWrite` and requests close.

- [ ] **Step 5: Run TCP unit and race tests**

Run: `go test . -run 'TestTCPConnection'`

Expected: PASS.

Run: `go test -race . -run 'TestTCPConnection'`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

```bash
git add tcp_connection.go tcp_connection_test.go connection.go server.go
git commit -m "fix: harden TCP connection IO"
```

### Task 7: Harden WebSocket Framing and Control-Frame Activity

**Files:**
- Rewrite: `ws_connection.go`
- Create: `ws_connection_test.go`
- Modify: `connection.go`
- Modify: `server.go`

- [ ] **Step 1: Write failing WebSocket connection tests**

Use a local `httptest.Server` only for transport-level tests. Cover:

- one binary message routes successfully.
- text messages report `OperationProtocol` with `ErrInvalidFrame` and close the connection.
- malformed and oversized binary payloads close only that connection.
- ping and pong handlers refresh last-active time.
- exactly one writer goroutine calls Gorilla application-data write APIs; ping/pong handlers may use Gorilla's concurrency-safe `WriteControl` exception.
- read quiescing sets a read deadline and does not prevent an already accepted response write.

- [ ] **Step 2: Run WebSocket tests and confirm failure**

Run: `go test . -run 'TestWebSocketConnection'`

Expected: FAIL because current code accepts text frames, handles ping after reading it as data, ignores write errors, and has no quiesce semantics.

- [ ] **Step 3: Install control handlers and binary-only dispatch**

Before starting the reader, install ping and pong handlers that refresh activity. The ping handler must send a pong through Gorilla's concurrency-safe `WriteControl` API with a bounded deadline. This is the only reader-side write exception; all application binary messages still flow through the single writer goroutine. In the read loop, accept only `websocket.BinaryMessage`; reject text and other data types as `ErrInvalidFrame`.

- [ ] **Step 4: Implement writer and quiesce behavior**

Only the writer goroutine sends binary application messages with `WriteMessage`. Treat any error as `OperationWrite`. Quiescing sets an immediate read deadline so the reader exits; writer shutdown follows the shared send-drain protocol.

- [ ] **Step 5: Run WebSocket unit and race tests**

Run: `go test . -run 'TestWebSocketConnection'`

Expected: PASS.

Run: `go test -race . -run 'TestWebSocketConnection'`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit Task 7**

```bash
git add ws_connection.go ws_connection_test.go connection.go server.go
git commit -m "fix: enforce WebSocket protocol boundaries"
```

### Task 8: Implement Server State, Atomic Startup, and Error-Safe Hooks

**Files:**
- Create: `server_state.go`
- Create: `server_state_test.go`
- Rewrite: `server.go`
- Create: `tcp_server.go`
- Create: `websocket_server.go`
- Rewrite: `server_test.go`
- Modify: `router.go`
- Modify: `router_test.go`
- Modify: `examples/**/server/main.go`

- [ ] **Step 1: Write failing state-machine tests**

Cover:

- first `Run` claims `new`; concurrent `Run` returns `ErrServerRunning`.
- a stopped server cannot run again and returns `ErrServerStopped`.
- `Shutdown` on `new` returns nil.
- configuration mutation during `starting`/`running` returns `ErrServerRunning`.
- configuration mutation after stop returns `ErrServerStopped`.
- `Use` and `RegisterRoute` specifically obey those mutation-state errors.
- canceling `Run` during startup rolls back listeners and returns nil.
- startup bind failure closes any listener already created and leaves state stopped.
- invalid TLS certificate loading rolls back all previously bound transport resources.
- when `Shutdown` is already waiting during startup failure, `Run` returns the startup cause, that waiting `Shutdown` returns nil, and later `Shutdown` calls also return nil because startup errors are not stored as shutdown results.

Introduce package-private listener factories in tests rather than relying on races between real binds:

```go
type listenFunc func(network, address string) (net.Listener, error)
```

- [ ] **Step 2: Write failing hook and error-handler tests**

Cover open-hook panic, close-hook panic, and error-callback panic. Each panic must be recovered; cleanup continues; the callback is not recursively invoked for its own panic.

- [ ] **Step 3: Run focused server tests and confirm failure**

Run: `go test . -run 'Test(ServerState|ServerRun|ServerStartup|Hook|ErrorHandler|RouterFreeze)'`

Expected: FAIL because `Run`, `Shutdown`, state transitions, route freezing, and panic-safe callbacks do not exist.

- [ ] **Step 4: Implement synchronized lifecycle state**

`server_state.go` defines `stateNew`, `stateStarting`, `stateRunning`, `stateStopping`, and `stateStopped`, plus guarded helpers. `starting` maps to `ErrServerRunning` for duplicate run and mutation calls.

The server owns `startupDone`, `shutdownDone`, `stopRequested`, terminal shutdown error, runtime error channel, listener map, address snapshot, service wait group, manager, and worker pool.

- [ ] **Step 5: Freeze routing and callbacks before listener creation**

Make route registration methods return `error`. Keep the callback registration API explicit and state-checked:

```go
func (s *Server) OnConnectionOpen(func(Connection)) error
func (s *Server) OnConnectionClose(func(Connection)) error
func (s *Server) OnConnectionError(ConnectionErrorHandler) error
```

On `Run`, deep-copy route handler slices into an immutable snapshot. Readers dispatch from that snapshot without locks. Hooks and the error callback are also captured before binding. The default error handler logs through the existing Ramix debug/logger path.

- [ ] **Step 6: Implement atomic transport startup**

`Run(ctx)` performs:

```text
claim new -> freeze/validate -> initialize pool -> bind every enabled listener
-> start workers and transport goroutines behind a closed startup gate
-> publish running -> open startup gate -> wait
```

Check stop request and `ctx.Err()` between creation steps. On any failure or cancellation, close every created resource, transition to stopped, close `startupDone`, and return the specified result.

For TCP, bind with `net.Listen` and wrap with `tls.NewListener` when TLS is configured. For WebSocket, create a private `http.ServeMux`, register only the configured path, create an `http.Server`, and call `Serve` on the already-bound listener. Do not use global `http.HandleFunc`, `ListenAndServe`, or signal handling.

Launch transport goroutines before publishing `running`, but require each goroutine to wait on `serveGate` before entering `Accept` or `http.Server.Serve`. After worker startup and goroutine launch, transition to `running`, close `serveGate`, then close `startupDone`. This ensures goroutines exist before readiness while no connection can be accepted in `starting`. Filter `net.ErrClosed` and `http.ErrServerClosed` during expected shutdown.

- [ ] **Step 7: Add panic-safe hook and error reporting helpers**

Use one helper per callback type with `defer recover`. Open-hook panic reports `OperationOpenHook` then closes the connection. Close-hook panic reports `OperationCloseHook` after cleanup is already guaranteed. Error-handler panic writes through the normal logger/debug path and never re-enters the callback.

- [ ] **Step 8: Replace old lifecycle API in examples**

Remove `Serve` usage. Use signal contexts:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

server, err := ramix.NewServer(...)
if err != nil { log.Fatal(err) }
if err := server.Run(ctx); err != nil { log.Fatal(err) }
```

Update handlers to call `Connection.Send(context.Context, ...)`, normally passing the Ramix handler context because it embeds `context.Context`.

- [ ] **Step 9: Run server tests and full tests**

Run: `go test . -run 'Test(Server|Hook|ErrorHandler|Router)'`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 10: Commit Task 8**

```bash
git add server_state.go server_state_test.go server.go server_test.go tcp_server.go websocket_server.go router.go router_test.go examples
git commit -m "feat: add explicit atomic server lifecycle"
```

### Task 9: Verify Real TCP and WebSocket Routing and Isolation

**Files:**
- Create: `server_integration_test.go`
- Create: `websocket_integration_test.go`
- Modify: `server.go`
- Modify: `tcp_server.go`
- Modify: `websocket_server.go`

- [ ] **Step 1: Add test startup helpers with hard deadlines**

Create package-test helpers that run `server.Run` in a goroutine, wait until internal state is running, read the bound listener address, and always register cleanup. Use port `0` and loopback IP. Every wait uses `context.WithTimeout` or `select` with a timer.

- [ ] **Step 2: Write failing TCP integration tests**

Cover:

- request/response routing on an ephemeral TCP port.
- a frame split across two socket writes.
- two frames in one socket write and ordered responses.
- one malformed client being disconnected while a second client continues successfully.
- concurrent clients maintaining per-connection handler order.
- the configured maximum connection count rejecting an extra client without affecting existing clients.
- TCP/TLS request/response using the repository test certificate.

- [ ] **Step 3: Write failing WebSocket integration tests**

Cover:

- binary request/response on an ephemeral WebSocket port and configured path.
- text-message rejection while a second connection remains healthy.
- malformed binary rejection while the server remains healthy.
- ping/pong extending activity beyond one heartbeat interval.
- secure WebSocket request/response using the repository test certificate.
- WebSocket worker-queue saturation reporting `OperationTask`, closing the offending connection, and leaving another connection healthy.

The checked-in certificate expired in 2024 and has no subject alternative name. These tests verify encrypted transport wiring, not PKI validation, so TCP/TLS clients must use `&tls.Config{InsecureSkipVerify: true}` and the WebSocket dialer must use the same test-only TLS configuration. Keep `// #nosec G402 -- test fixture intentionally bypasses certificate verification` directly above those test configurations if the linter requires it. Production examples and APIs must not install this setting server-side.

- [ ] **Step 4: Run integration tests and record the baseline**

Run: `go test . -run 'TestIntegration_(TCP|WebSocket)' -count=1`

Expected: The tests may already PASS because Task 8 implements startup ordering and Task 6/7 implement isolation. If any test fails, the failure must identify a real integration gap before Step 5. This task is allowed to be a test-only commit when all new black-box tests pass immediately; do not create an artificial production defect just to force a red phase.

- [ ] **Step 5: Make only corrections proven necessary by Step 4**

If failures occur, correct readiness publication, bound address storage, accept/upgrade failure handling, max-connection checks, or connection registration ordering as indicated. If all tests pass, leave production code unchanged. Do not add a new public readiness API unless tests prove internal state cannot express the required behavior.

- [ ] **Step 6: Run integration, full, and race tests**

Run: `go test . -run 'TestIntegration_(TCP|WebSocket)' -count=1`

Expected: PASS.

Run: `go test -race . -run 'TestIntegration_(TCP|WebSocket)' -count=1`

Expected: PASS with no race report.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit Task 9**

```bash
git add server_integration_test.go websocket_integration_test.go server.go tcp_server.go websocket_server.go
git commit -m "test: cover TCP and WebSocket runtime isolation"
```

### Task 10: Implement Graceful Drain, Forced Timeout, and Repeated Shutdown

**Files:**
- Modify: `server.go`
- Modify: `server_state.go`
- Modify: `connection.go`
- Modify: `manager.go`
- Modify: `worker_pool.go`
- Modify: `tcp_server.go`
- Modify: `websocket_server.go`
- Modify: `server_integration_test.go`
- Modify: `websocket_integration_test.go`
- Modify: `server_test.go`

- [ ] **Step 1: Write failing shared shutdown tests**

Cover:

- multiple concurrent `Shutdown` callers observe the same completion.
- one caller timing out returns its own context error while shared shutdown continues.
- later calls after normal stop return nil.
- later calls after forced cleanup return an error matching `ErrShutdownTimeout`.
- `Shutdown` during `starting` records a stop request and waits for rollback.
- canceling the context passed to an already-running `Run` starts the same shared shutdown sequence, allows a concurrent `Shutdown` waiter to observe it, and makes `Run` return nil after normal cleanup.
- an unexpected TCP/WebSocket serving error is returned from `Run` with `errors.Is` preserving the injected cause.
- an unexpected serving error combined with forced shutdown timeout makes `Run` match both the injected cause and `ErrShutdownTimeout`, proving `errors.Join` behavior.

Use a scripted listener whose `Accept` returns a sentinel error only after `serveGate` opens. For the joined-error case, also hold a non-cooperative handler until test cleanup.

- [ ] **Step 2: Write failing graceful response-drain integration tests**

For both TCP and WebSocket:

1. Route handler signals that it started.
2. Test initiates shutdown.
3. Handler sends its response after shutdown has stopped new reads.
4. Client receives the complete response.
5. `Run` and `Shutdown` return nil.
6. The transport listener rejects new dials, existing clients observe closure, the connection registry count becomes zero, every worker completion channel is closed, and service `WaitGroup` completion is observable.

This proves the required order: quiesce reads -> drain worker tasks -> stop sends -> drain writes -> close connections.

- [ ] **Step 3: Write failing timeout and cooperative-cancellation tests**

Add one handler that blocks on `<-ctx.Done()` and verifies forced shutdown reaches it. Add a deliberately non-cooperative handler that blocks on a private channel; verify `Run` and `Shutdown` return an error matching `ErrShutdownTimeout` without waiting forever. Release the private channel in test cleanup so the process does not retain the goroutine.

- [ ] **Step 4: Run shutdown tests and confirm failure**

Run: `go test . -run 'Test(ServerShutdown|Integration_.*Shutdown)' -count=1`

Expected: FAIL because shutdown is not yet phased or independently deadline-owned.

- [ ] **Step 5: Implement one shared shutdown owner**

The first stop trigger starts `shutdown()` exactly once and creates:

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), s.options.ShutdownTimeout)
```

Caller contexts only select while waiting for `shutdownDone`. They never cancel shared cleanup.

- [ ] **Step 6: Implement the required shutdown phases**

Use these exact phases:

```text
stateStopping and reject pool submissions
close listeners / HTTP server intake
snapshot and quiesce connection reads + heartbeat
drain accepted worker tasks
stop sends and drain each writer queue
request full connection close and wait for supervisors
wait for serving goroutines
store result, stateStopped, close shutdownDone
```

Before closing `shutdownDone`, assert in implementation invariants that listener references are closed, the manager snapshot is empty, connection supervisors have completed, workers that returned cooperatively are done, and serving goroutines have exited. Tests should observe these through package-private completion channels and counts rather than process-wide goroutine totals.

If any phase reaches `shutdownCtx.Done()`, call pool force cancellation, force-close all connections, stop waiting for non-cooperative handler workers, and store `fmt.Errorf("%w: %v", ErrShutdownTimeout, shutdownCtx.Err())`.

If an unexpected runtime failure initiated shutdown and cleanup also fails, `Run` returns `errors.Join(runtimeErr, shutdownErr)`.

Preserve the first unexpected serving/internal error in a buffered channel or guarded field. Do not let expected listener-close errors overwrite it. Add deterministic injection points only at listener/server boundaries; do not add production-only sleeps or global test hooks.

- [ ] **Step 7: Run shutdown tests repeatedly and under race**

Run: `go test . -run 'Test(ServerShutdown|Integration_.*Shutdown)' -count=20`

Expected: PASS on all repetitions.

Run: `go test -race . -run 'Test(ServerShutdown|Integration_.*Shutdown)' -count=1`

Expected: PASS with no race report.

- [ ] **Step 8: Run the complete repository suite**

Run: `go test ./...`

Expected: PASS.

Run: `go test -race ./...`

Expected: PASS.

- [ ] **Step 9: Commit Task 10**

```bash
git add server.go server_state.go connection.go manager.go worker_pool.go tcp_server.go websocket_server.go server_test.go server_integration_test.go websocket_integration_test.go
git commit -m "feat: drain server resources during shutdown"
```

### Task 11: Update Documentation, CI, and Final Verification

**Files:**
- Modify: `README.md`
- Modify: `README-CN.md`
- Create: `.github/workflows/test.yml`
- Modify: `.github/workflows/golangci-lint.yml` only if needed to keep its Go version consistent
- Modify: `examples/**/main.go`

- [ ] **Step 1: Update English documentation for the breaking API**

Document:

- `NewServer(...) (*Server, error)`.
- default TCP + WebSocket transports and `WithTransports`.
- signal context ownership in the application.
- synchronous `Run(ctx)`.
- optional explicit `Shutdown(ctx)` and its caller-wait semantics.
- `Connection.Send(ctx, event, body)`.
- fixed internal worker pool options.
- malformed-frame connection isolation.
- migration list removing `Serve`, `SendMessage`, `OnlyTCP`, `OnlyWebSocket`, and custom worker pools.

Remove the stale TODO claiming unit tests are absent.

- [ ] **Step 2: Apply the same content to the Chinese README**

Keep code samples identical and translate behavior precisely. Use the `chinese-typesetting` skill when editing mixed Chinese/English/number copy.

- [ ] **Step 3: Verify every example uses the final API**

Run:

```bash
rg 'Serve\(|SendMessage|OnlyTCP|OnlyWebSocket|UseWorkerPool|NewRoundRobinWorkerPool' README.md README-CN.md examples
```

Expected: no matches except migration text that explicitly names removed APIs.

- [ ] **Step 4: Add CI for the acceptance commands**

Create `.github/workflows/test.yml` with separate jobs or steps for:

```yaml
- run: go test ./...
- run: go test -race ./...
- run: go vet ./...
```

Use Go 1.20 to match `go.mod`. Do not replace the existing lint workflow in this task.

- [ ] **Step 5: Run formatting and static verification**

Run: `gofmt -w $(rg --files -g '*.go')`

Run: `git diff --check`

Expected: no formatting or whitespace errors.

Run: `go vet ./...`

Expected: PASS.

- [ ] **Step 6: Run all acceptance commands from a clean test cache**

Run: `go clean -testcache && go test ./...`

Expected: PASS.

Run: `go test -race ./...`

Expected: PASS with no race report.

Run: `go vet ./...`

Expected: PASS.

- [ ] **Step 7: Inspect the final public API and repository status**

Run: `go doc github.com/ramzeng/ramix.Server && go doc github.com/ramzeng/ramix.Connection`

Expected: only the intended lifecycle and send APIs are exposed.

Run: `git status --short`

Expected: only Task 11 documentation/CI changes are present before commit.

- [ ] **Step 8: Commit Task 11**

```bash
git add README.md README-CN.md .github/workflows examples
git commit -m "docs: publish stable lifecycle and CI baseline"
```

## Completion Check

After Task 11, verify the branch has exactly one implementation commit per task after the planning commits:

```bash
git log --oneline --decorate main..HEAD
```

Expected task commits, newest first:

```text
docs: publish stable lifecycle and CI baseline
feat: drain server resources during shutdown
test: cover TCP and WebSocket runtime isolation
feat: add explicit atomic server lifecycle
fix: enforce WebSocket protocol boundaries
fix: harden TCP connection IO
feat: supervise connection lifecycle safely
feat: add cancellable ordered worker processing
fix: return errors for malformed protocol frames
feat: validate server construction options
chore: restore full repository build baseline
```

Planning/specification commits may precede these and are not implementation tasks.
