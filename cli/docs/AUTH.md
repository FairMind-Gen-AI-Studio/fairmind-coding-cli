# Authentication

The CLI reuses FairMind's existing JWT model: it forwards the token and does
nothing else. Validation, tenant isolation, scopes, project roles and session
privacy are all enforced by the server (spec §13).

## Token sources (in order)

1. `FAIRMIND_TOKEN` environment variable. Use it for CI and GitHub-hosted
   agents, fed from a GitHub secret or environment secret.
2. The OS credential store, under service `fairmind-cli` with the profile name
   as the account:
   - macOS Keychain, through `security`. The token is written via
     `security -i` on stdin, so it never appears in process arguments.
   - Linux libsecret, through `secret-tool`. The token is passed on stdin.
   - Windows has no credential-store support yet (Phase 3); use the variable.

There is **no** `--token` flag. `--token`, `--jwt`, `--api-key` and
`--password` are rejected explicitly.

```bash
pbpaste | fairmind auth login          # macOS: nothing lands in shell history
fairmind auth login                    # interactive, input hidden
fairmind auth status                   # source + unverified expiry/scope + server check
fairmind auth logout
```

Avoid `export FAIRMIND_TOKEN="eyJ..."` in an interactive shell, because it ends
up in shell history. Prefer `auth login`.

## HTTP

```http
Authorization: Bearer <JWT>
X-FairMind-Client: fairmind-cli/<version>
X-FairMind-Agent: <FAIRMIND_AGENT or "cli">
X-FairMind-Project: <project, when resolved locally>
X-Request-Id: <uuid v4>
```

The production header names still have to be confirmed against the FairMind
backend (spec §11).

## Claims

`auth status` decodes the JWT payload **without verifying it**. It shows `iss`,
`aud`, `scope`/`scp` and `exp` for the user's convenience and reports
`claims_verified: false`. The CLI never makes access decisions from them.

The real claim set is **UNKNOWN** (spec §45 Q2–Q4). The mock assumes
`sub`, `company`, `scope` (space-separated `read`/`write`/`admin`),
optionally `project`, and `exp`. These are placeholders.

## Recommendations for the backend (open)

- Issue READ-only tokens by default and grant WRITE explicitly (spec §12).
- Prefer short-lived, project-scoped tokens. Add a device-flow login and
  refresh in Phase 3.
