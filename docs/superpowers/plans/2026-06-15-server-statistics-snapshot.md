# Server Statistics Snapshot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a zero-dependency, concurrency-safe `Server.Stats()` snapshot with aggregate and TCP/WebSocket connection, queue, message, error, and request-duration statistics.

**Architecture:** Add a focused `stats.go` unit containing the public snapshot types and private atomic counters. Every Ramix-created connection carries a private transport identity; connection, decoder, writer, error-reporting, and worker paths update narrow metric helpers, while `Stats()` atomically loads TCP and WebSocket snapshots and derives `Total`. Preserve existing routing, overload, framing, and shutdown behavior.

**Tech Stack:** Go 1.20+, standard library `sync/atomic`, existing TCP/WebSocket integration harness, `go test`, race detector, `go vet`.

**Execution prerequisite:** Implement on a dedicated `codex/` worktree or branch created from commit `df4191e`. Keep each task's commit separate so metric-core, runtime instrumentation, and documentation changes remain reviewable.

---

## File Map

- Create `stats.go`: public snapshots, private atomic storage, saturation helpers, transport lookup, and `Server.Stats()`.
- Create `stats_test.go`: snapshot, saturation, maximum, unknown-transport, lifecycle-access, and concurrency tests.
- Modify `server.go`: store `serverMetrics`, attach metric context to requests, count connection errors, and pass transport identity into connection construction.
- Modify `connection.go`: store private transport identity, count active connections, and count successful outgoing messages.
- Modify `context.go`: carry private metrics and transport identity with each accepted task.
- Modify `worker_pool.go`: account queued and rejected tasks around non-blocking submission.
- Modify `worker.go`: dequeue accounting and completed-request duration recording.
- Modify `tcp_connection.go`: record successfully decoded TCP messages and body bytes.
- Modify `ws_connection.go`: record successfully decoded WebSocket messages and body bytes.
- Modify `connection_test.go`, `tcp_connection_test.go`, and `ws_connection_test.go`: supply explicit transport identity and test lifecycle/write metrics.
- Modify `worker_pool_test.go`: test queue, rejection, cancellation, and request-duration accounting.
- Modify `server_state_test.go`: verify connection-error accounting does not depend on the application callback.
- Modify `server_integration_test.go`: add reusable stats waiting helpers and TCP end-to-end coverage.
- Modify `websocket_integration_test.go`: add WebSocket end-to-end and queue-rejection coverage.
- Modify `README.md` and `README-CN.md`: document polling `Stats()` and its approximate, lifetime-cumulative semantics.

---

### Task 1: Build the Atomic Statistics Core and Public Snapshot API

**Files:**
- Create: `stats.go`
- Create: `stats_test.go`
- Modify: `server.go:18-59`

- [ ] **Step 1: Write failing tests for zero snapshots and detached values**

Create `stats_test.go`:

```go
package ramix

import (
    "math"
    "sync"
    "testing"
    "time"
)

func TestServerStatsStartsAtZeroAndIsDetached(t *testing.T) {
    server, err := NewServer()
    if err != nil {
        t.Fatalf("NewServer() error = %v", err)
    }
    first := server.Stats()
    if first != (ServerStats{}) {
        t.Fatalf("Stats() = %+v, want zero snapshot", first)
    }
    first.TCP.ReceivedMessages = 99
    if second := server.Stats(); second != (ServerStats{}) {
        t.Fatalf("second Stats() = %+v, want detached zero snapshot", second)
    }
    if err := server.Shutdown(nil); err != nil {
        t.Fatalf("Shutdown(nil) error = %v", err)
    }
    if after := server.Stats(); after != (ServerStats{}) {
        t.Fatalf("Stats() after shutdown = %+v, want zero snapshot", after)
    }
}
```

- [ ] **Step 2: Add failing metric-unit tests for aggregation, saturation, and maximum duration**

Add tests that perform these updates and assertions:

