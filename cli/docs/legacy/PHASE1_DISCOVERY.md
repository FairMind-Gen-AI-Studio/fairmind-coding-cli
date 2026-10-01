# FairMind Copilot Bridge — Phase 1 Discovery

**Date:** 2026-09-24  
**Author:** Atlas (Tech Lead)  
**Spec:** `FairMind_Copilot_Bridge_Spec_v1.md`  
**Session note:** FairMind MCP server tools (`mcp__Fairmind__*`) were not connected to this discovery session. All findings that require live code inspection are marked **UNKNOWN**. Items derived from the spec document itself are marked **SPEC-DERIVED**.

---

## 1. Summary

This document records the Phase 1 discovery for the FairMind Copilot Bridge project: a Go CLI (`fairmind`) plus a server-side Agent API that lets GitHub Copilot CLI and Copilot Agents consume FairMind Project Context without MCP, specifically for customers operating under a Registry-Only MCP policy.

**Key finding: direct FairMind backend code inspection was not possible in this session** because the FairMind MCP server (`ai.fairmind/project-context`) was not connected. Every item that requires code-level verification is marked UNKNOWN and must be resolved by a human with access to the backend repositories before implementation begins.

**What can be determined from the spec alone:**

- The existing MCP surface is large (75 tools) and heterogeneous. The Agent API must expose approximately 7 aggregate commands, not 75 primitives.
- The MCP server is identified as `ai.fairmind/project-context` and exposes five tool families: Brain, Code, Studio, General, Insights.
- The spec records 10 open questions (section 45) that are intentionally unresolved; all 10 remain UNKNOWN pending code access.
- Go is the confirmed language choice for the CLI (single binary, no runtime, easy cross-platform distribution).
- The Agent API adapter is a thin orchestration layer on top of the existing FairMind service layer — it must NOT duplicate business logic.

**Recommended next step:** A human with FairMind backend repository access must answer the 10 open questions in section 5 of this document, particularly JWT structure, existing REST endpoints, and repository binding service APIs, before the Agent API adapter can be built.

---

## 2. Repositories Found

The FairMind MCP server is identified in the spec as:

```
ai.fairmind/project-context
```

**Direct code inspection was not possible in this session.** The following table reflects only what the spec documents:

| Name | ID/URL | Language | Framework | Purpose |
|------|--------|----------|-----------|---------|
| ai.fairmind/project-context | UNKNOWN | UNKNOWN | UNKNOWN | MCP server exposing Brain/Code/Studio/General/Insights tools |
| FairMind backend/core | UNKNOWN | UNKNOWN | UNKNOWN | Application service layer called by MCP |
| FairMind API gateway | UNKNOWN | UNKNOWN | UNKNOWN | May or may not exist as separate service |

> **Action required:** Identify repositories by name/URL from the FairMind GitHub/GitLab org and inspect with `Code_list_repositories` in a session where FairMind MCP is connected.

---

## 3. MCP → Service Layer Mapping

Each aggregate Agent API endpoint and the MCP tools it would orchestrate. Service function names are UNKNOWN pending code inspection.

### 3.1 POST /v1/context:get

**Purpose:** "What does FairMind know that is relevant before performing this task?" — generic entry point for any intent.

**MCP tools to orchestrate (from spec §7.1):**

| Tool | Domain | Purpose in aggregate |
|------|--------|---------------------|
| `Brain_search` | Brain | Semantic search on intent text |
| `Brain_context` | Brain | Retrieve context for a project/node |
| `Studio_get_task` | Studio | Task details if task ID provided |
| `Studio_get_user_story` | Studio | Related story |
| `Studio_list_tests_by_userstory` | Studio | Tests linked to the story |
| `General_rag_retrieve_documents` | General | Relevant project documents |
| `Code_search` | Code | Code search if ingested repo exists |

**Service functions:** UNKNOWN — requires code inspection  
**Selection/ranking logic:** MUST remain server-side (FairMind IP per spec §2.1)  
**Notes:** The `--budget <tokens>` parameter triggers server-side ranking and trimming. The ranking algorithm must never live in the CLI.

