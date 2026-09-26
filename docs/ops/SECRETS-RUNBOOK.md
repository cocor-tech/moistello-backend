# Production Secrets Provisioning and Rotation Runbook

This runbook covers all secrets required for Moistello production deployment, how to generate or provision them, where they live, and rotation procedures. **One missed secret = crash-looping deploy. One leaked secret = full compromise.** Follow this procedure exactly.

## Overview

The following secrets are required for production:

| Secret | Purpose | Generated | Stored | Rotation |
|--------|---------|-----------|--------|----------|
| `JWT_PRIVATE_KEY` | Sign access tokens | Once | Vault | Never |
| `JWT_PUBLIC_KEY` | Verify access tokens | Once | Vault/Config | Never |
| `WALLET_PEPPER` | Stretch wallet auth nonces | Quarterly | Vault | Quarterly |
| `PASSKEY_PEPPER` | Stretch passkey challenges | Quarterly | Vault | Quarterly |
| `ENCRYPTION_KEY` | Encrypt sensitive data at rest | Quarterly | Vault | Quarterly |
| `STELLAR_MASTER_SECRET` | Sign Stellar transactions | Once | Vault | Never (backup only) |
| `REDIS_PASSWORD` | Redis authentication | Quarterly | Vault | Quarterly |
| `DB_PASSWORD` | PostgreSQL authentication | On deploy | Vault | On deploy |
| `ADMIN_API_KEY` | Admin metrics endpoint | Quarterly | Vault | Quarterly |
| `WEBHOOK_SECRET` | Sign webhooks | Quarterly | Vault | Quarterly |
| Provider API Keys | External service auth (YellowCard, etc.) | Per provider | Vault | Per provider SLAs |

---

## Secret Generation

### JWT Key Pair (RSA-4096)

**When:** Once during initial setup.
**Rotation:** Never (immutable).

```bash
# Generate RSA private key (4096 bits)
openssl genrsa -out jwt_private.pem 4096

# Extract public key
openssl rsa -in jwt_private.pem -pubout -out jwt_public.pem

# Verify keys
openssl pkey -in jwt_private.pem -text -noout
openssl pkey -in jwt_public.pem -text -pubin -noout

# Store in Vault:
vault kv put secret/moistello/jwt \
  private_key=@jwt_private.pem \
  public_key=@jwt_public.pem

# Securely delete originals from disk
shred -u jwt_private.pem jwt_public.pem
```

### 32-Byte Hex Encryption Keys (Pepper, Redis, etc.)

**When:** Initial setup + quarterly rotation.
**Rotation:** Quarterly (store old keys temporarily for decryption fallback).

```bash
#!/bin/bash
# Generate secure random 32-byte hex key
generate_hex_key() {
  openssl rand -hex 32
}

# Generate all quarterly-rotation keys
WALLET_PEPPER=$(generate_hex_key)
PASSKEY_PEPPER=$(generate_hex_key)
ENCRYPTION_KEY=$(generate_hex_key)
REDIS_PASSWORD=$(openssl rand -base64 32)
ADMIN_API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)

# Store in Vault
vault kv put secret/moistello/keys \
  wallet_pepper="$WALLET_PEPPER" \
  passkey_pepper="$PASSKEY_PEPPER" \
  encryption_key="$ENCRYPTION_KEY" \
  redis_password="$REDIS_PASSWORD" \
  admin_api_key="$ADMIN_API_KEY" \
  webhook_secret="$WEBHOOK_SECRET"

echo "✓ All keys generated and stored in Vault"
```

### Stellar Master Account Secret

**When:** Once during initial setup.
**Rotation:** Never generate a new one; backup only.
**Security:** Never stored as plaintext file. Vault-only access.

```bash
# Generate using stellar CLI
stellar account create

# Or import existing account secret
# (ask founding team member for the master secret)

# Store ONLY in Vault, never in files:
vault kv put secret/moistello/stellar \
  master_secret="S..." \
  master_account="G..." \
  network_passphrase="Public Global Stellar Network ; September 2015"

# Verify access (no plaintext output)
vault kv get secret/moistello/stellar
```

### PostgreSQL Password

**When:** On each deploy/environment setup.
**Rotation:** On infrastructure changes.

```bash
# Generate strong password
DB_PASSWORD=$(openssl rand -base64 32)

# Set in Vault
vault kv put secret/moistello/postgres \
  password="$DB_PASSWORD" \
  host="prod-db.internal" \
  port="5432" \
  database="moistello"

# Apply to RDS/managed database UI, then verify connection
psql -h prod-db.internal -U moistello -W -c "SELECT NOW();"
```

---

## Where Secrets Live

### ✅ Correct Storage

- **Vault** (primary): All secrets at rest
  - `secret/moistello/jwt` — JWT keypair
  - `secret/moistello/keys` — Peppers, encryption, API keys
  - `secret/moistello/stellar` — Stellar master secret
  - `secret/moistello/postgres` — Database credentials
  - `secret/moistello/external` — Provider API keys

- **Environment Variables** (runtime): Inject at container startup from Vault
  - Kubernetes: Vault injector sidecar
  - ECS: ECS Secrets integration
  - Docker: Pass via `.env` sourced from Vault during CI/CD

- **Config** (public, non-secret): Only public keys in `config.yaml`
  - `JWT_PUBLIC_KEY` (for verification only)

### ✗ Never Store Here

- ❌ Git repositories (committed history is forever)
- ❌ `.env.example` or `.env` files in code
- ❌ Docker images as ENV RUN (leaks in layer history)
- ❌ Logs or error messages
- ❌ Local disk on servers
- ❌ Slack messages or email

---

## Pre-Deploy Secrets Validation

Run this check before any deploy:

