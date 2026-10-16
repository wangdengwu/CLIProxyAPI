# Companion key and Claude pinned-lowest (PRD)

## Problem

Two gaps in the operator workflow that this PRD closes together because they touch the same surface (the companion page at `/usage-mode.html` and the operator-set-keys family on Claude accounts):

1. **The companion page shares the management key.** `/usage-mode.html` is reachable to anyone who has the bearer for `/v0/management/*` — same secret as the external `management.html`. The companion page is meant for a narrower trust set than the full management plane. The operator wants to lock it independently, and wants to be able to set that lock from the companion page itself rather than from `config.yaml` + a redeploy.

2. **No way to pin a Claude account to "always picked last".** A `priority` integer field already exists on auth files and the scheduler already orders by it, but there is no anchor for an absolute bottom slot. The operator wants to designate one Claude account — currently indistinguishable from the others to a reader of `management.html` — as the last fallback, and wants that designation to be **invisible to the external management panel** (not listed, not patchable) and **survivable across OAuth re-login**.

## Solution

Add `remote-management.companion-secret` and a `CompanionAuthMiddleware` that gates `/usage-mode.html` and two new companion endpoints when the secret is set. The secret is empty by default (page is open), and is set, rotated, or cleared from the companion page itself through one new management-keyed endpoint, so the first-run flow is "open → set lock from the page → locked".

Add a Claude-only operator-set-key `claude_pin_to_lowest: bool`. When set on a Claude account, the scheduler pins that account to the lowest priority bucket (`math.MinInt`) regardless of the account's own `priority` integer. The field lives only behind companion-keyed endpoints; the standard `/v0/management/*` listing does not surface it and the standard field-patch whitelist does not accept it, so the external panel neither sees it nor writes it. The companion page gains a per-row toggle.

## User Stories

1. As an operator, I want `/usage-mode.html` to require a separate password that only I hold, so that anyone who only has the management key cannot view it.
2. As an operator, on first install I want the page to be reachable without any password so that I can configure the lock from within the page itself.
3. As an operator, I want to set, change, or remove the lock from the page (using my existing management key to authenticate the underlying write), so that I do not need to edit `config.yaml` and redeploy whenever I rotate the lock.
4. As an operator, I want the lock to take effect on the very next request after I save it (hot-reload), so that I can confirm the lock works by reloading the page and seeing it ask for a password.
5. As an operator, I want the existing `claude_usage_mode` toggle (`shared`/`dedicated`) to keep working with the management key as it does today, so that introducing the companion key does not break any current workflow.
6. As an operator, I want the companion page to display Claude accounts and let me mark a particular one as "always picked last", so that I can keep a specific account as a last-resort fallback.
7. As an operator, I want that flag to survive OAuth re-login, so that refreshing tokens does not silently revert my designation.
8. As an operator, I want that flag to be invisible in the external management panel — neither listed nor patchable — so that no one with the management key alone can see or change it.
9. As an operator, I want the priority effect to be absolute, not relative: a pinned account is the very last bucket regardless of what other operators do to other accounts' priorities.
10. As an operator, I want multiple accounts pinned lowest to share the lowest tier and round-robin among themselves, so that I can mark several as last-pick without one starving the others.
11. As an operator, I want to be able to unpin an account (set the flag off) and have it return to its normal priority slot, so that the flag is reversible.
12. As an operator, I want the lock-setter endpoint to require the management key (not the companion key), so that an attacker who somehow guesses or phishes the companion key cannot reconfigure the lock itself.
13. As an operator, I want the new companion endpoints (read pin-states, toggle pin-to-lowest) to require only the companion key, so that I can use the companion page in read/toggle workflows without re-entering the management key.
14. As a maintainer, I want the standard `/v0/management/*` plane unchanged — same routes, same auth, same whitelist — so that no upstream `management.html` panel breaks.
15. As a maintainer, I want the new field to be additive in the `Operator-set-keys` family (preserved across OAuth rebind, written to both `Metadata` and `Attributes`), so that it follows the existing invariant rather than introducing a new storage shape.

## Implementation decisions

### Unit 1 — Companion lock: config + middleware + setter endpoint

Add `RemoteManagement.CompanionSecret` next to the existing `RemoteManagement.SecretKey`. The existing `LoadConfig` flow that hashes `secret-key` on first save is reused verbatim: if the loaded value is non-empty and not already a bcrypt hash, hash it before persisting back to `config.yaml`. Document the new field in `config.example.yaml` next to `secret-key`.

Add a new `CompanionAuthMiddleware` that accepts the companion secret via either `Authorization: Bearer …` or `X-Companion-Key`. When `companion-secret` is empty, the middleware bypasses (the page is open). When non-empty, it verifies against the configured hash and returns 401 with `WWW-Authenticate: Basic realm="Companion panel"` on failure so browsers can offer native credentials.

