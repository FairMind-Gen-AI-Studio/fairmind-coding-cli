# FairMind Copilot Bridge — `fairmind` CLI

Gives GitHub Copilot CLI, Copilot agents and IDE agents (VS Code / IntelliJ)
access to FairMind Project Context by calling the FairMind **MCP server
directly** from the shell — no MCP configured in Copilot, so a "Registry Only"
MCP policy is left untouched.

```text
Copilot / IDE agent --shell--> fairmind CLI --MCP (Bearer token)--> FairMind MCP server --> FairMind core
```

The CLI runs only light orchestration (call tool A, then B, assemble the
context pack). The proprietary FairMind logic — ranking, retrieval,
knowledge-graph — stays inside the MCP tools on the server.

> **Deployment:** FairMind exposes only the MCP server (no `/v1` Agent API), so
> the CLI's **direct-MCP mode is the default** and needs no local process. If a
> server-side Agent API is ever published, set `api_url` and the CLI uses that
> instead. `docs/AGENT_API.md` describes that optional `/v1` contract; the
> standalone `fairmind-devbridge` implements it over MCP for debugging only.
>
> Two editions: **Go** (single binary) and **Node.js** (`node/`, runs under the
> already-signed `node`, so Windows Smart App Control does not block it).

## Agent-facing commands

```bash
fairmind context get --intent "implement password reset" --json
fairmind context for-task TASK-123 --json          # also US-*, NEED-*, ObjectIds
fairmind context for-code-change --diff --json     # or --files a.ts b.ts
fairmind search "token expiry" --in brain,docs --json
```

Work items: `fairmind work list --kind task|story|epic [--parent <ID>]`.

**Full FairMind toolset.** All 75 FairMind tools are available as commands
generated from the live catalog:

```bash
fairmind tools list                                    # every tool, with its access class
fairmind studio --help                                 # commands of one namespace
fairmind studio list-user-stories-by-project --json    # any tool; flags from its schema
fairmind brain record-issue ... --dry-run              # writes: preview, then --yes
```

Destructive and sensitive tools also need `FAIRMIND_ALLOW_DESTRUCTIVE=1`, set
by a human. The Copilot skill ships a generated reference of every tool
(`github/skills/fairmind-project-context/reference/tools.md`).

Diagnostics: `fairmind status`, `fairmind auth status|login|logout`,
`fairmind version`. Full reference: [`docs/CLI.md`](docs/CLI.md).

## Configure

Direct-MCP mode is the default and needs **no configuration beyond a token**:

```bash
# token: OS credential store (Keychain / libsecret / Windows DPAPI), or FAIRMIND_TOKEN for CI
pbpaste | fairmind auth login
fairmind auth status          # api.mode = "direct-mcp"
```

The MCP endpoint defaults to `https://project-context.fairmind.ai/mcp/mcp`;
override it per user (never in a committed file) with `FAIRMIND_MCP_URL` or a
profile: `~/.config/fairmind/config.json`
→ `{"profiles":{"default":{"mcp_url":"https://<your-mcp-host>/mcp/mcp"}}}`.
Set `api_url` instead only if a server-side `/v1` Agent API ever exists.

Optionally commit non-sensitive defaults per repository in
`.fairmind/config.json`: `{"project": "<id|name>", "repository": "<id|name>"}`
(credentials, `api_url` and `mcp_url` are refused there).

## Enable it in Copilot, Claude Code or Codex

One command scaffolds a repository for one or more AI tools (default: all).
Templates are embedded in the binary; it is idempotent and keeps any existing
content of the instruction files.

```bash
fairmind setup /path/to/repo --project "<FairMind project>"          # all tools
fairmind setup /path/to/repo --target claude,codex                   # a subset
```

| Target | Skill dir | Instructions |
|---|---|---|
| `copilot` | `.github/skills/` (+ `.github/agents/fairmind.agent.md`) | `.github/copilot-instructions.md` |
| `claude` | `.claude/skills/` | `CLAUDE.md` |
| `codex` | `.agents/skills/` | `AGENTS.md` |

