---
id: 1
slug: usage-mode-quota-display
state: ready-for-agent
category: enhancement
blocked_by: []
---

## What to build

On the operator companion page (`usage-mode.html`), show each Claude account's current
**5-hour** and **7-day** quota usage next to its shared/dedicated control, fetched live
with no backend change.

**Current behavior.** The page lists Claude accounts (name/email, current mode, a
shared/dedicated select). It shows nothing about how much of each account's rate-limit
window is consumed.

**Desired behavior.** For each listed Claude account, after the account list loads, the
page fetches that account's usage and renders, per account:
- 5h: utilization percent + a reset time (absolute and/or countdown).
- 7d: utilization percent + a reset time.

Usage is fetched **on-demand, per account, entirely from the frontend** by POSTing to the
existing management endpoint `POST /v0/management/api-call`, which proxies an authenticated
GET to Anthropic's usage endpoint using the account's token. No new backend route, no
stored state — the number is always live.

Each account's usage fetch is independent: a slow or failing fetch for one account must
not block the others, must not block the mode toggles, and must degrade to a clear
"unavailable" indicator for just that row.

## Key interfaces

- `POST /v0/management/api-call` (existing) — request
  `{ "auth_index": <entry.auth_index or entry.id>, "method": "GET",
     "url": "https://api.anthropic.com/api/oauth/usage",
     "header": { "Authorization": "Bearer $TOKEN$", "Content-Type": "application/json",
                 "anthropic-beta": "oauth-2025-04-20" } }`.
  `$TOKEN$` is substituted server-side with the account's access token. Authenticate the
  call with the same management key the page already holds (Bearer header).
- Response envelope (HTTP 200 when the proxied call itself ran): `{ status_code, header,
  body }` where `body` is the **upstream response body as a string** — the page must
  `JSON.parse(body)` and must first check `status_code === 200` before trusting it.
- Account identity — the list entries already carry `auth_index` and `id`; use the same
  identifier the existing mode-toggle PATCH uses so both operate on the same account.

## Known data variants

Real sample of the parsed `body` (fields the page depends on shown; many sibling keys
omitted — do not assume they are absent, just unused):
- `five_hour`: `{ "utilization": 45.0, "resets_at": "2026-08-18T07:09:59.911059+00:00", ... }`
- `seven_day`: `{ "utilization": 35.0, "resets_at": "2026-08-22T11:59:59.911085+00:00", ... }`

Variants to survive:
- `utilization` is a number on a **0–100 scale** (percent), not a 0–1 ratio; may be `0.0`;
  may be `null` (seen on sibling buckets like `extra_usage`) → render "—", not "NaN%".
- `resets_at` is ISO-8601 with **fractional seconds and a `+00:00` offset**; may be `null`
  (seen on sibling buckets) → render the percent without a countdown, don't crash.
- `five_hour` or `seven_day` themselves may be absent/`null` for an account with no such
  window → render that window as unavailable, not an error for the whole row.
- `api-call` envelope `status_code` may be non-200 (revoked/expired token → upstream 401,
  or `api-call` returns 400 "auth token refresh failed"/"auth token not found") → that
  row shows "usage unavailable", the rest of the page stays functional.
- `body` may fail `JSON.parse` (defensive) → treat as unavailable for that row.

## Acceptance criteria

- [ ] After the account list loads, each Claude row shows its 5h and 7d utilization as a
      percent and each window's reset, sourced from a live `api-call` fetch.
- [ ] `utilization` is rendered on the correct 0–100 scale (45.0 → "45%", not "4500%").
- [ ] A `null`/absent `utilization`, `resets_at`, or window object renders a placeholder
      for just that field/window — no crash, no "NaN"/"Invalid Date".
- [ ] A per-account fetch that returns non-200 `status_code`, a non-200 `api-call`, or an
      unparseable body shows "usage unavailable" for that row only; other rows and the
      mode toggles keep working.
- [ ] Per-account fetches run independently (one slow/failed account does not block others
      or the page).
- [ ] Changing an account's mode still works exactly as before (this slice does not regress
      the shared/dedicated toggle).
- [ ] No backend/Go change — the diff is limited to the embedded page asset.

## Out of scope

- Any backend change: no new route, no stored ratelimit state, no change to `api-call`,
  `ListAuthFiles`, or the rate-limit engine.
- Showing usage for non-Claude accounts (the page lists only Claude accounts).
- Dollar-denominated fields (`limit_dollars`/`spend`), per-model buckets
  (`seven_day_opus`/`seven_day_sonnet`/…), or `extra_usage` — 5h + 7d utilization only.
- Auto-refresh/polling of usage on a timer (a manual Refresh already reloads the list;
  usage re-fetches with it). Live polling is a separate ask if wanted.
- Automated browser/UI tests for the page (static asset; hand-verified), consistent with
  how the rest of this page is treated.
