# Security

How each client-side requirement of spec §32 is met:

| Requirement | Implementation | Test |
|---|---|---|
| Never log the JWT; redact `Authorization`, cookies and API keys | Every byte on stdout and stderr goes through `output.Redactor`. It removes the exact token value, JWT-shaped strings, `Bearer …` values and credential-like key/value pairs. Verbose header logs mask `Authorization`, `Cookie` and `X-Api-Key`. | `TestNoTokenLeakInVerboseModeOrEchoedResponses`, every e2e test (the harness fails on any leak), `TestRedact` |
| No token flags | There is no flag for the token; `--token`, `--jwt`, `--api-key` and `--password` are rejected | `TestTokenFlagIsRejected` |
| Token only in the `Authorization` header | The token never appears in the URL, query or body | `TestTokenNeverInURL` |
| TLS verification | Go default verification with TLS ≥ 1.2. There is no "insecure" switch. | — |
| Validate the API host | `https` is required; `http` only for loopback. URLs with credentials, queries or fragments are rejected. A repository-committed `.fairmind/config.json` cannot set `api_url`. | `TestValidateAPIURL`, `TestInsecureAPIURLRejected`, `TestProjectFileCannotRedirectAPI` |
| No credential forwarding on redirects | Redirects are never followed; the CLI returns `UNEXPECTED_REDIRECT` instead | `TestRedirectIsNotFollowed` |
| Explicit network timeouts | 30 s overall by default (`--timeout`), 10 s to connect and 10 s for the TLS handshake | `TestTimeout` |
| Cap the response size | 8 MiB | — |
| No secrets in config | A config file containing any credential-like key is refused | `TestRejectsSecretsInConfig` |
| No repository upload unless required | `for-code-change` sends only metadata: paths, status, counts, hunk ranges and, with `--symbols`, identifier names. File contents are never read. Credentials embedded in git remotes are stripped. | `TestCodeChangeBoundRepository` |
| Client identification | `X-FairMind-Client`, `User-Agent` and `X-Request-Id` headers | `TestTokenNeverInURL` |
| Controlled writes | Write tools need `--yes`, and a `--dry-run` preview is available. Destructive and sensitive tools also need `FAIRMIND_ALLOW_DESTRUCTIVE=1`, set by a human. The server gets `X-FairMind-Write-Intent` / `X-FairMind-Allow-Destructive` headers and an `Idempotency-Key`. Server-side scopes and roles still apply. | `TestWriteToolRequiresYes`, `TestDestructiveToolNeedsHumanOptIn`, `TestReadOnlyTokenCannotWrite`, `TestToolCallGuardsAndProjectResolution` |
| Copilot permissions | The `fairmind-copilot` launcher pre-approves only `shell(fairmind:*)` and denies `security` and `printenv`. The skill declares no `allowed-tools`. | — |
| Enterprise proxies | `HTTPS_PROXY` / `NO_PROXY` are honoured | — |

## Residual risks and notes

- Setting `FAIRMIND_TOKEN` exposes the token to every child process of that
  shell, including the agent. This is inherent to environment-based CI secrets.
  The skill tells Copilot never to read or print it, and the server must
  enforce least privilege (READ-only by default).
- JWT-shaped redaction also masks any JWT that happens to appear in FairMind
  content. That is intentional.
- Git runs with `LC_ALL=C` and `GIT_TERMINAL_PROMPT=0`, so it never prompts
  for credentials and its messages parse the same on every locale.
- There are no third-party Go dependencies. The supply-chain surface is the Go
  toolchain alone. Signed releases and an SBOM are Phase 3.
