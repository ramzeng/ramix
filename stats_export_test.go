package ramix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

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
			if got, want := recorder.Header().Get("Allow"), "GET, HEAD"; got != want {
				t.Fatalf("Allow = %q, want %q", got, want)
			}
		})
	}
}

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

func TestStatsJSONHandlerHeadOmitsBody(t *testing.T) {
	server := newStatsExportServer(t)
	seedStatsExportMetrics(server)

	recorder := httptest.NewRecorder()
	StatsJSONHandler(server).ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/stats", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	assertContentType(t, recorder, "application/json; charset=utf-8")
	if got := recorder.Body.String(); got != "" {
		t.Fatalf("HEAD body = %q, want empty", got)
	}
}

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
	assertPrometheusFamilyOrder(t, body, wantPrometheusFamilies())
	assertPrometheusContains(t, body, "# TYPE ramix_active_connections gauge")
	assertPrometheusContains(t, body, `ramix_active_connections{transport="tcp"} 2`)
	assertPrometheusContains(t, body, `ramix_active_connections{transport="websocket"} 1`)
	assertPrometheusContains(t, body, "# TYPE ramix_queued_tasks gauge")
	assertPrometheusContains(t, body, `ramix_queued_tasks{transport="tcp"} 1`)
	assertPrometheusContains(t, body, "# TYPE ramix_received_messages_total counter")
	assertPrometheusContains(t, body, `ramix_received_messages_total{transport="tcp"} 1`)
	assertPrometheusContains(t, body, `ramix_received_bytes_total{transport="websocket"} 32`)
	assertPrometheusContains(t, body, `ramix_sent_bytes_total{transport="tcp"} 64`)
	assertPrometheusContains(t, body, `ramix_rejected_tasks_total{transport="tcp"} 1`)
	assertPrometheusContains(t, body, `ramix_connection_errors_total{transport="tcp"} 1`)
	assertPrometheusContains(t, body, `ramix_completed_requests_total{transport="websocket"} 1`)
	assertPrometheusContains(t, body, `ramix_request_duration_seconds_total{transport="tcp"} 1.5`)
	assertPrometheusContains(t, body, `ramix_request_duration_seconds_max{transport="websocket"} 0.25`)
	if strings.Contains(body, `transport="total"`) {
		t.Fatalf("Prometheus output contains transport total series:\n%s", body)
	}
}

func TestStatsPrometheusHandlerHeadOmitsBody(t *testing.T) {
	server := newStatsExportServer(t)
	seedStatsExportMetrics(server)

	recorder := httptest.NewRecorder()
	StatsPrometheusHandler(server).ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	assertContentType(t, recorder, "text/plain; version=0.0.4; charset=utf-8")
	if got := recorder.Body.String(); got != "" {
		t.Fatalf("HEAD body = %q, want empty", got)
	}
}

func TestStatsExportHandlersServeConcurrentlyWithMetricUpdates(t *testing.T) {
	server := newStatsExportServer(t)
	handlers := []http.Handler{
		StatsJSONHandler(server),
		StatsPrometheusHandler(server),
	}

	var waitGroup sync.WaitGroup
	start := make(chan struct{})
	errors := make(chan string, 32)
	for workerID := 0; workerID < 8; workerID++ {
		waitGroup.Add(1)
		go func(workerID int) {
			defer waitGroup.Done()
			<-start
			for iteration := 0; iteration < 100; iteration++ {
				server.metrics.connectionOpened(TransportTCP)
				server.metrics.connectionClosed(TransportTCP)
				server.metrics.messageReceived(TransportWebSocket, uint64(iteration))
				server.metrics.requestCompleted(TransportTCP, time.Duration(iteration)*time.Nanosecond)

				recorder := httptest.NewRecorder()
				handler := handlers[(workerID+iteration)%len(handlers)]
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stats", nil))
				if recorder.Code != http.StatusOK {
					errors <- recorder.Body.String()
					return
				}
			}
		}(workerID)
	}
	close(start)
	waitGroup.Wait()
	close(errors)

	for err := range errors {
		t.Fatalf("concurrent stats export request failed: %s", err)
	}
}

func newStatsExportServer(t *testing.T) *Server {
	t.Helper()

	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

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

func assertContentType(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()

	if got := recorder.Header().Get("Content-Type"); got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
}

func assertJSONTransportStats(t *testing.T, got, want map[string]uint64) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON transport stats = %+v, want %+v", got, want)
	}
}

func assertPrometheusContains(t *testing.T, body, want string) {
	t.Helper()

	if !strings.Contains(body, want) {
		t.Fatalf("Prometheus output missing %q:\n%s", want, body)
	}
}

func assertPrometheusFamilyOrder(t *testing.T, body string, families []string) {
	t.Helper()

	previous := -1
	for _, family := range families {
		marker := "# HELP " + family + " "
		index := strings.Index(body, marker)
		if index == -1 {
			t.Fatalf("Prometheus output missing family %q:\n%s", family, body)
		}
		if index < previous {
			t.Fatalf("Prometheus family %q appears out of order:\n%s", family, body)
		}
		previous = index
	}
}

func wantPrometheusFamilies() []string {
	return []string{
		"ramix_active_connections",
		"ramix_queued_tasks",
		"ramix_received_messages_total",
		"ramix_received_bytes_total",
		"ramix_sent_messages_total",
		"ramix_sent_bytes_total",
		"ramix_rejected_tasks_total",
		"ramix_connection_errors_total",
		"ramix_completed_requests_total",
		"ramix_request_duration_seconds_total",
		"ramix_request_duration_seconds_max",
	}
}

func wantTCPExport() map[string]uint64 {
	return map[string]uint64{
		"active_connections":          2,
		"queued_tasks":                1,
		"received_messages":           1,
		"received_bytes":              128,
		"sent_messages":               1,
		"sent_bytes":                  64,
		"rejected_tasks":              1,
		"connection_errors":           1,
		"completed_requests":          1,
		"total_request_duration_ns":   1500 * uint64(time.Millisecond),
		"maximum_request_duration_ns": 1500 * uint64(time.Millisecond),
	}
}

func wantWebSocketExport() map[string]uint64 {
	return map[string]uint64{
		"active_connections":          1,
		"queued_tasks":                0,
		"received_messages":           1,
		"received_bytes":              32,
		"sent_messages":               1,
		"sent_bytes":                  16,
		"rejected_tasks":              0,
		"connection_errors":           0,
		"completed_requests":          1,
		"total_request_duration_ns":   250 * uint64(time.Millisecond),
		"maximum_request_duration_ns": 250 * uint64(time.Millisecond),
	}
}

func wantTotalExport() map[string]uint64 {
	return map[string]uint64{
		"active_connections":          3,
		"queued_tasks":                1,
		"received_messages":           2,
		"received_bytes":              160,
		"sent_messages":               2,
		"sent_bytes":                  80,
		"rejected_tasks":              1,
		"connection_errors":           1,
		"completed_requests":          2,
		"total_request_duration_ns":   1750 * uint64(time.Millisecond),
		"maximum_request_duration_ns": 1500 * uint64(time.Millisecond),
	}
}