```go
func TestServerMetricsSnapshotAggregatesTransports(t *testing.T) {
    var metrics serverMetrics
    metrics.connectionOpened(TransportTCP)
    metrics.messageReceived(TransportTCP, 3)
    metrics.messageSent(TransportTCP, 5)
    metrics.requestCompleted(TransportTCP, 4*time.Millisecond)
    metrics.connectionOpened(TransportWebSocket)
    metrics.messageReceived(TransportWebSocket, 7)
    metrics.messageSent(TransportWebSocket, 11)
    metrics.requestCompleted(TransportWebSocket, 9*time.Millisecond)

    got := metrics.snapshot()
    if got.Total.ReceivedMessages != 2 || got.Total.ReceivedBytes != 10 {
        t.Fatalf("total receive stats = %+v", got.Total)
    }
    if got.Total.SentMessages != 2 || got.Total.SentBytes != 16 {
        t.Fatalf("total send stats = %+v", got.Total)
    }
    if got.Total.CompletedRequests != 2 || got.Total.TotalRequestDuration != 13*time.Millisecond {
        t.Fatalf("total request stats = %+v", got.Total)
    }
    if got.Total.MaximumRequestDuration != 9*time.Millisecond {
        t.Fatalf("maximum duration = %s, want 9ms", got.Total.MaximumRequestDuration)
    }
}

func TestServerMetricsSaturatesAndGuardsGaugeUnderflow(t *testing.T) {
    var metrics serverMetrics
    tcp := metrics.forTransport(TransportTCP)
    tcp.receivedMessages.Store(math.MaxUint64)
    tcp.receivedBytes.Store(math.MaxUint64 - 1)
    tcp.totalRequestDuration.Store(uint64(math.MaxInt64 - 1))
    metrics.messageReceived(TransportTCP, 10)
    metrics.requestCompleted(TransportTCP, 10*time.Nanosecond)
    metrics.connectionClosed(TransportTCP)

    got := metrics.snapshot().TCP
    if got.ReceivedMessages != math.MaxUint64 || got.ReceivedBytes != math.MaxUint64 {
        t.Fatalf("receive stats = %+v, want saturation", got)
    }
    if got.TotalRequestDuration != time.Duration(math.MaxInt64) {
        t.Fatalf("duration = %s, want saturation", got.TotalRequestDuration)
    }
    if got.ActiveConnections != 0 {
        t.Fatalf("active connections underflowed to %d", got.ActiveConnections)
    }
}

func TestServerMetricsIgnoresUnknownTransport(t *testing.T) {
    var metrics serverMetrics
    metrics.connectionOpened(Transport(255))
    metrics.messageReceived(Transport(255), 4)
    metrics.messageSent(Transport(255), 4)
    metrics.taskQueued(Transport(255))
    metrics.taskDequeued(Transport(255))
    metrics.taskRejected(Transport(255))
    metrics.connectionError(Transport(255))
    metrics.requestCompleted(Transport(255), time.Second)
    if got := metrics.snapshot(); got != (ServerStats{}) {
        t.Fatalf("snapshot = %+v, want zero", got)
    }
}

func TestServerMetricsMaximumDurationIsMonotonicConcurrently(t *testing.T) {
    var metrics serverMetrics
    durations := []time.Duration{time.Millisecond, 9 * time.Millisecond, 3 * time.Millisecond, 7 * time.Millisecond}
    var wg sync.WaitGroup
    for _, duration := range durations {
        duration := duration
        wg.Add(1)
        go func() {
            defer wg.Done()
            metrics.requestCompleted(TransportTCP, duration)
        }()
    }
    wg.Wait()
    if got := metrics.snapshot().TCP.MaximumRequestDuration; got != 9*time.Millisecond {
        t.Fatalf("maximum duration = %s, want 9ms", got)
    }
}
```

Also add two explicitly named edge-case tests:

```go
func TestServerMetricsTotalSaturatesEveryAdditiveField(t *testing.T)
func TestServerMetricsNegativeDurationClampsToZero(t *testing.T)
```

