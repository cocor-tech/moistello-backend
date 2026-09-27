# Contract Event Versioning

The indexer writes every decoded Soroban event to `contract_events`, an
append-only audit log. Each row records the **contract version** that emitted
it: the executable (WASM) hash of the contract at the time the event was
recorded.

## Why it exists

A Soroban contract ID is stable across an upgrade. The contract keeps its
address, keeps its storage, and swaps the code behind it, so the contract ID
alone cannot tell you which implementation produced a given event. That makes
historical events ambiguous across deployments: after an upgrade, a payload
recorded by v1 and one recorded by v2 look identical in the log.

Recording the WASM hash alongside each event keeps them distinguishable, so
events can be queried and interpreted per implementation.

## What is recorded

`contract_events.contract_version` holds the hex-encoded 32-byte executable hash,
or the literal string `unknown`.

The column is `NOT NULL`. `unknown` is used instead of `NULL` deliberately:

- It is a first-class, queryable value, so "show me the events whose version I
  could not determine" is a normal filter.
- A non-null column keeps a single equality comparison working, instead of
  quietly returning a different set of rows through a forgotten `IS NULL` branch.

Rows written before the column existed are backfilled to `unknown` by migration
`046_add_contract_event_version`. Their original version is not recoverable: the
code that produced them cannot be read back from the ledger after the fact.

## How the version is resolved

The version is **not** in the event XDR. The SDK's transaction meta does not
carry a contract's executable, so it cannot be decoded from `result_meta_xdr`
alongside the event.

It is read from the ledger instead: `getLedgerEntries` is called for the
contract's instance ledger entry, whose value is the contract instance, whose
`executable` field holds the WASM hash.

- `pkg/stellar/soroban/ledger.go` — `LedgerClient.GetContractWasmHash`.
- `internal/indexer/contract_version.go` — `ContractVersionResolver` and the
  caching wrapper.

Lookups are cached per contract for `DefaultContractVersionTTL` (15 minutes),
because the resolver is consulted once per event and a contract's executable
only changes on upgrade. Failures are cached for a much shorter window, so a
broken or misconfigured contract cannot cause a ledger read per event while a
transient network error still clears quickly.

## Known limitation

The hash is read from the contract's executable **as of the ledger the RPC node
currently serves**. It is therefore the version the contract runs *now*, not a
guaranteed record of the version at the event's historical ledger. If a contract
is upgraded between an event being emitted and its version being resolved, that
event is recorded against the newer version.

This is why the value is recorded rather than derived on read: recording it
still pins the answer at index time, which is strictly better than resolving it
later, and a read-time resolution would drift for every historical row.

For deployments where versions are known ahead of time,
`indexer.StaticContractVersions` resolves from a fixed map instead and has no
such window.

## Failure behaviour

Resolution never fails the write. An event that cannot be attributed to a
version is still recorded, as `unknown`, because dropping the audit row loses
more than the version does. Every such event increments
`moistello_indexer_contract_version_unknown_total` and is logged, so a
persistently broken resolver is visible on `/metrics` rather than silent.

## Querying

`internal/domain/contractevent` reads the log. `ListFilter.ContractVersion`
filters on the version, which is the point of the column:

```go
events, total, err := repo.List(ctx, contractevent.ListFilter{
    ContractID:      "CDABC…",
    ContractVersion: "9f2c…",
}, 1, 50)
```

`ContractVersion: "unknown"` selects exactly the events with no resolved
version. Omitting the field does not filter on version at all — the two are
deliberately different.

`ListFilter` also filters by `TxHash`, `ContractID`, `EventType`, and an
inclusive ledger range. Results are ordered newest ledger first with a stable
tie-break, and the total matching count is returned alongside the page.