```bash
#!/bin/bash
# deploy/verify-secrets.sh

set -e
. deploy/config-validate.sh

REQUIRED_SECRETS=(
  "JWT_PRIVATE_KEY"
  "JWT_PUBLIC_KEY"
  "WALLET_PEPPER"
  "PASSKEY_PEPPER"
  "ENCRYPTION_KEY"
  "STELLAR_MASTER_SECRET"
  "REDIS_PASSWORD"
  "DB_PASSWORD"
  "ADMIN_API_KEY"
  "WEBHOOK_SECRET"
)

echo "Checking required secrets..."
MISSING=()

for secret in "${REQUIRED_SECRETS[@]}"; do
  if [ -z "${!secret}" ]; then
    MISSING+=("$secret")
  fi
done

if [ ${#MISSING[@]} -gt 0 ]; then
  echo "❌ DEPLOY BLOCKED: Missing secrets:"
  printf '  - %s\n' "${MISSING[@]}"
  exit 1
fi

echo "✓ All required secrets present"

# Validate secret format
if ! [[ ${#JWT_PRIVATE_KEY} -gt 100 ]]; then
  echo "❌ JWT_PRIVATE_KEY too short (not RSA 4096?)"
  exit 1
fi

if ! [[ $ENCRYPTION_KEY =~ ^[0-9a-f]{64}$ ]]; then
  echo "❌ ENCRYPTION_KEY not 32-byte hex"
  exit 1
fi

echo "✓ Secret formats valid"
echo "✓ Ready to deploy"
```

Add to CI/CD pipeline (pre-deploy gate):

```yaml
# .github/workflows/deploy.yml
deploy:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - name: Validate secrets before deploy
      run: bash deploy/verify-secrets.sh
      env:
        JWT_PRIVATE_KEY: ${{ secrets.JWT_PRIVATE_KEY }}
        JWT_PUBLIC_KEY: ${{ secrets.JWT_PUBLIC_KEY }}
        # ... all other secrets
    - name: Deploy
      run: bash deploy/deploy.sh
```

---

## Rotation Procedures

### Quarterly Rotation (Peppers, API Keys, Passwords)

**Timeline:** First Monday of each quarter, 2am UTC (low-traffic window).

1. **Generate new secrets:**
   ```bash
   bash deploy/generate-secrets.sh quarterly
   vault kv put secret/moistello/keys-new \
     wallet_pepper="..." \
     passkey_pepper="..." \
     encryption_key="..." \
     redis_password="..." \
     admin_api_key="..." \
     webhook_secret="..."
   ```

2. **Blue-green deploy:**
   - Deploy canary with new secrets to staging
   - Run full test suite against canary
   - Deploy to production (old and new instances run in parallel)
   - Drain connections from old instances
   - After 5 minutes, remove old instances

3. **Keep old secrets in Vault for 48 hours:**
   - Allows graceful shutdown of in-flight requests using old keys
   - Set metadata expiry on old secret
   ```bash
   vault kv metadata put secret/moistello/keys-old \
     max_versions=1
   # (expires after 48 hours per policy)
   ```

4. **Test rotation:**
   - Verify new admin API key works: `curl -H "X-Admin-API-Key: $NEW_KEY" https://api.moistello.com/metrics`
   - Verify nonce generation works (tests hitting /auth/nonce)
   - Verify webhook signing with new secret

5. **Notify team:**
   - Slack #ops: `✓ Quarterly secret rotation complete (wallet_pepper, encryption_key, etc.)`

### Incident: Leaked Secret

**If any secret is exposed, treat as P1.**

1. **Immediate (within 15 minutes):**
   - Rotate that secret immediately
   - Deploy with new value
   - Audit logs for unauthorized access

2. **Within 1 hour:**
   - Disable old secret in Vault (set zero-length placeholder)
   - Revoke all active sessions if auth secret leaked
   - Force re-authentication of all users

3. **Post-incident (within 24 hours):**
   - Audit what was accessed under old secret
   - Document timeline and impact in #incidents
   - Update security group/network rules if DB password leaked

---

## Emergency Access

**In a true emergency** (e.g., Vault is down, need to deploy a hotfix):

1. **Contact on-call security lead:**
   - Slack: @on-call-security
   - Phone: (ops contact)

2. **Emergency secret access:**
   - Vault emergency backup (stored offline)
   - Backup is in physical safe at [location]
   - Requires 2 signatures to open

3. **After emergency:**
   - Rotate all secrets within 24 hours
   - Document what was accessed and why
   - Review controls to prevent future emergency

---

## Compliance & Auditing

- All secret access is logged in Vault audit log: `vault audit list`
- Rotation events trigger Slack notification (automation)
- Monthly rotation report required for compliance team
- Unrotated secrets (JWT keys) documented as intentional

---

## Checklists

### Initial Setup
- [ ] Generate JWT keypair, store in Vault
- [ ] Generate all quarterly-rotation secrets
- [ ] Generate Stellar master secret
- [ ] Store all in Vault with appropriate TTLs
- [ ] Document in this runbook
- [ ] Test pre-deploy validation script

### Quarterly Rotation
- [ ] Schedule in calendar
- [ ] Generate new secrets
- [ ] Deploy to staging, run tests
- [ ] Deploy to production
- [ ] Verify with manual spot checks
- [ ] Document in #ops
- [ ] Remind team in engineering standup

### New Environment (Staging, QA, Local Dev)
- [ ] Copy secrets from Vault (or generate new for dev/staging)
- [ ] Verify connectivity (database, Redis)
- [ ] Run pre-deploy validation
- [ ] Document environment in Vault metadata

---

## Support

Questions or issues:
- **#ops** channel for deployment help
- **@security-team** for secret or compliance questions
- **See:** [SECURITY.md](../SECURITY.md) for incident response procedures