For `TestServerMetricsTotalSaturatesEveryAdditiveField`, directly seed every additive
TCP private counter/gauge (`ActiveConnections`, `QueuedTasks`, receive/send counts and
bytes, rejections, errors, completed requests, and total duration) one below its limit,
seed the corresponding WebSocket value with `2`, and assert every `Total` field
saturates. Seed different transport maximum durations and assert `Total` takes the
larger maximum rather than adding them. Use a table of field labels and actual/wanted
values so no public field is omitted.

For `TestServerMetricsNegativeDurationClampsToZero`, call
`requestCompleted(TransportTCP, -time.Second)` and assert completed requests is one
while total and maximum duration are both zero.

- [ ] **Step 3: Run tests and verify the API is missing**

Run:

```bash
go test ./... -run 'TestServerStats|TestServerMetrics' -count=1
```

Expected: FAIL to compile because `ServerStats`, `serverMetrics`, and `(*Server).Stats` do not exist.

- [ ] **Step 4: Implement public snapshots and private counters in `stats.go`**

Define the approved API exactly:

```go
type ServerStats struct {
    Total     TransportStats
    TCP       TransportStats
    WebSocket TransportStats
}

type TransportStats struct {
    ActiveConnections uint64
    QueuedTasks        uint64
    ReceivedMessages   uint64
    ReceivedBytes      uint64
    SentMessages       uint64
    SentBytes          uint64
    RejectedTasks      uint64
    ConnectionErrors   uint64
    CompletedRequests      uint64
    TotalRequestDuration   time.Duration
    MaximumRequestDuration time.Duration
}
```

Document both exported types and every exported field. Add `metrics serverMetrics` to `Server`; its zero value is ready for use. Implement `(*Server).Stats()` as `return s.metrics.snapshot()`.

Use `serverMetrics` with two `transportMetrics` groups. Each private field is `atomic.Uint64`; durations are nanoseconds. Implement narrow helpers named in the spec: `connectionOpened`, `connectionClosed`, `messageReceived`, `messageSent`, `taskQueued`, `taskDequeued`, `taskRejected`, `connectionError`, `requestCompleted`, and `snapshot`.

Use these CAS helpers:

```go
func saturatingAdd(value *atomic.Uint64, delta, limit uint64) {
    for {
        current := value.Load()
        if current >= limit {
            return
        }
        next := current + delta
        if next < current || next > limit {
            next = limit
        }
        if value.CompareAndSwap(current, next) {
            return
        }
    }
}

func decrementGauge(value *atomic.Uint64) {
    for {
        current := value.Load()
        if current == 0 {
            return
        }
        if value.CompareAndSwap(current, current-1) {
            return
        }
    }
}

func updateMaximum(value *atomic.Uint64, candidate uint64) {
    for {
        current := value.Load()
        if candidate <= current || value.CompareAndSwap(current, candidate) {
            return
        }
    }
}
```

`requestCompleted` clamps negative duration to zero, saturates duration total at `math.MaxInt64`, and increments completed requests at `math.MaxUint64`. `combineTransportStats` saturated-adds all counters/gauges and total duration, but takes the larger maximum duration.

- [ ] **Step 5: Format and run core verification**

Run:

```bash
gofmt -w stats.go stats_test.go server.go
go test ./... -run 'TestServerStats|TestServerMetrics' -count=1
go test -race ./... -run 'TestServerMetricsMaximumDurationIsMonotonicConcurrently' -count=1
```

Expected: all focused tests PASS and no race is reported.

- [ ] **Step 6: Commit the metric core**

```bash
git add stats.go stats_test.go server.go
git commit -m "feat: add server statistics snapshot core"
```

---

### Task 2: Attribute Connections and Successful Writes to a Transport

**Files:**
- Modify: `connection.go:45-88,176-237,325-343`
- Modify: `server.go:579-604`
- Modify: `connection_test.go`
- Modify: `tcp_connection_test.go`
- Modify: `ws_connection_test.go`
- Modify: `stats_test.go`

