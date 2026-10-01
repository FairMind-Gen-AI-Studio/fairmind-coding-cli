# CLAUDE.md — fairmind CLI

Project memory for working **on** this repository. (The `CLAUDE.md` that
`fairmind setup` writes into *other* repos is a different file — the FairMind
usage guidance for those projects.)

## What this is

`fairmind` is a thin CLI that lets shell-capable AI coding agents — GitHub
Copilot, Claude Code, OpenAI Codex — use FairMind Project Context. It calls the
**FairMind MCP server directly** and runs the light `/v1` orchestration
in-process. No MCP server is configured inside the agents (a "Registry Only"
policy stays intact); the proprietary FairMind logic (ranking, retrieval) stays
in the MCP tools on the server.

```
AI agent --(shell: fairmind … --json)--> fairmind CLI --(MCP, Bearer JWT)--> FairMind MCP server --> core
```

## Two editions — keep them in parity

- **Node.js (`node/`) — primary/recommended.** Runs under the already-signed
  `node` (not blocked by Windows Smart App Control); token in DPAPI on Windows.
- **Go (root module) — optional.** Single static binary for macOS/Linux.

They implement the **same behavior**: identical commands, JSON envelope, exit
codes, write/destructive guards, redaction, git diff metadata, direct-MCP + the
optional `api_url` Agent-API mode. **Any change to one edition must be mirrored
in the other.** A parity harness diffs both against a shared mock and the live
MCP; output is byte-identical except volatile fields.

## Build, test, lint

```bash
# Go
make build            # -> bin/fairmind   (make vet: gofmt + go vet;  make dist: signed-ready tarballs)
go test -race ./...   # 74 tests

# Node
cd node && node scripts/copy-templates.js && npm install -g .
cd node && npm test   # 27 tests (node:test, zero deps)
```

Both must stay green and `gofmt` clean before any commit.

## Architecture map

| Path | Role |
|---|---|
| `cmd/fairmind/` | Go CLI entry point |
| `internal/commands/` | command tree, arg parsing, dynamic tool commands, `setup`, `direct.go` (in-process MCP client) |
| `internal/devbridge/` | MCP Streamable-HTTP client + `/v1` orchestration (`Server.Dispatch`), shared by direct mode and the optional standalone server |
| `internal/api/` | HTTPS client for the optional Agent-API mode (TLS, no redirects, timeouts, size cap) |
| `internal/auth/` | token from env / OS keychain (Keychain/libsecret/DPAPI); unverified claim peek |
| `internal/config/` | profiles, `.fairmind/config.json`, MCP/API URL validation |
| `internal/git/` | remote normalization, diff **metadata only** (no file contents) |
| `internal/output/` | JSON envelope, error/exit codes, **secret redaction**, human rendering |
| `internal/assets/` | embedded Copilot/Claude/Codex templates (`go:embed`) for `fairmind setup` |
| `internal/mock/` | mock server + fixtures (tests) |
| `node/src/` | Node edition mirroring all of the above |
| `github/` | **canonical** skill / agent / instruction templates |
| `docs/` | CLI, AUTH, SECURITY, SECURITY-REVIEW, AGENT_API; `docs/legacy/` |

## Conventions and invariants (do not break)

- **No third-party dependencies.** Go stdlib only; Node zero runtime deps.
- **Secrets never leak.** Token is never an argv flag, never in a URL, never
  logged; everything printed passes through the redactor. Don't add code paths
  that print raw responses without it.
- **TLS always verified; redirects never followed.** No "insecure" switch.
- **Config trust:** a committed `.fairmind/config.json` must not set `api_url`
  or `mcp_url` and must not hold credentials.
- **Data minimization:** `for-code-change` sends only metadata (paths, counts,
  hunk ranges, optional symbol names) — never file contents.
- **Write guards:** write tools require `--yes`; destructive/sensitive tools
  also require `FAIRMIND_ALLOW_DESTRUCTIVE=1`. Guards run before any request.
- **git args:** never pass an untrusted value that could be an option (a base
  ref starting with `-` is rejected — see the git argument-injection fix).

## Templates: one source, two embeds

`github/` is canonical. After editing it, resync the copies:

```bash
make sync-templates             # -> internal/assets/templates (Go embed)
node node/scripts/copy-templates.js   # -> node/templates (gitignored)
```

## Modes

- **direct-mcp (default):** no config beyond a token; MCP URL defaults to the
  FairMind production endpoint (override with `FAIRMIND_MCP_URL` / `mcp_url`).
- **agent-api (optional):** set `api_url` to use a server-side `/v1` API if one
  ever exists; the standalone `fairmind-devbridge` implements that `/v1` over
  MCP for debugging/hosting.

## Repo facts

- Module path: `github.com/FairMind-Gen-AI-Studio/fairmind-cli`.
- License: MIT (`LICENSE`, and `"license": "MIT"` in `node/package.json`), the same as the plugin repository.
- Internal spec: `FairMind_Copilot_Bridge_Spec_v1.md`.
