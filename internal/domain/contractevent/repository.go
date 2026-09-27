package contractevent

import "context"

// Repository reads the contract_events audit log written by the indexer.
type Repository interface {
	// List returns one page of events matching filter, newest ledger first, and
	// the total number of events matching that same filter.
	List(ctx context.Context, filter ListFilter, page, limit int) ([]ContractEvent, int, error)
}
