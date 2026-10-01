# FairMind Agent API v1 — contract the CLI expects (proposal)

This is the server contract the Phase 1 CLI is built and tested against, in
`internal/mock`. It is a **proposal for the FairMind backend team**. Nothing
here has been checked against the real backend, whose source was not available
(see `docs/legacy/PHASE1_DISCOVERY.md`).

The Agent API should be a thin adapter over the same application/service layer
the MCP server uses (spec §2). It must not duplicate MCP business logic.

## Common rules

- Auth: `Authorization: Bearer <JWT>`, validated server-side. Tenant, user,
  scope and project come from the token, never from the request.
- Every response uses the envelope `{ok, data, page, evidence, error}`
  (`schemas/error.schema.json`), including errors from gateways and proxies
  where possible.
- Error codes are UPPER_SNAKE (e.g. `TASK_NOT_FOUND`). The CLI derives exit
  codes from the code first and the HTTP status second.
- A request that is not authorized to see a resource from another tenant gets
  `404`, not `403`, so resource existence does not leak.
- IDs: accept ObjectIds and `TASK-`/`US-`/`NEED-` ids alike. Return both in
  `evidence.resolved_id`.
- Items keep FairMind trust semantics: `status` (e.g. `superseded`, with
  `superseded_by`) and `review_state` (`confirmed` | `proposed` | `rejected`).
  Superseded or proposed knowledge is labelled, never dropped silently.
- Budgeting and ranking are server-side and are FairMind IP. The server returns
  `budget.{requested_tokens, estimated_tokens, omitted_items}`.

## Endpoints

| Endpoint | Body / query | Would orchestrate (MCP primitives, from the spec) |
|---|---|---|
| `GET /v1/projects` | — | `General_list_projects` |
| `POST /v1/context:get` | `{intent, project?, task?, paths?, budget_tokens?, repository?}` | `Brain_search`, `Brain_context`, Studio task/story/need, tests, `General_rag_retrieve_documents`, `Code_search` |
| `POST /v1/context:for-task` | `{id, project?, budget_tokens?, repository?}` | `Studio_get_task` → `Studio_get_user_story` → `Studio_get_need`, `Studio_list_tests_by_userstory`, `Studio_get_related_user_stories`, Brain decisions and issues, RAG documents, code anchors |
| `POST /v1/context:for-code-change` | see `schemas/code-change-request.schema.json` | repository binding resolution, `Brain_context` per file/symbol, `Brain_timeline`, `Code_find_usages` |
| `GET /v1/search` | `q, k, in=brain,docs,code,studio, project?, repository?/repository_remote?` | `Brain_search`, `General_rag_*`, `Code_search`, `General_rag_platform_knowledge` |
| `GET /v1/brain/nodes/{id}` | — | `Brain_get` / `Brain_expand` |

### Full toolset and work items

| Endpoint | Purpose |
|---|---|
| `GET /v1/tools[?refresh=true]` | Catalog of every capability. Each entry is `{name, namespace, command, description, access, input_schema}`, where `access` is `read`, `write`, `destructive` or `sensitive`. |
| `GET /v1/tools/{name}` | One catalog entry. |
| `POST /v1/tools/{name}:call` | Body `{arguments, dry_run?}`. `data` is the tool's result. |
| `GET /v1/work?kind=task\|story\|epic&status=&parent=&limit=&cursor=&all=` | Normalized work-item list with `{id, object_id, kind, title, status, parent, excerpt}`. |

Rules for `POST /v1/tools/{name}:call`:

- **Write intent.** Non-`read` tools require `X-FairMind-Write-Intent:
  confirmed`; without it the server answers `400 WRITE_NOT_CONFIRMED`.
- **Destructive or sensitive tools** also require
  `X-FairMind-Allow-Destructive: 1`; without it the server answers
  `403 DESTRUCTIVE_NOT_ALLOWED`.
- **Idempotency.** Writes carry an `Idempotency-Key`.
- **Project ids.** `project`, `project_id` and `projectId` are normalized from
  a name to an id before the call.
- **Access classes.** They come from MCP annotations, then from the tool-name
  verb, and fall back to `write` when unknown.
  `General_get_mcp_configs_for_agent` is `sensitive` (spec §25, §31.10).