Add one new management-keyed endpoint `POST /v0/management/companion-secret` with body `{current_secret?, new_secret}`:

- Empty current `companion-secret`, any `new_secret` → set (first-run path).
- Non-empty current, matching `current_secret` → set or clear (`new_secret=""`).
- Non-empty current, missing or wrong `current_secret` → 400.

The endpoint writes through the same `SaveConfigPreserveCommentsUpdateNestedScalar` path used by the existing secret rotation, and relies on the existing watcher to hot-reload `cfg` into the live handler so the next request enforces the new state. Startup logs an explicit warning when `companion-secret` is empty so the "first run is open" state is observable.

Apply `CompanionAuthMiddleware` to:

- `GET /usage-mode.html`
- `GET /v0/management/companion/claude-pin-states`
- `POST /v0/management/companion/claude-pin-states/:id`

### Unit 2 — Pin-to-lowest: storage + scheduler + companion endpoints

Add `claude_pin_to_lowest` (bool) to the `Operator-set-keys` family. Like `claude_usage_mode`, it is written to both `Metadata` (on-disk source of truth) and `Attributes` (in-memory mirror that the selector reads first). Empty / `false` means delete from both maps, matching the convention used by `priority` and `note`. The field is meaningful only when `auth.Attributes["type"] == "claude"`; writes to non-Claude accounts are rejected with 400, and reads from non-Claude accounts ignore the field.

Extend `authPriority` so that when `isPinnedLowest(auth)` is true for a Claude account, the function returns `math.MinInt`, regardless of the account's own `priority` integer. This makes the floor absolute: even if another operator later sets some account's priority to a very negative number, a pinned account stays at the bottom. Multiple pinned Claude accounts land in the same bottom bucket and the existing `pickReadyLocked` round-robins among them. Non-Claude providers ignore the flag and use the unchanged priority path.

Hide the field from the external management panel by:

- Filtering `claude_pin_to_lowest` out of the existing `GET /v0/management/auth-files` listing (it does not appear in any entry).
- Not adding `claude_pin_to_lowest` to the existing `PATCH /v0/management/auth-files/fields` request struct or whitelist; the existing behavior of 400 on unknown fields already gates this.

Add two companion-keyed endpoints:

- `GET /v0/management/companion/claude-pin-states` returns a list of `{auth_id, pinned_to_lowest}` for every Claude account.
- `POST /v0/management/companion/claude-pin-states/:id` accepts `{pinned_to_lowest: bool}`. On `true` for a Claude account, the write goes through the same atomic `Metadata`+`Attributes` path as `claude_usage_mode` and clears any in-memory rate-limit block via the symmetric `ClearRatelimitBlock` so the account returns to a selectable state at once. On `false`, the key is deleted from both maps. Non-Claude accounts return 400; missing IDs return 404.

### Unit 3 — Companion page UI

`usage-mode.html` is restructured so the lock state is visible and operable from the top of the page. A `LockState` card sits above the management-key card and renders one of three states based on what the page detects by probing the new companion endpoint:

