
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

## 2026-08-18 · task-complete · Release + lab deploy v2026.8.15
Shipped. ff-merged feat/operator-usage-mode-companion-page -> main (82466aa1..7b6b8188),
pushed; tagged v2026.8.15 on 7b6b8188 -> GitHub Action docker-image.yml built+pushed multi-arch
wangdengwu/cli-proxy-api:v2026.8.15 (run 32096568248, success). Deployed to lab context
dengwu.wang-local-lab ns gemini.

Deploy method — used kubectl set image, not apply. Live deploy was cleanly at v2026.8.14 this
time (no out-of-band image drift, unlike the 8.13-vs-manifest-7.4 drift last PRD). Bumped only the
image via set image deployment/cliproxyapi server=...:v2026.8.15 — the journal-recommended
long-term fix that sidesteps the gitignored istio/deployment.yaml drift entirely. Rollout clean.

Verified. Pod cliproxyapi-6d746d7767-qpxcc Running image v2026.8.15; startup log
'CLIProxyAPI Version: v2026.8.15, Commit: 7b6b818'; /healthz {"status":"ok"}; and the new
feature route in-pod: GET /usage-mode.html -> HTTP 200 with the page marker.

Owed. Browser round-trip of the page against lab (render + toggle a real Claude account + confirm
persist and that a blocked->dedicated account starts serving) — the only unverified piece; the
page's underlying API calls are proven at the HTTP layer. Then req:learn for the PRD.

## 2026-08-18 · task-complete · Ticket usage-mode-quota-display
Built. usage-mode.html now shows per-Claude-account 5h/7d quota. Each row fetches on demand
via POST /v0/management/api-call proxying GET api.anthropic.com/api/oauth/usage ($TOKEN$
substituted server-side), parses the envelope body string, renders five_hour/seven_day.utilization
(0..100 scale) + resets_at countdown + a meter bar. Per-account fetches independent; non-200
status_code / failed api-call / unparseable body -> that row 'unavailable' only; null util/reset/
window -> placeholders (no NaN / Invalid Date). Frontend-only, zero backend change. Commit fc81349c
on feat/usage-mode-quota-display.

Verification. Pure display/parse logic wrapped in a marked pure:begin/end block; a throwaway node
script (/tmp/usage_pure_check.mjs, NOT committed — repo has no JS test infra, brief scopes out UI
tests) extracts that block VERBATIM from the shipped file (no copy -> no drift), evals it, and runs
30 assertions against the operator's REAL /api/oauth/usage sample plus every Known-data variant:
45.0->45% (0..100 not 4500%), null/undefined/NaN util->em dash, null/garbage/past resets_at,
null/absent five_hour->{null,null}, and envelope 401/500/unparseable/empty/null->not-ok. go build
./... clean; gating test TestServeUsageModePanel still green (marker + management endpoint intact);
HTTP serve smoke: /usage-mode.html 200 with the 5h/7d columns and the oauth/usage call present.

Coverage adversary — independence not achieved. Subagents disallowed this session, so the
real-world-data gate could not run as an independent subagent; the variant construction was done by
the same context that wrote the code, and only ONE real sample exists. Residual unknown: the exact
JSON of a 100%/rejected account (does utilization cap at 100? does a field flip?) can't be inferred
from a single normal sample. barWidth clamps to 100 and fmtPercent would show e.g. 105% — no crash —
but the precise shape is unverified. Flagged for the operator to confirm on lab if an over-limit
account exists.

Owed. Browser round-trip on lab: render, confirm live 5h/7d numbers appear per account and a
revoked/expired account degrades to 'unavailable'. Needs deploy first (lab runs the image).

detect_changes. Not run — gitnexus MCP closed (-32000), index stale (2c6b493). Scope: single file
internal/api/usage-mode.html.

## 2026-08-18 · task-complete · Deploy v2026.8.16 (quota display)
Shipped. ff-merged feat/usage-mode-quota-display -> main (b9303178..1c68cc05), pushed; tagged
v2026.8.16 on 1c68cc05 -> Action docker-image (run 32098407246, success) built+pushed multi-arch
wangdengwu/cli-proxy-api:v2026.8.16. Deployed to lab dengwu.wang-local-lab ns gemini via
kubectl set image (was cleanly on v2026.8.15). Rollout clean.

Verified. Pod Running image v2026.8.16; startup log Version: v2026.8.16, Commit: 1c68cc0;
/healthz ok; /usage-mode.html in-pod serves the 5h/7d columns + the api/oauth/usage call.

Owed. One browser round-trip on lab covers everything now: mode toggle (Task 3) AND live 5h/7d
quota rendering + the revoked-account 'unavailable' degrade. Then req:learn for the whole body of
work (PRD + this ticket).

## 2026-08-18 · task-complete · Iterate usage-mode: on-demand load + rate-limit error body
Iterated on ticket usage-mode-quota-display from live operator feedback (two items):
1) On-demand load, not fan-out. render() no longer auto-fetches every account on list load — each
row has a per-row Load button (delegated click on the table host, so retry buttons survive
re-render). Fanning out a usage call to every account on open could itself trip Anthropic rate
limits (which is what the operator hit).
2) Rate-limit error body. The operator captured a rate-limited account: the api-call body is valid
JSON {"error":{"type":"rate_limit_error","message":...}} (pretty-printed, often HTTP 200).
Old code parsed it, found no five_hour/seven_day, and showed two blank em-dashes — misleading.
readEnvelope now checks for an  body FIRST (regardless of status_code) and returns
{ok:false, reason}; the row shows 'rate limited' (humanized) with a retry. This was exactly the
single-sample coverage gap flagged when the ticket shipped — the operator supplied the real sample.

Verification. node /tmp/usage_pure_check.mjs (extracts the pure block verbatim from the file) now
25 assertions over BOTH real samples (normal 45/35 + the real rate_limit_error body) plus variants:
error-body-at-200 and error-body-at-429 both surface reason 'rate_limit_error'->'rate limited';
over-limit 105.4%->'105%' + barWidth clamps to 100; null/absent windows still ok. go build ./...
clean; gating test green; HTTP serve smoke: page 200, data-load button present, delegated handler
present, NO auto-fetch-all on render, 'rate limited' present. Commit 65f8e65b on main.

Owed. Redeploy (v2026.8.17) then the operator browser round-trip.

## 2026-08-18 · task-complete · Fix usage fetch: auth_index not filename
Fixed the usage-fetch identifier bug the operator caught by diffing the working panel request.
The api-call endpoint resolves the per-account token via authByIndex(auth_index), matching auth.Index
(a stable hex runtime id like c4e92118e023e341, exposed as entry.auth_index). The page was passing
the filename/id (claude-hai.yang@sayweee.com.json) as auth_index -> lookup miss -> $TOKEN$ left
literal -> unauthenticated upstream call -> 429 rate_limit_error. Fix: per-row Load now uses
e.auth_index; accounts with no auth_index render an em-dash and no Load button. Mode toggle still
keys on id (PATCH matches GetByID/FileName) — the two operations legitimately use different ids.

Verification. Whole-<script> JS syntax compile check via new Function (catches render/template
errors go build + the pure-block node test cannot); auth_index wiring greps present; node pure
assertions still 25/25; go build + gating test green. Commit pending tag v2026.8.18.

Learning worth keeping: the auth-files list exposes THREE distinct identifiers — id/FileName (for
PATCH/GetByID) and auth_index (for api-call/authByIndex). They are not interchangeable; api-call
needs auth_index specifically.

