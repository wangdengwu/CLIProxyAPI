---
id: 2
slug: usage-mode-management-api
prd: docs/prds/2026-08-18-operator-usage-mode-companion-page.md
state: ready-for-agent
category: enhancement
blocked_by: [1]
---

## What to build

The backend surface for reading and setting a Claude account's `claude_usage_mode`
through the existing management API — so that, via curl or any client, an operator can see
each account's current usage mode and flip an account between `shared` and `dedicated`
without hand-editing auth files. Switching to `dedicated` also clears any active
rate-limit block on that account, so a throttled account recovers immediately.

Two ends of one path:

**Write.** Extend the management field-patch handler's request whitelist with an optional
`claude_usage_mode`. On receipt, normalize with the existing usage-mode normalizer
(`exclusive` → `dedicated`; any unrecognized value → HTTP 400, write nothing). Then:
- `dedicated`: write the canonical value to both the auth's `Metadata` and `Attributes`
  (Attributes so the live in-memory auth reflects it without a reload — the rate-limit
  accessor reads Attributes first), then call `ClearRatelimitBlock` (task 1) for the auth.
- `shared`: delete the key from both `Metadata` and `Attributes` (empty-value-deletes,
  matching how this handler already treats `priority`/`note`). Do **not** write an
  explicit `"shared"` — unset resolves to the configured default, which is `shared`.
The change persists through the existing auth-manager update path so metadata preservation
keeps the key alive across future logins.

**Read.** The account-listing handler includes `claude_usage_mode` in each entry, read
with the same Attributes-first-then-Metadata fallback already used for `priority`/`note`.
When the key is absent, omit the field (clients treat absence as `shared`).

## Key interfaces

- field-patch request struct — add an optional `claude_usage_mode` string pointer,
  alongside the existing `prefix`/`proxy_url`/`headers`/`priority`/`note`. `nil` = field
  not present in the request = leave unchanged.
- usage-mode normalizer — reuse the existing `shared`/`dedicated`/`exclusive`→canonical
  helper; only `shared` and `dedicated` are valid write targets after normalization;
  anything else is a 400.
- `Auth.ClaudeUsageMode()` — the read accessor (Attributes-first, then Metadata) is the
  behavioral oracle: after a `dedicated` write it must return `dedicated`; after a
  `shared` write it must return empty (falls back to default).
- `ClearRatelimitBlock(authID)` — from task 1; called on the `dedicated` path only.
- account-listing entry builder — add `claude_usage_mode` exposure mirroring the
  `priority`/`note` exposure it already does.

## Acceptance criteria

- [ ] PATCH with `claude_usage_mode: "dedicated"` sets both `Metadata` and `Attributes`
      to `dedicated`; `Auth.ClaudeUsageMode()` returns `dedicated`.
- [ ] PATCH with `claude_usage_mode: "shared"` deletes the key from both maps;
      `Auth.ClaudeUsageMode()` returns empty (default fallback).
- [ ] PATCH with `claude_usage_mode: "exclusive"` normalizes and stores `dedicated`.
- [ ] PATCH with an unrecognized usage-mode value returns HTTP 400 and mutates nothing.
- [ ] An auth with an active rate-limit block, patched to `dedicated`, has its
      `RatelimitBlockUntil` cleared (via task 1) and becomes selectable again.
- [ ] The listing exposes `claude_usage_mode` for an account that has one, and omits the
      field for an account that does not.
- [ ] Omitting `claude_usage_mode` from a patch request leaves any existing value
      untouched (still merges alongside prefix/proxy_url/headers/priority/note as before).

## Out of scope

- The companion page (task 3) — this slice is the API only, demoable via curl.
- Adding any other operator key to the whitelist.
- Changing rate-limit evaluation, thresholds, or the `default-usage-mode` config default.
- Clearing the block on any transition other than → `dedicated`.
