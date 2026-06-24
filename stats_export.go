package ramix

import (
	"encoding/json"
	"net/http"
)

type statsJSONSnapshot struct {
	Total     statsJSONTransport `json:"total"`
	TCP       statsJSONTransport `json:"tcp"`
	WebSocket statsJSONTransport `json:"websocket"`
}

type statsJSONTransport struct {
	ActiveConnections        uint64 `json:"active_connections"`
	QueuedTasks              uint64 `json:"queued_tasks"`
	ReceivedMessages         uint64 `json:"received_messages"`
	ReceivedBytes            uint64 `json:"received_bytes"`
	SentMessages             uint64 `json:"sent_messages"`
	SentBytes                uint64 `json:"sent_bytes"`
	RejectedTasks            uint64 `json:"rejected_tasks"`
	ConnectionErrors         uint64 `json:"connection_errors"`
	CompletedRequests        uint64 `json:"completed_requests"`
	TotalRequestDurationNS   int64  `json:"total_request_duration_ns"`
	MaximumRequestDurationNS int64  `json:"maximum_request_duration_ns"`
}

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
		if err := json.NewEncoder(writer).Encode(statsJSONSnapshotFrom(server.Stats())); err != nil {
			return
		}
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

func statsJSONSnapshotFrom(stats ServerStats) statsJSONSnapshot {
	return statsJSONSnapshot{
		Total:     statsJSONTransportFrom(stats.Total),
		TCP:       statsJSONTransportFrom(stats.TCP),
		WebSocket: statsJSONTransportFrom(stats.WebSocket),
	}
}

func statsJSONTransportFrom(stats TransportStats) statsJSONTransport {
	return statsJSONTransport{
		ActiveConnections:        stats.ActiveConnections,
		QueuedTasks:              stats.QueuedTasks,
		ReceivedMessages:         stats.ReceivedMessages,
		ReceivedBytes:            stats.ReceivedBytes,
		SentMessages:             stats.SentMessages,
		SentBytes:                stats.SentBytes,
		RejectedTasks:            stats.RejectedTasks,
		ConnectionErrors:         stats.ConnectionErrors,
		CompletedRequests:        stats.CompletedRequests,
		TotalRequestDurationNS:   int64(stats.TotalRequestDuration),
		MaximumRequestDurationNS: int64(stats.MaximumRequestDuration),
	}
}
