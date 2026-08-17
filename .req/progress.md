# Progress ledger — 2026-08-17-shared-claude-daytime-reserve

Branch: `feat/shared-claude-daytime-reserve` (single branch for the whole PRD).

Previous PRD `2026-08-17-preserve-auth-metadata-on-rebind` shipped; the ledger is reset
per PRD because `load_states()` keys on task id alone — stale lines from a shipped PRD
would make this PRD's Task 1 parse as already done.

Running inline (no worktree): single code task, subagents disallowed this session — stated in dev.

<!-- ledger lines below, one per task, in the parseable format -->

- Task 1: complete (commit a3449b2d, review clean; inline TDD, two red→green cycles captured; coverage adversary N/A — no real-world data parsing)
- Task 2: complete (released tag v2026.8.14, deployed to lab context dengwu.wang-local-lab ns gemini; verified pod image + version log commit 0cf95c4 + /healthz ok; ready-for-human, each outward step operator-confirmed; no repo commit — istio/ is gitignored)
