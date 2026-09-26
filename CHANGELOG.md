# Changelog

## [Unreleased]

### Added
- Hexagonal architecture baseline (ADR-0002): `core` (domain/port/service), HTTP inbound adapter (Gin), PostgreSQL and in-memory outbound adapters, composition root in `cmd/api`.
- Reference feature `intra-bank-transfer`: `GET /v1/accounts/{id}`, `POST /v1/transfers` (idempotent), `GET /v1/transfers/{id}` with JWT auth, ownership checks, audit log, and PII-free structured logs.
- Migrations `0001_init`, Docker Compose dev database with synthetic seed, `cmd/devtoken`, Dockerfile (distroless, non-root).
- Integration test gate in `make verify`; `depguard` rules that enforce hexagonal boundaries; GitHub Actions CI.
