---
id: 2
slug: release-and-lab-rollout
prd: docs/prds/2026-08-17-shared-claude-daytime-reserve.md
state: ready-for-human
category: enhancement
blocked_by: [1]
---

## What to build

Ship the retuned defaults from Task 1 as a new release image and roll it out to the lab
Kubernetes environment via Istio, so the new shared policy actually takes effect.

`ready-for-human` because every step is irreversible/outward-facing and needs live external
access (push to `main`, GitHub Actions, `kubectl` against the lab cluster). Even with
auto-decision armed, each of push / tag / apply must be confirmed with the operator before
execution — this is not an AFK slice.

End-to-end sequence:

1. Commit the Task 1 change directly to `main` and push (operator chose direct-to-main, not
   a PR).
2. Create and push tag **`v2026.8.14`** (date-based `v2026.8.x`, next after the current
   latest `v2026.8.13`) to trigger the Docker release workflow that builds and pushes
   `wangdengwu/cli-proxy-api:v2026.8.14`.
3. **Wait** for the image build+push to complete — hard prerequisite; applying before the
   image exists causes ImagePullBackOff.
4. On kube context `dengwu.wang-local-lab`, set the image tag to `:v2026.8.14` in the
   gitignored local `istio/deployment.yaml`, then apply it (namespace `gemini`) to trigger
   the Istio rollout.
5. Verify the rollout landed on the new image and the new policy is live.

## Key interfaces

- Release trigger: pushing a `v*` tag runs `.github/workflows/docker-image.yml`, producing
  multi-arch `wangdengwu/cli-proxy-api:<tag>`. Tag scheme is date-based `v2026.8.x` — bump
  the last number (→ `v2026.8.14`).
- Lab rollout: gitignored `istio/deployment.yaml` (image `wangdengwu/cli-proxy-api`,
  Deployment `cliproxyapi`, namespace `gemini`); `kubectl apply -f istio/deployment.yaml`
  (or `kubectl rollout restart deployment/cliproxyapi -n gemini`) on context
  `dengwu.wang-local-lab`.
- Verification surfaces: pod image tag; the startup `CLIProxyAPI Version:` log line;
  `/healthz` on port 8317.

## Acceptance criteria

- [ ] Task 1 change is committed to `main` and pushed.
- [ ] Tag `v2026.8.14` pushed; the release workflow finished and
      `wangdengwu/cli-proxy-api:v2026.8.14` exists in the registry.
- [ ] `istio/deployment.yaml` references `:v2026.8.14` and has been applied to namespace
      `gemini` on context `dengwu.wang-local-lab`.
- [ ] Running pod reports image tag `v2026.8.14` and a healthy `/healthz`.
- [ ] Each irreversible step (push, tag, apply) was explicitly confirmed with the operator
      before running.

## Out of scope

- No code/behavior changes (all in Task 1).
- No PR flow (operator chose direct-to-main; note the `gh --repo wangdengwu/CLIProxyAPI`
  gotcha only matters if a PR is ever used).
- No production configmap edit; no CI/workflow changes beyond pushing the tag.
- Not delegable to an unattended agent — human confirmation required per step.
