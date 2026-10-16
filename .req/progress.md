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
- Task 2: complete (commit f53d2c5e, review clean; inline TDD across 16 behaviors — authPriority 10-row table covers Claude-pinned + non-Claude-pinned + numeric priority interactions + Metadata-only fallback + multiple-pinned round-robin, scheduler 2 cases for pinned-below-non-pinned and pin-beats-higher-numeric, LIST not exposing the field, PATCH rejecting the field, companion endpoints for write-paths/both-maps/clear/Claude-only-400/missing-404/companion-key-only-when-secret-set/clear-block-on-pin; mutations verified — skip isPinnedLowest in authPriority flips 3+ cases red, leak claude_pin_to_lowest in listing flips the pin-hidden test red, ClearRatelimitBlock before Update re-installs the previous block — the ordering matters because Update clones the auth snapshot the endpoint already mutated, so clear must come AFTER persist; new IsPinnedToLowest exported alongside isPinnedLowest for the management package to consume; companion group is a SEPARATE engine.Group with CompanionAuthMiddleware only — management key alone cannot reach it; go build + go vet + gofmt clean)
- Task 3: complete (commit a82b9818, review clean; inline TDD across 6 source-shape assertions on the served page HTML — LockState card / companion key card / dual-key localStorage (cpa-companion-key vs cliproxy.mgmtKey) / companion endpoints referenced / lock-setter uses managementHeaders / Lowest column header + data-pin attr / 401-fallback reload; three mutations verified red — collapse companion-key into the management key, change POST pin-states URL to non-Companion path, swap managementHeaders → companionHeaders on the lock-setter (the last is the most consequential — a phished companion key would otherwise remove its own gate); page JS uses location.reload() after set/clear/toggle so middleware enforces new state on next request; per memory \"UI verification without node\" — no JS runtime test, source shape is asserted via the real gin engine serving the embedded HTML; the existing TestServeUsageModePanel_ServesWhenEnabled still passes (page marker + Content-Type + auth-files endpoint reference unchanged); go build + go vet + gofmt clean)