# Stats Export Handlers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add zero-dependency HTTP handlers that export `Server.Stats()` as JSON and Prometheus text exposition.

**Architecture:** Add a focused `stats_export.go` unit containing two public handler constructors plus private JSON and Prometheus formatting helpers. The handlers read `server.Stats()` once per request, validate HTTP method and nil server state, and do not start or own an HTTP server. Tests live in `stats_export_test.go` and exercise HTTP behavior, output shape, metric semantics, and race-safe concurrent use.

**Tech Stack:** Go 1.20, standard library `net/http`, `net/http/httptest`, `encoding/json`, existing Ramix `serverMetrics` test hooks, `go test`, race detector, `go vet`, `golangci-lint v2.12.0`.

---

## File Structure

- Create `stats_export.go`
  - Public API: `StatsJSONHandler(*Server) http.Handler`, `StatsPrometheusHandler(*Server) http.Handler`.
  - Private export structs for stable snake_case JSON.
  - Private Prometheus formatting helpers with deterministic metric family order.
  - Shared method and nil-server handling helpers.
- Create `stats_export_test.go`
  - Unit tests for JSON, Prometheus, method handling, nil server behavior, HEAD behavior, and concurrent handler access.
- Modify `README.md`
  - Add a short "Exporting Statistics" section with `/stats` and `/metrics` example.
- Modify `README-CN.md`
  - Add the same guidance in Chinese.

## Prometheus Output Contract

Tests should assert metric families in this order:

1. `ramix_active_connections`
2. `ramix_queued_tasks`
3. `ramix_received_messages_total`
4. `ramix_received_bytes_total`
5. `ramix_sent_messages_total`
6. `ramix_sent_bytes_total`
7. `ramix_rejected_tasks_total`
8. `ramix_connection_errors_total`
9. `ramix_completed_requests_total`
10. `ramix_request_duration_seconds_total`
11. `ramix_request_duration_seconds_max`

Each family emits `# HELP`, `# TYPE`, then two samples in `transport="tcp"` and
`transport="websocket"` order. Do not emit `transport="total"`.

---

### Task 1: HTTP Handler Method And Nil-Server Behavior

**Files:**
- Create: `stats_export_test.go`
- Create: `stats_export.go`

- [ ] **Step 1: Write failing tests for method and nil-server behavior**

Add tests:

```go
func TestStatsExportHandlersRejectNilServer(t *testing.T) {
    handlers := map[string]http.Handler{
        "json":       StatsJSONHandler(nil),
        "prometheus": StatsPrometheusHandler(nil),
    }
    for name, handler := range handlers {
        t.Run(name, func(t *testing.T) {
            recorder := httptest.NewRecorder()
            handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
            if recorder.Code != http.StatusInternalServerError {
                t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
            }
        })
    }
}

func TestStatsExportHandlersRejectUnsupportedMethods(t *testing.T) {
    server := newStatsExportServer(t)
    handlers := map[string]http.Handler{
        "json":       StatsJSONHandler(server),
        "prometheus": StatsPrometheusHandler(server),
    }
    for name, handler := range handlers {
        t.Run(name, func(t *testing.T) {
            recorder := httptest.NewRecorder()
            handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/metrics", nil))
            if recorder.Code != http.StatusMethodNotAllowed {
                t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
            }
            if got := recorder.Header().Get("Allow"); got != "GET, HEAD" {
                t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
            }
        })
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestStatsExportHandlersReject' -count=1`

Expected: FAIL to compile because `StatsJSONHandler`, `StatsPrometheusHandler`, and
`newStatsExportServer` do not exist.

- [ ] **Step 3: Implement minimal handler constructors and shared request validation**

In `stats_export.go`, add:

