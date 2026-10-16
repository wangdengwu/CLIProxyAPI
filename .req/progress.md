# Progress ledger — 2026-10-16-companion-key-and-claude-pinned-lowest

Branch: `feat/companion-key-and-claude-pinned-lowest` (single branch for the whole PRD).

Worktrees skipped (same as prior PRD): `git worktree` is outside this project's
permission allowlist. Running directly on the PRD branch.

Agent tool unavailable in this project (same as prior PRD): test-honesty isolation
not bought — the controller writes both tests and implementation. Compensating
discipline: each behavior red-verified before its implementation; mutation checks
on the discriminating assertions (PRD 2026-09-30's standard); tests run through
the real gin engine via `engine.ServeHTTP` because gin buffers status codes and
a bodyless 304 reads as 200 to `gin.CreateTestContext` (same finding as
PRD 2026-09-30).

Tasks 1 + 2 are coupled for rollback: Task 2's endpoints consume Task 1's
`CompanionAuthMiddleware`. A standalone revert of Task 1 would leave the
companion endpoints ungated; a standalone keep of Task 2 without Task 1 would
expose them unauthenticated. Order of rollback must be reverse (2 → 1).

Task 3 only touches `usage-mode.html` (a `go:embed` asset) and is independently
revertable.

<!-- ledger lines below, one per task, in the parseable format -->
- Task 1: complete (commit 6d82b306, review clean; inline TDD across 9 behaviors — middleware passthrough/wrong-bearer/missing-key/correct-bearer/x-companion-key/bcrypt-stored, LoadConfig empty/plaintext/bcrypt, endpoint first-set/overwrite/wrong-current/clear/requires-mgmt-key/hashForStorage-shape, hot-reload proven in same process by NOT calling SetConfig from the test; four mutations verified red — delete WWW-Authenticate, skip current_secret, skip bcrypt hash, skip hot-reload — each caught by the relevant assertion; CompanionSecret misspells added to RemoteManagement and LoadConfig mirrors the secret-key hash-on-load path; PostCompanionSecret follows PutConfigYAML's persist+reload pattern; new endpoint lives under /v0/management with management middleware (NOT companion middleware) so a phished companion key can't lock/unlock itself; companion middleware wired in served under /usage-mode.html; go build + go vet + gofmt clean; coverage adversary N/A — no real-world data distribution in this slice, the shape is config+endpoint and the existing TestPanelRepositoryOverridden_ThroughRealConfigLoad pattern already proves the loader path is exercised; NOTE: pre-existing test TestCodexFreeModelsExcludeGPT55 in internal/registry fails on the bare branch, untouched and unrelated)