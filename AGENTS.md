# AGENTS.md — Project Operating Manual for AI Agents

> Single source of truth for every AI tool (Antigravity, Copilot in VS Code, Claude Code, Codex).
> `CLAUDE.md` and `.github/copilot-instructions.md` only point here.

## 1. Roles (non-negotiable)

| Who | Owns |
|---|---|
| **Human (Architect / Owner)** | Business problem, architecture, ADRs, interfaces (OpenAPI/AsyncAPI), data model, specs & acceptance criteria, final approval |
| **AI Agent (Implementer)** | Code, tests, migrations, docs, IaC — **only** within an approved spec |

- The AI **never** changes architecture, public contracts, DB schema, or security controls on its own. If the spec requires it, STOP and propose an ADR / contract change instead.
- The human is accountable for everything merged. AI output is a draft until verified.

## 2. Source of truth (read before any task)

1. `docs/00-business/problem-statement.md` — why we build this, success metrics, constraints
2. `docs/01-architecture/architecture.md` — context, containers, key flows, NFRs
3. `docs/01-architecture/adr/` — decisions already made (do not re-litigate)
4. `contracts/` — OpenAPI / AsyncAPI are the API truth; code must conform
5. `docs/02-specs/<feature>.md` — the task you are implementing

If these conflict: contracts > ADR > spec > architecture doc > your assumptions. Report conflicts; never silently pick one.

## 3. Workflow (skills, invoked as slash commands)

`/design` → `/spec` → `/implement` → `/verify`  (see `.agents/skills/`; helpers: `write-adr`, `threat-model`)

- One spec = one branch = one PR. Small, reviewable diffs.
- Every task ends with the **Verification Report** (see skill `verify`).

## 4. Tech defaults (override via ADR)

- Go 1.25+ (toolchain pinned in `go.mod`), Gin, PostgreSQL (pgx), Kafka, Redis; Angular frontend
- **Hexagonal architecture (ADR-0002)** — dependencies point inward only:
  - `internal/core/domain` entities, value objects, errors — stdlib only
  - `internal/core/port` inbound (use-case) and outbound (repository, tx, clock, id) interfaces
  - `internal/core/service` use cases; depend on ports only
  - `internal/adapter/inbound/*` (HTTP) → call inbound ports; `internal/adapter/outbound/*` (postgres, memory) → implement outbound ports
  - `cmd/<app>` is the only composition root; `internal/platform/*` holds config/logging/auth
  - Enforced by `depguard` in `.golangci.yml`; a violation fails `make lint`
- Money: `int64` minor units (`domain.Money`); never float
- Config via env vars; no hard-coded URLs, credentials, or tenant data
- Errors: wrap with context, map to contract error codes; never leak internals to clients
- Logging: structured JSON, include `trace_id`; **never** log PII, tokens, card/account numbers

## 5. Commands

| Purpose | Command |
|---|---|
| Full verification gate | `make verify` |
| Unit tests (no DB) | `make test` |
| Integration tests (PostgreSQL in Docker) | `make test-integration` |
| Run locally | `make run` (Postgres) / `make run-memory` |
| Dev JWT | `make token SUB=demo-alice` |
| Lint | `make lint` |
| Security scans | `make sec` |
| Contract lint | `make contract-check` |

Run `make verify` before claiming a task is done. `NOT RUN` counts as failure. Paste the real output — do not summarize a run you did not execute.

## 6. Hard rules

- Do not invent APIs, tables, env vars, or library functions. If unknown → mark `UNCONFIRMED` and ask.
- Do not add dependencies without stating why in the PR (license + maintenance).
- Do not disable tests, linters, or security checks to make things pass.
- Do not touch `contracts/`, `docs/01-architecture/`, migrations of released versions unless the spec explicitly allows it.
- Temp scripts go in `tmp/` and are deleted after use.
- Answer the human in Thai unless asked otherwise; code, commits, and docs in English.
