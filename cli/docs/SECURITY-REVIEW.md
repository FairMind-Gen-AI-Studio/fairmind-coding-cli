# Security review — FairMind Copilot Bridge CLI

Scope: the artifact distributed to the customer — the `fairmind` CLI (Go and
Node.js editions), the direct-MCP client, the optional devbridge, and the
setup/pilot scripts. Reviewed for distribution to a critical customer building
sensitive software. Date: 2026-09-28.

Threat model: the CLI runs on a developer/CI machine, holds a FairMind JWT, and
talks to the FairMind MCP server on the developer's behalf. Adversaries
considered: a malicious/compromised repository the developer opens, a
network attacker (MITM), a co-tenant process, and prompt-injected AI agents
that drive the CLI.

## Summary

Overall posture is strong: no third-party dependencies, TLS always verified,
secrets never logged or placed in argv/URLs, config files cannot redirect the
token or hold secrets, and code-change context sends metadata only. One real
vulnerability (git argument injection) was found and fixed during the review.
Remaining items are hardening and distribution recommendations.

## Findings

### F1 — Git argument injection via `--base` — FIXED
`context for-code-change --diff --base <ref>` passed `<ref>` to `git diff` /
`git rev-parse`. Git parses options anywhere before `--`, so a value such as
`--output=/path` (a real `git diff` option) or `-ext-diff`/`--upload-pack=`
could write files or alter git behavior. Exploitable when the `--base` value
comes from an untrusted source (e.g. an AI agent fed a prompt-injected repo).
**Fix:** both editions now reject a base ref beginning with `-`
(`internal/git/git.go`, `node/src/cli/git.js`), with tests. Other git
invocations use only fixed arguments and repository-relative paths validated by
`relToRoot`, which rejects paths outside the repo.

### F2 — No integrity/authenticity on distributed binaries — OPEN (distribution)
The pilot binaries are unsigned. On Windows this both triggers Smart App
Control / WDAC blocks and, more importantly for a critical customer, provides no
tamper-evidence. `SHA256SUMS` is shipped but is not a signature.
**Recommendation:** sign the binaries (Azure Trusted Signing or an OV/EV
certificate) and, for Go, produce reproducible builds; publish checksums over a
trusted channel. The Node edition runs under the customer's already-signed
`node`, which is the pragmatic path on locked-down Windows.

### F3 — Token in environment variable on Windows (Go edition) — OPEN (use Node)
The Go edition has no Windows credential store, so on Windows the token must be
`FAIRMIND_TOKEN`, readable by every child process of that user session.
**Recommendation:** distribute the **Node edition** on Windows, which stores the
token with DPAPI (`%LOCALAPPDATA%\fairmind`, per-user encryption, 0600, passed
to PowerShell over stdin, never argv). macOS/Linux use Keychain/libsecret in
both editions.

### F4 — No host allowlist for the MCP/API endpoint — OPEN (low)
`mcp_url`/`api_url` may be any `https` host (credentials, query and fragment are
rejected; `http` only for loopback). The default is the FairMind production URL,
and these cannot be set from a committed `.fairmind/config.json`. A user with a
malicious profile/env could still point the token at an attacker host.
**Recommendation (optional):** for the customer, pin or allowlist the MCP host
via a managed profile, or warn when the endpoint differs from the sanctioned
one.

### F5 — Keychain command string on macOS uses the profile name — OPEN (low)
`security -i` receives a command line built with the profile name (`%q` in Go,
`JSON.stringify` in Node); the token is guarded against quotes/backslash/space
and passed inside that line. Profile is operator-controlled, not attacker-
controlled, so risk is low. **Recommendation:** validate the profile against
`^[A-Za-z0-9._-]+$` as defence in depth.

## Controls verified (no action needed)

- **TLS**: verification always on (Go `crypto/tls` default, min TLS 1.2; Node
  `https` default `rejectUnauthorized`, min TLS 1.2). No insecure/skip switch
  exists anywhere in the code.
- **Redirects** are never followed; a 3xx is surfaced as `UNEXPECTED_REDIRECT`,
  preventing bearer-token forwarding to another host.
- **Secret hygiene**: the token is never an argv flag (`--token` etc. are
  rejected), never in a URL or query, and never logged. All stdout/stderr,
  including `--verbose`, passes through a redactor that removes the exact token,
  JWT-shaped strings, `Bearer …`, and credential-shaped key/values; the
  `Authorization` header is masked in verbose logs. `auth login` reads the token
  from stdin (hidden on a TTY) and registers it with the redactor before use.
- **Config trust**: a committed `.fairmind/config.json` cannot set `api_url` or
  `mcp_url` (no token redirection) and any credential-like key is rejected.
- **Command execution**: all `exec`/`spawn` calls use fixed argv with no shell;
  git runs with `LC_ALL=C` and `GIT_TERMINAL_PROMPT=0` (no credential prompts);
  the Copilot launcher denies `security`/`printenv` tools.
- **Data minimization**: `for-code-change` transmits only repository identity,
  paths, change status, line counts, hunk ranges and (opt-in) symbol names —
  never file contents. Credentials embedded in git remotes are stripped.
- **Resource limits**: explicit network timeouts; response size caps (8 MiB API,
  16 MiB MCP); `@file` inputs capped at 4 MiB.
- **Network reach**: the devbridge binds loopback only and stores no
  credentials (forwards the caller's Authorization header).
- **Supply chain**: Go uses only the standard library; Node has zero runtime
  dependencies. Attack surface is the language toolchain alone.
- **Server-side authority**: the CLI makes no authorization decisions; scopes,
  roles, tenant isolation and ranking stay in the MCP server. Write tools
  require `--yes`; destructive/sensitive tools require an explicit human opt-in
  (`FAIRMIND_ALLOW_DESTRUCTIVE=1`).

## Recommendations before customer distribution

1. Ship the **Node edition** on Windows (F3) and **sign** all distributed
   binaries (F2).
2. Provide a managed profile pinning the sanctioned MCP host (F4).
3. Issue **read-only, project-scoped, short-lived** tokens by default; grant
   write scope only where the write loop is required.
4. Add the profile-name charset guard (F5).
5. Have the customer's security team confirm that the developer machine calling
   the FairMind MCP endpoint directly is acceptable under their egress policy.
