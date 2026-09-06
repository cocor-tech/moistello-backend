package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "moistello_http_requests_total",
		Help: "Total HTTP requests",
	}, []string{"method", "path", "status"})

	HTTPRequestsTotal = HTTPRequests

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "moistello_http_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	HTTPLatencySeconds = HTTPDuration

	HTTPErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "moistello_http_errors_total",
		Help: "Total HTTP error responses (status >= 400)",
	}, []string{"method", "path", "status"})

	WSActiveConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "moistello_websocket_active_connections",
		Help: "Current number of active WebSocket connections",
	})

	DBPoolUtilization = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "moistello_db_pool_utilization",
		Help: "Database connection pool stats and utilization",
	}, []string{"type"})

	RPCLatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "moistello_rpc_latency_seconds",
		Help:    "RPC call latency in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"service", "method"})

	CirclesCreated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "moistello_circles_created_total",
		Help: "Total circles created",
	})

	ActiveUsers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "moistello_active_users",
		Help: "Number of active users",
	})
)
