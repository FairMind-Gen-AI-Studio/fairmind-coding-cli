---
description: Connect this checkout to its Fairmind project — verify the per-project MCP entry (or the `fairmind` CLI) and the key, pre-flight the tenant, and bind the repository to the one the platform ingested using its catalog identity where the installed client supports it
allowed-tools: Bash(python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_connect.py:*), Bash(python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py:*), Read, AskUserQuestion, mcp__Fairmind__General_list_projects
---

# /fairmind-connect

Run this once per checkout, per machine, before the first `/fairmind-loop` or
`/fairmind-develop`. It replaces the paragraph that used to tell a human to go
to Studio, copy a key and hope — and records the repository identity minted by the platform catalog. The judge
lane adopts that identity when the installed client supports it; older clients
continue to use the directory name.

**It never writes `~/.claude.json`.** That file holds a bearer token; a command
that edits it on someone's behalf is a command that can point a credential
somewhere. When something is missing it prints the exact line to run.

**Two transports, one binding.** With a per-project Fairmind MCP entry the
engine checks that entry and its key, as it always did. Without one, and with
the `fairmind` CLI installed (or runnable from the copy bundled with this
plugin), it does the same checks through the CLI: `fairmind auth status` for
the key, `Insights_get_tenant_health` for the tenant, `Insights_bind_repository`
for the bind. The CLI keeps its key in the OS credential store and never hands
it out. The report's first heading says which transport ran. Force one with
`--via mcp` or `--via cli`. An MCP entry wins in `auto` because the ambient
capture lanes send with that entry's key, so the binding they key on has to be
made with the same key. Those lanes stay off on a CLI-only checkout.

**The binding is per machine, not per repository.** It lands in
`.fairmind/active-context.json`, which is gitignored, so each teammate runs this
in their own clone.

## Step 1 — run it

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_connect.py
```

The script prints the whole report. **Show that output; do not re-type it, do
not summarise it into a sentence of your own.** It is the record of what was
checked and what will now leave the machine, and a paraphrase of it is a record
that can drift from what actually happened.

## Step 2 — branch on the exit code

| Exit | Meaning | What you do |
| --- | --- | --- |
| `0` | Bound. | Relay the report. Nothing else. |
| `1` | One or more traps, each printed with the fix. | Relay the report. Do not attempt the fixes yourself — every one of them is either a Studio action, an edit to the user's own MCP config, or a `fairmind auth login` the user runs in their own terminal (never ask for the key in the chat). |
| `3` | The key is not scoped to a project and none was named. | Go to step 3. |
| `4` | The platform did not answer (503, network, VPN). | Say so and offer to re-run. Nothing was changed. |

## Step 3 — only on exit `3`: ask which project

The key carries no `projectId` claim, so the project has to be named once.

1. Call `mcp__Fairmind__General_list_projects` — or, when the report's first
   heading was `Fairmind CLI`, `python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_cli.py tools call General_list_projects --json`
   and read `data`. Each row carries `id` and `name` — **`id`, not `_id` and not `project_id`**.
2. Ask the user with `AskUserQuestion`, one question, the project names as the
   options. Do not guess from the directory name: that a folder is called
   `payments-api` is exactly the signal this command exists to stop trusting.
3. Resolve the selected name to the `id` from that same list, then re-run with
   that ID. If names are duplicated, ask which project ID they intend. Never pass
   the display name as the `--project` value:

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_connect.py --project <id>
```

If the tool answers something that is not a list (a `message` dict is its
failure shape), report that and stop — do not fall back to a name you inferred.

## Step 4 — only on a `409`: the branch is ambiguous

A catalog row is one `(url, branch)` pair, so a repository ingested on several
branches has several rows. When none of them is on the current branch the door
refuses to guess — binding the wrong row looks exactly like not being bound at
all — and names the candidates. The report already prints the exact re-run line
per candidate; relay it and let the user pick.

```bash
python3 "${CLAUDE_PLUGIN_ROOT}"/scripts/fairmind_connect.py --branch <name>
```

## What this does not do

- It does not resolve the repository itself. The origin url goes to the door
  that owns the catalog and the server answers; no matching logic lives here.
- It does not change what the ambient session digest sends. That payload was,
  and stays, an opaque one-way hash of this checkout's location. What the
  binding adds is the platform's ability to join that hash to the repository —
  a consequence of an act the developer performed deliberately, which is why it
  is this command and not a default.
- It does not turn capture on or off. That is `/fairmind-config`.
