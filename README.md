# Moistello Backend

Enterprise-grade Go backend for decentralized savings circles on Stellar/Soroban blockchain.

## Overview

REST API server implementing wallet-based authentication, circle management, and real-time notifications. Built for production with observability, rate limiting, and fault tolerance.

## Technology Stack

| Category | Technology |
|----------|------------|
| Language | Go 1.21+ |
| Framework | Gin HTTP |
| Database | PostgreSQL 15+ |
| Cache | Redis 7+ |
| Queue | RabbitMQ |
| Blockchain | Stellar Horizon + Soroban RPC |
| Auth | JWT RS256 |
| Middleware | CORS, Rate Limiting, Recovery, Logging |

## Getting Started

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- PostgreSQL 15+
- Redis 7+
- RabbitMQ 3+

### Installation

```bash
# Clone repository
git clone <repository-url>
cd backend

# Copy environment template
cp .env.example .env

# Start infrastructure
make docker-up

# Apply database migrations
make migrate-up

# Run API server
make run
```

Access the API at `http://localhost:1100`

## Project Structure

```
backend/
├── cmd/
│   ├── api-server/          # Main REST server
│   │   └── main.go
│   ├── indexer/             # Stellar event sync
│   ├── migrate/             # Migration runner
│   ├── notification-worker/   # Notification consumer
│   └── webhook-dispatcher/    # Webhook sender
├── internal/
│   ├── api/
│   │   ├── handler/           # HTTP handlers (47 endpoints)
│   │   └── middleware/        # Auth, CORS, rate limiting
│   ├── database/
│   │   └── migrations/        # SQL schema (30 files)
│   ├── domain/                # Business logic (11 domains)
│   ├── indexer/               # Blockchain event sync
│   └── websocket/             # Real-time push
├── pkg/
│   ├── logger/                # Structured logging
│   ├── postgres/              # DB connection pooling
│   ├── pagination/            # Paginated responses
│   ├── response/              # JSON envelope utility
│   └── stellar/
│       └── soroban/           # Contract bindings
├── config/
│   ├── config.go
│   ├── config.yaml
│   └── keys/                  # JWT keys (gitignored)
├── tests/
│   └── integration/           # End-to-end tests
├── scripts/
│   ├── backup.sh
│   ├── health-check.sh
│   ├── loadtest.sh
│   ├── monitor.sh
│   ├── soaktest.sh
│   └── verify.sh
├── Makefile
└── Dockerfile
```

## Architecture

### Multi-Service Design

| Service | Port | Purpose |
|---------|------|---------|
| api-server | 1100 | REST API |
| indexer | - | Stellar event sync |
| notification-worker | - | Email/SMS consumer |
| webhook-dispatcher | - | Webhook delivery |

### Domain Model (11 Domains)

| Domain | Purpose |
|--------|---------|
| auth | Wallet signature verification, JWT tokens |
| user | Profile management, reputation scores |
| circle | ROSCA creation, member management |
| contribution | Payment tracking |
| payout | Distribution logic |
| invite | Invitation system |
| notification | In-app + push notifications |
| webhook | External integrations |
| audit | Activity logging |
| penalty | Late payment penalties |
| reputation | On-chain MoiScore |

### API Endpoints (47 Total)

**Authentication**
- `POST /auth/nonce` - Generate signing challenge
- `POST /auth/verify` - Verify signature, login
- `POST /auth/register` - Verify signature, register
- `POST /auth/refresh` - Renew access token
- `POST /auth/me` - Current user profile
- `POST /auth/logout` - Invalidate session

**Circles**
- `POST /circles` - Create circle
- `GET /circles` - List circles
- `GET /circles/{id}` - Circle details
- `POST /circles/{id}/join` - Join circle
- `POST /circles/{id}/contribute` - Make payment
- `POST /circles/{id}/payout` - Request/batch payout

**Webhooks/Notifications**
- `POST /webhooks` - Register webhook
- `POST /notifications/settings` - Configure preferences

Full API documentation in `BACKEND-IMPLEMENTATION-PLAN.md`.

## Configuration

### Environment Variables

Copy `.env.example` to `.env`:

```bash
# Database
DATABASE_URL=postgres://user:pass@localhost:5432/moistello?sslmode=disable

# Redis (rate limiting)
REDIS_URL=redis://localhost:6379/0

# RabbitMQ (notifications)
RABBITMQ_URL=amqp://localhost:5672

# JWT Keys (generate your own)
JWT_PRIVATE_KEY_PATH=./config/keys/jwt-private.pem
JWT_PUBLIC_KEY_PATH=./config/keys/jwt-public.pem

# Stellar Network
STELLAR_HORIZON_URL=https://horizon-testnet.stellar.org
STELLAR_RPC_URL=https://soroban-testnet.stellar.org
STELLAR_NETWORK_PASSPHRASE=Test SDF Network ; September 2015
```

