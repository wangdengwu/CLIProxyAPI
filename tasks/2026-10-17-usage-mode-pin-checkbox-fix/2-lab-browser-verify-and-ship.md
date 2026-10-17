---
id: 2
slug: lab-browser-verify-and-ship
state: ready-for-human
category: bug
blocked_by: [1]
---

## What to build

Get the fixed page onto lab and prove in a real browser that the "always last" checkbox works. This is human-owned: it needs a real browser, the lab companion key, and release approval — and it is the only check that can prove render ordering, which the Go source-shape tests cannot.

**Why it can't be delegated.** The previous two cycles reported "done" on source-shape tests alone and the checkbox still never appeared. The browser round-trip is the gate; an agent has no browser, no lab companion key, and shipping is an outward-facing step that needs explicit approval.

**Steps.**
1. Find out which version lab is actually running. The lab page currently answers 401 behind the companion lock, so inspect the deployment's image tag (gemini namespace) rather than the page. If it is older than `v2026.10.16.2`, note that: part of the "still no checkbox" report may be an undeployed fix, not only the ordering bug.
2. After task 1 is merged, cut the next date tag (`v2026.10.16.3`) and let the image build; deploy to lab per the usual flow (gemini namespace; `gh` needs `--repo wangdengwu/CLIProxyAPI`).
3. Browser check on lab:
   - Load accounts → every Claude row shows a checkbox in the Lowest column, no dash flash.
   - Tick one → it stays ticked; untick → it clears; an error (e.g. wrong companion key) reverts the box and shows a message.
   - Click Refresh and reload the page → the ticked state is preserved.
   - The table fits at a normal laptop width without a horizontal scrollbar and reads less cramped than the old 940px.
   - The management panel's auth-files listing still does not expose the pin field (unchanged invariant from the 2026-10-16 PRD).
4. Record the deployment and the browser result in `.req/journal.md` as verify evidence, in the same shape as the earlier lab records.

## Key interfaces
- Lab deployment in the gemini namespace — current image version unknown; desired: the new date tag.
- Companion key for lab — held by the operator; it is bcrypt-hashed server-side and cannot be recovered, only reset (see the emergency-reset journal record).

## Acceptance criteria
- [ ] The version lab was running before this release is recorded in the journal.
- [ ] The new tag is built and deployed to lab.
- [ ] All four browser checks above pass, with the result written to the journal. If any fails, task 1 is reopened rather than patched ad hoc.
- [ ] The management auth-files listing is confirmed not to expose the pin field.

## Out of scope
- Any code change (a failure goes back to task 1).
- Production rollout beyond lab.
- Adding an emergency companion-secret reset endpoint (already decided against in the earlier journal).
