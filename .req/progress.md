# Progress ledger — 2026-07-01-claude-ratelimit-alert-block

Branch: `feat/claude-ratelimit-alert-block` (single branch for the whole PRD).

- Task 1: complete (spike; CONFIRMED CONTRACT recorded in `tasks/.../1-confirm-header-fields.md`; no code)
- Task 2: complete (commits ff2eecb7..8c96421d, review clean, coverage adversary in-scope findings addressed [I-2 fix + C-1/C-2/I-3 tests]; C-3/C-4/I-1/I-5 deferred to tasks 3/4 by scope)
- Task 3: complete (branch feat/claude-ratelimit-alert-wecom, commit 6de44240 off main b4545970, review clean, coverage adversary N/A — operates on own RatelimitState type, not real-world data)
- Task 4: complete (branch feat/claude-ratelimit-block-switch stacked on task 3, commit 2089f3db; selector.go gains account-level Unavailable pre-check [confirmed with user]; review clean, coverage adversary N/A)
