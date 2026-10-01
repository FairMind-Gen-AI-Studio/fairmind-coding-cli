---
name: fairmind-project-context
description: >
  Use FairMind (via the `fairmind` CLI) whenever project requirements,
  architectural decisions, previous implementation knowledge, tests, project
  documents, epics / user stories / tasks, or implementation history may
  affect the work; before implementing a FairMind item (TASK-*, US-*, EPIC-*,
  NEED-* or a 24-char id) or modifying existing code; when the user asks what
  is planned, open or decided; and to record issues, decisions, journals or
  task status in FairMind when the user asks for it.
# No allowed-tools on purpose: pre-approving `shell` would let any command run
# unconfirmed. Approve only this CLI when starting Copilot:
#   copilot --allow-tool 'shell(fairmind:*)'
---

# FairMind Project Context

FairMind is the project's knowledge and workflow layer. It holds epics, user
stories, tasks, tests, requirements, decisions and why they were taken, open
issues, documents and code knowledge. Reach it **only** through the `fairmind`
CLI:

- Always pass `--json` and parse the result.
- Never try to reach FairMind any other way: no MCP, no curl, no reading config
  files.

Every call returns `{"ok": bool, "data": ..., "page": ..., "error": ...}`
and an exit code (see *Errors*).

## 1. Start with the aggregate commands

These combine several FairMind sources server-side. Prefer them for context.

| Situation | Command |
|---|---|
| Which projects exist / are available | `fairmind general list-projects --json` |
| The user names a FairMind item (`TASK-2026-0548`, `US-2026-0600`, `EPIC-2026-0007`, or an ObjectId) | `fairmind context for-task <ID> --json` |
| A non-trivial request without an id | `fairmind context get --intent "<one-sentence task>" --json` |
| Before editing existing files (once you know which) | `fairmind context for-code-change --files <path>... --json` |
| After editing, before finishing | `fairmind context for-code-change --diff --json` |
| Looking for a rule, concept or document | `fairmind search "<query>" --json` (`--in brain,docs,code,studio`) |
| "What is there to do?" / listing work | `fairmind work list --kind task\|story\|epic [--status <s>] [--parent <ID>] [--all] --json` |
| A result lists a `follow_up` command | run it as given |

Skip FairMind for trivial edits (typos, formatting) that cannot conflict with a
requirement or decision.

## 2. The full FairMind toolset

Every FairMind tool is also a CLI command, generated from the live catalog:

```
fairmind <namespace> <command> [--flag value ...] --json
namespaces: brain | code | general | insights | studio
```

**Discovery:**
- `fairmind tools list --json`, filtered with `--namespace studio` or `--access read`.
- `fairmind <namespace> --help`.
- `fairmind <namespace> <command> --help`.
- The full reference with every parameter is
  [reference/tools.md](reference/tools.md). Read the section you need rather
  than guessing flags.

**Conventions:**
- Required parameters can be passed positionally:
  `fairmind studio get-user-story US-2026-0600 --json`.
- `project` / `project_id` default to this folder's project
  (`.fairmind/config.json`) and accept the project **name**. `git_remote`
  defaults to the local remote. `agent` defaults to the configured agent name.
- Arrays take repeated or comma-separated values:
  `--kinds decision,issue`.
- Objects and complex arrays take JSON, e.g.
  `--relates '{"caused_by":["DEC-1"]}'`. Alternatively pass the whole argument
  set with `--args-json '{...}'`.
- Long text: `--journal-content @journal.md` reads a file and `@-` reads stdin.

**Useful reads** (all safe):

| Need | Command |
|---|---|
| Story / task / epic details | `studio get-user-story <ID>`, `studio get-task <ID>`, `studio get-need <ID>` |
| Stories of an epic / tasks of a story | `work list --kind story --parent <EPIC-ID>`, `work list --kind task --parent <US-ID>` (reliable; the `list-*-by-need` tools can return incomplete results) |
| Related stories | `studio get-related-user-stories <ID>` |
| Test cases of a story | `studio list-tests-by-userstory <story ObjectId>` |
| Requirements of a session | `studio list-functional-requirements-by-session`, `studio list-technical-requirements-by-session` |
| Why a file/symbol is like this | `brain context --file-path <path> --repository <repo>` |
| How a decision evolved | `brain timeline --knowledge-id <node_id>` |
| Full knowledge node | `brain get <node_id>` |
| Semantic knowledge search with filters | `brain search --query "<q>" --kinds decision,issue` |
| Project documents | `general rag-retrieve-documents --query "<q>"`, `general get-document-content <id>` |
| Indexed code (other repositories too) | `code list-repositories`, `code search --repository <r> --query "<q>"`, `code cat`, `code grep`, `code find-usages` |
| Sessions | `general list-work-sessions` |