---

### 3.2 POST /v1/context:for-task

**Purpose:** Return all relevant context for implementing a specific FairMind task, story, or need.

**Expected traversal (from spec §7.2):**

```
Task
 └── User Story
      └── Need
           ├── Tests
           ├── Functional/technical requirements
           ├── Related stories
           ├── Decisions
           ├── Issues
           ├── Relevant documents
           ├── Code anchors
           └── Repository information
```

**MCP tools to orchestrate:**

| Tool | Domain | Purpose |
|------|--------|---------|
| `Studio_get_task` | Studio | Resolve task by ID |
| `Studio_get_user_story` | Studio | Parent story |
| `Studio_get_need` (inferred) | Studio | Parent need |
| `Studio_list_tests_by_userstory` | Studio | Tests |
| `Brain_get` | Brain | Requirements/decisions linked to need |
| `Brain_search` | Brain | Related decisions/issues |
| `General_rag_retrieve_documents` | General | Relevant docs |
| `Code_find_usages` | Code | Code anchors where repo is indexed |

**Service functions:** UNKNOWN  
**ID normalization:** Both ObjectId and `TASK-*`/`US-*`/`NEED-*` must be accepted. Normalization MUST be server-side.  
**Notes:** This endpoint is expected to be the highest-value endpoint for structured project workflows.

---

### 3.3 POST /v1/context:for-code-change

**Purpose:** Before Copilot changes existing code, retrieve the WHY behind that code (architectural decisions, requirements, historical decisions, open issues, superseded decisions, cross-repository impact).

**Input modes (from spec §7.3):**
- `--diff` (stdin): pipe `git diff` output
- `--files src/auth.ts src/session.ts`: explicit file list
- `--symbols`: optional symbol extraction

**MCP tools to orchestrate:**

| Tool | Domain | Purpose |
|------|--------|---------|
| `Insights_*` (repo binding) | Insights | Resolve git remote → FairMind repo binding |
| `Brain_context` | Brain | Context per file/symbol |
| `Brain_timeline` | Brain | Historical decisions for affected files |
| `Code_find_usages` | Code | Cross-repository impact |
| `Brain_search` | Brain | Related requirements/decisions |

**Service functions:** UNKNOWN — particularly the repository binding resolution service  
**Notes:** This is described in the spec as "expected to be the highest-value capability for GitHub Copilot" (§7.3). The repository binding service (resolving git remote URL → FairMind project) is a critical dependency for this endpoint. Its API is UNKNOWN.

**Privacy requirement (spec §33):** Send only file paths, symbol names, diff metadata — not full source files — when the repo is already indexed.

---

### 3.4 GET /v1/search

**Purpose:** Federated search across all FairMind knowledge domains with a single normalized interface.

**MCP tools to orchestrate:**

| Tool | Domain | Purpose |
|------|--------|---------|
| `Brain_search` | Brain | Semantic search on knowledge graph |
| `General_rag_retrieve_documents` | General | Project document RAG |
| `General_rag_retrieve_documents_for_session` | General | Session-scoped RAG |
| `Code_search` | Code | Indexed code search |

**Service functions:** UNKNOWN  
**Domain filter:** `--in brain,docs,code,studio`  
**Notes:** All results must identify their source domain. Inconsistent RAG result-count behavior noted in spec §31.5 — Agent API must normalize.

---

## 4. Authentication Findings

**All items are UNKNOWN.** The FairMind MCP server was not accessible for code inspection in this session. The following is what the spec states as requirements and assumptions:

| Item | Status | Source/Evidence |
|------|--------|----------------|
| HTTP header for JWT transport | SPEC-DERIVED | Spec §11.1: `Authorization: Bearer <JWT>` recommended |
| Issuer | UNKNOWN | Not in spec; requires backend code inspection |
| Audience | UNKNOWN | Not in spec; requires backend code inspection |
| Token type (access token vs API-key-like JWT) | UNKNOWN | Spec §45.4 explicitly leaves this open |
| Token claims (tenant, user, scope, project) | SPEC-DERIVED | Spec §11: "company/tenant, user identity, scope (read/write/admin), optionally project scope" |
| Login/refresh mechanism | UNKNOWN | Spec §45.5 explicitly leaves this open; spec §12 recommends OAuth/OIDC device flow for production |
| Token validation location | SPEC-DERIVED | Server-side only; CLI must never infer access from local state (spec §13) |
| Optional metadata headers | SPEC-DERIVED | `X-FairMind-Project`, `X-FairMind-Client: fairmind-cli/<version>`, `Idempotency-Key` |

