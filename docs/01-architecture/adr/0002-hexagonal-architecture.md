# ADR-0002: Use hexagonal architecture (ports & adapters) for Go services

- **Status:** Accepted
- **Date:** 2026-09-27
- **Deciders:** Thapanut L. (architect / owner)

## Context
Banking services live for years while their edges change: REST today, gRPC or Kafka consumers tomorrow; PostgreSQL today, a core-banking API or a different store later. Business rules around money movement (balance checks, idempotency, audit) must be testable without a database or HTTP server, and must be reviewable by humans in one place. AI agents implement most code, so the structure must make boundary violations obvious in review.

## Options considered
| Criterion | A. Layered (handler → service → repository) | B. Hexagonal (ports & adapters) | C. Full Clean Architecture (entities / use cases / interface adapters / frameworks, one package per use case) |
|---|---|---|---|
| Complexity | Low | Medium | High: many packages and mappers |
| Cost / effort | Lowest to start | Small extra cost for port interfaces | Highest; boilerplate per use case |
| Testability of business rules | Service often coupled to DB/ORM types | Core tested with in-memory adapters, no DB/HTTP | Same as B |
| Swapping transport / storage | Touches service code | Add an adapter; core unchanged | Same as B |
| Security / compliance | Rules can leak into handlers | Idempotency, audit, and ownership are enforced in the core, so every adapter gets them | Same as B |
| Operability | Simple | Simple; composition root in `cmd/` | More moving parts |
| Reversibility | — | Easy to collapse to A | Easy to collapse to B |

## Decision
Structure every service as hexagonal architecture: a dependency-free `core` (domain + ports + services) with inbound adapters (HTTP) and outbound adapters (PostgreSQL, in-memory), wired only in `cmd/`. We chose it because money-movement rules must be enforced and tested in one place no matter how a request arrives, which is common practice in Thai banks.

### Package rules (enforced in review)
```
cmd/<app>/                     composition root: config, wiring, lifecycle
internal/core/domain/          entities, value objects, domain errors (stdlib only)
internal/core/port/            inbound (use-case) and outbound (driven) interfaces
internal/core/service/         use-case implementations; depend on ports only
internal/adapter/inbound/http  HTTP handlers, DTOs, middleware, error → contract mapping
internal/adapter/outbound/*    postgres, memory, … implement outbound ports (library choice: ADR-0003)
internal/platform/*            cross-cutting infra (config, logging, auth) used by adapters and cmd
```
- `core` imports nothing from `adapter`, `platform`, an HTTP framework, an ORM, or a DB driver.
- Adapters depend on `core`, never on each other.
- Transactions are a port (`TxManager.WithinTx`), so the core decides the unit of work without knowing SQL.

## Consequences
- **Positive:** Business rules are unit-tested in milliseconds; a gRPC or Kafka adapter can be added without touching the core; reviewers can check "does core import an adapter?" mechanically.
- **Negative:** More interfaces and DTO↔domain mapping; newcomers must learn where code belongs.
- **Revisit when:** A service is a thin CRUD proxy with no business rules (use layered), or mapping boilerplate slows delivery noticeably.