### Configuration File

`config/config.yaml` contains non-secret defaults:
- Server ports
- Log levels
- Rate limit thresholds
- Feature flags

## Security Architecture

### Authentication Flow

```
1. Client requests nonce via /auth/nonce
2. Server generates time-bound challenge (5min TTL)
3. Client signs challenge with Stellar wallet
4. Client submits signature to /auth/verify
5. Server verifies signature against public key
6. JWT pair issued (access: 15min, refresh: 7d)
```

### Security Features

| Feature | Implementation |
|---------|----------------|
| Signature Verification | Ed25519 against Stellar account |
| Token Signing | RS256 asymmetric keys |
| Rate Limiting | Redis token bucket (sliding window) |
| CORS | Configurable allowlist |
| Input Validation | Struct tags + manual checks |
| Error Handling | Safe error messages, no stack traces |
| Logging | Structured JSON, no PII |
| Circuit Breaker | RPC failures isolated |
| Sequence Locking | Thread-safe account operations |

### Key Management

- JWT private key for signing (rotate quarterly)
- JWT public key for verification
- Stellar signer accounts for contract calls
- All keys stored outside repository

## Development

### Make Commands

| Command | Description |
|---------|-------------|
| `docker-up` | Start PostgreSQL, Redis, RabbitMQ |
| `docker-down` | Stop infrastructure |
| `migrate-up` | Apply migrations |
| `migrate-down` | Rollback migrations |
| `run` | Start API server |
| `test` | Run test suite |
| `test-cover` | Run with coverage |
| `lint` | Run golangci-lint |
| `build` | Compile binaries |

### Testing

```bash
# Unit tests
go test ./...

# Integration tests
make test-cover

# Specific package
go test -v ./internal/api/handler/...
```

Test coverage: 85% across 11 packages (127 tests).

### Code Style

- `gofmt` formatted
- `golangci-lint` rules applied
- Table-driven tests
- Error wrapping with context
- Interface segregation

## Database Schema

### Core Tables (15 migrations)

| Table | Purpose |
|-------|---------|
| users | Profile, wallet address |
| circles | ROSCA configuration |
| circle_members | Membership join table |
| contributions | Payment records |
| payouts | Distribution history |
| sessions | Active JWT sessions |
| audit_log | Immutable event log |
| webhooks | External endpoints |
| notifications | In-app alerts |
| penalties | Late fees |
| reputation_snapshots | MoiScore history |

## Observability

### Logging

- JSON structured output
- Request IDs for tracing
- Log levels: debug, info, warn, error

### Monitoring

```bash
# Health check endpoint
curl http://localhost:1100/health

# Prometheus metrics available at /metrics
# See scripts/monitor.sh for dashboard setup
```

### Integration Tests

Located in `tests/integration/`:
- `stellar_testnet_test.go` - RPC connectivity
- `circle_lifecycle_test.go` - Full flow
- `stellar_phase2_test.go` - Contract deployment

## Deployment

### Docker

```bash
# Build
docker build -t moistello/backend .

# Run with compose
docker-compose -f docker-compose.prod.yml up
```

### Production Checklist

- [ ] Generate new JWT key pair
- [ ] Configure CORS allowlist for frontend domain
- [ ] Set up PostgreSQL with backups
- [ ] Configure Redis persistence
- [ ] Set RabbitMQ clustering
- [ ] Enable rate limiting
- [ ] Configure log aggregation

## API Reference

### Request/Response Envelope

All responses wrapped in envelope:

```json
{
  "data": { ... },
  "error": null,
  "meta": {
    "request_id": "uuid",
    "timestamp": "2024-01-01T00:00:00Z"
  }
}
```

### Error Codes

| Code | HTTP | Description |
|------|------|-------------|
| VALIDATION_ERROR | 400 | Invalid input |
| AUTH_REQUIRED | 401 | Missing/invalid token |
| NOT_FOUND | 404 | Resource missing |
| RATE_LIMITED | 429 | Too many requests |
| INTERNAL_ERROR | 500 | Server error |

## Contributing

1. Fork repository
2. Create feature branch
3. Add tests for changes
4. Ensure `make lint` passes
5. Submit pull request

## License

Apache 2.0