package indexer

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const DefaultStallThreshold = 5 * time.Minute

type StallWatchdog struct {
	mu           sync.Mutex
	threshold    time.Duration
	lastProgress time.Time
	alerted      bool
	now          func() time.Time
	alert        func(time.Duration)
}

func NewStallWatchdog(threshold time.Duration, alert func(time.Duration)) *StallWatchdog {
	return newStallWatchdogWithClock(threshold, alert, time.Now)
}

func newStallWatchdogWithClock(threshold time.Duration, alert func(time.Duration), now func() time.Time) *StallWatchdog {
	if threshold <= 0 {
		threshold = DefaultStallThreshold
	}
	if alert == nil {
		alert = func(stalledFor time.Duration) {
			log.Error().Str("security_event", "indexer_stalled").Dur("stalled_for", stalledFor).Msg("indexer has not processed a new ledger")
		}
	}
	return &StallWatchdog{threshold: threshold, lastProgress: now(), now: now, alert: alert}
}

func (w *StallWatchdog) Processed() {
	w.mu.Lock()
	w.lastProgress = w.now()
	w.alerted = false
	w.mu.Unlock()
}

func (w *StallWatchdog) Check() bool {
	w.mu.Lock()
	stalledFor := w.now().Sub(w.lastProgress)
	if stalledFor < w.threshold || w.alerted {
		w.mu.Unlock()
		return false
	}
	w.alerted = true
	alert := w.alert
	w.mu.Unlock()
	alert(stalledFor)
	return true
}

func (w *StallWatchdog) Run(ctx context.Context, stop <-chan struct{}) {
	interval := w.threshold / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.Check()
		case <-ctx.Done():
			return
		case <-stop:
			return
		}
	}
}