```go
package ramix

import (
    "net/http"
)

func StatsJSONHandler(server *Server) http.Handler {
    return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
        if !allowStatsExportRequest(writer, request) || !requireStatsServer(writer, server) {
            return
        }
        writer.Header().Set("Content-Type", "application/json; charset=utf-8")
        if request.Method == http.MethodHead {
            return
        }
        _, _ = writer.Write([]byte("{}\n"))
    })
}

func StatsPrometheusHandler(server *Server) http.Handler {
    return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
        if !allowStatsExportRequest(writer, request) || !requireStatsServer(writer, server) {
            return
        }
        writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
        if request.Method == http.MethodHead {
            return
        }
        _, _ = writer.Write([]byte(""))
    })
}
```

Add helper functions:

```go
func allowStatsExportRequest(writer http.ResponseWriter, request *http.Request) bool {
    if request.Method == http.MethodGet || request.Method == http.MethodHead {
        return true
    }
    writer.Header().Set("Allow", "GET, HEAD")
    http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
    return false
}

func requireStatsServer(writer http.ResponseWriter, server *Server) bool {
    if server != nil {
        return true
    }
    http.Error(writer, "ramix stats server is nil", http.StatusInternalServerError)
    return false
}
```

In `stats_export_test.go`, add `newStatsExportServer(t)` using `NewServer()` and
failing the test on construction error.

- [ ] **Step 4: Run tests to verify Task 1 passes**

Run: `go test ./... -run 'TestStatsExportHandlersReject' -count=1`

Expected: PASS.

- [ ] **Step 5: Format and commit Task 1**

Run:

```bash
gofmt -w stats_export.go stats_export_test.go
git diff --check
git add stats_export.go stats_export_test.go
git commit -m "feat: add stats export handler shell"
```

---

### Task 2: JSON Statistics Export

**Files:**
- Modify: `stats_export.go`
- Modify: `stats_export_test.go`

- [ ] **Step 1: Write failing JSON output tests**

Add `TestStatsJSONHandlerExportsSnapshot`:

```go
func TestStatsJSONHandlerExportsSnapshot(t *testing.T) {
    server := newStatsExportServer(t)
    seedStatsExportMetrics(server)

    recorder := httptest.NewRecorder()
    StatsJSONHandler(server).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stats", nil))

    if recorder.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
    }
    assertContentType(t, recorder, "application/json; charset=utf-8")

    var body map[string]map[string]uint64
    if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
        t.Fatalf("json.Unmarshal() error = %v; body = %s", err, recorder.Body.String())
    }

    assertJSONTransportStats(t, body["tcp"], wantTCPExport())
    assertJSONTransportStats(t, body["websocket"], wantWebSocketExport())
    assertJSONTransportStats(t, body["total"], wantTotalExport())
}
```

Add `TestStatsJSONHandlerHeadOmitsBody` to verify status/header without body.

Use helper functions that compare expected fields:

```go
func wantTCPExport() map[string]uint64
func wantWebSocketExport() map[string]uint64
func wantTotalExport() map[string]uint64
```

Seed metrics through existing private helpers because tests are in package `ramix`:

```go
func seedStatsExportMetrics(server *Server) {
    server.metrics.connectionOpened(TransportTCP)
    server.metrics.connectionOpened(TransportTCP)
    server.metrics.taskQueued(TransportTCP)
    server.metrics.messageReceived(TransportTCP, 128)
    server.metrics.messageSent(TransportTCP, 64)
    server.metrics.taskRejected(TransportTCP)
    server.metrics.connectionError(TransportTCP)
    server.metrics.requestCompleted(TransportTCP, 1500*time.Millisecond)

    server.metrics.connectionOpened(TransportWebSocket)
    server.metrics.messageReceived(TransportWebSocket, 32)
    server.metrics.messageSent(TransportWebSocket, 16)
    server.metrics.requestCompleted(TransportWebSocket, 250*time.Millisecond)
}
```

- [ ] **Step 2: Run JSON tests to verify they fail**

Run: `go test ./... -run 'TestStatsJSONHandler' -count=1`

