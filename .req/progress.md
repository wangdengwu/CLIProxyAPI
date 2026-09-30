# Progress ledger — 2026-09-30-pin-management-panel-asset

Branch: `feat/pin-management-panel-asset` (single branch for the whole PRD).

The ledger is reset per PRD because `load_states()` keys on task id alone — stale lines
from a shipped PRD would make this PRD's Task 1 parse as already done. Previous PRD
`2026-09-16-auth-availability-window` shipped as v2026.9.16 and was operator-verified on
lab; its history lives in `.req/journal.md`.

Task 2 is hard-blocked by Task 1: turning the background updater off before the asset is
embedded would leave a freshly recreated pod with no panel at all (404), which is worse
than the bug being fixed.

Task 1 carries one owed human verification — an operator browser round-trip on lab
confirming v1.24.2 still works against this v6.10.9-lineage v0 API.

POST-DEPLOY CORRECTION (commit 44694eff): v2026.9.30 shipped inert. LoadConfig backfills
panel-github-repository with the default repository, so both slices' "no override
configured" branch was unreachable in production while every unit test stayed green —
they built Config by hand and skipped the loader. Both slices now decide on
RemoteManagement.PanelRepositoryOverridden() (set AND different from the default), with
regression tests that go through LoadConfig and assert the backfill still happens so they
cannot silently go vacuous. The startup log line added for an acceptance criterion is what
caught it.

Worktrees skipped: `git worktree` is outside this project's permission allowlist. Running
directly on the PRD branch instead.

<!-- ledger lines below, one per task, in the parseable format -->
- Task 1: complete (commit fb9f3d66, review clean; inline TDD in two waves, red verified before each; asset vendored from release v1.24.2 with sha256 cross-checked against the GitHub release digest; earlier "pinned URL but still download" design self-rejected — no agility bought, runtime dependency kept, and it forced disabling the fallback page which always serves latest; the v0-not-v8 assertion's discriminating power shown against the two REAL files (lab's broken v1.25.0 has 3 hits of /v8/management, the embedded v1.24.2 has 0) rather than a synthetic mutation; two implementation mutations verified red (ETag dropped → 304 test fails; branch forced to embedded → escape-hatch test fails), the second of which exposed a 2.7MB failure dump now truncated; tests moved off gin.CreateTestContext onto a real gin engine because gin buffers the status code and a bodyless 304 reads as 200 to a bare recorder; go build + go vet + internal/api -race green; coverage adversary N/A — this slice serves opaque bytes, no real-world data distribution to be wrong about; NOTE: test-honesty isolation not bought — Agent tool unavailable in this project, controller wrote both tests and impl; OWED: operator browser round-trip on lab; pre-existing and untouched: gofmt violation in internal/api/server.go (HEAD already violates, misplaced `_ "embed"`) and 4 racy Delete*Key tests in internal/api/handlers/management (reproduced at HEAD via git stash))
- Task 2: complete (commit 1d960a4b, review clean; inline TDD, red verified first; brief deviation written back — the predicate returns a skip REASON string rather than a bool, because the four gates each carry a distinct operator-facing debug line and a bool would force the caller to re-derive the same conditions to pick one, guaranteeing drift; mutation verified: deleting the new condition turns exactly the two intended subtests red and leaves the other three green; blank-repository case covered separately because "   " would fall back to the default repository via resolveReleaseURL and silently undo the whole change; documentation debt repaid — panel-github-repository had NO entry in config.example.yaml despite now being the unpin switch, and disable-auto-update-panel's description was stale; go build + go vet + gofmt clean on both touched files + managementasset/api -race green; coverage adversary N/A — pure config predicate, no real-world data; NOTE: test-honesty isolation not bought, Agent tool unavailable)
