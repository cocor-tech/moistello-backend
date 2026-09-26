#!/bin/bash
set -euo pipefail

# Pre-deploy secrets validation for Moistello
# Ensure all required secrets are present and in correct format
# Exit code 0 = ready to deploy, non-zero = blocked

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

MISSING=()
INVALID=()

echo "🔐 Verifying production secrets..."
echo ""

# Check all required secrets are set
for secret in "${REQUIRED_SECRETS[@]}"; do
  if [ -z "${!secret:-}" ]; then
    MISSING+=("$secret")
  fi
done

if [ ${#MISSING[@]} -gt 0 ]; then
  echo "❌ DEPLOY BLOCKED: Missing required secrets:"
  printf '   - %s\n' "${MISSING[@]}"
  echo ""
  exit 1
fi

# Validate secret formats
echo "Validating secret formats..."

# JWT private key should be PEM format
if ! [[ ${JWT_PRIVATE_KEY:-} =~ ^-----BEGIN ]]; then
  INVALID+=("JWT_PRIVATE_KEY: not PEM format")
fi

# JWT public key should be PEM format
if ! [[ ${JWT_PUBLIC_KEY:-} =~ ^-----BEGIN ]]; then
  INVALID+=("JWT_PUBLIC_KEY: not PEM format")
fi

# Peppers should be 32-byte hex
if ! [[ ${WALLET_PEPPER:-} =~ ^[0-9a-f]{64}$ ]]; then
  INVALID+=("WALLET_PEPPER: not 32-byte hex")
fi

if ! [[ ${PASSKEY_PEPPER:-} =~ ^[0-9a-f]{64}$ ]]; then
  INVALID+=("PASSKEY_PEPPER: not 32-byte hex")
fi

if ! [[ ${ENCRYPTION_KEY:-} =~ ^[0-9a-f]{64}$ ]]; then
  INVALID+=("ENCRYPTION_KEY: not 32-byte hex")
fi

# API keys should be hex
if ! [[ ${ADMIN_API_KEY:-} =~ ^[0-9a-f]{64}$ ]]; then
  INVALID+=("ADMIN_API_KEY: not 64-char hex")
fi

if ! [[ ${WEBHOOK_SECRET:-} =~ ^[0-9a-f]{64}$ ]]; then
  INVALID+=("WEBHOOK_SECRET: not 64-char hex")
fi

# Stellar secret should start with S
if ! [[ ${STELLAR_MASTER_SECRET:-} =~ ^S[A-Z0-9]{55}$ ]]; then
  INVALID+=("STELLAR_MASTER_SECRET: invalid Stellar secret format")
fi

# Passwords should be reasonably long
if [ ${#REDIS_PASSWORD} -lt 16 ]; then
  INVALID+=("REDIS_PASSWORD: too short (< 16 chars)")
fi

if [ ${#DB_PASSWORD} -lt 16 ]; then
  INVALID+=("DB_PASSWORD: too short (< 16 chars)")
fi

if [ ${#INVALID[@]} -gt 0 ]; then
  echo "❌ DEPLOY BLOCKED: Invalid secret formats:"
  printf '   - %s\n' "${INVALID[@]}"
  echo ""
  exit 1
fi

# Warn if running in non-production environment
if [ "${ENVIRONMENT:-}" != "production" ]; then
  echo "⚠️  WARNING: Not running in production mode (ENVIRONMENT=$ENVIRONMENT)"
  echo "   For production deploys, set ENVIRONMENT=production"
fi

echo ""
echo "✅ All required secrets present and valid"
echo "   → Ready to deploy"

exit 0
