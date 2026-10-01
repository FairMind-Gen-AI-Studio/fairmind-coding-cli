// Help texts, kept identical to the Go edition (internal/commands/usage.go).

export const globalUsage = "\nGlobal flags:\n  --project <id|name>   FairMind project (default: .fairmind/config.json or profile)\n  --repo <id|name>      repository override (default: detected from git remote)\n  --json                force JSON output (default when stdout is not a terminal)\n  --profile <name>      configuration / credential profile\n  --timeout <seconds>   network timeout (default 30)\n  --verbose             diagnostics on stderr (secrets are always redacted)\n  --dry-run             show what a tool call would send, without calling FairMind\n";

export const rootUsage = "fairmind \u2014 FairMind Project Context for GitHub Copilot and other shell agents\n\nAgent commands:\n  context get --intent \"<text>\"         context relevant to a free-text task\n  context for-task <ID>                 context for a TASK-/US-/NEED- id or ObjectId\n  context for-code-change --diff        the WHY behind code you are about to change\n  search \"<query>\"                      federated search across FairMind sources\n\nWork items:\n  work list [--kind task|story|epic] [--status <s>] [--parent <ID>] [--all]\n\nFull FairMind toolset (every tool of the FairMind platform, from the live catalog):\n  brain | code | general | insights | studio <command> [flags]\n                                        e.g. fairmind studio list-tasks-by-project\n                                             fairmind brain get <node_id>\n  tools list [--namespace <ns>] [--access read|write]   discover tools\n  tools describe <Tool_name | \"ns command\">             parameters of one tool\n  tools call <Tool_name> [--arg k=v ...] [--args-json '{...}']\n  tools docs [--output <file.md>]                       markdown reference\n  Write tools need --yes (preview with --dry-run); destructive/sensitive\n  tools also need FAIRMIND_ALLOW_DESTRUCTIVE=1 set by a human.\n\nSetup and diagnostics:\n  auth login | auth logout | auth status\n  status                                configuration, repository and API reachability\n  version\n\nAuthentication: FAIRMIND_TOKEN environment variable, or the OS credential\nstore via " +
  "`fairmind auth login`" +
  ". Tokens are never accepted as arguments.\n\nExit codes: 0 ok, 1 error, 2 auth, 3 not found, 4 forbidden, 5 validation,\n6 conflict, 7 service unavailable, 8 timeout.\n\nRun " +
  "`fairmind <command> --help`" +
  " for details.\n" +
  globalUsage;

export const usageContextGet = "Usage: fairmind context get --intent \"<text>\" [--task <id>] [--path <path>...] [--budget <tokens>] [--json]\n\nReturns what FairMind knows that is relevant before performing the task.\nSelection, ranking and trimming to --budget happen server-side.\n" +
  globalUsage;

export const usageContextForTask = "Usage: fairmind context for-task <TASK_OR_STORY_OR_NEED_ID> [--budget <tokens>] [--json]\n\nReturns requirements, decisions, tests, issues, documents and code anchors for\na FairMind task, user story or need. Accepts ObjectIds and TASK-/US-/NEED- ids.\n" +
  globalUsage;

export const usageContextForCodeChange = "Usage: fairmind context for-code-change (--diff [--base <ref>] [--symbols] | --files <path>...) [--budget <tokens>] [--json]\n\nReturns decisions, requirements, issues and history anchored to the code you\nare changing. Only metadata is sent: repository identity, file paths, change\nstatus, line counts and hunk ranges. With --symbols, the names of enclosing\nfunctions/classes are also sent. File contents are never uploaded.\n\n  --diff         use local changes (staged, unstaged, untracked) against --base (default HEAD)\n  --files        explicit paths; combined with --diff, filters the diff to these files\n  --symbols      include enclosing symbol names extracted from diff hunks\n" +
  globalUsage;

export const usageSearch = "Usage: fairmind search \"<query>\" [--in brain,docs,code,studio] [--k 10] [--json]\n\nOne normalized search over Brain knowledge, project documents, code and Studio\nentities. Every result identifies its source.\n" +
  globalUsage;

export const usageVersion = "Usage: fairmind version [--json]\n";

export const usageStatus = "Usage: fairmind status [--offline] [--json]\n\nShows resolved configuration, detected repository, token source (never the\ntoken) and whether the FairMind API accepts the token.\n" +
  globalUsage;

export const usageAuthStatus = "Usage: fairmind auth status [--offline] [--json]\n\nShows where the token comes from and its UNVERIFIED claims (expiry, scope),\nthen asks the server to confirm it. The token itself is never printed.\n" +
  globalUsage;

export const usageAuthLogin = "Usage: fairmind auth login [--profile <name>]\n\nReads a FairMind token from stdin (hidden when typed in a terminal) and stores\nit in the OS credential store (macOS Keychain, Linux libsecret).\n\n  pbpaste | fairmind auth login        # macOS, avoids shell history\n  fairmind auth login < token.file     # then delete the file\n";

export const usageAuthLogout = "Usage: fairmind auth logout [--profile <name>]\n\nRemoves the stored token for the profile from the OS credential store.\n";

export const usageToolsList = "Usage: fairmind tools list [--namespace brain|code|general|insights|studio] [--access read|write|destructive|sensitive] [--refresh] [--json]\n\nLists every FairMind tool available to your token, with its CLI command and\naccess class. The catalog is cached for 24h (--refresh reloads it).\n" +
  globalUsage;

export const usageToolsDescribe = "Usage: fairmind tools describe <Tool_name | namespace command> [--json]\n\nShows a tool's description, access class and parameters (name, flag, type,\nrequired, allowed values).\n" +
  globalUsage;

export const usageToolsCall = "Usage: fairmind tools call <Tool_name> [--arg key=value ...] [--args-json '{...}' | --args-file <file|->] [--yes] [--dry-run] [--json]\n\nCalls any FairMind tool by its exact name with raw arguments. --arg values are\nconverted using the tool schema. Prefer the generated commands\n(fairmind <namespace> <command>) for typed flags.\n" +
  globalUsage;

export const usageToolsDocs = "Usage: fairmind tools docs [--output <file.md>]\n\nGenerates a markdown reference of every FairMind tool from the live catalog\n(used for the Copilot skill reference).\n" +
  globalUsage;

export const usageWorkList = "Usage: fairmind work list [--kind task|story|epic] [--status <status>] [--parent <ID>] [--limit 50] [--cursor <c>] [--all] [--json]\n\nLists the project's tasks (default), user stories or epics/needs with id,\nstatus, parent and a short excerpt. Uses the folder's project unless\n--project is given. --parent <EPIC-/US- id or ObjectId> keeps only the\nchildren of that epic or story (reads all pages).\n" +
  globalUsage;
