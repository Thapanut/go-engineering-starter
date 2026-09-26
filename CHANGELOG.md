# Changelog

## [Unreleased]

### Added
- Hexagonal architecture baseline (ADR-0002): `internal/core` (domain, port, service), HTTP inbound adapter (Fiber v2) with a `Module` extension point, PostgreSQL (GORM) and in-memory outbound adapters, composition root in `cmd/api`.
- Platform: env config, structured JSON logging, JWT bearer auth; middleware for trace id, access log, panic recovery, request timeout, body limit; strict JSON decoding; domain error → contract error mapping.
- `/healthz` and `/readyz` probes, `domain.Money` (int64 minor units), Docker Compose dev database, Dockerfile (distroless, non-root), `cmd/devtoken`.
- ADR-0003: Fiber v2 + GORM, with guardrails.
- Integration test gate in `make verify`; `depguard` rules that enforce hexagonal boundaries; GitHub Actions CI.
