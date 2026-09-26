# ADR-0001: Record architecture decisions and use an AI-assisted, human-verified SDLC

- **Status:** Accepted
- **Date:** YYYY-MM-DD

## Context
We use AI agents to produce most implementation artifacts. We need traceability of *why* decisions were made and clear accountability.

## Decision
Humans own architecture, contracts, and specs, recorded as ADRs and specs in this repo. AI agents implement only APPROVED specs and must pass `make verify` plus human review before merge.

## Consequences
- **Positive:** Faster delivery with auditable decisions; prompts/specs are versioned and reproducible.
- **Negative:** Upfront spec effort; reviewers must actually read diffs.
- **Revisit when:** Defect escape rate from AI-produced code exceeds agreed threshold.