Expected: FAIL because the handler currently returns `{}` and omits required fields.

- [ ] **Step 3: Implement JSON export structs and conversion**

In `stats_export.go`, add:

```go
type statsJSONSnapshot struct {
    Total     statsJSONTransport `json:"total"`
    TCP       statsJSONTransport `json:"tcp"`
    WebSocket statsJSONTransport `json:"websocket"`
}

type statsJSONTransport struct {
    ActiveConnections       uint64 `json:"active_connections"`
    QueuedTasks             uint64 `json:"queued_tasks"`
    ReceivedMessages        uint64 `json:"received_messages"`
    ReceivedBytes           uint64 `json:"received_bytes"`
    SentMessages            uint64 `json:"sent_messages"`
    SentBytes               uint64 `json:"sent_bytes"`
    RejectedTasks           uint64 `json:"rejected_tasks"`
    ConnectionErrors        uint64 `json:"connection_errors"`
    CompletedRequests       uint64 `json:"completed_requests"`
    TotalRequestDurationNS  int64  `json:"total_request_duration_ns"`
    MaximumRequestDurationNS int64 `json:"maximum_request_duration_ns"`
}
```

Use `encoding/json` and convert `time.Duration` with `int64(duration)`. Update
`StatsJSONHandler` to encode `statsJSONSnapshotFrom(server.Stats())` and append a
newline through `json.Encoder`.

- [ ] **Step 4: Run JSON tests to verify they pass**

Run: `go test ./... -run 'TestStatsJSONHandler' -count=1`

Expected: PASS.

- [ ] **Step 5: Format and commit Task 2**

Run:

```bash
gofmt -w stats_export.go stats_export_test.go
git diff --check
git add stats_export.go stats_export_test.go
git commit -m "feat: export server stats as json"
```

---

### Task 3: Prometheus Statistics Export

**Files:**
- Modify: `stats_export.go`
- Modify: `stats_export_test.go`

- [ ] **Step 1: Write failing Prometheus output tests**

Add `TestStatsPrometheusHandlerExportsMetrics`:

```go
func TestStatsPrometheusHandlerExportsMetrics(t *testing.T) {
    server := newStatsExportServer(t)
    seedStatsExportMetrics(server)

    recorder := httptest.NewRecorder()
    StatsPrometheusHandler(server).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

    if recorder.Code != http.StatusOK {
        t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
    }
    assertContentType(t, recorder, "text/plain; version=0.0.4; charset=utf-8")

    body := recorder.Body.String()
    assertPrometheusContains(t, body, "# TYPE ramix_active_connections gauge")
    assertPrometheusContains(t, body, `ramix_active_connections{transport="tcp"} 2`)
    assertPrometheusContains(t, body, `ramix_active_connections{transport="websocket"} 1`)
    assertPrometheusContains(t, body, "# TYPE ramix_received_messages_total counter")
    assertPrometheusContains(t, body, `ramix_received_messages_total{transport="tcp"} 1`)
    assertPrometheusContains(t, body, `ramix_request_duration_seconds_total{transport="tcp"} 1.5`)
    assertPrometheusContains(t, body, `ramix_request_duration_seconds_max{transport="websocket"} 0.25`)
    if strings.Contains(body, `transport="total"`) {
        t.Fatalf("Prometheus output contains transport total series:\n%s", body)
    }
}
```

Add a helper that verifies family ordering:

```go
func assertPrometheusFamilyOrder(t *testing.T, body string, families []string)
```

Add `TestStatsPrometheusHandlerHeadOmitsBody`.

- [ ] **Step 2: Run Prometheus tests to verify they fail**

Run: `go test ./... -run 'TestStatsPrometheusHandler' -count=1`

Expected: FAIL because the handler currently returns no metrics.

- [ ] **Step 3: Implement deterministic Prometheus formatting**

In `stats_export.go`, add a private descriptor type:

