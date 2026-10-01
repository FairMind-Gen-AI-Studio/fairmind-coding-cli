---
name: fairmind-cli
description: Use before any Fairmind call made by a command or skill of this plugin - decides whether Fairmind is reached through the `fairmind` CLI (preferred when it is installed and authenticated) or through a Fairmind MCP mount, and gives the one rule that turns every `mcp__Fairmind__<Tool>` step into the equivalent CLI invocation, how to read its JSON envelope and exit codes, and how writes are confirmed
---

# Fairmind through the CLI

The commands and skills of this plugin describe every Fairmind call as an MCP tool
(`mcp__Fairmind__Studio_get_task`, `mcp__Fairmind__Brain_context`, …). The `fairmind`
CLI (bundled under `cli/` in this plugin) calls the **same tools on the same server**
from the shell, with its token kept in the OS credential store. When it is available,
use it: no MCP entry has to be configured in Claude Code.

Nothing else changes. The orchestrator still makes every Fairmind call (no sub-agent
does), results still go down to the team as files under `${FAIRMIND_BASE}`, and every
write still happens only at the moment the calling command or skill authorizes it.

## Step 1 — pick the transport, once per command run

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py --probe --online
```

| Probe exit | Transport | What it means |
|---|---|---|
| `0` | **CLI** | A CLI was found (installed `fairmind`, or the bundled copy run with Node ≥ 20), it holds a key, and the server accepted it. |
| `2` | not usable yet | CLI present, but no key, an expired key, or a refused key. Tell the user to run `fairmind auth login` **themselves** in a terminal. Never ask for the key, never put it in a command. |
| `7` | not reachable | Network, VPN or service down. Say so; offer to retry. |
| `1` | — | No CLI. Fall through to the MCP check. |

If the CLI transport is not usable, fall back to the Fairmind MCP tools **when they are
available to you** — test for the family (`mcp__fairmind…__*`, case-insensitive: the
mount may be `Fairmind` or `Fairmind-dev`), not the literal spelling. Neither available
means **standalone**: the calling command decides whether that is an error or a mode.

## Step 2 — the translation rule

Every step written as

> call `mcp__Fairmind__<Tool>` with `{…arguments…}`

is, on the CLI transport:

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py tools call <Tool> --args-json '<arguments as JSON>' --json
```

- `<Tool>` is the **exact MCP tool name** (`Studio_get_task`, `Brain_record_decision`).
  `tools call` takes it verbatim, so no translation to `fairmind <namespace> <command>`
  is needed (both forms reach the same tool).
- The arguments are the **same JSON object** the MCP call would take, same parameter
  names. Unsure of a name? `python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py tools describe <Tool> --json`
  (`data.parameters[].name`).
- A step that checks tools "are in the tool list" reads
  `python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py tools list --json` instead: the names
  are in `data.tools[].name`, each with its `access` (`read`, `write`, `destructive`,
  `sensitive`). Add `--refresh` if a tool the server just shipped is missing (the
  catalog is cached for 24 h).
- The CLI fills `project` / `project_id` (from `--project`, `FAIRMIND_PROJECT` or the
  repository's `.fairmind/config.json`), `git_remote` and `agent` when you omit them.
  Pass them explicitly whenever the calling step names a value — an explicit value
  always wins.
- **Quoting.** If the JSON contains a single quote, or is longer than a few lines, write
  it to a file under the run's `.fairmind/` workspace and pass `--args-file <path>`
  instead of `--args-json`.

### Writes

A tool whose access is `write` is refused unless the call carries `--yes`
(`WRITE_NOT_CONFIRMED`, exit 5). Add `--yes` **only** at a step where the calling
command or skill already performs that MCP write — after the human approval it
requires, never earlier, never on your own initiative. `--dry-run` instead of `--yes`
prints `{tool, access, arguments}` without calling Fairmind: use it to show the user
what will be sent when the step asks you to.

`destructive` and `sensitive` tools also need `FAIRMIND_ALLOW_DESTRUCTIVE=1`. That is set
by a human, in their own shell. **Never set it yourself.**

### Insights / brain payloads from `insights_flush_payload.py`

Do not paste a flush payload into `--args-json`. Send each category, and each batch of a
list category, straight from the `--out` file:

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py --send Insights_record_loop_stats --from <out> --category loop
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py --send Insights_record_agent_decisions --from <out> --category decisions --batch 0
```

The payload goes over stdin, so its size never meets the Bash output cap or argv. `--send`
always confirms the write (`--yes`): use it only where the command already sends that
category.

## Step 3 — read the answer

stdout is **one JSON envelope**: `{"ok", "data", "page", "evidence", "error"}`. `data` is
exactly what the MCP tool returns, so every field name the calling step refers to is in
`data`.

**A call succeeded only when all three hold:** the exit code is `0`, `ok` is `true`, and
`data` is not a bare `{"message": "…"}` (the shape in which the Fairmind doors report a
refusal). `--send` checks all three itself and exits `9` (`TOOL_REFUSED`) on the last one.
Anything a command says to do "only after the MCP call returned success" — a
`--commit <category>`, a status line — follows that rule.

| Exit | Meaning | Retry? |
|---|---|---|
| 0 | success (still check `ok` and the refusal shape) | — |
| 2 | key missing, expired or invalid | no — user runs `fairmind auth login` |
| 3 | not found | no |
| 4 | forbidden (scope, role, private session, `DESTRUCTIVE_NOT_ALLOWED`) | no |
| 5 | validation (bad arguments, `WRITE_NOT_CONFIRMED`, `PROJECT_REQUIRED`) | no — fix the call |
| 6 | conflict / invalid state | no |
| 7 | service unavailable (5xx, 429, network) | yes |
| 8 | timeout | yes |
| 9 | `--send` only: the tool answered with a refusal message | no |
| 127 | no CLI could be found | — fall back to MCP or standalone |

**Results that go to a file.** Whenever a step says to write a result verbatim (the
`inbox/` of `/fairmind-develop` and `/fairmind-loop`), or the answer can be large
(document content, a session's requirements, brain reads with bodies; a Bash result is
capped at about 30,000 bytes), use `--save`. On success it writes the envelope's `data`,
which is exactly what the MCP tool would have returned, and prints only
`{"ok": true, "saved": …, "bytes": …}`. On failure it prints the envelope and writes
nothing:

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py --save "${FAIRMIND_BASE}"/inbox/story.json tools call Studio_get_user_story --args-json '{"user_story_id":"US-142"}'
```

`Read` the saved file when you need its content yourself.

## What the CLI does not change

- **Ambient capture and the judge hook** send through REST doors with the key of the
  per-project MCP entry; the CLI never hands its key out. Without an MCP entry those
  lanes stay off. `/fairmind-config` shows their state.
- **Sub-agents** still never call Fairmind. They receive files.
- **Standalone** stays a mode, not an error, wherever the calling command says so.
