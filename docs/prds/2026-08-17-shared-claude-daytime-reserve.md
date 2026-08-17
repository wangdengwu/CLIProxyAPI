# Strengthen shared-account daytime reserve + shift the night window earlier (PRD)

## Problem

Shared Claude accounts are proactively self-blocked by the proxy so that a slice of the
rolling 5h unified-rate-limit budget stays reserved for the account owner's direct use.
The current defaults protect too weakly for the owner's working hours:

- **Daytime** the proxy consumes up to **80%** of the 5h window before blocking — only
  20% is left for the owner while they are actively working. The operator considers this
  insufficient.
- **Nighttime** (currently 22:00–08:00) the proxy is allowed to drain to **98%**. Because
  the 5h window is a fixed-anchor rolling reset that does not continuously regenerate, a
  window drained near-full late at night can still be near-full when the owner arrives at
  ~08:00, defeating any daytime reserve at the morning boundary.

## Solution

Retune the **shared** policy defaults (dedicated accounts are unaffected — they are never
proactively blocked):

- Daytime 5h ceiling: **0.80 → 0.50** — always reserve half the 5h budget for the owner
  during working hours.
- Night window: **22:00–08:00 → 19:00–05:00** — owner goes idle at 19:00, and night ends
  at 05:00 (not 08:00) so that after 05:00 the 0.50 daytime cap governs and the window has
  a ~3h buffer to catch a reset before the owner returns at ~08:00.
- Night 5h ceiling: **0.98 (unchanged)** — nights stay permissive.

This is a defaults-only change: no new config fields, no new evaluation logic. The morning
protection is a **probabilistic mitigation, not a guarantee** — whether the window is fresh
at 08:00 depends on the un-controlled reset anchor falling into the 05:00–08:00 gap. This
residual risk is accepted.

The change ships as new baked-in defaults (new release image) and is deployed to the lab
k8s environment via an Istio rollout.

## User Stories

1. As an account owner working during the day, I want the proxy to stop consuming the
   shared account at 50% of the 5h window, so that half the budget is always reserved for
   my direct Claude use.
2. As an operator, I want the permissive night window to begin at 19:00 (when I leave),
   so that off-hours throughput is maximized while I'm idle.
3. As an account owner arriving in the morning, I want the night window to end at 05:00 so
   that the daytime 0.50 cap re-engages early and the 5h window has a buffer to reset
   before I start work, reducing the chance I hit an already-drained window.
4. As an operator, I want nights to remain permissive (0.98) so that batch/off-hours load
   is not needlessly throttled.
5. As an operator of dedicated accounts, I want this change to leave dedicated-mode
   behavior untouched (never proactively blocked).
6. As a maintainer, I want the two in-code default sources and the example config to agree,
   so that a fresh install and a running pod apply the same policy and nothing drifts.
7. As an operator, I want the retuned defaults built into a new release image and rolled
   out to the lab environment, so that the new policy actually takes effect in production.

## Implementation decisions

Two in-code default sources define the shared policy and MUST be changed together, or they
drift:

- The config layer's `SetDefaults` for the shared rate-limit policy.
- The policy layer's `defaultClaudeSharedRatelimitPolicy` (used when resolving a policy
  from a nil/empty config, e.g. the block-path tests).

Three coordinated edits, all carrying the same values:

- Config defaults: `day-block-threshold` 0.50; `night-start` 19:00; `night-end` 05:00;
  `night-block-threshold` stays 0.98; all other fields (`seven-day-soft-start` 0.70,
  `seven-day-hard-cap` 0.98, `min-block-threshold` 0.03, `alert-margin` 0.05, timezone
  Asia/Shanghai, cooldown 5m) unchanged.
- Policy-layer defaults: identical three values.
- `config.example.yaml`: same three values + comment text, so the documented example is not
  misleading. (Production configmap does NOT pin these keys, so new code defaults take
  effect on deploy; no configmap edit required.)

Derived quantities follow automatically from the existing formula — no code change:
- Daytime alert threshold = 0.50 − 0.05 = 0.45.
- The 7-day guard still only *shrinks* the effective 5h threshold below the day/night base;
  0.50 is the daytime maximum.
- Night-window evaluation already handles the wrap-around case (start > end) for 19:00–05:00.

Boundary semantics after the change: night = 19:00–05:00; day = 05:00–19:00. At/after 05:00
the 0.50 cap applies, so a window drained high overnight is blocked (used ≥ 0.50) until its
reset — this is the intended morning protection.

**Deployment** (release + lab rollout), executed with per-step confirmation because these
actions are irreversible/outward-facing:
- Commit the code change directly to `main` and push (operator chose direct-to-main, not a PR).
- Push tag `v2026.8.14` (date-based `v2026.8.x`, next after `v2026.8.13`) to trigger the
  Docker release workflow building `wangdengwu/cli-proxy-api:v2026.8.14`.
- Wait for the image build+push to finish (hard prerequisite — applying before the image
  exists causes ImagePullBackOff).
- On kube context `dengwu.wang-local-lab`, set the image tag in the gitignored local
  `istio/deployment.yaml` to `:v2026.8.14` and apply it (namespace `gemini`) to trigger the
  Istio rollout.
- Verify: pod image tag, the startup `CLIProxyAPI Version:` log line, and `/healthz`.

## Testing decisions

Two existing seams, one per default source — no new test file needed. Tests verify external
policy behavior (the decision a given state/time yields), not implementation details.

- **Policy behavior seam** — the block-path test file that exercises the pure
  `EvaluateClaudeRatelimitPolicy(state, policy, now)` via `sharedPolicyForMode("shared", nil)`
  (which resolves from the policy-layer defaults) and `sharedNow(t, hour)` (Asia/Shanghai
  timestamp at a given hour). Prior art already covers day @ hour 12 (threshold 0.80, block)
  and night @ hour 23 (threshold 0.98, block). Update/extend to the new policy:
  - Daytime @ hour 12: effective threshold is 0.50; a 5h state at 0.50 blocks, at 0.49 does
    not (replaces the old 0.80/0.79 cases).
  - Night @ hour 23 and @ hour 4: still night; threshold 0.98 (unchanged).
  - New night-boundary cases: hour 19 and hour 4 are night; hour 5 (05:30) and hour 18 are
    day — asserting `IsNight` flips at the new 19:00/05:00 edges.
  - Morning-protection case: at hour 5 (day), a 5h state already at ~0.90 blocks because
    used ≥ 0.50 — encodes the intended re-engagement of the daytime cap after 05:00.

- **Config defaults seam** — the config alert-defaults test that asserts `SetDefaults`
  produces the shared policy values. Update the expected day-block-threshold to 0.50 and the
  night window to 19:00–05:00; keep the "explicit config value is retained over the default"
  case (proves config still overrides defaults). Night-block-threshold expectation stays 0.98.

## Out of scope

- No new config fields (no separate global `MaxBlockThreshold` ceiling — the operator chose a
  day-only 0.50 cap, which the existing `day-block-threshold` already expresses).
- No change to dedicated-mode behavior, the 7-day guard curve, alert plumbing/WeCom, cooldown,
  or the in-memory/restart-clears nature of blocks.
- No production configmap edit (lab configmap does not pin these keys).
- No guaranteed elimination of the morning-boundary drain — mitigation is probabilistic and
  the residual risk is accepted.
- No automated deployment/CI changes beyond pushing the release tag; the Istio rollout is a
  manual, confirmed, per-step operation.
