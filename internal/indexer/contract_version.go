package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/moistello/backend/pkg/stellar/soroban"
)

// ContractVersionUnknown is recorded when the deployed version of a contract
// could not be determined. It is stored as a literal string rather than NULL so
// the unknown state is queryable like any other version, and so a reader never
// has to remember an IS NULL branch that returns a different set of rows.
const ContractVersionUnknown = "unknown"

const (
	// DefaultContractVersionTTL is how long a successfully resolved contract
	// version is reused before the version is looked up again. A contract's
	// executable only changes on upgrade, so a comparatively long window keeps
	// the indexer to one ledger read per contract per window while still
	// following an upgrade promptly.
	DefaultContractVersionTTL = 15 * time.Minute

	// failureTTLDivisor derives how long a failed lookup is remembered from the
	// success TTL. Failures are cached too, otherwise a misconfigured or
	// nonexistent contract triggers one RPC call per indexed event, but for much
	// less time than successes so a transient network or node error is not
	// turned into a long stretch of events recorded as unknown.
	failureTTLDivisor = 10
)

// ContractVersionResolver reports the deployed version of a contract, which is
// the executable (WASM) hash identifying the code it runs.
type ContractVersionResolver interface {
	ResolveContractVersion(ctx context.Context, contractID string) (string, error)
}

// SorobanContractVersions resolves contract versions from the Soroban ledger by
// reading each contract's instance entry.
type SorobanContractVersions struct {
	Ledger *soroban.LedgerClient
}

// ResolveContractVersion returns the hex-encoded WASM hash the contract runs.
func (s SorobanContractVersions) ResolveContractVersion(ctx context.Context, contractID string) (string, error) {
	if s.Ledger == nil {
		return "", fmt.Errorf("no soroban ledger client configured")
	}
	return s.Ledger.GetContractWasmHash(ctx, contractID)
}

// StaticContractVersions resolves versions from a fixed map, for deployments
// where the version of each contract is known ahead of time instead of being
// read from the ledger. Lookup is exact, so a contract ID has to be written the
// same way the events carry it.
type StaticContractVersions map[string]string

// ResolveContractVersion returns the configured version for contractID, or an
// error when the contract is not configured.
func (s StaticContractVersions) ResolveContractVersion(_ context.Context, contractID string) (string, error) {
	version, ok := s[contractID]
	if !ok || version == "" {
		return "", fmt.Errorf("no contract version configured for %q", contractID)
	}
	return version, nil
}

// cachedContractVersion is a single memoized lookup outcome. err is retained so
// the distinction between "resolved" and "failed" survives the cache.
type cachedContractVersion struct {
	version string
	err     error
	expires time.Time
}

// cachingContractVersionResolver memoizes version lookups per contract ID so a
// busy contract is read from the ledger once per TTL instead of once per event.
type cachingContractVersionResolver struct {
	inner      ContractVersionResolver
	ttl        time.Duration
	failureTTL time.Duration
	now        func() time.Time

	mu    sync.Mutex
	cache map[string]cachedContractVersion
}

// NewCachingContractVersionResolver wraps inner so each contract is resolved at
// most once per ttl. A ttl of zero or less uses DefaultContractVersionTTL.
//
// Both successes and failures are cached, the latter for a much shorter window,
// so a broken or unknown contract cannot turn into an RPC call per event while a
// blip still clears quickly.
func NewCachingContractVersionResolver(inner ContractVersionResolver, ttl time.Duration) *cachingContractVersionResolver {
	if ttl <= 0 {
		ttl = DefaultContractVersionTTL
	}
	failureTTL := ttl / failureTTLDivisor
	if failureTTL <= 0 {
		failureTTL = time.Second
	}
	return &cachingContractVersionResolver{
		inner:      inner,
		ttl:        ttl,
		failureTTL: failureTTL,
		now:        time.Now,
		cache:      make(map[string]cachedContractVersion),
	}
}

// ResolveContractVersion returns the cached version for contractID when it is
// still fresh, and otherwise delegates to the wrapped resolver.
func (c *cachingContractVersionResolver) ResolveContractVersion(ctx context.Context, contractID string) (string, error) {
	now := c.now()

	c.mu.Lock()
	entry, ok := c.cache[contractID]
	c.mu.Unlock()

	if ok && now.Before(entry.expires) {
		return entry.version, entry.err
	}

	version, err := c.inner.ResolveContractVersion(ctx, contractID)
	if version == "" && err == nil {
		// A resolver that reports neither a version nor a failure would
		// otherwise be cached as a successful empty version, which would be
		// written to the audit log as if it were a real version.
		err = fmt.Errorf("resolver returned no version for %q", contractID)
	}

	ttl := c.ttl
	if err != nil {
		version = ""
		ttl = c.failureTTL
	}

	c.mu.Lock()
	c.cache[contractID] = cachedContractVersion{
		version: version,
		err:     err,
		expires: c.now().Add(ttl),
	}
	c.mu.Unlock()

	return version, err
}
