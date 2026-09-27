package contractevent

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ContractEvent is one row of the contract_events audit log written by the
// indexer. The log is append-only, so a row is never updated or deleted.
type ContractEvent struct {
	ID         uuid.UUID `json:"id" db:"id"`
	TxHash     string    `json:"txHash" db:"tx_hash"`
	Ledger     int64     `json:"ledger" db:"ledger"`
	ContractID string    `json:"contractId" db:"contract_id"`
	EventType  string    `json:"eventType" db:"event_type"`
	// ContractVersion is the executable (WASM) hash of the contract that emitted
	// the event. It identifies the code that produced the event across upgrades,
	// which leave the contract ID unchanged. The literal "unknown" means the
	// version could not be resolved when the event was recorded; it is never
	// empty, so a caller never has to treat a blank value as a third state.
	ContractVersion string `json:"contractVersion" db:"contract_version"`
	// Payload is the decoded event data as stored, left raw so a caller can
	// decode it into the shape matching EventType without the repository needing
	// to know every event type's payload.
	Payload     json.RawMessage `json:"payload,omitempty" db:"payload"`
	ProcessedAt time.Time       `json:"processedAt" db:"processed_at"`
}
