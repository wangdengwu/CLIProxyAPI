# Progress ledger — 2026-09-16-auth-availability-window

Branch: `feat/auth-availability-window` (single branch for the whole PRD).

The ledger is reset per PRD because `load_states()` keys on task id alone — stale lines
from a shipped PRD would make this PRD's Task 1 parse as already done. Previous PRD
`2026-08-18-operator-usage-mode-companion-page` shipped and was operator-verified on lab
(v2026.8.18); its history lives in `.req/journal.md`.

Task 1 is a prefactor (pure refactor, no user-visible change) — it exists so Task 3 can add
a second error code without duplicating the whole error-rendering path.

Task 5 requires a browser round-trip on lab for final confirmation; the rest is
agent-executable.

<!-- ledger lines below, one per task, in the parseable format -->
