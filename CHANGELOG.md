# Changelog

## [Unreleased]

### Added — 2C2P payment webhook (`docs/02-specs/2c2p-payment-webhook.md`)
- `POST /webhooks/2c2p`: verifies the 2C2P PGW v4 notification (JWT HS256 = HMAC-SHA256, alg pinned, constant-time compare, merchant check) and transitions a payment `PENDING → SUCCESS | FAILED` atomically.
- Idempotent redelivery: row lock + terminal-state rule + conditional update; duplicates return `DUPLICATE` without writing, and conflicting outcomes are ignored and logged for reconciliation.
- Ports `WebhookUseCase`, `PaymentRepository`, `WebhookVerifier`; adapters: GORM (`payments` table, model kept in the adapter), in-memory, and `twoc2p`.
- Migration `0001_payments`; env `TWOC2P_MERCHANT_ID`, `TWOC2P_SECRET_KEY`; `httpapi.PublicModule` for signature-authenticated routes.

### Added
- Hexagonal architecture baseline (ADR-0002): `internal/core` (domain, port, service), HTTP inbound adapter (Fiber v2) with a `Module` extension point, PostgreSQL (GORM) and in-memory outbound adapters, composition root in `cmd/api`.
- Platform: env config, structured JSON logging, JWT bearer auth; middleware for trace id, access log, panic recovery, request timeout, body limit; strict JSON decoding; domain error → contract error mapping.
- `/healthz` and `/readyz` probes, `domain.Money` (int64 minor units), Docker Compose dev database, Dockerfile (distroless, non-root), `cmd/devtoken`.
- ADR-0003: Fiber v2 + GORM, with guardrails.
- Integration test gate in `make verify`; `depguard` rules that enforce hexagonal boundaries; GitHub Actions CI.
