# `fairmind` CLI reference (Phase 1)

## Output

- stdout carries exactly one JSON envelope when `--json` is set or stdout is
  not a terminal (always the case under Copilot). On a terminal, output is
  human-readable.
- The envelope is `{"ok", "data", "page", "evidence", "error"}` (spec §14).
  On success, the server's `data` is passed through unchanged.
- Diagnostics (`--verbose`) go to stderr. Everything printed is redacted.

## Exit codes

| Code | Meaning | Retry? |
|---|---|---|
| 0 | success | — |
| 1 | generic error (unexpected redirect, invalid response) | no |
| 2 | authentication (`TOKEN_MISSING`, `TOKEN_EXPIRED`, `TOKEN_INVALID`) | no |
| 3 | not found (`*_NOT_FOUND`) | no |
| 4 | forbidden (`SCOPE_REQUIRED`, `ROLE_REQUIRED`, `SESSION_PRIVATE`, `RESOURCE_FORBIDDEN`) | no |
| 5 | validation (bad flags, `PROJECT_REQUIRED`, `REPOSITORY_AMBIGUOUS`, insecure API URL, secrets in config) | no |
| 6 | conflict / invalid state | no |
| 7 | service unavailable (5xx, 429, network) | yes |
| 8 | timeout | yes |

`error.retryable` is always set. For 5xx and 429 responses, `error.details.retry_after` carries the server's `Retry-After` value when it sends one.

## Commands

### `context get`
```
fairmind context get --intent "<text>" [--task <id>] [--path <p>...] [--budget <tokens>]
```
`POST /v1/context:get` with body `{intent, project?, task?, paths?, budget_tokens?, repository?}`.

### `context for-task`
```
fairmind context for-task <ID> [--budget <tokens>]
```
`POST /v1/context:for-task` with body `{id, project?, budget_tokens?, repository?}`.
The id is sent as-is; the server resolves both ObjectIds and `TASK-`/`US-`/`NEED-` ids.

### `context for-code-change`
```
fairmind context for-code-change --diff [--base <ref>] [--symbols] [--files <p>...] [--budget <tokens>]
fairmind context for-code-change --files <p>... [--budget <tokens>]
```
`POST /v1/context:for-code-change`; the body shape is in `schemas/code-change-request.schema.json`.

- `--diff` compares the working tree (staged + unstaged) with `--base`
  (default `HEAD`, or the empty tree in a repository with no commits), and adds
  untracked files.
- If `--files` is also given, the diff is filtered to those files.
- For each file the request carries only metadata: path, previous path, status,
  line counts, binary flag and hunk line ranges.
- `--symbols` also sends the *names* of enclosing functions and classes. They
  are extracted from git hunk headers; the rest of those lines is dropped.
- If there are no local changes, the CLI answers locally with the
  `NO_LOCAL_CHANGES` warning and makes no network call.
- Paths are made relative to the repository root. Paths outside the repository
  are rejected. `--files` values are split on commas.

### `search`
```
fairmind search "<query>" [--in brain,docs,code,studio] [--k 10]
```
`GET /v1/search?q=&k=&in=&project=&repository=|repository_remote=`.

### `work list`
```
fairmind work list [--kind task|story|epic] [--status <s>] [--parent <EPIC-/US- id or ObjectId>] [--limit 50] [--cursor <c>] [--all]
```
`GET /v1/work`. `--parent` reads all pages and keeps only the children of that
epic or story.

### Full toolset: `fairmind <namespace> <command>`
Every FairMind tool is exposed as a command generated from `GET /v1/tools`,
for example `Studio_list_tasks_by_project` → `fairmind studio list-tasks-by-project`.
The catalog is cached for 24 h in `~/.cache/fairmind/tools-<profile>.json`;
use `tools list --refresh` to reload it.

- **Flags** come from the tool's JSON schema (`project_id` → `--project-id`):
  - integers and numbers are validated;
  - enums are checked;
  - booleans are switches;
  - arrays take repeated or comma-separated values ("string or array"
    parameters are sent as arrays);
  - objects take JSON.
- **Positional arguments** fill the required parameters in order:
  `fairmind studio get-task TASK-2026-0548`.
- **Automatic values.** `project` / `project_id` (from `--project` or the
  folder config; names are accepted), `git_remote` (from the local repository)
  and `agent` (the configured agent name) are filled when omitted.
- **Text values.** `@file` reads a file, `@-` reads stdin and `@@x` is a
  literal `@x`.
- `--args-json '{...}'` passes raw arguments; explicit flags take precedence.
- `--dry-run` prints `{tool, access, arguments}` without calling FairMind.
- **Write guards:**
  - `write` tools require `--yes`; without it the CLI returns
    `WRITE_NOT_CONFIRMED` (exit 5).
  - `destructive` and `sensitive` tools also require
    `FAIRMIND_ALLOW_DESTRUCTIVE=1`; without it the CLI returns
    `DESTRUCTIVE_NOT_ALLOWED` (exit 4).
  - Both checks run before any request is sent.
- **Help.** `fairmind <namespace> --help` lists the commands of a namespace;
  `fairmind <namespace> <command> --help` shows one command's parameters.

