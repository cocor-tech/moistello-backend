package contractevent

// ListFilter narrows a contract event query. A zero-valued field is not
// filtered on, so an empty filter lists everything.
//
// ContractVersion is the reason this filter exists: the same contract ID is
// reused across upgrades, so filtering on it is what separates the events a
// given implementation produced from those of its predecessors. Passing
// "unknown" selects exactly the events whose version could not be resolved,
// which is distinct from omitting the field, which does not filter on version at
// all.
type ListFilter struct {
	// TxHash matches contract_events.tx_hash.
	TxHash string
	// ContractID matches contract_events.contract_id.
	ContractID string
	// EventType matches contract_events.event_type, e.g. "CircleCreated".
	EventType string
	// ContractVersion matches the executable (WASM) hash of the emitting
	// contract, or "unknown" for events with no resolved version.
	ContractVersion string
	// FromLedger is an inclusive lower bound on contract_events.ledger.
	FromLedger *int64
	// ToLedger is an inclusive upper bound on contract_events.ledger.
	ToLedger *int64
}

// IsZero reports whether the filter selects every event.
func (f ListFilter) IsZero() bool {
	return f.TxHash == "" &&
		f.ContractID == "" &&
		f.EventType == "" &&
		f.ContractVersion == "" &&
		f.FromLedger == nil &&
		f.ToLedger == nil
}

// Inverted reports whether the ledger bounds cannot both be satisfied, which
// callers should reject rather than silently return nothing.
func (f ListFilter) Inverted() bool {
	return f.FromLedger != nil && f.ToLedger != nil && *f.FromLedger > *f.ToLedger
}
