---
id: 1
slug: retune-shared-policy-defaults
prd: docs/prds/2026-08-17-shared-claude-daytime-reserve.md
state: ready-for-agent
category: enhancement
blocked_by: []
---

## What to build

Retune the **shared** Claude rate-limit policy defaults so that during the day the proxy
reserves half the rolling 5h budget for the account owner, and the permissive night window
starts earlier and ends earlier:

- Daytime 5h ceiling: **0.80 → 0.50**.
- Night window: **22:00–08:00 → 19:00–05:00**.
- Night 5h ceiling: **0.98 (unchanged)**.

All three values live in **two in-code default sources** plus the example config, and MUST
change together — a partial change leaves the sources drifted (a fresh install / nil-config
policy would disagree with the config-defaulted one). No new config fields and no change to
the evaluation logic; this is purely a defaults retune. Dedicated-mode accounts are never
proactively blocked and must stay untouched.

Derived behavior follows automatically from the unchanged formula: daytime alert threshold
becomes 0.45 (0.50 − alert-margin 0.05); the 7-day guard still only shrinks the effective
threshold below the day/night base; the night wrap-around (start 19:00 > end 05:00) is
already handled. After the change, day = 05:00–19:00, night = 19:00–05:00, so at/after 05:00
the 0.50 cap re-engages and a window drained high overnight blocks (used ≥ 0.50) until reset.

## Key interfaces

- Config-layer shared-policy defaults (the `SetDefaults` path for `claude-ratelimit-alert`):
  set day-block-threshold 0.50, night-start "19:00", night-end "05:00"; leave
  night-block-threshold 0.98, seven-day-soft-start 0.70, seven-day-hard-cap 0.98,
  min-block-threshold 0.03, alert-margin 0.05, timezone Asia/Shanghai, cooldown 5m as-is.
- Policy-layer `defaultClaudeSharedRatelimitPolicy()` — the fallback used when a policy is
  resolved from a nil/empty config (e.g. block-path tests via
  `sharedPolicyForMode("shared", nil)`): the same three values, kept identical to the config
  layer.
- `config.example.yaml` `claude-ratelimit-alert.shared` block — the same three values plus
  their inline comment text, so the documented example is not misleading.
- `EvaluateClaudeRatelimitPolicy(state, policy, now)` — unchanged behavior; used by tests to
  assert the new thresholds and night boundaries.

## Acceptance criteria

- [ ] Config `SetDefaults` yields day-block-threshold 0.50, night-start 19:00, night-end
      05:00; night-block-threshold stays 0.98; all other shared fields unchanged.
- [ ] `defaultClaudeSharedRatelimitPolicy()` returns the identical three new values (no drift
      vs the config layer).
- [ ] `config.example.yaml` shows 0.50 / 19:00 / 05:00 with matching comments; other keys
      unchanged.
- [ ] Policy-behavior tests updated/extended: daytime (e.g. 12:30) blocks at 5h used 0.50 and
      does not block at 0.49; `IsNight` is true at 19:30 and 04:30, false at 05:30 and 18:30;
      night (23:30) threshold stays 0.98; a daytime (05:30) state at ~0.90 blocks because
      used ≥ 0.50.
- [ ] Config-defaults test updated to expect 0.50 and the 19:00–05:00 window, while retaining
      a case proving an explicit config value still overrides the default.
- [ ] Dedicated-mode remains never-proactively-blocked (existing behavior, unbroken).
- [ ] `go build ./...` and `go test ./...` pass (modulo the known pre-existing
      `internal/registry` failure unrelated to this change).

## Out of scope

- No new config field (no separate global `MaxBlockThreshold`); the day-only 0.50 cap is
  expressed by the existing day-block-threshold.
- No change to the 7-day guard curve, alert/WeCom plumbing, cooldown, or the
  in-memory/restart-clears block behavior.
- No consolidation of the two default sources into one (a tempting refactor, deliberately
  deferred).
- No production/lab configmap edit (that env does not pin these keys).
- Deployment/release is Task 2.