- **Authorization stays server-side.** The headers are UX guards, not
  authorization: scopes and roles are still enforced by FairMind, so a
  read-only token gets `SCOPE_REQUIRED`.

The generic tool endpoints make every current and future FairMind tool
reachable without a CLI release. The aggregate endpoints stay the recommended
entry points for context.

Every context endpoint returns a Context Pack (`schemas/context-pack.schema.json`).

`repository` in request bodies is
`{explicit?, remote?, branch?, head_commit?, configured?}`, where `remote` is a
normalized `host/owner/name`. Resolution order is explicit → remote binding →
configured (spec §22):

- The binding is ambiguous → `409 REPOSITORY_AMBIGUOUS` with
  `details.projects`.
- `for-code-change` finds no binding and no project → `404 REPOSITORY_NOT_FOUND`.
- A project was given but the repository is not bound → `200` with the warning
  `REPOSITORY_NOT_BOUND`.
- The ingested copy is older than `head_commit` → warning
  `STALE_REPOSITORY_DATA` with `details.ingested_commit` and
  `details.ingested_at`.

Warning codes the CLI and skill understand: `STALE_REPOSITORY_DATA`,
`REPOSITORY_NOT_BOUND`, `NO_KNOWLEDGE_FOR_FILES`, `NO_RELEVANT_CONTEXT`,
`SUPERSEDED_KNOWLEDGE`, `PROPOSED_KNOWLEDGE`, `HISTORY_OMITTED`.

## Headers received

`X-FairMind-Client`, `X-FairMind-Agent`, `X-FairMind-Project` (a hint only;
authorization never relies on it), `X-Request-Id`. The server should log these
(spec §34) and never log the `Authorization` header.

## Verified facts (2026-09-24, from public, unauthenticated sources)

- The production MCP endpoint is `https://project-context.fairmind.ai/mcp/mcp`.
  Clients send the JWT as `Authorization: Bearer <token>`. Source: the
  fairmind-integration plugin `SETUP.md` and the local MCP client config. This
  partly answers Q2: the transport is known, the issuer and audience are not.
- The same host publishes `/openapi.json` ("FairMind API" 0.1.0, security
  scheme `HTTPBearer`). Its REST surface covers only `mcp-configs`,
  `mcp-oauth`, `insights/v1/*` (including `POST /insights/v1/bind-repository`
  and `GET /insights/v1/tenant-status`), `sentinel/*` and health endpoints.
  There are **no REST endpoints for Brain, Studio, Code or General
  retrieval**. Those capabilities are reachable only through MCP today, so the
  `/v1/context:*`, `/v1/search` and `/v1/projects` endpoints do not exist yet
  (`GET /v1/projects` returns 404).

- `Brain_search` sees only what was delivered to the Brain. In "Community
  Pulse", Studio holds 12 user stories, but Brain has indexed 2 nodes (one
  story and one decision). Search built only on Brain silently misses most
  Studio work items. The devbridge prototype also matches Studio titles and
  emits `BRAIN_INDEX_SPARSE`. The real Agent API should search a single
  complete index, or guarantee Studio → Brain delivery.

- `Brain_search` accepts "name or ID" for `project`, but with the project
  **name** it silently returns 0 results; with the id it works. The devbridge
  always sends ids.
- `Studio_list_user_stories_by_need` returned 0 stories for an epic that has 6
  (their `needId` matches). This is spec §31.2, list-by-parent queries being
  incomplete. `GET /v1/work?parent=` filters the project list instead.
- Live catalog, 2026-09-24: 75 tools, of which 50 read, 23 write, 1
  destructive (`Insights_erase_subject`) and 1 sensitive
  (`General_get_mcp_configs_for_agent`).

## Still UNKNOWN (spec §45) — needed from the backend team

1. Existing REST endpoints and the service layer behind the MCP tools.
2. How the MCP receives the JWT, and its issuer and audience.
3. Whether the token is an access token or an API-key-like JWT, and its
   lifetime.
4. The login and refresh mechanism.
5. The repository-binding service API. `Insights_bind_repository` exists as an
   MCP tool; its read side is unknown.
6. Whether any aggregate context endpoints already partially exist.
7. The outbound-network and secret configuration for Copilot cloud agents at
   the customer.

Session mapping to Copilot sessions (Q9) is deferred to Phase 2.
