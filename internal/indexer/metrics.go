package indexer

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// IndexerMetrics exposes Prometheus counters and gauges for the indexer engine.
type IndexerMetrics struct {
	EventsProcessed       prometheus.Counter
	PollErrors            prometheus.Counter
	ProcessErrors         prometheus.Counter
	LastLedger            prometheus.Gauge
	ReconcilerRuns        prometheus.Counter
	DedupSize             prometheus.Gauge
	CursorLagSeconds      prometheus.Gauge
	UnknownContractEvents prometheus.Counter
}

// NewIndexerMetrics creates and registers all indexer Prometheus metrics.
func NewIndexerMetrics() *IndexerMetrics {
	return &IndexerMetrics{
		EventsProcessed: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_events_processed_total",
			Help: "Total indexer events processed",
		}),
		PollErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_poll_errors_total",
			Help: "Total indexer poll errors",
		}),
		ProcessErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_process_errors_total",
			Help: "Total indexer process errors",
		}),
		LastLedger: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "moistello_indexer_last_ledger",
			Help: "Last processed ledger number",
		}),
		ReconcilerRuns: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_reconciler_runs_total",
			Help: "Total reconciler runs",
		}),
		DedupSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "moistello_indexer_dedup_size",
			Help: "Number of tracked dedup hashes",
		}),
		CursorLagSeconds: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "moistello_indexer_cursor_lag_seconds",
			Help: "Seconds since the indexer cursor was last advanced",
		}),
		UnknownContractEvents: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_unknown_contract_events_total",
			Help: "Total contract events skipped because they came from an unknown contract",
		}),
	}
}
