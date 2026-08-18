# Operator usage-mode companion page (PRD)

## Problem

`static/management.html` is not our code — it is fetched from the external
`router-for-me/Cli-Proxy-API-Management-Center` GitHub release and auto-overwritten
every few hours by the management-asset updater. So any UI control we need but the
external panel does not provide cannot be added there: a local edit is wiped on the
next update, and we cannot ship changes through that project's release flow.

The concrete gap: an operator cannot set a Claude account's `claude_usage_mode`
(`shared` vs `dedicated`) from any interface. The external panel does not offer the
control, and even if it did, the backend would reject it — the management field-patch
API's whitelist accepts only `prefix` / `proxy_url` / `headers` / `priority` / `note`.
Today the only way to mark an account `dedicated` is to hand-edit its auth file, which
`CONTEXT.md` already flags as the standing pain.

## Solution

Stop trying to modify the un-modifiable external panel. Instead ship an **operator
companion page we fully own** — embedded in our own binary, served on our own route,
untouched by the external updater — that hosts exactly the controls the external panel
lacks. This version implements one control (`claude_usage_mode`), but the page and the
backend whitelist are structured so the next operator key is an append, not a rebuild.

From the operator's view: open the companion page, enter the same management secret they
use for the external panel, see the list of Claude accounts with each one's current
usage mode, and flip any account between `shared` and `dedicated`. Flipping to
`dedicated` also immediately clears any active rate-limit block on that account, so an
account that was throttled recovers at once instead of staying 503 until its 5-hour
window resets.

## User Stories

1. As an operator, I want to set a Claude account to `dedicated` from a web interface, so that I no longer have to hand-edit auth files to exempt an account from throttling.
2. As an operator, I want to set a Claude account back to `shared`, so that I can re-enroll it in dynamic reserve throttling without editing files.
3. As an operator, I want the companion page to show each Claude account's current usage mode, so that I can see at a glance which accounts are exempt before changing anything.
4. As an operator, I want the page to list only Claude accounts, so that I am not shown a control that has no meaning for gemini/codex/other accounts.
5. As an operator, I want to authenticate the page with the same management secret I already use, so that I do not have to manage a second credential.
6. As an operator, when I flip an account to `dedicated` while it is currently rate-limit-blocked, I want it to start taking traffic immediately, so that "I set it dedicated but it is still 503ing" never happens.
7. As an operator, I want my usage-mode choice to survive the account's next OAuth login/refresh, so that a token refresh does not silently revert an account to `shared`.
8. As an operator, I want the companion page to disappear when the control panel is disabled, so that disabling the panel disables all operator UI consistently.
9. As a maintainer, I want the external `management.html` left completely untouched, so that its auto-update keeps working and we carry no fork of someone else's project.
10. As a maintainer, I want the backend field-patch whitelist and the page structured for extension, so that exposing the next operator key is a small append rather than new plumbing.

## Implementation decisions

Four units. The source of truth for `claude_usage_mode` on disk is the auth file's
top-level `Metadata` key; `Attributes` is the synthesizer-populated in-memory mirror that
the rate-limit accessor (`Auth.ClaudeUsageMode()`) reads first. Every write must keep the
two in agreement so the change takes effect on the live in-memory auth without a reload.

**Unit 1 — Backend write (extend the field-patch handler).**
Add `claude_usage_mode` to the management field-patch request whitelist as an optional
field. On receipt, normalize via the existing usage-mode normalizer (`exclusive` →
`dedicated`; unrecognized value → 400). Semantics:
- `dedicated`: write the canonical value to both `Metadata` and `Attributes`; then clear
  any active rate-limit block on the auth (Unit 3).
- `shared`: delete the key from both `Metadata` and `Attributes` (empty-value-deletes,
  matching the existing `priority`/`note` convention). Unset then resolves to the
  configured `default-usage-mode`, which defaults to `shared`. Deleting — rather than
  writing an explicit `"shared"` — is the deliberate choice: `shared` carries no
  information beyond the default.
The write persists through the existing auth-manager update path, so the metadata-
preservation logic from the prior PRDs keeps the operator key alive across logins.

**Unit 2 — Backend read (expose current mode in the listing).**
The account-listing handler must include `claude_usage_mode` in each entry, read with the
same Attributes-first-then-Metadata fallback used for `priority`/`note`. Absent key → the
field is omitted (companion page renders it as `shared`).

**Unit 3 — Clear rate-limit block (new, symmetric reverse operation).**
Add a `ClearRatelimitBlock(authID)` operation that is the exact inverse of the existing
`applyRatelimitBlock`: under the manager lock, zero `RatelimitBlockUntil`, set
`Unavailable = false`, restore active status, zero `NextRetryAfter`, snapshot, and
re-upsert to the scheduler so selection resumes picking the auth. In-memory only, like
its counterpart. Called by Unit 1 when an account is switched to `dedicated`.

**Unit 4 — Companion page (embedded, own route).**
A new static page embedded via `go:embed` and served on its own route, following the
exact serving pattern of `management.html`: the HTML is served **unauthenticated**;
authorization happens when the page's JS calls the existing `/v0/management/*` endpoints
with the management secret (same `SecretKey` / `MANAGEMENT_PASSWORD` / local password as
the external panel; entered once, kept in `localStorage`). The page:
- respects the `disable-control-panel` flag — not served when the panel is disabled;
- lists Claude accounts only, each showing current mode and a `shared`/`dedicated` toggle;
- drives the toggle through the Unit-1 patch endpoint and refreshes from the Unit-2 listing.

No new config fields, no change to rate-limit evaluation logic, no change to the external
asset or its updater.

## Testing decisions

Two existing backend seams carry the behavior; the page is static and hand-verified (no
automated test), consistent with how `management.html` is already treated.

- **Field-patch handler seam** — `auth_files_patch_fields_test.go` (existing; prior art:
  `TestPatchAuthFileFields_MergeHeadersAndDeleteEmptyValues`). New cases: patch
  `dedicated` → both `Metadata` and `Attributes` carry `dedicated` and
  `Auth.ClaudeUsageMode()` returns `dedicated`; patch `shared` → both keys deleted and the
  accessor returns empty (falls back to default); patch `exclusive` → normalized to
  `dedicated`; unrecognized value → 400; an already-blocked auth patched to `dedicated`
  has its `RatelimitBlockUntil` cleared (the new cross-unit behavior — red→green).
- **Account-listing exposure** — same handler package seam: a listed Claude account with a
  set mode exposes `claude_usage_mode`; one without the key omits it.
- **Clear-block seam** — `ratelimit_block_test.go` (existing; symmetric to the
  `applyRatelimitBlock` tests): `ClearRatelimitBlock` zeroes `RatelimitBlockUntil`, flips
  `Unavailable` back, and re-upserts to the scheduler; no-op on unknown/empty auth ID.

Good tests here assert external behavior — what the accessor returns, what the listing
exposes, whether the selector would skip the auth — not the internal field-set order.

## Out of scope

- Any modification to the external `management.html` or its auto-updater.
- Exposing operator keys other than `claude_usage_mode` (`priority`/`note` are already
  patchable elsewhere; the page/whitelist are merely *structured* to add more later).
- A general-purpose configurable operator-key editor — explicitly rejected as over-design
  for a single current need.
- Automated tests for the companion page itself (static asset; hand-verified).
- Changing rate-limit evaluation, thresholds, windows, or the `default-usage-mode` config.
- Clearing rate-limit blocks on any transition other than → `dedicated` (e.g. → `shared`
  re-enrolls in throttling and re-blocks naturally on the next over-threshold response).
