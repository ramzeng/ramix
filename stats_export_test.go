package ramix

import (
	"net/http"
	"net/http/httptest"
	"testing"
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

func newStatsExportServer(t *testing.T) *Server {
	t.Helper()

	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}
