# Progress ledger — 2026-08-18-operator-usage-mode-companion-page

Branch: `feat/operator-usage-mode-companion-page` (single branch for the whole PRD).

The ledger is reset per PRD because `load_states()` keys on task id alone — stale lines
from a shipped PRD would make this PRD's Task 1 parse as already done. Previous PRD
`2026-08-17-shared-claude-daytime-reserve` shipped.

Running inline (no worktree): sequential execution, subagents disallowed this session
(stated in dev). Task 3 is ready-for-human (static page, manual browser verification) —
out of the req:dev agent-executable set; handed to the operator after Task 2.

<!-- ledger lines below, one per task, in the parseable format -->
- Task 1: complete (commit 905b9677, review clean; inline TDD red→green: 4 behaviors, -race clean, package regression green; coverage adversary N/A — no real-world data)
- Task 2: complete (commit 078abe3e, review clean; inline TDD red→green: 6 write/read behaviors + 1 regression guard, full management + auth packages green; coverage adversary skipped — CRUD over own schema, no real-world data distribution)
- Task 3: complete (commit 5265da59, built + HTTP-smoke-verified: route serves 200 with page, auth-files API 200 with key / 401 without, gating 404 when panel disabled; browser round-trip verification owed to operator on lab — ready-for-human)
- Ticket usage-mode-quota-display: complete (commit fc81349c, review clean; frontend-only, 5h/7d live quota via api-call; verified: 30 node assertions over the real /api/oauth/usage sample + null/non-200/unparseable variants, go build + gating test green, HTTP serve smoke; browser round-trip owed on lab; coverage-adversary independence N/A — subagents disallowed this session, one real sample only)