> **Critical action required:** Before implementing the auth module, obtain from the FairMind backend team:
> 1. The JWT issuer and audience values used in production
> 2. Whether the JWT is a short-lived OAuth access token or a long-lived API-key-style JWT
> 3. The current login/token-issuance flow (is there a `/auth/token` endpoint?)
> 4. Whether refresh tokens exist and where they are stored

---

## 5. Open Questions — Section 45 Status

| # | Question (from spec §45) | Status | Evidence / Notes |
|---|--------------------------|--------|-----------------|
| 1 | Exact existing FairMind REST endpoints | UNKNOWN | FairMind MCP not connected; cannot inspect route files |
| 2 | Exact JWT transport currently used by the MCP | UNKNOWN | Cannot inspect MCP server auth middleware |
| 3 | Issuer/audience details | UNKNOWN | Cannot inspect JWT validation config |
| 4 | Whether JWT is access token or API-key-like JWT | UNKNOWN | Spec §45.4 explicitly unresolved |
| 5 | Refresh/login mechanism currently available | UNKNOWN | Spec §45.5 explicitly unresolved |
| 6 | Existing repository binding service APIs | UNKNOWN | Cannot inspect Insights service layer |
| 7 | Which MCP orchestration logic can be directly extracted/reused | UNKNOWN | Cannot inspect service layer; spec §43 explicitly requires this investigation |
| 8 | Whether aggregate context endpoints already partially exist | UNKNOWN | Cannot inspect existing REST routes |
| 9 | How work sessions should map to Copilot CLI sessions | PARTIAL-SPEC | Spec §9 describes `fairmind work start` creating/associating sessions; details of session identity (how Copilot CLI sessions are identified) are UNKNOWN. Phase 1 is read-only so this is deferred. |
| 10 | Cloud Copilot Agent outbound-network and secret configuration at customer | UNKNOWN | External/customer-side — cannot determine from FairMind code. Requires dialogue with the customer's GitHub Enterprise administrator. |

**Summary:** 9 of 10 questions are fully UNKNOWN. Question 9 is partially addressed by the spec but requires Phase 2 investigation. All require human action before the Agent API adapter can be built.

---

## 6. Proposed Server-Side Additions (Agent API Adapter)

### What Already Exists (UNKNOWN — must be confirmed)

The spec notes (§31.8) that "some session ACL behavior differs across tool families," implying the existing backend has session-aware access control. Whether any `/v1/context:*` aggregate endpoints exist is explicitly listed as UNKNOWN in spec §45.8.

**Assumption (conservative):** No aggregate `/v1/context:*` endpoints exist today. The MCP server calls internal service layer functions directly, not via REST.

### What Needs to Be Built

A new **Agent API Adapter** — a thin HTTP service (or a route group within the existing FairMind API) that:

1. Accepts JWT via `Authorization: Bearer` (same as MCP)
2. Orchestrates existing service layer functions (same layer MCP uses)
3. Returns normalized JSON envelopes (spec §14)
4. Applies server-side token budgeting/ranking
5. Normalizes IDs, error envelopes, and pagination

**This is a new surface, not a new product.** The architecture is:

```
                   FairMind Core (existing)
                          |
          +--------------+---------------+
          |                              |
    MCP Adapter (existing)        Agent API Adapter (NEW)
          |                              |
 Claude/Cursor/etc.              fairmind CLI
                                        |
                                 GitHub Copilot
```

### Minimal New Endpoints (Phase 1 — read-only)

