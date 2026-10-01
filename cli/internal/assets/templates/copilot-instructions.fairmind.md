<!-- fairmind:begin (managed by `fairmind setup`; edits inside this block are overwritten) -->
## FairMind Project Context

This repository uses **FairMind** as the source of truth for epics, user
stories, tasks, tests, requirements, architectural decisions, open issues and
project history.

- Reach FairMind only through the `fairmind` CLI, always with `--json`.
- Follow the `fairmind-project-context` skill. Its `reference/tools.md` lists
  every FairMind command.

Rules:

- Before a non-trivial change:
  - with a known FairMind id (TASK-/US-/EPIC-/NEED-):
    `fairmind context for-task <ID> --json`;
  - otherwise: `fairmind context get --intent "<task>" --json`.
- Before editing existing files:
  `fairmind context for-code-change --files <paths> --json`.
- To list available projects: `fairmind general list-projects --json`.
- To see planned work: `fairmind work list --kind task|story|epic --json`.
- Any FairMind tool: `fairmind <brain|code|general|insights|studio> <command> --help`.
- Confirmed knowledge is binding. `proposed` is only a hint. `superseded` is
  history. Surface conflicts with the request instead of choosing silently.
- The local checkout is authoritative for current code; FairMind is
  authoritative for rationale and workflow state.
- Write to FairMind (issues, decisions, journals, status) only when the user
  asked for it: `--dry-run` first, then `--yes`.
- Never run destructive or sensitive tools.
- Never read, print or request `FAIRMIND_TOKEN`, the keychain or any
  credential. On exit code 2, ask the user to run `fairmind auth login`.
- If FairMind has no context, say so. Never invent requirements, decisions or
  ids.
<!-- fairmind:end -->
