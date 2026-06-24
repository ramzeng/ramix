# Stats Export Handlers Design

## Summary

Ramix now exposes `Server.Stats()` as a zero-dependency, concurrency-safe snapshot
API. Applications can poll that API directly, but production users still need a
small amount of repetitive glue code to expose those values to monitoring systems
or lightweight admin tooling.

This change adds standard-library HTTP handlers that export a server's statistics
as JSON and as Prometheus text exposition. The handlers do not start their own HTTP
server, bind ports, manage background goroutines, or join the Ramix server
lifecycle. Applications mount them on an existing `http.ServeMux`, admin server, or
debug server and own authentication, network exposure, and shutdown policy.

## Goals

- Provide a first-party way to export `Server.Stats()` over HTTP.
- Keep the feature zero-dependency and compatible with Go 1.20.
- Support both human/debug use through JSON and production scraping through
  Prometheus text format.
- Preserve the current Ramix server lifecycle and avoid adding monitoring ports or
  background workers to `Server.Run`.
- Make metric names, labels, duration units, and error behavior explicit enough for
  stable downstream use.

## Non-Goals

- Adding the Prometheus Go client library or OpenTelemetry.
- Starting or configuring an HTTP server inside Ramix.
- Adding authentication, authorization, TLS, CORS, rate limiting, or IP allowlists.
- Exporting histograms, quantiles, per-route, per-worker, or per-connection metrics.
- Adding resettable metrics or changing existing `Server.Stats()` semantics.
- Exposing a `transport="total"` Prometheus label value.

## Public API

Add two exported helpers:

```go
func StatsJSONHandler(server *Server) http.Handler
func StatsPrometheusHandler(server *Server) http.Handler
```

Both functions return a reusable `http.Handler`. They accept a `*Server` rather than
an interface so the API stays aligned with the existing concrete `Server.Stats()`
method and does not add a new public abstraction.

Typical usage:

```go
mux := http.NewServeMux()
mux.Handle("/stats", ramix.StatsJSONHandler(server))
mux.Handle("/metrics", ramix.StatsPrometheusHandler(server))

admin := &http.Server{
    Addr:    ":9090",
    Handler: mux,
}
go func() {
    _ = admin.ListenAndServe()
}()
```

The application owns the admin server's security boundary and lifecycle. Ramix only
provides handlers.

## HTTP Behavior

Both handlers support `GET` and `HEAD`.

- `GET` returns the exported statistics body.
- `HEAD` returns the same status and headers as `GET` without a response body.
- Any other method returns `405 Method Not Allowed` and sets `Allow: GET, HEAD`.
- A nil `*Server` returns `500 Internal Server Error` rather than panicking.

Normal requests do not have business-level failures because `Server.Stats()` cannot
return an error. Encoding or write errors are handled through the normal
`net/http` response path; the handlers do not log or retry.

## JSON Export

`StatsJSONHandler` returns `Content-Type: application/json; charset=utf-8`.

The JSON body mirrors `ServerStats` with snake_case field names. It includes
`total`, `tcp`, and `websocket` because JSON clients are likely to inspect the
snapshot directly rather than aggregate with a query language.

Durations are emitted as integer nanoseconds using explicit `_ns` field suffixes.
This avoids Go-specific duration strings and keeps the API stable for machines.

Example shape:

```json
{
  "total": {
    "active_connections": 2,
    "queued_tasks": 0,
    "received_messages": 10,
    "received_bytes": 512,
    "sent_messages": 10,
    "sent_bytes": 512,
    "rejected_tasks": 0,
    "connection_errors": 1,
    "completed_requests": 10,
    "total_request_duration_ns": 2500000,
    "maximum_request_duration_ns": 800000
  },
  "tcp": {
    "active_connections": 1,
    "queued_tasks": 0,
    "received_messages": 5,
    "received_bytes": 256,
    "sent_messages": 5,
    "sent_bytes": 256,
    "rejected_tasks": 0,
    "connection_errors": 1,
    "completed_requests": 5,
    "total_request_duration_ns": 1200000,
    "maximum_request_duration_ns": 600000
  },
  "websocket": {
    "active_connections": 1,
    "queued_tasks": 0,
    "received_messages": 5,
    "received_bytes": 256,
    "sent_messages": 5,
    "sent_bytes": 256,
    "rejected_tasks": 0,
    "connection_errors": 0,
    "completed_requests": 5,
    "total_request_duration_ns": 1300000,
    "maximum_request_duration_ns": 800000
  }
}
```

The JSON handler reads `server.Stats()` once per request and encodes that detached
snapshot. It does not cache or mutate statistics.

## Prometheus Export

`StatsPrometheusHandler` returns
`Content-Type: text/plain; version=0.0.4; charset=utf-8`.

It emits Prometheus text exposition without importing a Prometheus client library.
The output includes only per-transport samples:

- `transport="tcp"`
- `transport="websocket"`

It does not emit a `transport="total"` sample. Prometheus users should calculate
totals with `sum(...)`; emitting an extra total series would make common queries
double-count.

Metric names:

```text
ramix_active_connections{transport="tcp"}
ramix_queued_tasks{transport="tcp"}

ramix_received_messages_total{transport="tcp"}
ramix_received_bytes_total{transport="tcp"}
ramix_sent_messages_total{transport="tcp"}
ramix_sent_bytes_total{transport="tcp"}
ramix_rejected_tasks_total{transport="tcp"}
ramix_connection_errors_total{transport="tcp"}
ramix_completed_requests_total{transport="tcp"}

ramix_request_duration_seconds_total{transport="tcp"}
ramix_request_duration_seconds_max{transport="tcp"}
```

The same metrics are emitted for `transport="websocket"`.

Metric types:

- `ramix_active_connections` is a gauge.
- `ramix_queued_tasks` is a gauge.
- All `*_total` metrics are counters.
- `ramix_request_duration_seconds_max` is a gauge.

Duration values are exported as decimal seconds. The handler converts
`time.Duration` nanoseconds to seconds for Prometheus naming consistency. It does
not export histograms or summaries because `Server.Stats()` does not track bucketed
or sampled duration data.

The output includes `# HELP` and `# TYPE` lines for every metric family. Help text
must match the existing `Server.Stats()` definitions: byte metrics exclude Ramix
headers and transport overhead, duration metrics cover handler execution time, and
counter values are lifetime cumulative.

## Data Flow

For each request:

1. The handler validates the HTTP method and server pointer.
2. It calls `server.Stats()` exactly once.
3. The JSON handler converts the snapshot to small export structs with snake_case
   JSON tags and nanosecond duration fields.
4. The Prometheus handler formats TCP and WebSocket transport snapshots into text
   exposition samples.
5. The handler writes the response through the provided `http.ResponseWriter`.

No additional synchronization is needed because `Server.Stats()` already returns a
detached value snapshot.

## Testing Strategy

Unit tests should cover:

- JSON status code, content type, snake_case fields, `total`/`tcp`/`websocket`
  presence, and duration nanosecond serialization.
- Prometheus status code, content type, help/type lines, metric names, transport
  labels, counter/gauge type choices, and duration seconds conversion.
- Prometheus output excludes `transport="total"`.
- `GET` writes a body and `HEAD` does not.
- Unsupported methods return `405` with `Allow: GET, HEAD`.
- Nil server handlers return `500` without panicking.
- Concurrent requests to both handlers while stats are being updated are safe under
  `go test -race`.

Documentation should add a short README and README-CN section showing how to mount
both handlers on an application-owned admin HTTP server.

Verification commands:

```bash
gofmt -w <changed-go-files>
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.0 run
git diff --check
```

