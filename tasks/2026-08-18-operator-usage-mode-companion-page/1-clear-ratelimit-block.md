---
id: 1
slug: clear-ratelimit-block
prd: docs/prds/2026-08-18-operator-usage-mode-companion-page.md
state: ready-for-agent
category: enhancement
blocked_by: []
---

## What to build

A reverse of the existing in-memory rate-limit block: an operation that lifts an
account-level rate-limit block immediately, so a blocked account can be made available
again without waiting for its window to reset or restarting the process.

Today `applyRatelimitBlock` latches a block on an auth by setting `RatelimitBlockUntil`
(the durable in-memory source of truth the selector checks) plus the aggregate
availability fields, then re-upserts the auth to the scheduler. That block is never
recomputed away — it persists until the reset time passes. There is no way to clear it
early. This slice adds that clear path. It is a self-contained enabler: task 2 calls it
when an account is switched to `dedicated`.

## Key interfaces

- `ClearRatelimitBlock(authID string)` — new exported entry point, mirroring the shape of
  `ApplyRatelimitBlock(authID, resetAt)`: resolves the active manager, no-op when
  `authID` is empty or no manager is registered, delegates to a manager method under the
  lock.
- manager clear method (symmetric to `applyRatelimitBlock`) — under the manager lock,
  for the auth in the map: zero `RatelimitBlockUntil`, set `Unavailable = false`, restore
  active status (clear the block status message), zero `NextRetryAfter`, snapshot, then
  `scheduler.upsertAuth(snapshot)` so selection resumes picking the auth. In-memory only,
  not persisted — exactly like its counterpart.
- Behavior when the auth ID is unknown to the manager: no-op with the same diagnostic-log
  posture `applyRatelimitBlock` uses (do not panic, do not create an entry).

## Acceptance criteria

- [ ] `ClearRatelimitBlock` on an auth that currently has a future `RatelimitBlockUntil`
      zeroes it, sets `Unavailable=false`, restores active status, and zeroes
      `NextRetryAfter`.
- [ ] After clearing, the selector would no longer skip the auth for the block reason
      (the auth is re-upserted to the scheduler).
- [ ] `ClearRatelimitBlock("")` and clearing an auth ID absent from the manager are
      no-ops (no panic, no new map entry).
- [ ] Clearing an auth that is not currently blocked is a harmless no-op (idempotent).
- [ ] Does not persist anything to disk (block state stays process-local, matching apply).

## Out of scope

- Any call site — task 2 wires the call. This slice only adds the capability + its test.
- Changing `applyRatelimitBlock` or the selector's block-check logic.
- Persisting block/clear state across restarts.