- `unlocked-not-set`: server has no `companion-secret`; show a "set lock" form (new + confirm) and warn that the page is currently reachable to anyone with the URL.
- `locked-no-key`: server has a lock, the page has no `companionKey` in `localStorage` (or it's wrong); show the companion-key input and hide the table.
- `unlocked-with-key`: server has a lock and the locally-stored companion key was just verified; show a "change lock" form and a "remove lock" button (the latter requires the current key).

The form actions call the management-keyed `/v0/management/companion-secret` endpoint, then reload the page so the new state is enforced by the middleware on the very next request. `localStorage` stores `cpa-companion-key` and `cpa-management-key` separately. The existing management-key card and the table stay in place.

The Claude accounts table gains a "Lowest priority" column with a per-row toggle. The page bootstraps by calling the new companion-keyed `GET /v0/management/companion/claude-pin-states` and merging the result into the existing row list. Toggling a row calls `POST /v0/management/companion/claude-pin-states/:id` with the companion key and refreshes that row's state without a full reload. Non-Claude rows never render the toggle. The toggle uses the warning-color tone already in the page's palette and a tooltip explaining "always picked last".

The page is unchanged otherwise and remains an embedded static asset; no build step, no new dependency.

### Unit 4 — Testing and verify

All Go tests run through the real gin engine via `engine.ServeHTTP` (the seam proven out in PRD 2026-09-30). New test files:

- A middleware test file: empty `companion-secret` lets the page through; non-empty rejects wrong bearer / wrong `X-Companion-Key` with 401 + `WWW-Authenticate`; correct bearer / correct key returns 200; bcrypt-hashed stored value still verifies against the plain-text secret supplied by the caller.
- A companion-secret endpoint test file: first-time set without `current_secret`; overwriting requires correct `current_secret`; clearing requires correct `current_secret`; the write is persisted to `config.yaml` (verify via the same `SaveConfigPreserveCommentsUpdateNestedScalar` path) and the live handler picks up the new state on the next request (proving hot-reload); the endpoint requires the management key, not the companion key, including when the management key alone is presented.
- `auth_priority_test.go` covers the five behaviors in the Unit 2 selector table and a mutation check: changing `math.MinInt` back to the base priority must turn at least three of the five green cases red.
- `auth_files_pin_hidden_test.go` asserts that `claude_pin_to_lowest` is absent from every entry of `GET /v0/management/auth-files` and that a PATCH body containing it returns the existing 400 for unknown fields.
- `companion_pin_states_test.go` covers the new endpoints: GET without a companion key when `companion-secret` is empty returns 200; GET without a companion key when `companion-secret` is set returns 401; POST `true` for a Claude account writes to both `Metadata` and `Attributes`, returns `math.MinInt` from `authPriority`, and clears the in-memory block; POST `false` deletes from both; POST to a non-Claude account returns 400; POST to a missing id returns 404; both endpoints require the companion key (not the management key) once `companion-secret` is set.
- `scheduler_test.go` adds a case where multiple Claude accounts share the pinned-lowest bucket and verifies round-robin among them across repeated picks; the existing non-pinned round-robin behavior continues to pass.

Hand-verification (no automation for the page, consistent with prior companion-page PRDs): operator opens the page in a browser on lab, walks the lock lifecycle (open → set → unlock → change → remove → wrong-key), toggles lowest-priority on a Claude account and observes both the UI state and (via a separate management-keyed client) that `GET /v0/management/auth-files` does not expose the field.

## Testing decisions

The seams for this PRD are:

- The existing management handler test packages under `internal/api/handlers/management/` — the same seam used for `auth_files_*_test.go` and `auth_files_usage_mode_test.go`. The new companion endpoints and the companion-secret endpoint slot into this package; their tests follow the same engine-driven shape (build the real gin engine, send real `httptest.NewRequest`s, assert on status code and body). New test files in this package: a companion-auth middleware test, a companion-secret endpoint test, a pin-hidden test on the existing field-patch whitelist and listing, and a companion-pin-states test for the new endpoints.
- The existing `sdk/cliproxy/auth/` test packages — the same seam as `auth_priority_test.go` and the broader selector/scheduler suite. `isPinnedLowest` and `authPriority` are exercised here, and the multi-account-pinned scheduler case extends the existing round-robin tests.
- The existing `usage-mode.html` is hand-verified per the convention established in PRD 2026-08-18 and reaffirmed in PRD 2026-09-30 (page is a static asset; automated tests have not been retrofitted for it and there is no value in doing so for this PRD).

Tests assert external behavior (status code, response body shape, what `authPriority` returns, what `GET /v0/management/auth-files` does or does not include) rather than internal field-set order. Mutation checks are used where the assertion is otherwise indistinguishable from the no-op branch, following the precedent in PRD 2026-09-30 (e.g., turning `math.MinInt` back to the base value, removing the new whitelist entry, removing the new middleware).

The verify chain is: `go test ./...` clean; lab deployment under a `v2026.10.x` date tag; operator browser round-trip on lab; PRD journal records both the lab deployment and the browser verification, in the same shape as PRD 2026-09-30.

## Out of scope

- Any change to `management.html` or its auto-updater. The external panel keeps using the existing `/v0/management/*` routes unchanged.
- Any change to the `priority` integer field's semantics. The new `claude_pin_to_lowest` flag is additive and orthogonal; existing operators who set `priority` continue to get the same behavior.
- A general-purpose "field-by-name visibility filter" for `/v0/management/*`. The hiding is implemented as a single targeted omission of `claude_pin_to_lowest`, not as a generalized config-driven filter.
- Any new gating semantics for other operators or roles. The PRD introduces exactly one new secret (`companion-secret`) and exactly one new key (`claude_pin_to_lowest`); no RBAC, no per-endpoint scoping beyond the three new companion routes.
- A separate "clear ratelimit block on unpin" behavior. Unpinning leaves the account's existing block state alone; only pinning clears the block (matching `claude_usage_mode → dedicated` symmetry).
- Automated tests for the companion page itself. Hand-verified per the existing convention.
- Migrating any existing `priority: -N` settings into `claude_pin_to_lowest: true`. They are different mechanisms with different semantics; existing operators who already use negative integers keep their behavior.