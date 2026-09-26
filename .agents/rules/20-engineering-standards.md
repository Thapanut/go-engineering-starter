---
trigger: always_on
applyTo: "**"
description: Coding, testing, and change-size standards.
---

- Follow existing patterns in the repo before introducing new ones.
- Layering: handler (transport, validation) → service (business rules) → repository (persistence). No cross-layer shortcuts.
- Every acceptance criterion in the spec maps to at least one automated test. Name tests after the AC id (e.g. `TestAC03_RejectsDuplicateTransfer`).
- Cover the failure paths listed in the spec, not only the happy path.
- Keep diffs small; if a change exceeds ~400 lines, propose splitting the spec.
- Public functions have doc comments; complex business rules reference the spec/AC id.
- Update `CHANGELOG.md` and any affected docs in the same change.
