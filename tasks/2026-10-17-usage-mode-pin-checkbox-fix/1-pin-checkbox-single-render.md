---
id: 1
slug: pin-checkbox-single-render
state: ready-for-agent
category: bug
blocked_by: []
---

## What to build

Make the companion page's "Lowest" column actually show the "always last" checkbox on every Claude row, and widen the page.

**Current behavior.** Clicking "Load accounts" renders the table with an em-dash in the Lowest column and it never turns into a checkbox. Cause: the pin states are requested right after the account list request is *started*, not after the table is *rendered*. The pin response usually returns first, finds no rows to fill, and then the table render overwrites each Lowest cell with the em-dash placeholder. A previous fix (86556fc8) only repaired which function the click listener called; it did not touch this ordering, and its test only greps the page source, so it could not see it.

**Desired behavior.**
- Loading accounts fetches the account list (management key) and the Claude pin states (companion key) **in parallel**, waits for both, then renders the table **once** with each Claude row's checkbox already in place — checked when that account is pinned. There is no "render dashes, patch later" step, so there is no ordering to get wrong. Non-Claude rows never get a checkbox.
- Toggling a checkbox saves through the existing companion pin-state write, updates only that row, and reverts the checkbox with a visible error if the save fails. Clicks are handled by one delegated listener on the table host (same style as the existing window-input handler), so redraws never need to rebind anything.
- If the pin-state request fails (non-200 or network error), the Lowest column shows the em-dash and the page shows a visible "pin state failed to load" message. It must **not** fall back to all-unchecked boxes: that would display pinned accounts as unpinned and invite a wrong toggle. A failure of the account-list request behaves exactly as today.
- The "wrap the loader and re-bind listeners" machinery is deleted entirely, not patched.
- The page content area is widened from today's 940px (980px max width less 20px side padding each) to something comfortable for the 7-column table. Pick a sensible max width around 1280px, keep the 20px side padding, and make sure the table does not need a horizontal scrollbar at a ~1280px viewport.

The backend is unchanged: the pin-state GET/POST shapes and the account ids already line up with the account-list entries.

## Key interfaces
- `loadAccounts()` — current: starts the list fetch, a wrapper separately starts the pin fetch. Desired: one flow that awaits both and calls `render` once.
- `render(entries, pinByID)` — current: takes only entries. Desired: also takes a map of account id → pinned state (or an explicit "unavailable" signal for the failure case) and emits the checkbox inline.
- Pin checkbox cell — keeps the `data-pin` attribute (URL-encoded id), the "always last" label, and the warning-tone tooltip "always picked last" from the existing design.
- `togglePin` — keeps its contract (POST `{pinned_to_lowest: bool}` with companion headers; on 401 evict the companion key and reload).
- Removed: `applyPinStates`, `fetchPinStatesAndApply`, `origLoadAccounts`, `origLoadAccountsForListener`, the reassignment of `loadAccounts`, and the remove/add-listener re-bind lines.

## Acceptance criteria
- [ ] The served page no longer contains the loader-reassignment wrapper or any `origLoadAccounts*` identifier.
- [ ] The served page requests the account list and the claude-pin-states endpoint together (one `Promise.all`), and `render` receives the pin map.
- [ ] The Lowest cell for a Claude row is produced inside `render` with the `data-pin` checkbox, `checked` driven by the pin map.
- [ ] A delegated `change` listener on the table host handles `data-pin`; there is no per-render `addEventListener` on the checkboxes.
- [ ] The pin-state failure path renders the em-dash plus a visible message and never renders unchecked boxes.
- [ ] The old 980px max width is gone and the new wider max width is present.
- [ ] `TestServeUsageModePanel_PinColumnWrapperBoundToClickHandler` is replaced by tests for the above (its subject no longer exists); the other companion-panel tests stay green.
- [ ] Every new assertion has a recorded mutation check (reintroduce the old wrapper / drop the pin map from `render` / render unchecked on failure / restore 980px → the matching test goes red).
- [ ] `go build ./...` and `go test ./internal/api/...` are clean.
- [ ] `.req/journal.md` has a record noting the miss: a source-shape-only test passed while an ordering bug shipped, so behavior on this page is only proven by the lab browser check in task 2.

## Out of scope
- Any backend, route, or auth change; the companion lock card and key cards.
- Changes to the shared/dedicated select, window input, or usage-loading behavior beyond what the single render requires.
- Cutting the release tag or deploying (task 2).
- Adding a JS runtime or browser test harness to the repo. No node is available; keep to Go tests over the served body.

Process notes: use Write/Edit only for file changes (no heredoc, `sed -i`, or `node`). Run GitNexus impact on any symbol before editing it and `detect_changes()` before committing; the page is a static embedded asset, so the Go side only touches the test file.