IDs: most `get` tools accept both the FairMind id (`US-2026-0600`) and the
ObjectId. Tools documented as "MongoDB ObjectId" need the `id` / `object_id`
field. Get it from `get-*`, `work list` or `context for-task`
(`evidence.resolved_id.object_id`).

## 3. Reading results: trust model

- `review_state: "confirmed"` is accepted human knowledge. Follow it.
- `review_state: "proposed"` is NOT confirmed, and is often agent-inferred.
  Treat it as a hint and say so explicitly.
- `status: "superseded"` / `"retired"` is history. Follow the item named in
  `superseded_by`; use the old one only to explain the rationale.
- Always read `data.warnings`:
  - `STALE_REPOSITORY_DATA`: trust the local checkout for current code and
    FairMind for rationale.
  - `BRAIN_INDEX_SPARSE`: some items were matched by title only. Open them with
    `context for-task <id>`.
  - `REPOSITORY_NOT_BOUND` / `NO_KNOWLEDGE_FOR_FILES`: FairMind has nothing
    anchored there. Do not invent it.
- If a confirmed requirement or decision conflicts with the request, stop and
  point out the conflict before acting.

Source of truth: the **local checkout** for current code and git state;
**FairMind** for requirements, decisions, history and workflow state.

## 4. Writing to FairMind

Write tools are marked `write` in `tools list`. They change shared project
state and are only allowed when **the user asked for it**, or when the current
task explicitly includes updating FairMind.

1. Preview first with `--dry-run --json`. This sends nothing and shows the
   exact arguments.
2. Then run the same command with `--yes`. Without it the CLI refuses
   (`WRITE_NOT_CONFIRMED`, exit 5).
3. Report what you wrote, with the returned ids.

Recipes (check `--help` for the full parameter list):

| Goal | Command |
|---|---|
| Out-of-scope finding (bug, gap, tech debt, question) | `brain record-issue --issue-kind bug --title "…" --body @finding.md --natural-key "<stable-key>" [--task-ref <ID>] --yes` |
| A non-obvious choice you made | `brain record-decision --repository <repo> --decisions '[{…}]' --yes` (row fields in `--help`) |
| A requirement inferred from code (stays `proposed` for human review) | `brain record-requirement --req-type functional --title "…" --text "…" --natural-key "<key>" --yes` |
| Link the working branch to a task | `studio update-implementation-branch --task-id <task ObjectId> --branch <name> --yes` |
| Close the loop after implementing a task | `studio process-journal --task-id <task ObjectId> --journal-content @journal.md --yes` |
| Advance status | `studio bulk-update-status --ids <ObjectId,...> --status in_progress\|done [--entity-type task\|user_story\|need] --yes` |

`natural-key` must be **your own stable key**, so re-running updates the same
record instead of duplicating it. Use a form like
`copilot:<repo>:<path>:<short-slug>`. Agent-written knowledge stays
`proposed` until a human confirms it; never describe it as confirmed.

If a write fails with `SCOPE_REQUIRED` or `ROLE_REQUIRED` (exit 4), the token
is read-only or the user lacks editor rights. Tell the user; do not retry.

## 5. Never

- Never read, print, request or inspect `FAIRMIND_TOKEN`, the keychain
  (`security`), environment dumps (`printenv`, `env`) or credential files.
  Never pass a token as an argument.
- Never run **destructive** or **sensitive** tools (for example
  `insights erase-subject`, `general get-mcp-configs-for-agent`). They exist
  for humans only and require `FAIRMIND_ALLOW_DESTRUCTIVE=1`; never set it
  yourself.
- Never invent requirements, decisions or ids. When FairMind has no context,
  say so and continue from repository evidence when safe.

## Errors

| Exit | Meaning | What to do |
|---|---|---|
| 0 | ok | — |
| 2 | `TOKEN_MISSING` / `TOKEN_EXPIRED` / `TOKEN_INVALID` | Ask the user to run `fairmind auth login`. Do not retry. |
| 3 | `*_NOT_FOUND` | The id, tool or repository is unknown. Say so. |
| 4 | forbidden: `SCOPE_REQUIRED`, `ROLE_REQUIRED`, `DESTRUCTIVE_NOT_ALLOWED` | Report it. Do not work around it. |
| 5 | validation: bad or missing flags, `PROJECT_REQUIRED`, `REPOSITORY_AMBIGUOUS`, `WRITE_NOT_CONFIRMED` | Fix the command (`--help`, add `--project`) |
| 7 / 8 | unavailable or timeout (`error.retryable: true`) | Retry once, then continue without FairMind and say so. |
