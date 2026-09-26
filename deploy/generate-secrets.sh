#!/bin/bash
set -euo pipefail

# Generate production secrets for Moistello backend
# Usage: ./generate-secrets.sh [jwt|quarterly|all]
# - jwt: Generate JWT keypair (one-time only)
# - quarterly: Generate quarterly-rotation secrets (peppers, API keys)
# - all: Generate everything

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Helper to generate 32-byte hex
generate_hex_32() {
  openssl rand -hex 32
}

# Helper to generate base64
generate_base64_32() {
  openssl rand -base64 32
}

# Generate JWT keypair (RSA-4096)
generate_jwt_keys() {
  echo "📝 Generating JWT RSA-4096 keypair..."

  # Create temp directory for keys
  local tmpdir=$(mktemp -d)
  trap "rm -rf $tmpdir" EXIT

  # Generate private key
  openssl genrsa -out "$tmpdir/jwt_private.pem" 4096 2>/dev/null

  # Extract public key
  openssl rsa -in "$tmpdir/jwt_private.pem" -pubout -out "$tmpdir/jwt_public.pem" 2>/dev/null

  # Read keys (PEM format is safe to output)
  local private_key=$(cat "$tmpdir/jwt_private.pem")
  local public_key=$(cat "$tmpdir/jwt_public.pem")

  # Output as environment variables (caller pipes to vault or .env)
  echo "JWT_PRIVATE_KEY='$private_key'"
  echo "JWT_PUBLIC_KEY='$public_key'"

  echo "✓ JWT keypair generated (store in Vault)" >&2
}

# Generate quarterly-rotation secrets
generate_quarterly_secrets() {
  echo "📝 Generating quarterly-rotation secrets..." >&2

  echo "WALLET_PEPPER='$(generate_hex_32)'"
  echo "PASSKEY_PEPPER='$(generate_hex_32)'"
  echo "ENCRYPTION_KEY='$(generate_hex_32)'"
  echo "REDIS_PASSWORD='$(generate_base64_32)'"
  echo "ADMIN_API_KEY='$(generate_hex_32)'"
  echo "WEBHOOK_SECRET='$(generate_hex_32)'"

  echo "✓ Quarterly secrets generated (store in Vault)" >&2
}

# Validate secret format
validate_secrets() {
  local secret_type=$1

  if [ "$secret_type" = "jwt" ] || [ "$secret_type" = "all" ]; then
    [[ ${JWT_PRIVATE_KEY:-} =~ ^-----BEGIN ]] || { echo "❌ JWT_PRIVATE_KEY invalid format"; return 1; }
    [[ ${JWT_PUBLIC_KEY:-} =~ ^-----BEGIN ]] || { echo "❌ JWT_PUBLIC_KEY invalid format"; return 1; }
    echo "✓ JWT keys valid" >&2
  fi

  if [ "$secret_type" = "quarterly" ] || [ "$secret_type" = "all" ]; then
    [[ ${ENCRYPTION_KEY:-} =~ ^[0-9a-f]{64}$ ]] || { echo "❌ ENCRYPTION_KEY not 32-byte hex"; return 1; }
    [[ ${WALLET_PEPPER:-} =~ ^[0-9a-f]{64}$ ]] || { echo "❌ WALLET_PEPPER not 32-byte hex"; return 1; }
    [[ ${PASSKEY_PEPPER:-} =~ ^[0-9a-f]{64}$ ]] || { echo "❌ PASSKEY_PEPPER not 32-byte hex"; return 1; }
    echo "✓ Quarterly secrets valid" >&2
  fi

  return 0
}

# Main
main() {
  local mode="${1:-all}"

  case "$mode" in
    jwt)
      generate_jwt_keys
      ;;
    quarterly)
      generate_quarterly_secrets
      ;;
    all)
      generate_jwt_keys
      echo ""
      generate_quarterly_secrets
      ;;
    validate)
      # Load secrets from environment and validate
      validate_secrets "all"
      ;;
    *)
      echo "Usage: $0 [jwt|quarterly|all|validate]" >&2
      exit 1
      ;;
  esac
}

main "$@"