| Endpoint | New | Orchestrates |
|----------|-----|-------------|
| `POST /v1/context:get` | NEW | Brain_search + Brain_context + Studio lookups + RAG |
| `POST /v1/context:for-task` | NEW | Studio traversal + Brain + RAG + Code |
| `POST /v1/context:for-code-change` | NEW | Repo binding resolution + Brain_context + Brain_timeline |
| `GET /v1/search` | NEW | Brain_search + RAG + Code_search (federated) |
| `GET /v1/projects` | NEW or EXISTING | List accessible projects |
| `GET /v1/auth/status` | NEW | Validate JWT + return identity |

### Agent API Standard Envelope (per spec §14)

Success:
```json
{
  "ok": true,
  "data": {},
  "page": null,
  "evidence": null,
  "error": null
}
```

Error:
```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "TASK_NOT_FOUND",
    "message": "Task TASK-123 was not found",
    "retryable": false,
    "details": {}
  }
}
```

### Expected HTTP Error Codes (per spec §13)

```
401 token_missing / token_expired / token_invalid
403 scope_required / role_required / session_private / resource_forbidden
404 project_not_found / task_not_found / repository_not_found / knowledge_not_found
409 invalid_state_transition / idempotency_conflict
```

---

## 7. CLI Phase 1 Implementation Plan (Go)

Phase 1 implements **read-only commands only**: `auth status`, `context get`, `context for-task`, `context for-code-change`, `search`.

### Prerequisites Before Coding Starts

1. **Resolve open questions 1–6** (JWT format, existing endpoints, repo binding API) with the FairMind backend team.
2. **Build or confirm Agent API adapter** is deployed at a known base URL (e.g., `https://api.fairmind.ai/v1`).
3. **Obtain a test JWT** for development/staging — stored in env var, never in code.

### Implementation Steps

#### Step 1 — Repository scaffold
```
mkdir fairmind-copilot-bridge
cd fairmind-copilot-bridge
git init
go mod init github.com/fairmind/fairmind-copilot-bridge
```
Create directory tree from spec §36 (see section 8 of this document).

#### Step 2 — Auth module (`internal/auth/`)
File: `internal/auth/token.go`
- Read `FAIRMIND_TOKEN` from environment (only source allowed in Phase 1)
- Validate it is non-empty (do not validate JWT signature client-side)
- Provide `func GetToken() (string, error)` — returns error if not set
- NEVER log the token value
- NEVER return it in any JSON output
- NEVER accept it as a CLI flag

#### Step 3 — Config module (`internal/config/`)
File: `internal/config/config.go`
- Load `FAIRMIND_BASE_URL` from env (default: `https://api.fairmind.ai`)
- Load `.fairmind/config.json` (project, repository defaults)
- Merge: CLI flags > env vars > config file > defaults
- No credential storage in config file

#### Step 4 — Git remote detection (`internal/git/`)
File: `internal/git/remote.go`
- Execute `git remote get-url origin` via `exec.Command`
- Normalize remote URL to repository identity (strip .git, normalize github.com/org/repo)
- Return empty string (not error) when no git repo detected
- Used by `context for-code-change` for automatic repo resolution

#### Step 5 — HTTP client (`internal/api/`)
Files:
- `internal/api/client.go` — base HTTP client
- `internal/api/envelope.go` — Go structs for success/error envelopes
- `internal/api/endpoints.go` — typed callers for each Agent API endpoint

Client requirements:
- Set `Authorization: Bearer <token>` header (token from auth module)
- Set `X-FairMind-Client: fairmind-cli/<version>` header
- Set `X-FairMind-Agent: github-copilot` header when detected
- TLS certificate verification (no skip)
- Explicit timeout (default 30s, configurable via `--timeout`)
- Response size cap (e.g., 10 MB)
- Redact Authorization header in any debug/verbose output

#### Step 6 — Output module (`internal/output/`)
Files:
- `internal/output/json.go` — marshal standard envelope to stdout
- `internal/output/human.go` — human-readable table/text for interactive use
- `internal/output/exit.go` — exit code mapping per spec §15:

