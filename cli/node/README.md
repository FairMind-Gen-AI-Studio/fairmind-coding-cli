# FairMind Copilot Bridge — Node.js edition

The same `fairmind` CLI as the Go edition, rewritten in Node.js so it runs
under `node` (which is already installed and code-signed wherever GitHub Copilot
CLI runs). This avoids shipping unsigned native binaries, which Windows **Smart
App Control** and **App Control for Business / AppLocker** block.

- **Zero runtime dependencies** — Node standard library only.
- **Same contract as the Go edition**: identical commands, JSON envelope, exit
  codes, write guards, redaction and repository detection. Verified by a
  parity harness that diffs Node vs Go output against the same mock and the same
  live FairMind MCP server (byte-identical except volatile fields and one
  documented cosmetic redaction of credential-shaped prose).

## Why Node for the customer

`node.exe` is signed by the OpenJS Foundation and already trusted on any machine
that installed Copilot CLI (`npm install -g @github/copilot`). A `.js` CLI run by
that `node` is not a new unsigned executable, so Smart App Control does not block
it — unlike the Go `fairmind.exe`. Signing the Go binaries remains the other
valid path; this edition removes the signing prerequisite for the pilot.

## Install

```bash
# from this folder (no build step)
npm install -g .            # or: npm link
fairmind version
```

Or run without installing: `node bin/fairmind.js <command>`.

## Configure

By default the CLI calls the FairMind **MCP server directly** — no `api_url`, no
devbridge, no local process. The only thing it needs is a token:

```bash
# token: OS credential store (Keychain / libsecret / Windows DPAPI), or FAIRMIND_TOKEN for CI
pbpaste | fairmind auth login          # macOS/Linux
fairmind auth status                   # shows: api.mode = "direct-mcp"
```

The MCP endpoint defaults to `https://project-context.fairmind.ai/mcp/mcp`.
Override it per user if needed (never in a committed file):

```bash
export FAIRMIND_MCP_URL="https://<your-mcp-host>/mcp/mcp"
# or ~/.config/fairmind/config.json:
# {"profiles":{"default":{"mcp_url":"https://<your-mcp-host>/mcp/mcp"}}}
```

Set `api_url` instead of `mcp_url` only if a server-side `/v1` Agent API ever
exists; then the CLI uses that and does no local orchestration.

On **Windows** the token is stored with DPAPI (`ConvertFrom-SecureString`,
per-user) under `%LOCALAPPDATA%\fairmind`, so it persists across terminals
without a plaintext file. Everywhere else it uses the macOS Keychain or Linux
libsecret.

## Commands

Identical to the Go edition — see [`../docs/CLI.md`](../docs/CLI.md):

```bash
fairmind context get --intent "…" --json
fairmind context for-task US-2026-0600 --json
fairmind context for-code-change --diff --json
fairmind search "…" --json
fairmind work list --kind story --parent NEED-2026-0276 --json
fairmind <brain|code|general|insights|studio> <command> --help   # all 75 tools
fairmind tools list | tools describe | tools call | tools docs
```

Write tools need `--yes` (preview with `--dry-run`); destructive/sensitive
tools also need `FAIRMIND_ALLOW_DESTRUCTIVE=1`.

## Prepare a repository for Copilot

```bash
fairmind-setup <repo> --project "<FairMind project>"
cd <repo> && fairmind-copilot
```

`fairmind-setup` installs `.github/skills/…`, the custom agent, the
`copilot-instructions.md` block and `.fairmind/config.json`, and refreshes the
tool reference from the live catalog. `fairmind-copilot` starts Copilot CLI with
`--allow-tool 'shell(fairmind:*)'` and credential tools denied.

## How it talks to FairMind

```
fairmind CLI ──(MCP Streamable HTTP, Bearer token)──▶ FairMind MCP server ──▶ FairMind core
```

The CLI runs the light orchestration (call tool A, then B, assemble the context
pack) in-process and calls the MCP tools directly. The proprietary logic
(ranking, retrieval) stays inside the MCP tools on the server. No `/v1` API and
no devbridge are involved in this mode.

`fairmind-devbridge` is kept only as an optional standalone `/v1` server (for a
future hosted Agent API or for debugging). It is not needed for normal use.

## Tests

```bash
npm test          # 27 tests: unit + e2e, covering both direct-MCP and /v1 modes against a fake MCP
```

## Layout

```text
bin/            fairmind, fairmind-devbridge, fairmind-copilot, fairmind-setup
src/cli/        command tree, args, output, auth, config, git, api, catalog, tools
src/cli/directmcp.js  in-process /v1 orchestration over MCP (default mode, no bridge)
src/devbridge/  MCP Streamable-HTTP client + /v1 logic (shared by direct mode and the optional server)
src/http.js     TLS-verified transport (no redirects, timeouts, size cap, proxy)
templates/      Copilot assets, generated from ../github by scripts/copy-templates.js
test/           unit + e2e + fake MCP
```
