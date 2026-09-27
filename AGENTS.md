# Moistello Backend Conventions & Guidelines

This document outlines core architectural guidelines, coding conventions, and static analysis rules for contributors and AI agents working on the `moistello-backend` codebase.

---

## 1. Monetary Amount Construction (`pkg/money`)
- **No Panicking Constructors in Request Paths**:
  - `money.MustFromString` and `money.MustFromFloat64` are strictly restricted to package tests, constants/internal definitions, and seed/migration scripts.
  - **Never** call `money.Must*` in HTTP request handlers, controllers, or domain service methods that process user or external input.
  - Always use error-returning constructors: `money.FromString(s)` or `money.FromFloat64(f)` and return a descriptive `400 Bad Request` or validation error on invalid input to prevent process crashes.
  - Enforced in CI via `forbidigo` lint rule in `.golangci.yml`.

---

## 2. Configuration & Startup Validation (`config`)
- **Single-Pass Multi-Error Reporting**:
  - `config.Load(path)` must never panic on missing configuration keys.
  - All missing or invalid configuration values are collected and returned as a unified error listing every problem at once.
  - Application entrypoints (`cmd/api-server`, `cmd/indexer`, `cmd/migrate`) log configuration errors with `log.Fatal` without goroutine stack dumps.
- **Offline Configuration Preflight (`config-validate`)**:
  - Use `moistello-api config-validate` (or `cmd/config-validate`) to perform offline preflight validation of configuration and secret formats (including JWT key parsing) without opening network connections.

---

## 3. Structured Logging
- Use `pkg/logger` or `github.com/rs/zerolog/log` for all application logging.
- `fmt.Print*`, `log.Print*`, and builtin `print/println` are forbidden by linter rules.

---

## 4. Indexer & Event Processing
- All blockchain event handlers (`internal/indexer/processor.go`) must be idempotent on composite business keys (such as `(circle_id, round, bidder)` for auction bids).
- Re-processing transactions or historical ledger blocks must never create duplicate records or double-count contributions.

---

## 5. Testing Standards
- All changes must include unit tests covering both the happy path and edge/failure cases.
- Indexer event handlers must have unit tests verifying domain entity persistence and mock expectations.