- [ ] **Step 1: Write failing lifecycle and write-accounting tests**

Add `waitForStats` to `stats_test.go`:

```go
func waitForStats(t *testing.T, server *Server, condition func(ServerStats) bool, label string) ServerStats {
    t.Helper()
    deadline := time.Now().Add(time.Second)
    for time.Now().Before(deadline) {
        stats := server.Stats()
        if condition(stats) {
            return stats
        }
        time.Sleep(time.Millisecond)
    }
    stats := server.Stats()
    t.Fatalf("timed out waiting for %s; final stats = %+v", label, stats)
    return ServerStats{}
}
```

Add `TestConnectionMetricsTrackLifecycleAndSuccessfulWrites`: start a TCP test
connection, wait for one active connection, send body `body`, wait for one sent
message/four sent bytes, close it, and assert the active gauge returns to zero.

Add `TestConnectionMetricsDoNotCountFailedWrite`: use a writer returning
`errors.New("write failed")`, wait for connection finalization, and assert sent
messages/bytes remain zero.

- [ ] **Step 2: Run tests and verify attribution is absent**

Run:

```bash
go test ./... -run 'TestConnectionMetrics' -count=1
```

Expected: FAIL because connections do not carry metric transport identity and runtime paths do not update metrics.

- [ ] **Step 3: Add explicit transport identity to `netConnection`**

Change the constructor signature:

```go
func newNetConnection(
    connectionID uint64,
    server *Server,
    metricTransport Transport,
    transport connectionTransport,
    writeMessage func([]byte) error,
) (*netConnection, error)
```

Store `metricTransport Transport` and expose a private `statsTransport() Transport` method. Production TCP construction passes `TransportTCP`; WebSocket passes `TransportWebSocket`.

Update every result from `rg -n 'newNetConnection\(' --glob '*.go'`: `connection_test.go` and `tcp_connection_test.go` pass TCP, while `ws_connection_test.go` passes WebSocket. Do not add an optional or inferred default.

- [ ] **Step 4: Instrument active connection and successful write metrics**

Inside `startOnce.Do`, before child goroutines start, call `c.server.metrics.connectionOpened(c.metricTransport)`. During supervisor finalization, immediately after `removeConnection`, call `connectionClosed` before the close hook.

Extract a helper to remove duplicated writer branches:

```go
func (c *netConnection) writeOutgoing(data []byte) error {
    if err := c.writeMessage(data); err != nil {
        return err
    }
    if len(data) >= 8 {
        c.server.metrics.messageSent(c.metricTransport, uint64(len(data)-8))
    }
    return nil
}
```

Use it in normal and drain writes. Existing error reporting and close behavior remains unchanged.

- [ ] **Step 5: Format and verify all constructor call sites**

Run:

```bash
gofmt -w connection.go server.go connection_test.go tcp_connection_test.go ws_connection_test.go stats_test.go
go test ./... -run 'TestConnectionMetrics|TestConnectionConcurrentCloseRequests|TestConnectionBlockedSend' -count=1
go test ./... -count=1
```

Expected: focused and full tests PASS.

- [ ] **Step 6: Commit connection and send accounting**

```bash
git add connection.go server.go connection_test.go tcp_connection_test.go ws_connection_test.go stats_test.go
git commit -m "feat: track connection and send statistics"
```

---

### Task 3: Count Successfully Decoded Messages and Reported Connection Errors

**Files:**
- Modify: `stats.go`
- Modify: `server.go:559-577`
- Modify: `tcp_connection.go:61-89`
- Modify: `ws_connection.go:94-109`
- Modify: `tcp_connection_test.go`
- Modify: `ws_connection_test.go`
- Modify: `server_state_test.go:406-425`

- [ ] **Step 1: Write failing receive and error tests with explicit names**