The Node edition provides the same as the `fairmind-setup` command. `--project`
also writes a non-sensitive `.fairmind/config.json`.

`setup` is for agents that run **without** the `fairmind-coding` plugin (the
Copilot coding agent on GitHub, Copilot Chat in an IDE, Codex). With the plugin
installed in Claude Code or Copilot CLI, skip it: `fairmind auth login` plus the
plugin's `/fairmind-connect` writes `.fairmind/config.json`, and the plugin's own
skills and agents already tell the model to use this CLI.

## Build

Two editions share one behavior (verified by a parity harness). **Node is the
primary/recommended edition**; the Go edition is kept for single-binary
distribution on macOS/Linux.

**Node.js** (`node/`, primary) — runs under the already-signed `node`, so
Windows Smart App Control / App Control does not block it, and stores the token
with DPAPI on Windows:

```bash
cd node && node scripts/copy-templates.js && npm install -g .
```

**Go** (optional) — single static binary:

```bash
make build              # -> bin/fairmind
# or install to ~/.local/bin:
go build -trimpath -ldflags "-s -w -X github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/version.Commit=$(git rev-parse --short HEAD)" \
  -o ~/.local/bin/fairmind ./cmd/fairmind
```

## Distribute

- **Node (primary):** `cd node && npm pack` (a tarball) or `npm install -g .` on
  the target. No build step, no native binary, so no code-signing needed and no
  Smart App Control block on Windows.
- **Go (optional):** `make dist` builds `dist/` tarballs for macOS
  (arm64/amd64), Linux (amd64/arm64) and Windows (amd64) with `SHA256SUMS`.
  Sign the binaries before distributing them.

> **Before shipping to a customer**, read [`docs/SECURITY-REVIEW.md`](docs/SECURITY-REVIEW.md).
> Key points: prefer the **Node edition on Windows** (token in DPAPI, and no
> unsigned executable to be blocked), **sign** any Go binaries you distribute,
> pin the MCP host in a managed profile, and issue read-only, project-scoped,
> short-lived tokens.

## Tests

```bash
make test          # Go
cd node && npm test  # Node
```

Go: 74 tests; Node: 27. Unit tests plus end-to-end runs of the real command
tree against a mock and a fake MCP, covering direct-MCP and Agent-API modes,
auth failures, ID normalization, repo binding, trust labels, timeouts, 5xx,
network loss, redirects, the generated tool commands and their write guards,
and git argument-injection. They assert no token leaks to stdout, stderr,
verbose logs, URLs or request bodies, and that no source code leaves the
machine. A Go/Node parity harness diffs both editions against the same mock and
the live MCP.

## Layout

```text
cmd/fairmind/            CLI entry point (Go)
cmd/fairmind-devbridge/  optional standalone /v1 server over MCP (debug/hosted)
cmd/fairmind-mock/       mock Agent API (test only)
internal/api/            HTTPS client (TLS, timeouts, size cap, no redirects)
internal/auth/           token resolution (env, OS keychain), unverified claim peek
internal/config/         profiles, .fairmind/config.json, MCP/API URL validation
internal/git/            remote normalization, diff metadata (no contents)
internal/output/         envelope, error/exit codes, redaction, human rendering
internal/commands/       command tree, direct-MCP client, `setup`
internal/devbridge/      MCP client + /v1 orchestration (shared by direct mode and the server)
internal/assets/         embedded Copilot/Claude/Codex templates
internal/mock/           mock server + fixtures
node/                    Node.js edition (mirrors the Go behavior, zero deps)
github/                  canonical skill / agent / instructions templates
docs/                    CLI, AUTH, SECURITY, SECURITY-REVIEW, AGENT_API
schemas/                 JSON Schemas of the v1 contract
tests/                   Go end-to-end tests
```
