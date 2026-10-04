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
	// ReorgsDetected counts ledger reorganizations that were detected and
	// rolled back (#346).
	ReorgsDetected prometheus.Counter
	// DeadLettered counts events recorded in the indexer dead-letter queue
	// because they could not be processed (#349).
	DeadLettered prometheus.Counter
	// ContractVersionUnknown counts events recorded with an unknown contract
	// version because the deployed version could not be resolved. A sustained
	// rate means version resolution is broken, and those rows are permanently
	// unattributable to a contract version.
	ContractVersionUnknown prometheus.Counter
	// Events carries the per-event-type counters. It is nil only in tests that
	// deliberately build a partial IndexerMetrics.
	Events *EventCounters
}

// EventCounters are per-event-type processing counters, labelled by event type
// rather than defined one metric per type. That is what makes a brand new
// contract event type observable with no code change: the counters are
// incremented at the single dispatch choke point, so the first time a contract
// emits an event the indexer has never seen, a new event_type label appears on
// /metrics. No metric registration, no switch arm, no redeploy.
//
// The four counters partition the received events, so for every event type:
//
//	received == decoded + failed + dlq
//
// and therefore received is always >= the sum of the other three. Events that
// fail to decode at all are the one thing that cannot appear here: an event
// that cannot be decoded frequently has no readable type name. Those are
// counted separately by DecodeSkipped.
type EventCounters struct {
	// Received counts every contract event the indexer decoded off the chain
	// and handed to the processor, whatever contract it came from.
	Received *prometheus.CounterVec
	// Decoded counts events applied to domain state without error.
	Decoded *prometheus.CounterVec
	// Failed counts events whose handler returned an error or panicked. The
	// event may be partially applied; the operator needs to look.
	Failed *prometheus.CounterVec
	// DLQ counts events that were not applied and will not be retried in
	// place: they come from a contract the indexer does not track, so they are
	// only written to the contract_events log to be decoded later.
	DLQ *prometheus.CounterVec
	// DecodeSkipped counts events that could not be decoded at all, labelled by
	// why. It is deliberately not per event type — an undecodable event often
	// has no readable type name, and inventing one would make the per-type
	// counters lie.
	DecodeSkipped *prometheus.CounterVec
}

// NewEventCounters builds the per-event-type counters and registers them
// against reg. Tests pass a private registry so repeated construction does not
// trip duplicate-registration panics.
func NewEventCounters(reg prometheus.Registerer) *EventCounters {
	c := &EventCounters{
		Received: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "moistello_indexer_events_received_total",
			Help: "Contract events received off chain, by event type",
		}, []string{"event_type"}),
		Decoded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "moistello_indexer_events_decoded_total",
			Help: "Contract events successfully applied to domain state, by event type",
		}, []string{"event_type"}),
		Failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "moistello_indexer_events_failed_total",
			Help: "Contract events whose handler errored or panicked, by event type",
		}, []string{"event_type"}),
		DLQ: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "moistello_indexer_events_dlq_total",
			Help: "Contract events skipped and left in the contract_events log for later decoding, by event type",
		}, []string{"event_type"}),
		DecodeSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "moistello_indexer_events_decode_skipped_total",
			Help: "Contract events that could not be decoded, by reason",
		}, []string{"reason"}),
	}
	reg.MustRegister(c.Received, c.Decoded, c.Failed, c.DLQ, c.DecodeSkipped)
	return c
}

// Count records one event's outcome across the per-type counters. Keeping the
// whole partition in one method is what makes received ==
// decoded+failed+dlq hold for every event type: callers cannot increment a
// subset of the four and skew the totals.
func (c *EventCounters) Count(eventType string, outcome Outcome) {
	if c == nil || eventType == "" {
		return
	}
	c.Received.WithLabelValues(eventType).Inc()
	switch outcome {
	case OutcomeDecoded:
		c.Decoded.WithLabelValues(eventType).Inc()
	case OutcomeFailed:
		c.Failed.WithLabelValues(eventType).Inc()
	case OutcomeDLQ:
		c.DLQ.WithLabelValues(eventType).Inc()
	}
}

// Outcome is how one contract event fared in the processor.
type Outcome int

const (
	// OutcomeDecoded means the event was applied to domain state.
	OutcomeDecoded Outcome = iota
	// OutcomeFailed means the handler errored or panicked.
	OutcomeFailed
	// OutcomeDLQ means the event was skipped and only recorded for later.
	OutcomeDLQ
)

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
		ReorgsDetected: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_reorgs_detected_total",
			Help: "Total ledger reorganizations detected and rolled back",
		}),
		DeadLettered: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_dead_lettered_total",
			Help: "Total indexer events recorded in the dead-letter queue after a processing failure",
		}),
		ContractVersionUnknown: promauto.NewCounter(prometheus.CounterOpts{
			Name: "moistello_indexer_contract_version_unknown_total",
			Help: "Total contract events recorded with an unknown contract version because the deployed version could not be resolved",
		}),
		Events: NewEventCounters(prometheus.DefaultRegisterer),
	}
}
