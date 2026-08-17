# Progress ledger — 2026-08-17-preserve-auth-metadata-on-rebind

Branch: `feat/preserve-auth-metadata-on-rebind` (single branch for the whole PRD).

Previous PRD `2026-07-01-claude-ratelimit-alert-block` shipped and merged into `main`;
its per-task record lives in that PRD's own as-built section (tasks 1–4, PRs #3/#4/#6).
The ledger is reset per PRD because `load_states()` keys on task id alone — stale lines
from a shipped PRD would make this PRD's Task 1 parse as already done.

<!-- ledger lines below, one per task, in the parseable format -->
