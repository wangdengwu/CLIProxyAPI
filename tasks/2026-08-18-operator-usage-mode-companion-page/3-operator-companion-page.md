---
id: 3
slug: operator-companion-page
prd: docs/prds/2026-08-18-operator-usage-mode-companion-page.md
state: ready-for-human
category: enhancement
blocked_by: [2]
---

## What to build

An operator web page we fully own — embedded in our binary and served on our own route —
that lets an operator view Claude accounts and toggle each between `shared` and
`dedicated`. It exists because the external `management.html` (auto-fetched and
auto-overwritten from an upstream project) cannot be modified to host this control.

The page:
- is embedded via `go:embed` and served on its own route, following the **exact serving
  pattern** of `management.html`: the HTML is returned **unauthenticated**; authorization
  happens only when the page's JS calls the existing `/v0/management/*` endpoints;
- authenticates those API calls with the **same** management secret the external panel
  uses (`SecretKey` / `MANAGEMENT_PASSWORD` / local password), entered once by the
  operator and kept in `localStorage`;
- is **not served when `disable-control-panel` is set** — disabling the panel disables
  this page too;
- lists **Claude accounts only** (the field is meaningless for other account types), each
  row showing the account's current usage mode and a `shared`/`dedicated` toggle;
- reads current state from the account-listing endpoint (task 2's exposed field) and
  drives the toggle through the field-patch endpoint (task 2's write). Flipping to
  `dedicated` therefore also clears an active block, via the backend path from tasks 1–2.

The external `management.html` and its updater are left completely untouched.

## Why ready-for-human

The page is a static asset with no automated test seam — consistent with how
`management.html` itself is treated. The brief is complete and the build is delegable, but
acceptance requires **manual browser verification** against a running server: a real
management secret, at least one real Claude account, confirming the toggle round-trips and
that a blocked account flipped to `dedicated` starts serving. That verification cannot be
delegated to an AFK agent.

## Key interfaces

- account-listing endpoint (`GET /v0/management/auth-files`) — source of the account rows
  and each row's `claude_usage_mode` (exposed by task 2); filter client-side to Claude
  accounts.
- field-patch endpoint (`PATCH /v0/management/auth-files/fields`) — the write; send
  `{ name, claude_usage_mode }` (task 2 accepts it).
- management-asset serving pattern — mirror how `management.html` is embedded/served and
  how it is gated by `disable-control-panel`; do not route through the authenticated
  management group for the HTML itself (the external panel's HTML is served ungated too).

## Acceptance criteria

- [ ] Navigating to the page's route returns the embedded HTML without requiring a
      management secret for the page load itself.
- [ ] The page is **not** served when `disable-control-panel` is enabled.
- [ ] With a valid management secret entered, the page lists only Claude accounts, each
      showing current `shared`/`dedicated` state.
- [ ] Toggling an account to `dedicated` persists (survives reload) and the account is no
      longer throttled if it was blocked.
- [ ] Toggling back to `shared` persists and re-enrolls the account in throttling.
- [ ] The external `management.html` file and its auto-updater are unchanged.
- [ ] Manual browser verification performed and recorded before marking done.

## Out of scope

- Any automated test for the page (static asset; hand-verified).
- Exposing operator keys other than `claude_usage_mode` on the page.
- Any change to `management.html` or the management-asset updater.
- A general configurable key editor (rejected in the PRD as over-design).
