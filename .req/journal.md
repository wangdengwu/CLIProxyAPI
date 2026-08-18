
## task-complete — Task 1 (preserve-helper-and-file-store) — 2026-08-17

**Built.** `misc.ApplyPreservedMetadata(path, storage, recordMeta)` in `internal/misc/metadata_preserve.go`,
wired into the `auth.Storage != nil` branch of `FileTokenStore.Save`, replacing the local
`metadataSetter` assertion (behaviorally equivalent, plus preservation). 16 unit cases in
`internal/misc/metadata_preserve_test.go` + 5 wiring cases in `sdk/auth/filestore_test.go`.
Commit `dc8647e9` on `feat/preserve-auth-metadata-on-rebind`.

**Gotcha found mid-slice (not in the PRD or the brief).** The design says the preserved set
excludes "every key the fresh payload defines", and the brief operationalized that as
`MergeMetadata(storage, recordMeta)`'s key set. That is the *marshaled* key set, not the schema:
kimi's `scope`, `device_id` and `expired` are `omitempty`, so a refresh-token login that leaves
them empty drops them out of `fresh` — they then land in the preserved set and the *stale* values
get written back. A stale `expired` on a brand new token is precisely the credential-rollback the
invariant exists to forbid. Fixed by also excluding keys the storage type *declares*
(`declaredJSONKeys`, reflection over json tags, `omitempty` included, `-` excluded, untagged
embedded structs walked). Currently unreachable in production only because kimi's timestamp-based
filenames mean the old file is never at the new path — i.e. the safety came from an unrelated
accident, which is why it was worth fixing rather than documenting.

**Verification.** Red→green captured for every behavior. Mutation-tested the three invariants:
neutering the fresh-key exclusion fails 8 cases incl. the credential guard; removing the `disabled`
exclusion fails its case; reverting the file-store wiring to the old `SetMetadata` call fails the
wiring test. Swapping the merge order of preserved/recordMeta survives — an *equivalent* mutant,
not a gap: `recordMeta ⊆ fresh` always, so the two maps are disjoint by construction.
`go test ./...` clean except a pre-existing `internal/registry` failure
(`TestCodexFreeModelsExcludeGPT55`), confirmed failing on a stashed clean tree — unrelated.

**Deviations from the skill's prescribed loop.** Ran the *inline* shape (controller as both roles),
no worktree, no subagent coverage adversary — this session's operating constraints disallow
spawning subagents. The adversarial pass over the brief's Known data variants was therefore done
by the same context that wrote the tests, i.e. without the independence the gate is designed to
buy. It did find the `omitempty` hole, but a genuinely independent pass is still owed.

**Left for Task 2.** Postgres, object store and git store each need the same one-line call before
their write. No tests planned there (accepted risk in the PRD) — the shared logic is covered, only
"did we remember to call it" can drift.

## task-complete — Task 2 (wire-remaining-stores) — 2026-08-17

**Built.** One `misc.ApplyPreservedMetadata(path, auth.Storage, auth.Metadata)` call in the
`auth.Storage != nil` branch of `PostgresStore.Save`, `ObjectTokenStore.Save` and
`GitTokenStore.Save`, immediately before the write, identical to the file store's. Git store
needed the `misc` import; the other two already had it. Post-write steps untouched.
Commit `2d1fd0b9`.

**Worth recording about the drift.** The three backends did not merely lack *preservation* — they
had no metadata injection at all. So the record metadata a login prepared was already being
discarded on postgres/object/git before this PRD existed; the file store was the only one that
honored it. That makes this slice a slightly bigger behavior change than "wire in the new helper"
suggests: on those three backends, `auth.Metadata` now reaches the file for the first time.

**Review finding the brief did not raise.** The whole PRD is justified by "production and lab run
postgres", but preservation is anchored to a file path — so it is worth nothing if the old file
isn't on local disk at Save time. Checked: `PostgresStore.Bootstrap` calls `syncAuthFromDatabase`,
which wipes and rebuilds the local auth dir from the DB before serving. The old file is therefore
present even on a cold pod, and the postgres cut genuinely takes effect. Had that not held, the
whole PRD would have shipped as a no-op in the environment it was written for — this is the check
to repeat if the backend's local-spool strategy ever changes.

**Verification.** Review-based by design (no test infra for these three; accepted risk in the PRD).
`go build ./...`, `go vet ./internal/store/...` clean; `go test ./internal/store/...` passes
(existing gitstore tests unaffected); full `go test ./...` clean except the pre-existing
`internal/registry` failure. `detect_changes` vs main: 8 symbols, 0 affected processes, risk low —
the four `Save` methods and the task-1 test file, nothing else.

**Not done, deliberately.** No tests for the three backends. `internal/store/gitstore_test.go`
already has temp-repo infrastructure, so covering `GitTokenStore.Save` is cheap and would close
most of the accepted risk — the brief names this as a reasonable follow-up but explicitly out of
scope here.

## task-complete — Task 1 (retune-shared-policy-defaults) — 2026-08-17

