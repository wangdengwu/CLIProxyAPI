
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