Add `TestTCPConnectionReceiveStatsCountMultipleDecodedMessages`. Submit two valid
coalesced messages with bodies `one` and `payload`, drain their tasks, and assert two
messages/ten body bytes for TCP and `Total`, with WebSocket still zero.

Add `TestTCPConnectionReceiveStatsExcludeMalformedBatch`. Submit one valid encoded
message followed in the same `processInput` call by a malformed length header. Assert
the call returns a protocol error and receive statistics remain zero, matching current
`FrameDecoder.Decode` behavior that returns no frames from an errored call.

Add `TestWebSocketConnectionReceiveStatsCountDecodedMessage`. Send one valid binary
message with body `payload`, wait for handling, and assert one message/seven body bytes
for WebSocket and `Total`, with TCP zero.

Add `TestWebSocketConnectionReceiveStatsExcludePartialMessage`. Send a WebSocket
binary message containing only part of a Ramix frame, wait for protocol failure, and
assert receive statistics remain zero.

Add `TestReportConnectionErrorCountsBeforeApplicationCallback` with a test connection
implementing `statsTransport() Transport`. Call `reportConnectionError` with a non-nil
error and assert the application callback runs and `TCP.ConnectionErrors == 1`.
Repeat subtests without a callback and with a panicking callback; create a fresh server
per subtest and require exactly one count per report.

- [ ] **Step 2: Run tests and observe zero counters**

Run:

```bash
go test ./... -run 'Test(TCP|WebSocket)ConnectionReceiveStats|TestReportConnectionErrorCounts' -count=1
```

Expected: FAIL because decode success and error reporting do not update statistics.

- [ ] **Step 3: Add private connection transport lookup**

In `stats.go`:

```go
type statsTransportProvider interface {
    statsTransport() Transport
}

func transportForStats(connection Connection) Transport {
    provider, ok := connection.(statsTransportProvider)
    if !ok {
        return 0
    }
    return provider.statsTransport()
}
```

Unknown test doubles remain harmless and the public `Connection` interface remains unchanged.

- [ ] **Step 4: Record decode success and reported errors**

Immediately after `Decoder.Decode` succeeds and before `handleRequest`, add to both transports:

```go
c.server.metrics.messageReceived(c.metricTransport, uint64(len(message.Body)))
```

After the nil-error guard at the start of `reportConnectionError`, add:

```go
s.metrics.connectionError(transportForStats(connection))
```

Do not count malformed/partial frames or failed message decoding.

- [ ] **Step 5: Format, test, and commit**

Run:

```bash
gofmt -w stats.go server.go tcp_connection.go ws_connection.go tcp_connection_test.go ws_connection_test.go server_state_test.go
go test ./... -run 'Test(TCP|WebSocket)ConnectionReceiveStats|TestReportConnectionErrorCounts|TestTCPConnectionWorkerQueueFull|TestWebSocket' -count=1
```

Expected: focused tests PASS without changing existing disconnect behavior.

Commit:

```bash
git add stats.go server.go tcp_connection.go ws_connection.go tcp_connection_test.go ws_connection_test.go server_state_test.go
git commit -m "feat: track receive and connection error statistics"
```

---

### Task 4: Track Queued Tasks, Rejections, and Completed Request Durations

**Files:**
- Modify: `context.go:8-68`
- Modify: `server.go:471-489`
- Modify: `worker_pool.go:61-82`
- Modify: `worker.go:10-29`
- Modify: `worker_pool_test.go`

- [ ] **Step 1: Write failing queue, rejection, and cancellation metric tests**

Add `TestWorkerPoolMetricsTrackQueuedAndRejectedTasks`, based on the existing queue-full
test. Attach one `serverMetrics` and TCP attribution to its contexts. While the first
task runs and the second is queued, assert `QueuedTasks == 1`; after the third
submission returns `ErrWorkerQueueFull`, assert `RejectedTasks == 1`; after drain,
assert `QueuedTasks == 0`.