**Built.** Shared Claude 5h policy defaults retuned: `day-block-threshold` 0.80→0.50,
night window 22:00–08:00 → 19:00–05:00; `night-block-threshold` 0.98 unchanged. Changed in
BOTH in-code default sources — `config.go SetDefaults` and `ratelimit_policy.go`
`defaultClaudeSharedRatelimitPolicy` — plus `config.example.yaml`, so a fresh install and a
nil-config-resolved policy agree (the two sources would otherwise drift; the block-path tests
resolve via the policy-layer default, the config tests via SetDefaults, so each source has its
own seam). Defaults-only: no new fields, no evaluation-logic change. Commit `a3449b2d` on
`feat/shared-claude-daytime-reserve`.

**Verification.** Inline TDD (controller both roles — subagents disallowed this session), two
honest red→green cycles: (A) config-defaults test → red on 0.80/22:00/08:00 → green after
SetDefaults edit; (B) policy block test (4 existing assertions retargeted to the 0.50 day base +
2 new behaviors: `TestEvaluateSharedNightWindowBoundaries` asserting IsNight flips true@19:30/
04:30, false@05:30/18:30, and `TestEvaluateSharedMorningDayCapReengages` asserting a 0.90 window
blocks at 05:30 under the reinstated 0.50 cap) → red (5 fails) → green after the policy-default
edit. `go build ./...` OK; touched packages green; full `go test ./...` clean except the
pre-existing `internal/registry` `TestCodexFreeModelsExcludeGPT55` (untouched here, documented
failing on a clean tree in the previous PRD's journal).

**Gotcha worth keeping.** The 7-day guard test (`TestEvaluateSharedSevenDayGuard`) hardcodes the
day base inside its expected-value formula: `want := 0.50 * ((0.98-0.80)/(0.98-0.70))`. The 0.80
there is the 7d *used* input (matches `win(0.80)`), NOT the day threshold — only the leading
multiplier (0.50) is the day base. A naive grep-and-replace of "0.80" would have corrupted the
guard math. Changing a policy base means re-deriving this test's arithmetic by hand, not swapping
a literal.

**Coverage adversary.** Skipped by design — this slice parses no real-world data (no rate-limit
header distribution to be wrong about); it only sets threshold/time constants. The header parsing
that could carry variants lives upstream of this slice and is unchanged.

**Left for Task 2.** Release + lab rollout (ready-for-human): ff-merge this branch to main, push,
tag v2026.8.14, wait for the GitHub Action image, bump `istio/deployment.yaml` to :v2026.8.14 and
apply on context dengwu.wang-local-lab (ns gemini), verify pod image + version log + /healthz.
Each irreversible step confirmed with the operator.

## task-complete — Task 2 (release-and-lab-rollout) — 2026-08-17

**Shipped.** ff-merged `feat/shared-claude-daytime-reserve` → `main` (2c6b4932..0cf95c40),
pushed; tagged `v2026.8.14` on 0cf95c40 → GitHub Action `docker-image.yml` built+pushed
multi-arch `wangdengwu/cli-proxy-api:v2026.8.14` (run 32029805306, success ~1m). Applied to
lab context `dengwu.wang-local-lab` ns `gemini`; rolling update clean. Verified: deploy+pod
image `v2026.8.14`, startup log `CLIProxyAPI Version: v2026.8.14, Commit: 0cf95c4`, `/healthz`
`{"status":"ok"}`.

**Gotcha worth keeping — istio/deployment.yaml drifts from the live cluster.** The gitignored
local `istio/deployment.yaml` pinned `v2026.7.4`, but the cluster was actually running
`v2026.8.13`; the live `last-applied-configuration` annotation still recorded 7.4, proving 8.13
was set out-of-band (`kubectl set image`-style), not via `apply`. So the local manifest is NOT a
reliable mirror of cluster state. Before applying a stale manifest, run `kubectl diff` first — it
was read-only proof the only real delta was the image (8.13→8.14), every other field matched
live, so a full apply was safe. If a future rollout shows extra diff, stop and reconcile rather
than reverting live fields. Better long-term fix: either commit istio/ or always deploy via
`kubectl set image` so the file stops pretending to be authoritative.

**Env note.** The kubectl wrapper in this shell rejects flags collapsed into a shell variable
("flags cannot be placed before plugin name"); pass `--context`/`-n` inline per command.

**Nothing left in this PRD.** Both tasks complete; proceeding to req:learn.
## 2026-08-18 · task-complete · Task 1 (clear-ratelimit-block)
Built. ClearRatelimitBlock + m.clearRatelimitBlock in sdk/cliproxy/auth/ratelimit_block.go,
strict inverse of applyRatelimitBlock: under m.mu zero RatelimitBlockUntil, Unavailable=false,
Status=StatusActive, StatusMessage="", NextRetryAfter=zero, then scheduler.upsertAuth(snapshot).
In-memory only. Exported wrapper resolves activeRatelimitTarget, no-op on empty id / no manager.

Verification. Inline TDD (controller both roles, subagents disallowed this session), red->green:
4 behaviors appended to the existing ratelimit_block_test.go seam (LiftsBlockAndRestoresActive,
SelectorStopsSkipping, UnknownOrEmptyNoop, NotBlockedIsIdempotent). Compile-fail red confirmed,
then green; -race clean; full sdk/cliproxy/auth package regression green; go build ./... + vet OK.
Commit 905b9677.

Gotcha. detect_changes could not run — the gitnexus MCP connection is closed this session
(-32000). Fell back to git diff --stat for scope proof: only ratelimit_block.go (+47) and its
test (+80). GitNexus index is also stale (last 2c6b493); did not reindex.

Coverage adversary. N/A by design — pure in-memory state mutation, no real-world data parsed.

Left for Task 2. Wire ClearRatelimitBlock into the dedicated write path of PatchAuthFileFields.

## 2026-08-18 · task-complete · Task 2 (usage-mode-management-api)
Built. PatchAuthFileFields (internal/api/handlers/management/auth_files.go) gains an
optional claude_usage_mode: normalizeClaudeUsageModeValue (inlined mirror of the unexported
helps normalizer — management pkg cannot import runtime/executor/helps) maps
exclusive->dedicated, unknown->"". dedicated writes both Metadata+Attributes and sets
clearBlock; shared deletes both keys; invalid -> 400. After a successful authManager.Update,
clearBlock triggers coreauth.ClearRatelimitBlock(id) (Task 1). ListAuthFiles exposes
claude_usage_mode Attributes-first-then-Metadata in BOTH the manager path (buildAuthFileEntry)
and the disk-fallback path (listAuthFilesFromDisk, via gjson) for parity with priority/note.
Commit 078abe3e.

Verification. Inline TDD red->green, 7 tests in new auth_files_usage_mode_test.go seam:
dedicated-both-maps, shared-deletes-both, exclusive-normalizes, invalid->400-no-mutation,
dedicated-clears-block, omit-leaves-untouched (regression guard), list-exposes/omits. 5 strong
red first, then green; full management + sdk/cliproxy/auth packages green; go build ./... + vet
clean. Only failure repo-wide is the pre-existing internal/registry TestCodexFreeModelsExcludeGPT55
(documented failing on clean tree in prior PRDs; unrelated).

Review finding (self, resolved by verification not code). The invalid-mode path returns 400
after other fields may have mutated targetAuth. Checked: Manager.GetByID and List both return
auth.Clone(), so targetAuth is a detached copy — bailing discards it, no partial in-memory leak.
Left the mutate-then-validate ordering as-is (consistent with the rest of the handler); no early
validation needed. This is the check to repeat if GetByID/List ever return live pointers.

Coverage adversary. Skipped by design — CRUD over our own schema with a 3-value enum, no
real-world data distribution to be wrong about (PRD declared no Known data variants).

detect_changes. Not run — gitnexus MCP closed this session (-32000); index also stale (2c6b493).
Scope proven via git show --stat: only auth_files.go (+81) and the new test (+233).

Left for Task 3 (ready-for-human). Embedded companion page calling these two endpoints;
manual browser verification.

## 2026-08-18 · task-complete · Task 3 (operator-companion-page)
Built. Embedded operator companion page. internal/api/usage-mode.html (go:embed
usageModeHTML), route s.engine.GET("/usage-mode.html", s.serveUsageModePanel) next to
management.html, handler gates on RemoteManagement.DisableControlPanel (404) and serves the
embedded HTML unauthenticated. Vanilla-JS page: management key in localStorage sent as
Authorization: Bearer, GET /v0/management/auth-files filtered to provider==claude, per-row
shared/dedicated select -> PATCH /v0/management/auth-files/fields {name:id, claude_usage_mode}.
exclusive normalized to dedicated for display; absent mode shown as shared; HTML-escaped labels;
control reverts on PATCH failure. Commit 5265da59.

Why embedded, not external. management.html is fetched+auto-overwritten from
router-for-me/Cli-Proxy-API-Management-Center every ~3h; a companion control there cannot
survive. Embedding in our binary on our own route sidesteps the updater entirely.

Verification. Inline build (static asset; brief scoped OUT automated UI tests). Added a
server-route gating guard (internal/api/usage_mode_panel_test.go, NOT a UI test): serves 200 +
page marker + references the management endpoint when enabled; 404 when DisableControlPanel — a
security-relevant guard (disabling the panel must disable all operator UI). Both green. Then a
real HTTP smoke against a built binary + temp config: /usage-mode.html -> 200 with marker (proves
route registration + embed serving, which the direct-handler unit test bypasses);
/v0/management/auth-files -> 200 with Bearer key, 401 without. go build ./... + vet clean.

Owed (ready-for-human). Browser round-trip: render, toggle a real account, confirm persist +
that a blocked account flipped to dedicated starts serving. The API calls the page makes are
already proven at the HTTP layer; only the in-browser interaction is unverified. Operator will
do this on lab post-deploy.

detect_changes. Not run — gitnexus MCP closed (-32000), index stale (2c6b493). Scope via git:
server.go (embed var + 1 route + serveUsageModePanel), new usage-mode.html, new gating test.

