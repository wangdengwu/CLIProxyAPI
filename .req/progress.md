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
- Task 1: complete (commit 08fae584, review clean; inline TDD — characterization net first (7 cases, mutation-verified: wording + ceil→floor both turn it red), then red→green on the parameterized construction path; go build + full-package -race + vet/gofmt green; zero existing-test and zero call-site changes; coverage adversary N/A — pure refactor, no real-world data; NOTE: test-honesty isolation not bought — subagents disallowed this session, controller wrote both tests and impl)
- Task 2: complete (commit 091b19eb, review clean; inline TDD behavior-by-behavior; fail-open mutation-verified 36/36; go build + full-package -race + related packages + vet/gofmt green; coverage adversary ran as SELF-review only — subagents disallowed, independence NOT achieved — it still found two real silent-failure gaps ("18:00-24:00" and unpadded hours both failed open), both fixed and written back into the brief; NOTE: test-honesty isolation not bought, same reason; pre-existing unrelated failure internal/registry TestCodexFreeModelsExcludeGPT55 confirmed via git stash; pre-existing gofmt violation in ratelimit_block_test.go left untouched)
