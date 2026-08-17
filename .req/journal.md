
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
