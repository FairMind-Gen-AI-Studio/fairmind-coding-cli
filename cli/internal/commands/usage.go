package commands

const globalUsage = `
Global flags:
  --project <id|name>   FairMind project (default: .fairmind/config.json or profile)
  --repo <id|name>      repository override (default: detected from git remote)
  --json                force JSON output (default when stdout is not a terminal)
  --profile <name>      configuration / credential profile
  --timeout <seconds>   network timeout (default 30)
  --verbose             diagnostics on stderr (secrets are always redacted)
  --dry-run             show what a tool call would send, without calling FairMind
`

const rootUsage = `fairmind — FairMind Project Context for GitHub Copilot and other shell agents

Agent commands:
  context get --intent "<text>"         context relevant to a free-text task
  context for-task <ID>                 context for a TASK-/US-/NEED- id or ObjectId
  context for-code-change --diff        the WHY behind code you are about to change
  search "<query>"                      federated search across FairMind sources

Work items:
  work list [--kind task|story|epic] [--status <s>] [--parent <ID>] [--all]

Full FairMind toolset (every tool of the FairMind platform, from the live catalog):
  brain | code | general | insights | studio <command> [flags]
                                        e.g. fairmind studio list-tasks-by-project
                                             fairmind brain get <node_id>
  tools list [--namespace <ns>] [--access read|write]   discover tools
  tools describe <Tool_name | "ns command">             parameters of one tool
  tools call <Tool_name> [--arg k=v ...] [--args-json '{...}']
  tools docs [--output <file.md>]                       markdown reference
  Write tools need --yes (preview with --dry-run); destructive/sensitive
  tools also need FAIRMIND_ALLOW_DESTRUCTIVE=1 set by a human.

Repository setup:
  setup <dir> [--target copilot,claude,codex|all] [--project <id|name>]
                                        set up Copilot / Claude Code / Codex in a repo

Setup and diagnostics:
  auth login | auth logout | auth status
  status                                configuration, repository and API reachability
  version

Authentication: FAIRMIND_TOKEN environment variable, or the OS credential
store via ` + "`fairmind auth login`" + `. Tokens are never accepted as arguments.

Exit codes: 0 ok, 1 error, 2 auth, 3 not found, 4 forbidden, 5 validation,
6 conflict, 7 service unavailable, 8 timeout.

Run ` + "`fairmind <command> --help`" + ` for details.
` + globalUsage

const usageContextGet = `Usage: fairmind context get --intent "<text>" [--task <id>] [--path <path>...] [--budget <tokens>] [--json]

Returns what FairMind knows that is relevant before performing the task.
Selection, ranking and trimming to --budget happen server-side.
` + globalUsage

const usageContextForTask = `Usage: fairmind context for-task <TASK_OR_STORY_OR_NEED_ID> [--budget <tokens>] [--json]

Returns requirements, decisions, tests, issues, documents and code anchors for
a FairMind task, user story or need. Accepts ObjectIds and TASK-/US-/NEED- ids.
` + globalUsage

const usageContextForCodeChange = `Usage: fairmind context for-code-change (--diff [--base <ref>] [--symbols] | --files <path>...) [--budget <tokens>] [--json]

Returns decisions, requirements, issues and history anchored to the code you
are changing. Only metadata is sent: repository identity, file paths, change
status, line counts and hunk ranges. With --symbols, the names of enclosing
functions/classes are also sent. File contents are never uploaded.

  --diff         use local changes (staged, unstaged, untracked) against --base (default HEAD)
  --files        explicit paths; combined with --diff, filters the diff to these files
  --symbols      include enclosing symbol names extracted from diff hunks
` + globalUsage

const usageSearch = `Usage: fairmind search "<query>" [--in brain,docs,code,studio] [--k 10] [--json]

One normalized search over Brain knowledge, project documents, code and Studio
entities. Every result identifies its source.
` + globalUsage

const usageVersion = "Usage: fairmind version [--json]\n"

const usageStatus = `Usage: fairmind status [--offline] [--json]

Shows resolved configuration, detected repository, token source (never the
token) and whether the FairMind API accepts the token.
` + globalUsage

const usageAuthStatus = `Usage: fairmind auth status [--offline] [--json]

Shows where the token comes from and its UNVERIFIED claims (expiry, scope),
then asks the server to confirm it. The token itself is never printed.
` + globalUsage

const usageAuthLogin = `Usage: fairmind auth login [--profile <name>]

Reads a FairMind token from stdin (hidden when typed in a terminal) and stores
it in the OS credential store (macOS Keychain, Linux libsecret).

  pbpaste | fairmind auth login        # macOS, avoids shell history
  fairmind auth login < token.file     # then delete the file
`

const usageAuthLogout = `Usage: fairmind auth logout [--profile <name>]

Removes the stored token for the profile from the OS credential store.
`

const usageToolsList = `Usage: fairmind tools list [--namespace brain|code|general|insights|studio] [--access read|write|destructive|sensitive] [--refresh] [--json]

Lists every FairMind tool available to your token, with its CLI command and
access class. The catalog is cached for 24h (--refresh reloads it).
` + globalUsage

const usageToolsDescribe = `Usage: fairmind tools describe <Tool_name | namespace command> [--json]

Shows a tool's description, access class and parameters (name, flag, type,
required, allowed values).
` + globalUsage

const usageToolsCall = `Usage: fairmind tools call <Tool_name> [--arg key=value ...] [--args-json '{...}' | --args-file <file|->] [--yes] [--dry-run] [--json]

Calls any FairMind tool by its exact name with raw arguments. --arg values are
converted using the tool schema. Prefer the generated commands
(fairmind <namespace> <command>) for typed flags.
` + globalUsage

const usageToolsDocs = `Usage: fairmind tools docs [--output <file.md>]

Generates a markdown reference of every FairMind tool from the live catalog
(used for the Copilot skill reference).
` + globalUsage

const usageWorkList = `Usage: fairmind work list [--kind task|story|epic] [--status <status>] [--parent <ID>] [--limit 50] [--cursor <c>] [--all] [--json]

Lists the project's tasks (default), user stories or epics/needs with id,
status, parent and a short excerpt. Uses the folder's project unless
--project is given. --parent <EPIC-/US- id or ObjectId> keeps only the
children of that epic or story (reads all pages).
` + globalUsage

const usageSetup = `Usage: fairmind setup <dir> [--target copilot,claude,codex|all] [--project <id|name>] [--force] [--no-refresh] [--json]

Injects the FairMind integration into an existing repository for one or more
AI tools (default: all). Idempotent; existing instruction files are kept and
only the managed <!-- fairmind:begin --> block is replaced.

  copilot  -> .github/skills/, .github/agents/, .github/copilot-instructions.md
  claude   -> .claude/skills/, CLAUDE.md
  codex    -> .agents/skills/, AGENTS.md

The portable skill (SKILL.md + tool reference) is written for every target.
With --project it also writes .fairmind/config.json (no secrets). The tool
reference is refreshed from the live catalog when a token is available, else
the bundled snapshot is used (--no-refresh forces the snapshot).
` + globalUsage