```go
type prometheusMetric struct {
    name string
    help string
    typ  string
    value func(TransportStats) string
}
```

Implement an ordered slice for the 11 metric families from the spec. Use `fmt.Fprintf`
to write:

```text
# HELP <name> <help>
# TYPE <name> <type>
<name>{transport="tcp"} <value>
<name>{transport="websocket"} <value>
```

Use decimal seconds for duration metrics:

```go
func prometheusSeconds(duration time.Duration) string {
    return strconv.FormatFloat(duration.Seconds(), 'f', -1, 64)
}
```

Use `strconv.FormatUint(value, 10)` for uint64 metrics.

- [ ] **Step 4: Run Prometheus tests to verify they pass**

Run: `go test ./... -run 'TestStatsPrometheusHandler' -count=1`

Expected: PASS.

- [ ] **Step 5: Format and commit Task 3**

Run:

```bash
gofmt -w stats_export.go stats_export_test.go
git diff --check
git add stats_export.go stats_export_test.go
git commit -m "feat: export server stats as prometheus metrics"
```

---

### Task 4: Documentation And Full Verification

**Files:**
- Modify: `README.md`
- Modify: `README-CN.md`
- Modify as needed: `stats_export.go`
- Modify as needed: `stats_export_test.go`

- [ ] **Step 1: Add README usage examples**

In `README.md`, after the existing "Statistics" section or near "Worker Pool" if
there is no dedicated section, add:

````markdown
## Exporting Statistics

Ramix provides standard-library HTTP handlers for exposing `server.Stats()`:

```go
adminMux := http.NewServeMux()
adminMux.Handle("/stats", ramix.StatsJSONHandler(server))
adminMux.Handle("/metrics", ramix.StatsPrometheusHandler(server))

go func() {
    _ = http.ListenAndServe(":9090", adminMux)
}()
```

`/stats` returns JSON with `total`, `tcp`, and `websocket` snapshots. `/metrics`
returns Prometheus text exposition with per-transport samples. Ramix only provides
the handlers; applications own the admin server, authentication, and shutdown.
````

Add the equivalent Chinese copy to `README-CN.md`.

- [ ] **Step 2: Run documentation-adjacent checks**

Run:

```bash
go test ./... -run 'TestStats.*Handler' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 3: Run full verification**

Run:

```bash
gofmt -w stats_export.go stats_export_test.go
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.0 run
git diff --check
git status --short
```

Expected:

- `go test ./... -count=1`: PASS.
- `go test -race ./... -count=1`: PASS.
- `go vet ./...`: no output, exit 0.
- `golangci-lint`: `0 issues.`
- `git diff --check`: no output, exit 0.
- `git status --short`: only intended README or implementation files before the final commit.

- [ ] **Step 4: Commit documentation and any final fixes**

Run:

```bash
git add README.md README-CN.md stats_export.go stats_export_test.go
git commit -m "docs: document stats export handlers"
```

If no implementation files changed in this task, stage only the README files.

---

## Final Review Checklist

- [ ] Public API matches the spec exactly:
  - `func StatsJSONHandler(server *Server) http.Handler`
  - `func StatsPrometheusHandler(server *Server) http.Handler`
- [ ] Handlers support only `GET` and `HEAD`; unsupported methods return `405` and `Allow: GET, HEAD`.
- [ ] Nil server requests return `500` and do not panic.
- [ ] JSON uses snake_case fields and duration nanoseconds with `_ns` suffixes.
- [ ] JSON includes `total`, `tcp`, and `websocket`.
- [ ] Prometheus emits only `tcp` and `websocket` transport labels.
- [ ] Prometheus emits deterministic `# HELP`, `# TYPE`, and sample order.
- [ ] Prometheus durations use seconds.
- [ ] No third-party dependencies were added.
- [ ] README and README-CN state that applications own the admin HTTP server and security boundary.
