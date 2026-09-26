# ADR-0003: Use Fiber v2 for HTTP and GORM for PostgreSQL access

- **Status:** Accepted
- **Date:** 2026-09-27
- **Deciders:** Thapanut L. (architect / owner)

## Context
ADR-0002 fixes *where* the framework and database code live (adapters only), not *which* libraries to use. The team's services and the wider Thai banking ecosystem mostly use Fiber and GORM. Engineers moving between services should find the same stack, and existing middleware and know-how should carry over. Because of hexagonal architecture, this choice touches only `internal/adapter/*` and `cmd/`.

## Options considered
| Criterion | A. Fiber v2 + GORM | B. Gin + pgx/sqlc | C. net/http (stdlib) + pgx |
|---|---|---|---|
| Team familiarity / hiring | Highest in target ecosystem | Common | Lower for app teams |
| Performance | fasthttp; high throughput | Good | Good |
| Ecosystem compatibility | Not net/http: needs Fiber-specific middleware; no HTTP/2 (TLS/H2 terminated at gateway) | net/http compatible | net/http native |
| DB productivity | High: models, tx helper, error translation | Medium: SQL-first, generated code | Low: hand-written SQL |
| SQL visibility / control | Lower: generated SQL, needs discipline | High | High |
| Security | Parameterized by default; raw SQL still possible | Parameterized | Parameterized |
| Reversibility | High, since both live in adapters (ADR-0002) | — | — |

## Decision
Use **Fiber v2** (`github.com/gofiber/fiber/v2`) for inbound HTTP and **GORM** (`gorm.io/gorm` + `gorm.io/driver/postgres`) for PostgreSQL. We chose them because they are the stack the team and the target ecosystem already use. Fiber v2 is the most widely deployed line and is still patched. Moving to v3 later is a change inside `internal/adapter/inbound/httpapi` only.

### Guardrails
**Fiber:**
- Handlers return errors. One `ErrorHandler` maps them to contract codes.
- Use `decodeStrict` instead of `BodyParser`, which ignores unknown fields.
- Copy (`utils.CopyString`) any request-derived string kept beyond the handler, because fasthttp reuses buffers.
- Pass `c.UserContext()` (it carries the request timeout) to ports.

**GORM:**
- Use GORM models local to the adapter, never tags on domain types.
- Parameterized queries only.
- Take explicit locks with `clause.Locking` where needed.
- Run in `READ COMMITTED` transactions via `TxManager`.
- Set `TranslateError: true`.
- Log SQL only when it is slow or fails, with parameters redacted (`ParameterizedQueries`).
- **No `AutoMigrate`**: schema changes go through `migrations/`.

## Consequences
- **Positive:** The stack is familiar, so onboarding is fast. The transaction helper is less boilerplate. The framework and ORM stay isolated by depguard.
- **Negative:** fasthttp is not compatible with net/http middleware. GORM can hide inefficient SQL (N+1), so reviewers must check generated queries on hot paths.
- **Revisit when:** A service needs HTTP/2 end-to-end or streaming, or GORM becomes a performance bottleneck on a hot path. Then use sqlc/pgx for that repository only, behind the same port.
