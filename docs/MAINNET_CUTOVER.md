# Mainnet Cutover Checklist

This checklist guides the transition from testnet to mainnet. Follow each step carefully to avoid sending testnet configuration to production.

## Pre-Cutover: Staging Environment Dry Run

Run this entire checklist on a staging environment first to validate the process.

- [ ] Deploy all Soroban contracts to testnet
- [ ] Record testnet contract IDs
- [ ] Verify contract functionality with smoke tests
- [ ] Switch staging config to testnet (use testnet values from defaults)
- [ ] Fund staging master account with sufficient stroops
- [ ] Run full smoke test suite against testnet endpoint
- [ ] Verify all DNS and CORS configurations work on staging
- [ ] Document any issues or deviations

## Mainnet Cutover Steps

### 1. Contract Deployment (Run on Mainnet)

- [ ] Deploy circle factory contract to mainnet Soroban
- [ ] Deploy circle contract to mainnet Soroban
- [ ] Deploy reputation registry contract to mainnet Soroban
- [ ] Deploy governance token contract to mainnet Soroban
- [ ] Deploy treasury contract to mainnet Soroban
- [ ] Record all mainnet contract IDs

### 2. Configuration Update

Update `config/config.yaml` with mainnet values:

- [ ] Set `stellar.network: "mainnet"`
- [ ] Set `stellar.horizon_url: "https://horizon.stellar.org"`
- [ ] Set `stellar.soroban_rpc_url: "https://soroban-mainnet.stellar.org"` (or your RPC provider)
- [ ] Set `stellar.network_passphrase: "Public Global Stellar Network ; September 2015"`
- [ ] Set `stellar.usdc_issuer: "GBUQWP3BOUZX34ULNQG23RQ6F4YUSXHTQSXUSMIQ375YRKBY2S27425"` (mainnet USDC issuer)
- [ ] Update contract IDs in `stellar.*_contract_id` to mainnet values
- [ ] Verify config file does not contain any testnet values

### 3. Master Account Funding

- [ ] Fund mainnet master account (`MOISTELLO_STELLAR_MASTER_PUBLIC_KEY`) with:
  - Minimum 100 stroops for operational reserves
  - Additional stroops for transaction fees (estimate based on expected throughput)
  - Test with small amounts first, scale up after validation

### 4. Environment Variable Update

- [ ] Set `MOISTELLO_STELLAR_MASTER_SECRET_KEY` to mainnet master secret (from secure vault, never hardcoded)
- [ ] Set `MOISTELLO_STELLAR_MASTER_PUBLIC_KEY` to mainnet master public key
- [ ] Verify no environment variable still contains testnet keys

### 5. Smoke Testing with Real Stroops

Before full launch, run production-like smoke tests:

- [ ] Test circle creation with real stroops
- [ ] Test contribution recording on mainnet
- [ ] Test payout execution on mainnet
- [ ] Verify transactions appear on mainnet block explorer
- [ ] Check account balances and fees
- [ ] Monitor for any rate limiting or RPC issues

### 6. DNS and CORS Configuration

- [ ] Update DNS records to point to mainnet infrastructure
- [ ] Update `cors.allowed_origins` in config for mainnet domain(s)
- [ ] Verify CORS headers are correct for mainnet endpoints
- [ ] Test API calls from mainnet frontend domain

### 7. Startup Guard Validation

The system includes an automatic guard that refuses startup if mainnet mode is detected with any testnet values:

- [ ] The application startup will validate mainnet config
- [ ] If any testnet defaults are detected, startup will fail with clear error
- [ ] Example error: "mainnet cutover guard: mainnet mode requires all fields to differ from testnet defaults"
- [ ] Fix all reported issues before attempting restart

## Post-Cutover Verification

- [ ] Monitor application logs for errors
- [ ] Verify rate limiting is working correctly
- [ ] Check transaction processing latency
- [ ] Monitor account balance for unexpected fees
- [ ] Verify all user-facing endpoints work correctly
- [ ] Check for any webhook delivery issues
- [ ] Monitor database performance with mainnet data volume
- [ ] Have rollback plan ready if critical issues emerge

## Rollback Plan (If Needed)

If critical issues emerge:

- [ ] Revert config to testnet values
- [ ] Update environment variables back to testnet keys
- [ ] Restart application (will pass startup guard)
- [ ] Update DNS to route back to testnet or previous version
- [ ] Investigate root cause
- [ ] Fix issues on staging before next cutover attempt

## Verification Commands

```bash
# Verify mainnet network configuration
gh repo view stellar/js-stellar-sdk
stellar account info <MASTER_PUBLIC_KEY> --network mainnet

# Check application configuration (when running)
curl http://localhost:1100/health | jq '.config.stellar.network'

# Monitor logs for startup guard
grep "mainnet cutover guard" logs/*.log

# Verify contract IDs are mainnet (not testnet)
curl https://soroban-mainnet.stellar.org/<contract-id>/info
```

## Support

If issues arise during cutover, ensure you have:
- Access to secure key vault
- Database backups
- Previous deployment artifacts
- Stellar support channel access
- Team communication channels ready
