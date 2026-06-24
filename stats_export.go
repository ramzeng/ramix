package ramix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type prometheusMetric struct {
	name  string
	help  string
	typ   string
	value func(TransportStats) string
}

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

var statsPrometheusMetrics = []prometheusMetric{
	{
		name:  "ramix_active_connections",
		help:  "Approximate number of currently open Ramix connections.",
		typ:   "gauge",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.ActiveConnections }),
	},
	{
		name:  "ramix_queued_tasks",
		help:  "Approximate number of Ramix request tasks currently waiting to run.",
		typ:   "gauge",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.QueuedTasks }),
	},
	{
		name:  "ramix_received_messages_total",
		help:  "Lifetime-cumulative number of received Ramix messages.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.ReceivedMessages }),
	},
	{
		name:  "ramix_received_bytes_total",
		help:  "Lifetime-cumulative number of received Ramix message body bytes, excluding protocol headers and transport overhead.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.ReceivedBytes }),
	},
	{
		name:  "ramix_sent_messages_total",
		help:  "Lifetime-cumulative number of sent Ramix messages.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.SentMessages }),
	},
	{
		name:  "ramix_sent_bytes_total",
		help:  "Lifetime-cumulative number of sent Ramix message body bytes, excluding protocol headers and transport overhead.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.SentBytes }),
	},
	{
		name:  "ramix_rejected_tasks_total",
		help:  "Lifetime-cumulative number of Ramix request tasks rejected because worker queues were full.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.RejectedTasks }),
	},
	{
		name:  "ramix_connection_errors_total",
		help:  "Lifetime-cumulative number of reported Ramix connection errors.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.ConnectionErrors }),
	},
	{
		name:  "ramix_completed_requests_total",
		help:  "Lifetime-cumulative number of completed Ramix request handlers.",
		typ:   "counter",
		value: prometheusUint64(func(stats TransportStats) uint64 { return stats.CompletedRequests }),
	},
	{
		name:  "ramix_request_duration_seconds_total",
		help:  "Saturated lifetime-cumulative Ramix request handler duration in seconds.",
		typ:   "counter",
		value: prometheusDurationSeconds(func(stats TransportStats) time.Duration { return stats.TotalRequestDuration }),
	},
	{
		name:  "ramix_request_duration_seconds_max",
		help:  "Maximum observed Ramix request handler duration in seconds.",
		typ:   "gauge",
		value: prometheusDurationSeconds(func(stats TransportStats) time.Duration { return stats.MaximumRequestDuration }),
	},
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
		writePrometheusStats(writer, server.Stats())
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

func writePrometheusStats(writer http.ResponseWriter, stats ServerStats) {
	for _, metric := range statsPrometheusMetrics {
		_, _ = fmt.Fprintf(writer, "# HELP %s %s\n", metric.name, metric.help)
		_, _ = fmt.Fprintf(writer, "# TYPE %s %s\n", metric.name, metric.typ)
		_, _ = fmt.Fprintf(writer, "%s{transport=\"tcp\"} %s\n", metric.name, metric.value(stats.TCP))
		_, _ = fmt.Fprintf(writer, "%s{transport=\"websocket\"} %s\n", metric.name, metric.value(stats.WebSocket))
	}
}

func prometheusUint64(get func(TransportStats) uint64) func(TransportStats) string {
	return func(stats TransportStats) string {
		return strconv.FormatUint(get(stats), 10)
	}
}

func prometheusDurationSeconds(get func(TransportStats) time.Duration) func(TransportStats) string {
	return func(stats TransportStats) string {
		return strconv.FormatFloat(get(stats).Seconds(), 'f', -1, 64)
	}
}