Add `TestWorkerMetricsCanceledTaskIsDequeuedButNotCompleted`: cancel a
metrics-attributed task before submission, drain the pool, then assert queued returns
to zero and completed requests remains zero.

Add `TestWorkerPoolMetricsDoNotRejectWhenStopping`: stop accepting on a pool, submit a
metrics-attributed task, assert `ErrServerStopping`, and assert both queued and rejected
statistics remain zero.

- [ ] **Step 2: Write failing duration tests that exclude queue wait**

Add `TestWorkerMetricsRecordCompletedRequestDuration`: run one WebSocket-attributed
task whose handler blocks for at least 5ms. After drain, assert completed requests is
one, total duration is at least 5ms, and maximum equals total for the single request.

Add `TestWorkerMetricsExcludeQueueWaitFromDuration`: occupy the only worker with an
unattributed blocking task, queue a metrics-attributed second task for at least 200ms,
then release the first task. Make the second handler run for 5ms. After drain, assert
the recorded duration is at least 5ms but below 100ms. This deliberately leaves a
large margin for scheduler noise while proving the 200ms queue wait was not included.

- [ ] **Step 3: Run worker tests and verify accounting is absent**

Run:

```bash
go test ./... -run 'TestWorker(Pool)?Metrics' -count=1
```

Expected: FAIL because contexts and workers do not carry metric attribution.

- [ ] **Step 4: Attach metrics to request contexts**

Add private `metrics *serverMetrics` and `metricTransport Transport` fields to `Context`. In `Server.handleRequest`, immediately after `newContext`, assign `&s.metrics` and `transportForStats(connection)`.

Add nil-safe private context methods `taskQueued`, `taskDequeued`, `taskRejected`, and `requestCompleted(time.Duration)` that delegate to `serverMetrics` only when `metrics != nil`.

- [ ] **Step 5: Instrument race-safe submission accounting**

After `p.register(task)` and before channel send:

```go
task.taskQueued()
select {
case selectedWorker.tasks <- task:
    return nil
default:
    task.taskDequeued()
    task.taskRejected()
    p.unregister(task)
    return ErrWorkerQueueFull
}
```

Do not record a rejection when `accepting` is false and submission returns `ErrServerStopping`.

- [ ] **Step 6: Instrument dequeue and normal completion**

After receiving a non-nil task, call `task.taskDequeued()` before cancellation checking. For non-canceled tasks:

```go
started := time.Now()
task.Next()
task.requestCompleted(time.Since(started))
```

Import `time` in `worker.go`. Keep existing finish/unregister defers.

- [ ] **Step 7: Format, run worker tests, and race-check**

Run:

```bash
gofmt -w context.go server.go worker_pool.go worker.go worker_pool_test.go
go test ./... -run 'TestWorker|TestContext' -count=1
go test -race ./... -run 'TestWorker(Pool)?Metrics' -count=1
```

Expected: PASS with queue gauges balanced and no race reports.

- [ ] **Step 8: Commit worker accounting**

```bash
git add context.go server.go worker_pool.go worker.go worker_pool_test.go
git commit -m "feat: track queue and request duration statistics"
```

---

### Task 5: Verify TCP and WebSocket Statistics End to End

**Files:**
- Modify: `server_integration_test.go`
- Modify: `websocket_integration_test.go`

- [ ] **Step 1: Add an integration stats polling helper**

Add `waitForIntegrationStats`, polling `server.Stats()` until `integrationTimeout`, sleeping one millisecond between attempts, and printing the final snapshot on timeout.

- [ ] **Step 2: Add `TestIntegration_TCPStatisticsSnapshot`**

Use a TCP-only server with a handler that sleeps 5ms and sends `echo:<body>`. Verify:

- one active TCP connection after dialing;
- one received message/five body bytes for `hello`;
- one sent message/ten body bytes for `echo:hello`;
- one completed request, zero queued tasks, total duration at least 5ms, and maximum equal to total;
- WebSocket remains zero and `Total == TCP`;
- active connections return to zero after client close;
- explicit shutdown does not erase cumulative counters.

