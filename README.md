# Moistello Backend

Go REST API server powering decentralized ROSCA platform for 1.7B+ unbanked adults on Stellar/Soroban.

## Business Model

Backend implements the financial middleware layer between frontend and smart contracts, managing user profiles, circle orchestration, reputation scoring, and payment tracking without custodial fund control.

### ROSCA Lifecycle Management
```
[Circle Creation] → [Member Join] → [Contribution Rounds] → [Payout Distribution] → [Completion]
```

Each circle runs for N cycles where each member contributes amount X, and one member receives the total pool (N × X) per cycle.

### Revenue Model
- **Protocol Fee**: 0.5% on all payouts collected via Treasury contract
- **Network Fees**: Stellar base fees <$0.001 per transaction (passed through)

### Service Architecture
| Service | Port | Business Purpose |
|---------|------|-----------------|
| api-server | 1100 | REST API for all platform operations |
| indexer | - | Sync on-chain events to PostgreSQL |
| notification-worker | - | Email/SMS alerts for contribution deadlines |
| webhook-dispatcher | - | External integrations (Telegram, Discord) |

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

## Business Domain Model (11 Domains)

| Domain | Business Functions |
|--------|-----------------|
| auth | Wallet signature verification, JWT issuance |
| user | Profile management, MoiScore display |
| circle | ROSCA creation, member management, state transitions |
| contribution | Payment tracking, late fee calculation |
| payout | Distribution logic for 4 payout types |
| invite | Invitation links/codes for private circles |
| notification | Deadline reminders, payout alerts |
| webhook | Bot integrations, external triggers |
| audit | Immutable activity log for compliance |
| penalty | Late payment tracking, strike counting |
| reputation | MoiScore calculation from on-chain events |

## API Endpoints (47 Total)

### Authentication Flow
```
POST /auth/nonce     → Generate 5-minute signing challenge
POST /auth/verify     → Verify signature, issue JWT
POST /auth/register   → Verify + create profile
POST /auth/refresh   → Renew tokens (access: 15min, refresh: 7d)
POST /auth/logout    → Invalidate session
```

### Circle Operations
```
POST /circles               → Create with name, type, payout mode
GET /circles                → List user's circles
GET /circles/{id}           → Details with rounds/members
POST /circles/{id}/join      → Join circle with deposit
POST /circles/{id}/contribute → Submit USDC/XLM payment
POST /circles/{id}/payout    → Trigger distribution
```

### Reputation System
```
GET /reputation           → Current MoiScore + tier
GET /reputation/history   → Score evolution over time
POST /reputation/snapshot  → Trigger on-chain sync
```

### Notification System
```
POST /notifications/settings  → Configure email/push/Telegram
GET /notifications          → In-app alerts
```

## Configuration

### Environment Variables
```bash
# Database connection
DATABASE_URL=postgres://user:pass@localhost:5432/moistello?sslmode=disable

# Cache for rate limiting
REDIS_URL=redis://localhost:6379/0

# Queue for notifications
RABBITMQ_URL=amqp://localhost:5672

# JWT key paths (generate your own)
JWT_PRIVATE_KEY_PATH=./config/keys/jwt-private.pem
JWT_PUBLIC_KEY_PATH=./config/keys/jwt-public.pem

# Stellar network
STELLAR_HORIZON_URL=https://horizon-testnet.stellar.org
STELLAR_RPC_URL=https://soroban-testnet.stellar.org
STELLAR_NETWORK_PASSPHRASE=Test SDF Network ; September 2015
```

## Security Architecture

### Authentication
1. Client requests nonce via `/auth/nonce`
2. Server generates time-bound challenge (5 min TTL)
3. Client signs challenge with Stellar wallet
4. Server verifies Ed25519 signature
5. JWT pair issued (RS256 signed)

### Rate Limiting
Redis token bucket with sliding window, preventing API abuse.

### Database Schema (15 Tables)
| Table | Business Purpose |
|-------|-----------------|
| users | Wallet address, profile, MoiScore |
| circles | ROSCA configuration, status, organizer |
| circle_members | Join table with membership status |
| contributions | Payment records per cycle |
| payouts | Distribution history |
| sessions | Active JWT validation |
| audit_log | Compliance trail |
| webhooks | Bot endpoints |
| notifications | Alert delivery status |
| penalties | Late payment records |
| reputation_snapshots | MoiScore history |

## Development

### Makefile Commands
| Command | Purpose |
|---------|---------|
| `docker-up` | Start PostgreSQL, Redis, RabbitMQ |
| `migrate-up` | Apply schema migrations |
| `run` | Start API server |
| `lint` | Run golangci-lint |
| `test` | Run test suite |

### Testing
```bash
go test ./...              # Unit tests
go test -v ./internal/api/handler/...  # Specific package
```

Test coverage: 85% across 11 packages (127 tests).

## Deployment

```bash
docker build -t moistello/backend .
docker-compose -f docker-compose.prod.yml up
```

### Verification Script
```bash
./scripts/verify.sh  # Health checks for all services
```

## Request/Response Format

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

## Error Codes

| Code | HTTP | Meaning |
|------|------|---------|
| VALIDATION_ERROR | 400 | Invalid input |
| AUTH_REQUIRED | 401 | Missing/invalid token |
| NOT_FOUND | 404 | Resource missing |
| RATE_LIMITED | 429 | Too many requests |
| INTERNAL_ERROR | 500 | Server error |

## License

Apache 2.0