```
0  success
1  generic application error
2  authentication error (401)
3  resource not found (404)
4  forbidden / insufficient scope (403)
5  validation error
6  conflict / invalid state (409)
7  remote service unavailable (5xx, network error)
8  timeout
```

#### Step 7 — `fairmind auth status` (`internal/commands/auth.go`)
- Call `GET /v1/auth/status` (or `GET /v1/projects` as probe)
- Print identity and scope on success
- Exit code 2 on any auth error

#### Step 8 — `fairmind context get` (`internal/commands/context_get.go`)
Flags: `--intent`, `--project`, `--task`, `--path`, `--budget`, `--json`
- `POST /v1/context:get`
- Body: `{"intent": "...", "project": "...", "task": "...", "path": "...", "budget": 8000}`
- Output: Context Pack (spec §17)

#### Step 9 — `fairmind context for-task` (`internal/commands/context_for_task.go`)
Args: `<TASK_OR_STORY_OR_NEED_ID>` (positional)
Flags: `--budget`, `--json`
- `POST /v1/context:for-task`
- Body: `{"id": "TASK-123", "budget": 8000}`
- Accept both ObjectId and `TASK-*/US-*/NEED-*` format — pass as-is to server (normalization is server-side)

#### Step 10 — `fairmind context for-code-change` (`internal/commands/context_for_code_change.go`)
Flags: `--diff` (read from stdin), `--files <paths>`, `--symbols`, `--budget`, `--json`
- Auto-detect git remote (from `internal/git/remote.go`)
- `POST /v1/context:for-code-change`
- Body: `{"repository": "...", "diff": "...", "files": [...], "budget": 8000}`
- Do NOT send full file contents unless `--diff` explicitly pipes them
- Prefer sending file paths and symbol names when repo is indexed

#### Step 11 — `fairmind search` (`internal/commands/search.go`)
Flags: `--in brain,docs,code,studio`, `--project`, `--k`, `--json`
- `GET /v1/search?q=...&in=brain,docs&project=...&k=10`
- Output: list of items with source, kind, score, excerpt

#### Step 12 — CLI entry point (`cmd/fairmind/main.go`)
- Use `github.com/spf13/cobra` (de-facto Go CLI standard, used by `kubectl`, `gh`)
- Command tree:
  ```
  fairmind
    auth
      status
    context
      get
      for-task
      for-code-change
    search
    status
    version
  ```
- Global flags: `--project`, `--repo`, `--json`, `--profile`, `--timeout`, `--verbose`
- In `--json` mode: all output to stdout as JSON; all errors to stderr as JSON

#### Step 13 — Agent Skill + Custom Agent
- `.github/skills/fairmind-project-context/SKILL.md` — per spec §27
- `.github/agents/fairmind.agent.md` — per spec §28

#### Step 14 — Tests (per spec §44)

**Authentication tests** (`tests/auth_test.go`):
```
TestMissingJWT         → exit code 2, error.code = "token_missing"
TestMalformedJWT       → exit code 2, error.code = "token_invalid"
TestExpiredJWT         → exit code 2, error.code = "token_expired"
TestReadOnlyJWT        → success on read commands, would fail on write
TestProjectScopedJWT   → access limited to scoped project
TestWrongTenant        → exit code 4, error.code = "resource_forbidden"
```

**Context tests** (`tests/context_test.go`):
```
TestGeneralIntent             → valid JSON, ok=true
TestTaskByObjectId            → valid JSON, items populated
TestTaskByFairMindID          → same result as ObjectId (normalization)
TestUnknownTask               → exit code 3, error.code = "TASK_NOT_FOUND"
TestRepositoryNotBound        → ok=true, warnings contain repo binding warning
TestRepositoryBound           → ok=true, items include code anchors
TestFileWithNoKnowledge       → ok=true, items empty, no error
TestFileWithDecisions         → ok=true, items include decision with review_state
TestSupersededDecision        → ok=true, item present with status="superseded", labelled
TestProposedRequirement       → ok=true, item present with review_state="proposed"
TestStaleRepositoryData       → ok=true, warning about staleness in output
```