- [ ] **Step 3: Add error and concurrent snapshot checks to existing TCP tests**

Extend the malformed-client test to wait for `TCP.ConnectionErrors >= 1`. In the concurrent-clients test, run a goroutine that repeatedly calls `server.Stats()` until traffic finishes, then join it. This is the real-traffic race coverage.

- [ ] **Step 4: Add `TestIntegration_WebSocketStatisticsSnapshot`**

Mirror the TCP test with a WebSocket-only server and binary Ramix message. Verify WebSocket and `Total`, while TCP stays zero; close the client and wait for the active gauge to return to zero.

- [ ] **Step 5: Extend WebSocket queue saturation assertions**

After the existing saturation test observes `ErrWorkerQueueFull`, wait for `WebSocket.RejectedTasks == 1`, assert `Total.RejectedTasks == 1` and TCP rejected tasks is zero, then release handlers and wait for `WebSocket.QueuedTasks == 0`.

- [ ] **Step 6: Run integration and race suites**

Run:

```bash
gofmt -w server_integration_test.go websocket_integration_test.go
go test ./... -run 'TestIntegration_.*StatisticsSnapshot|TestIntegration_.*MalformedClientIsIsolated|TestIntegration_WebSocketWorkerQueueSaturationIsIsolated' -count=1
go test -race ./... -run 'TestIntegration_TCPConcurrentClientsPreservePerConnectionOrder|TestIntegration_.*StatisticsSnapshot' -count=1
```

Expected: all tests PASS and no races are reported.

- [ ] **Step 7: Commit integration coverage**

```bash
git add server_integration_test.go websocket_integration_test.go
git commit -m "test: cover server statistics end to end"
```

---

### Task 6: Document the Snapshot Contract and Run Full Verification

**Files:**
- Modify: `README.md:87-107`
- Modify: `README-CN.md:87-107`

- [ ] **Step 1: Add English statistics documentation**

Add a feature bullet and a `## Statistics` section between Sending Messages and Worker Pool:

```go
stats := server.Stats()
log.Printf(
    "connections=%d received=%d sent=%d queued=%d rejected=%d",
    stats.Total.ActiveConnections,
    stats.Total.ReceivedMessages,
    stats.Total.SentMessages,
    stats.Total.QueuedTasks,
    stats.Total.RejectedTasks,
)
```

State that values accumulate from construction, remain readable after shutdown, are individually atomic but not transactionally consistent across fields, and body-byte counters exclude protocol/transport headers.

- [ ] **Step 2: Add equivalent Chinese documentation**

Add the same example under `## 运行统计`, preserving API names and the exact lifetime, approximate-snapshot, and body-byte semantics.

- [ ] **Step 3: Run complete verification**

Run:

```bash
gofmt -w stats.go stats_test.go server.go connection.go context.go worker_pool.go worker.go tcp_connection.go ws_connection.go connection_test.go tcp_connection_test.go ws_connection_test.go worker_pool_test.go server_state_test.go server_integration_test.go websocket_integration_test.go
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
git diff --check
git status --short
```

Expected: all commands exit 0; status contains only files named by this plan.

- [ ] **Step 4: Inspect public API documentation**

Run:

```bash
go doc . Server.Stats
go doc . ServerStats
go doc . TransportStats
```

Expected: all three are present with lifetime and approximate-snapshot documentation.

- [ ] **Step 5: Commit documentation and final adjustments**

```bash
git add README.md README-CN.md stats.go stats_test.go server.go connection.go context.go worker_pool.go worker.go tcp_connection.go ws_connection.go connection_test.go tcp_connection_test.go ws_connection_test.go worker_pool_test.go server_state_test.go server_integration_test.go websocket_integration_test.go
git commit -m "docs: document server statistics snapshots"
```

- [ ] **Step 6: Verify the committed tree is clean**

Run:

```bash
git status --short
git log --oneline -6
```

Expected: empty status and one focused commit for each task.