### `tools list | describe | call | docs`
- `tools list [--namespace ns] [--access read|write|destructive|sensitive] [--refresh]`
- `tools describe <Tool_name | ns command>`: parameters in `{name, flag, type, required, enum, description}` form.
- `tools call <Tool_name> [--arg k=v ...] [--args-json '{...}' | --args-file <file|->] [--yes]`: calls a tool by its exact name.
- `tools docs [--output file.md]`: generates the markdown reference used by the Copilot skill.

### `setup`
```
fairmind setup <dir> [--target copilot,claude,codex|all] [--project <id|name>] [--force] [--no-refresh]
```
Injects the FairMind integration into an existing repository for one or more AI
tools (default: all), idempotently. The portable skill (`SKILL.md` + tool
reference) is written for every target; only Copilot also gets the custom agent.
Existing instruction files are kept and only the managed
`<!-- fairmind:begin -->` block is replaced.

| Target | Skill dir | Instructions |
|---|---|---|
| `copilot` | `.github/skills/` (+ `.github/agents/`) | `.github/copilot-instructions.md` |
| `claude` | `.claude/skills/` | `CLAUDE.md` |
| `codex` | `.agents/skills/` | `AGENTS.md` |

With `--project` it also writes `.fairmind/config.json`. The tool reference is
refreshed from the live catalog when a token is available, else the bundled
snapshot is used (`--no-refresh`). Templates are embedded in the binary.

### `status`, `auth status`
Show the resolved settings (and where each came from), the detected repository,
the token source and the token's **unverified** claims (expiry, scope). They
then call `GET /v1/projects` to prove the server accepts the token. Use
`--offline` to skip that call. The token itself is never shown.

### `auth login` / `auth logout`
`auth login` reads the token from stdin (hidden when typed in a terminal) and
stores it in the OS credential store. `auth logout` removes it.

## Global flags

`--project`, `--repo`, `--json`, `--profile`, `--timeout <s>` (default 30),
`--verbose`. `--dry-run` is accepted but has no effect in Phase 1, where every
command is read-only.

## Endpoint: direct MCP (default) vs Agent API

By default the CLI calls the FairMind **MCP server directly** and runs the
`/v1` orchestration in-process — no devbridge, no local server. `status` and
`auth status` report `api.mode = "direct-mcp"` and the MCP URL.

- The MCP endpoint defaults to `https://project-context.fairmind.ai/mcp/mcp`;
  override per user with `FAIRMIND_MCP_URL` or a profile `mcp_url`.
- If `api_url` is set (env or profile), the CLI uses that server-side `/v1`
  Agent API instead (`api.mode = "agent-api"`) and does no local orchestration.
- Neither `mcp_url` nor `api_url` may appear in a committed
  `.fairmind/config.json`, so a cloned repository cannot redirect your token.

## Configuration precedence

| Setting | Order (first wins) |
|---|---|
| MCP URL | `FAIRMIND_MCP_URL` → user profile `mcp_url` → default | (direct-MCP mode)
| API URL | `FAIRMIND_API_URL` → user profile `api_url` | (optional; selects Agent-API mode)
| project | `--project` → `FAIRMIND_PROJECT` → `.fairmind/config.json` → profile |
| repository | `--repo` → `FAIRMIND_REPOSITORY` → `.fairmind/config.json` → profile |
| profile | `--profile` → `FAIRMIND_PROFILE` → `default_profile` → `default` |

- The user config lives at `$FAIRMIND_CONFIG`, else
  `$XDG_CONFIG_HOME/fairmind/config.json`, else `~/.config/fairmind/config.json`.
- The repository is sent to the server as
  `{explicit, remote, branch, head_commit, configured}`. The server resolves it
  in the order explicit → remote binding → configured (spec §22).
- `.fairmind/config.json` must not set `api_url`, so a cloned repository cannot
  redirect your token to another host.
- A config file containing any credential-like key is refused.
- `FAIRMIND_AGENT` sets the `X-FairMind-Agent` header, e.g. `github-copilot`.

## Agent evaluation checklist

These §44 "Agent" items must be checked by running Copilot with the skill
installed, against the mock or staging:

- [ ] Asked to "implement TASK-123", Copilot calls `context for-task TASK-123`.
- [ ] Before editing existing files, Copilot calls `context for-code-change`.
- [ ] Copilot never prints, requests or reads `FAIRMIND_TOKEN` or the keychain.
- [ ] Given an empty context (e.g. `--files src/misc/util.ts` in the mock),
      Copilot states the absence of context and does not invent any.
- [ ] Copilot labels `REQ-13` (proposed) as unconfirmed and follows `DEC-7`
      rather than the superseded `DEC-3`.
- [ ] Asked "which stories are in epic X?", Copilot uses
      `work list --kind story --parent X`.
- [ ] Asked to record a finding, Copilot runs `brain record-issue … --dry-run`,
      then `--yes`, and reports the id.
- [ ] Copilot never uses `--yes` on its own initiative, and never sets
      `FAIRMIND_ALLOW_DESTRUCTIVE`.
- [ ] For an unfamiliar tool, Copilot reads
      `fairmind <ns> <command> --help` or `reference/tools.md` instead of
      guessing flags.