**CLI output tests** (`tests/cli_test.go`):
```
TestJSONValid                 → jq . succeeds on stdout
TestExitCodesCorrect          → per spec §15 mapping
TestTimeoutHandling           → exit code 8 on timeout
TestServer5xx                 → exit code 7
TestNetworkUnavailable        → exit code 7
TestNoJWTInStdout             → stdout does not contain FAIRMIND_TOKEN value
TestNoJWTInStderr             → stderr does not contain FAIRMIND_TOKEN value
TestNoJWTInVerboseMode        → --verbose output does not contain token
TestAuthHeaderRedacted        → --verbose shows "Authorization: [REDACTED]"
```

**Agent skill tests** (manual/integration):
```
TestCopilotChoosesForTask         → Copilot picks context for-task for known task
TestCopilotChoosesForCodeChange   → Copilot picks context for-code-change before file edits
TestCopilotDoesNotPrintToken      → no credential exposure in Copilot chat
TestCopilotHandlesEmptyContext    → safe continuation when context is empty
TestCopilotDistinguishesProposed  → treats proposed != confirmed
```

---

## 8. Repository Structure (Go)

```
fairmind-copilot-bridge/
├── cmd/
│   └── fairmind/
│       └── main.go                    # CLI entry point, cobra root command
├── internal/
│   ├── api/
│   │   ├── client.go                  # HTTP client: timeouts, TLS, size cap, header injection
│   │   ├── envelope.go                # Go types: SuccessEnvelope, ErrorEnvelope, ContextPack, SearchResult
│   │   └── endpoints.go               # Typed API callers: ContextGet(), ContextForTask(), Search(), etc.
│   ├── auth/
│   │   └── token.go                   # Read FAIRMIND_TOKEN; never log; provide GetToken()
│   ├── config/
│   │   └── config.go                  # FAIRMIND_BASE_URL, .fairmind/config.json, profile merging
│   ├── output/
│   │   ├── json.go                    # JSON marshaling to stdout; error envelope to stderr
│   │   ├── human.go                   # Human-readable table/text for interactive use
│   │   └── exit.go                    # Exit code constants and mapping function
│   ├── git/
│   │   └── remote.go                  # git remote get-url origin; normalize to repo identity
│   └── commands/
│       ├── auth.go                    # fairmind auth status
│       ├── context_get.go             # fairmind context get
│       ├── context_for_task.go        # fairmind context for-task <ID>
│       ├── context_for_code_change.go # fairmind context for-code-change --diff | --files
│       ├── search.go                  # fairmind search "<query>" --in ... --k
│       ├── status.go                  # fairmind status (auth + project summary)
│       └── version.go                 # fairmind version
├── schemas/
│   ├── context-pack.schema.json       # JSON Schema for Context Pack output
│   ├── error.schema.json              # JSON Schema for error envelope
│   └── search.schema.json             # JSON Schema for search result
├── .github/
│   ├── skills/
│   │   └── fairmind-project-context/
│   │       └── SKILL.md               # GitHub Copilot Agent Skill (spec §27)
│   └── agents/
│       └── fairmind.agent.md          # GitHub Copilot Custom Agent (spec §28)
├── tests/
│   ├── auth_test.go
│   ├── context_test.go
│   └── cli_test.go
├── docs/
│   ├── CLI.md                         # Command reference
│   ├── AUTH.md                        # Authentication guide
│   ├── SECURITY.md                    # Security model
│   └── PHASE1_DISCOVERY.md            # This file
├── go.mod
├── go.sum
├── Makefile                           # build, test, lint, release targets
└── README.md
```

**Key Go dependencies:**
- `github.com/spf13/cobra` — CLI framework (same as `kubectl`, `gh`)
- `github.com/spf13/viper` — config file + env var merging
- Standard library: `net/http`, `encoding/json`, `os/exec` (for git)
- For Phase 3 keychain: `github.com/zalando/go-keyring` or `github.com/99designs/keyring`
- For tests: `github.com/stretchr/testify`, `net/http/httptest` for mock server

---

## 9. Language Recommendation

**Confirmed: Go**

Rationale (per spec §35 + Phase 1 implementation plan):

