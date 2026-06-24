package ramix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
