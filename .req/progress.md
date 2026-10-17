# Progress ledger — 2026-10-17-usage-mode-pin-checkbox-fix

Source: ticket-style task folder `tasks/2026-10-17-usage-mode-pin-checkbox-fix/` (no PRD; discuss
conclusion went straight to tasks). Related PRD for context:
`docs/prds/2026-10-16-companion-key-and-claude-pinned-lowest.md` (Unit 3).

Branch: `fix/usage-mode-pin-checkbox-single-render` (single-task fix, one branch).

Worktree skipped: single task touching one embedded static asset + its test file. Inline TDD
(controller writes both tests and implementation) — the implementer-cheating risk that
isolation buys is not credible for a UI-shape change; compensating discipline is a mutation
check per new assertion (7 mutations, each turned exactly its own test red).

Task 2 (`lab-browser-verify-and-ship`) is `ready-for-human` and is the hard gate for this fix:
source-shape tests cannot prove render ordering, which is exactly what leaked last time. Task 1 is
code-complete but NOT verified-in-browser until Task 2 passes.

<!-- ledger lines below, one per task, in the parseable format -->
- Task 1: complete (commit 8380edda, review clean; inline TDD — 6 new source-shape assertions on the served page (NoLoaderWrapperMachinery / LoadsListAndPinStatesTogether / RenderEmitsPinCheckboxInline / PinToggleIsDelegated / PinStateFailureIsNotUnchecked / WiderLayout) replacing the obsolete PinColumnWrapperBoundToClickHandler; 7 mutations verified red (re-add wrapper, drop pin-states from Promise.all, static em-dash cell, drop data-pin delegate branch, fetchPinStates failure → {}, drop typeof-boolean guard, width back to 980px); extra non-repo evidence: JavaScriptCore parse OK + stub-DOM sync-promise race simulation — HEAD renders 0 checkboxes / 2 dashes, fixed renders a1 checked + a2 unchecked, pin 500 → 2 dashes + message; go build + go vet + go test ./internal/api/... clean; NOT browser-verified — see Task 2)
