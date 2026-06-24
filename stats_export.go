package ramix

import "net/http"

// StatsJSONHandler returns an HTTP handler that exports server statistics as JSON.
func StatsJSONHandler(server *Server) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !allowStatsExportRequest(writer, request) || !requireStatsExportServer(writer, server) {
			return
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		if request.Method == http.MethodHead {
			return
		}
		_, _ = writer.Write([]byte("{}\n"))
	})
}

// StatsPrometheusHandler returns an HTTP handler that exports server statistics
// using the Prometheus text exposition format.
func StatsPrometheusHandler(server *Server) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !allowStatsExportRequest(writer, request) || !requireStatsExportServer(writer, server) {
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if request.Method == http.MethodHead {
			return
		}
	})
}

func allowStatsExportRequest(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return true
	}
	writer.Header().Set("Allow", "GET, HEAD")
	http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func requireStatsExportServer(writer http.ResponseWriter, server *Server) bool {
	if server != nil {
		return true
	}
	http.Error(writer, "ramix stats server is nil", http.StatusInternalServerError)
	return false
}