| Criterion | Go verdict |
|-----------|-----------|
| Single binary distribution | Yes — `go build` produces one static binary |
| macOS/Linux/Windows cross-compile | Yes — `GOOS=darwin/linux/windows go build` |
| No runtime dependency in customer env | Yes — no JVM, no Node, no Python |
| HTTP/JSON standard library | Yes — `net/http` + `encoding/json` |
| TLS certificate verification | Yes — default in `net/http` |
| Secure credential store | Yes — `go-keyring` wraps macOS Keychain, SecretService, WinCred |
| CLI framework maturity | Yes — `cobra` used by `kubectl`, `gh`, `docker` |
| CI packaging + code signing | Yes — `goreleaser` is the standard |
| Code auditability | Yes — explicit, no magic |

TypeScript would be acceptable only if the FairMind team has existing Node tooling and prefers npm distribution. For a customer-facing CLI binary, Go's single-binary model is clearly superior.

---

## 10. Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|-----------|------------|
| JWT format/claims UNKNOWN — auth module cannot be written without this | HIGH | HIGH (must resolve) | Obtain JWT spec from FairMind backend team immediately |
| Agent API adapter doesn't exist — CLI has nothing to call | HIGH | HIGH | Build Agent API adapter server-side before CLI end-to-end tests |
| Repository binding API UNKNOWN — blocks `context for-code-change` | HIGH | HIGH | Inspect Insights service layer; identify binding resolution function |
| ID normalization strategy UNKNOWN — affects `context for-task` | MEDIUM | HIGH | Confirm whether service layer already normalizes TASK-*/ObjectId |
| MCP inconsistencies (spec §31) leak into Agent API | MEDIUM | MEDIUM | Agent API adapter must test each normalized response |
| Aggregate context endpoints partially exist (unknown) | MEDIUM | LOW | If endpoints exist, reuse them; if not, build from spec §24 |
| Token refresh UNKNOWN — MVP uses env var | MEDIUM | LOW | MVP env var is acceptable; OAuth device flow deferred to Phase 3 |
| Session ACL differences across tool families | MEDIUM | MEDIUM | Test per-domain access separately; do not assume uniform session ACL |
| Customer outbound network config for Copilot Cloud Agents | MEDIUM | MEDIUM | Customer/GitHub Enterprise responsibility; document requirements clearly |
| Work session → Copilot CLI session mapping | LOW (Phase 1 only) | N/A | Phase 1 is read-only; deferred to Phase 2 |
| Encoding/mojibake issues in document content (spec §31.9) | LOW | MEDIUM | Agent API adapter must validate encoding before returning |

---

## 11. Prerequisite Checklist Before Phase 1 Coding

The following must be completed by a human with FairMind backend access **before** the CLI or Agent API can be implemented:

- [ ] **Q1**: Document all existing REST routes in the FairMind backend (method + path + auth requirements)
- [ ] **Q2**: Confirm JWT transport: is it `Authorization: Bearer <JWT>` in all endpoints?
- [ ] **Q3**: Provide JWT issuer and audience values for staging and production
- [ ] **Q4**: Confirm whether the JWT is an OAuth access token (short-lived) or an API-key-style JWT (long-lived)
- [ ] **Q5**: Document the current login/token issuance flow (endpoint, flow type)
- [ ] **Q6**: Document the repository binding service API (how does a git remote URL resolve to a FairMind project?)
- [ ] **Q7**: Identify which service layer functions are called by `Brain_context`, `Brain_search`, `Studio_get_task`, `Studio_get_user_story`, `Code_find_usages` — provide file paths and function signatures
- [ ] **Q8**: Confirm whether any aggregate `/v1/context:*` endpoints already exist
- [ ] **Staging**: Provide Agent API base URL and test JWT for development
- [ ] **Repository access**: Grant the CLI developer access to the FairMind backend repository

---

*Document generated: 2026-09-24*  
*Spec version: FairMind_Copilot_Bridge_Spec_v1.md*  
*FairMind MCP tool access: NOT AVAILABLE in this session — all code-level findings are UNKNOWN*
