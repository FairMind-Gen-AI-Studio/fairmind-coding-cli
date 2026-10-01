# FairMind Copilot Bridge — Technical Specification v1.0

**Status:** Draft for implementation  
**Date:** 2026-09-24  
**Target implementers:** Claude Code, OpenAI Codex  
**Primary goal:** enable GitHub Copilot CLI and GitHub Copilot Agents to use FairMind Project Context without MCP, while preserving FairMind intellectual property and the existing JWT-based authorization model.

---

## 1. Context

FairMind currently exposes its Project Context capabilities through an MCP server identified as:

`ai.fairmind/project-context`

The authenticated MCP introspection identified:

- 75 total tools
- 11 `Brain_*`
- 7 `Code_*`
- 21 `General_*`
- 11 `Insights_*`
- 25 `Studio_*`
- 51 READ operations
- 23 WRITE operations
- 1 DELETE operation

The target customer uses GitHub Copilot with a highly restrictive **Registry Only** MCP policy. The FairMind MCP cannot be made available through the registry accepted by that customer.

The solution must therefore **not depend on MCP**.

The replacement integration must use capabilities already available to GitHub Copilot CLI / Agents, primarily:

- shell / command execution
- repository-level instructions
- GitHub custom agents
- Agent Skills
- HTTPS access to a FairMind-controlled backend

The FairMind proprietary implementation must remain server-side.

---

# 2. Architecture decision

## 2.1 Principle

Do **not** reproduce the MCP runtime inside the client.

The CLI must not contain FairMind business logic, orchestration logic, ranking algorithms, knowledge graph logic, prompts, retrieval strategies, or other FairMind intellectual property.

The intended architecture is:

```text
GitHub Copilot CLI / Agent
          |
          | shell
          v
     fairmind CLI
          |
          | HTTPS + JWT
          v
   FairMind Agent API
          |
   +------+------+------+
   |      |      |      |
 Brain  Studio  Code  General / Insights
          |
          v
 FairMind proprietary core
```

The existing MCP becomes one adapter of the FairMind application layer.

The new CLI/API integration becomes another adapter.

```text
                   FairMind Core
                        |
          +-------------+-------------+
          |                           |
      MCP Adapter                  Agent API
          |                           |
 Claude / Cursor / etc.          fairmind CLI
                                      |
                               GitHub Copilot
```

---

# 3. Product goals

The bridge MUST:

1. allow GitHub Copilot CLI to retrieve FairMind project context;
2. allow GitHub Copilot custom agents to retrieve FairMind context;
3. support read and controlled write workflows;
4. preserve the existing FairMind JWT authorization model;
5. keep all sensitive FairMind IP server-side;
6. expose a small, stable agent-facing interface instead of 75 MCP primitives;
7. normalize IDs, errors and output schemas;
8. be usable interactively by developers;
9. be usable non-interactively by agents;
10. support future reuse by other non-MCP AI clients.

The bridge SHOULD:

- reduce tool-selection ambiguity for agents;
- provide server-side token budgeting;
- support idempotent writes;
- preserve provenance;
- preserve FairMind review/trust semantics;
- make actions auditable.

---

# 4. Non-goals

The first version MUST NOT:

- bypass or weaken GitHub Enterprise MCP policies;
- emulate MCP locally just to bypass Registry Only;
- ship FairMind proprietary business logic inside the CLI;
- expose every MCP tool directly to Copilot;
- expose destructive administration operations to the agent;
- print or pass JWT tokens in command arguments;
- require the customer to run a FairMind backend locally.

---

# 5. Existing FairMind domains

The MCP introspection revealed five logical domains.

## Brain

Knowledge, requirements, issues, decisions, historical state, code-linked context and timelines.

Important primitives include:

- `Brain_context`
- `Brain_search`
- `Brain_get`
- `Brain_expand`
- `Brain_timeline`
- `Brain_record_decision`
- `Brain_record_issue`
- `Brain_record_requirement`
- `Brain_set_status`
- `Brain_supersede_decision`
- `Brain_add_document`

## Code

Repository inspection and AI-readiness analysis.

Important primitives include:

- `Code_list_repositories`
- `Code_search`
- `Code_tree`
- `Code_cat`
- `Code_grep`
- `Code_find_usages`
- `Code_analyze_repo`

## Studio

Needs, stories, requirements, tasks, tests, implementation branch, journals and project workflow.

## General

Projects, sessions, attachments, document content and retrieval.

## Insights

Repository bindings, audit, decisions, session activity, loop stats and tenant-level telemetry.

---

# 6. Design principle: aggregate capabilities

GitHub Copilot MUST NOT be taught the entire 75-tool surface.

Target agent-facing surface: approximately 7 commands.

Recommended commands:

```bash
fairmind context get
fairmind context for-task
fairmind context for-code-change
fairmind search
fairmind work start
fairmind work complete
fairmind record issue
```

Optional:

```bash
fairmind status
fairmind work list
fairmind context for-repository
```

Lower-level commands may exist for humans/debugging but should normally be hidden from agent instructions.

---

# 7. Agent-facing commands

## 7.1 `fairmind context get`

Primary generic entry point.

### Syntax

```bash
fairmind context get \
  --intent "<free text>" \
  [--project <id|name>] \
  [--task <id>] \
  [--path <path>] \
  [--budget <tokens>] \
  --json
```

### Purpose

Answer:

> What does FairMind know that is relevant before performing this task?

### Server-side orchestration

May use:

- `Brain_search`
- `Brain_context`
- Studio task/story/need retrieval
- tests related to the story
- relevant documents
- `Code_search` where an ingested repository exists
- historical decisions
- unresolved issues

The selection and ranking logic MUST be server-side.

### Output

```json
{
  "ok": true,
  "data": {
    "summary": "Relevant project context...",
    "intent": "Implement password reset",
    "project": {
      "id": "...",
      "name": "..."
    },
    "items": [
      {
        "source": "brain",
        "kind": "decision",
        "id": "...",
        "title": "...",
        "why_relevant": "...",
        "excerpt": "...",
        "status": "taken",
        "review_state": "confirmed"
      }
    ],
    "warnings": [],
    "follow_up": [
      {
        "command": "fairmind brain get <ID> --json",
        "reason": "Full decision content available"
      }
    ],
    "budget": {
      "requested_tokens": 8000,
      "estimated_tokens": 6230
    }
  },
  "error": null
}
```

---

## 7.2 `fairmind context for-task`

### Syntax

```bash
fairmind context for-task <TASK_OR_STORY_OR_NEED_ID> \
  [--budget <tokens>] \
  --json
```

### Purpose

Return all relevant context for implementing a FairMind task/story/need.

### Expected traversal

```text
Task
 |
 v
User Story
 |
 v
Need
 |
 +--> Tests
 +--> Functional / technical requirements
 +--> Related stories
 +--> Decisions
 +--> Issues
 +--> Relevant documents
 +--> Code anchors
 +--> Repository information
```

### Important requirement

Input IDs MUST accept both:

- Mongo/ObjectId-like identifiers
- FairMind/mindstream IDs such as `TASK-*`, `US-*`, `NEED-*`

ID normalization MUST happen server-side.

The agent must never need to know which internal endpoint requires which identifier format.

---

## 7.3 `fairmind context for-code-change`

This is expected to be the highest-value capability for GitHub Copilot.

### Syntax

```bash
fairmind context for-code-change \
  --diff \
  [--budget 8000] \
  --json
```

or:

```bash
fairmind context for-code-change \
  --files src/auth.ts src/session.ts \
  --json
```

Optional:

```bash
--symbols
```

### Purpose

Before Copilot changes existing code, retrieve the **WHY** behind that code.

Copilot already has direct access to the local checkout. FairMind should supply what is missing:

- architectural decisions;
- requirements;
- historical decisions;
- open issues;
- superseded decisions;
- external/project knowledge;
- cross-repository impact information.

### Server-side orchestration

Potentially:

```text
Changed files
    |
Repository binding resolution
    |
Brain_context per file/symbol
    |
Brain_timeline
    |
Code_find_usages where relevant
    |
Relevant requirements / decisions / issues
    |
Context pack
```

---

# 8. Federated search

## `fairmind search`

### Syntax

```bash
fairmind search "<query>" \
  [--in brain,docs,code,studio] \
  [--project <id|name>] \
  [--k 10] \
  --json
```

### Purpose

Provide one normalized search interface instead of exposing:

- Brain semantic search;
- project document RAG;
- session RAG;
- specific document retrieval;
- code search;
- platform knowledge search.

### Output

Every item MUST identify its source.

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "source": "brain",
        "kind": "requirement",
        "id": "...",
        "title": "...",
        "score": 0.91,
        "excerpt": "..."
      },
      {
        "source": "document",
        "kind": "specification",
        "id": "...",
        "title": "...",
        "score": 0.87,
        "excerpt": "..."
      }
    ]
  }
}
```

---

# 9. Work lifecycle

## 9.1 `fairmind work start`

### Syntax

```bash
fairmind work start <TASK_ID> \
  [--branch auto|<branch>] \
  [--session auto|none] \
  --json
```

### Responsibilities

The server may:

1. resolve the task ID;
2. validate authorization;
3. move task to `in_progress` where allowed;
4. register/update implementation branch;
5. create or associate a work session;
6. return `context for-task`.

The operation MUST be idempotent.

Repeated execution must not create duplicate sessions, duplicate transitions or duplicate records.

---

## 9.2 `fairmind work complete`

### Syntax

```bash
fairmind work complete <TASK_ID> \
  --journal <file|-> \
  [--decisions <file>] \
  [--issues <file>] \
  [--status done] \
  [--dry-run] \
  --json
```

### Purpose

Close the agent loop with one controlled transaction.

It may orchestrate:

- `Studio_process_journal`
- decision recording
- issue recording
- task status updates
- session activity
- telemetry/audit

Do not expose all these underlying writes independently to Copilot unless needed for debugging.

### Output

```json
{
  "ok": true,
  "data": {
    "task": "TASK-123",
    "status": "done",
    "operations": [
      {
        "type": "journal",
        "status": "recorded"
      },
      {
        "type": "decision",
        "id": "DEC-...",
        "status": "recorded"
      },
      {
        "type": "task_status",
        "status": "done"
      }
    ]
  }
}
```

---

# 10. Mid-task issue recording

## `fairmind record issue`

### Syntax

```bash
fairmind record issue \
  --kind bug|feature|question|tech_debt \
  --title "<title>" \
  --body "<body>" \
  [--file path[:symbol]] \
  [--task <id>] \
  --json
```

### Purpose

Allow Copilot to record findings outside the requested scope instead of silently ignoring or fixing them.

The server MUST create a deterministic `natural_key` when the caller does not provide one.

Agent-created issues should preserve FairMind review semantics and should not silently become confirmed human knowledge.

---

# 11. JWT authentication

The existing FairMind authorization model should be preserved.

Current tool documentation indicates the token carries or implies:

- company / tenant;
- user identity;
- scope such as `read`, `write`, `admin`;
- optionally project scope;
- project-level role checks;
- session privacy rules.

## 11.1 Mandatory rules

Never pass a token like this:

```bash
fairmind --token eyJ...
```

The token must never appear in:

- process arguments;
- shell history;
- Copilot prompt context;
- log messages;
- JSON output.

Supported sources should be:

1. OS secure credential store;
2. environment variable for CI/agent execution;
3. future OAuth/device-flow login.

Recommended environment variable:

```bash
FAIRMIND_TOKEN
```

### HTTP

Recommended:

```http
Authorization: Bearer <JWT>
```

Optional metadata:

```http
X-FairMind-Project: <project_id>
X-FairMind-Client: fairmind-cli/<version>
Idempotency-Key: <uuid>
```

The exact production headers MUST be confirmed against the FairMind backend.

---

# 12. CLI login

MVP may support:

```bash
export FAIRMIND_TOKEN="..."
```

Production SHOULD support:

```bash
fairmind login
fairmind logout
fairmind auth status
```

Preferred future login:

- OAuth/OIDC device flow;
- short-lived access token;
- refresh token stored in the OS keychain;
- project-scoped token where possible.

For automation / GitHub hosted agents:

```text
GitHub secret / environment secret
            |
            v
      FAIRMIND_TOKEN
            |
            v
       fairmind CLI
```

Default token should be READ-only.

WRITE capability should be granted explicitly.

---

# 13. Authorization model

Authorization MUST remain server-side.

The CLI must never infer access from local state.

Server checks may include:

```text
JWT validity
 |
 +--> tenant/company
 +--> user identity
 +--> scope
 +--> project role
 +--> project scoping
 +--> session privacy
 +--> entity access
```

Expected HTTP errors:

```text
401 token_missing
401 token_expired
401 token_invalid

403 scope_required
403 role_required
403 session_private
403 resource_forbidden

404 project_not_found
404 task_not_found
404 repository_not_found
404 knowledge_not_found

409 invalid_state_transition
409 idempotency_conflict
```

---

# 14. Standard API envelope

Current MCP outputs are heterogeneous.

The new Agent API MUST normalize them.

## Success

```json
{
  "ok": true,
  "data": {},
  "page": null,
  "evidence": null,
  "error": null
}
```

## Error

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

---

# 15. CLI exit codes

Recommended:

```text
0 success
1 generic application error
2 authentication error
3 resource not found
4 forbidden / insufficient scope
5 validation error
6 conflict / invalid state
7 remote service unavailable
8 timeout
```

Agents must be able to distinguish retryable from non-retryable failures.

---

# 16. Standard pagination

Normalize cursor and offset-based FairMind APIs.

CLI/API output:

```json
{
  "page": {
    "cursor": "...",
    "limit": 20,
    "total": 123,
    "has_more": true
  }
}
```

Do not expose internal pagination differences to agents.

---

# 17. Context Pack contract

All aggregate context operations should return a common structure.

```json
{
  "summary": "...",
  "items": [
    {
      "source": "brain|studio|code|document|insights",
      "kind": "decision|requirement|issue|task|test|document|code",
      "id": "...",
      "title": "...",
      "status": "...",
      "review_state": "...",
      "why_relevant": "...",
      "excerpt": "...",
      "anchors": [],
      "provenance": {}
    }
  ],
  "warnings": [],
  "follow_up": [],
  "evidence": {},
  "budget": {
    "requested_tokens": 8000,
    "estimated_tokens": 7200,
    "omitted_items": 4
  }
}
```

---

# 18. Server-side context budgeting

This is a key capability.

The agent should request:

```bash
fairmind context get \
  --intent "implement password reset" \
  --budget 8000
```

FairMind should rank and trim context server-side.

The ranking algorithm is FairMind IP and MUST NOT live in the CLI.

Priority should generally consider:

1. directly linked requirements;
2. confirmed decisions;
3. task/story/need hierarchy;
4. issues anchored to affected code;
5. tests;
6. recent relevant project knowledge;
7. documents;
8. historical/superseded knowledge, clearly labelled.

No confirmed/superseded/rejected state may be silently removed when its historical relevance matters.

---

# 19. Knowledge trust model

Preserve FairMind semantics.

Agent-inferred knowledge must not silently become confirmed human truth.

Examples:

- inferred requirement -> `proposed`;
- discovered issue -> `proposed`;
- confirmed human knowledge -> update through revision/supersede semantics;
- historical decisions -> remain readable;
- superseded knowledge -> labelled, not deleted.

The bridge must not weaken this model for convenience.

---

# 20. Idempotency

All aggregate WRITE operations MUST support idempotency.

Recommended header:

```http
Idempotency-Key: <uuid>
```

Additionally derive deterministic keys for domain entities where possible.

Examples:

- issue `natural_key` from project + task + anchor + normalized title;
- decisions preserve caller decision ID where already defined;
- repeated `work start` must reuse an existing active session/association where appropriate.

---

# 21. Dry-run

All aggregate writes SHOULD support:

```bash
--dry-run
```

Example:

```bash
fairmind work complete TASK-123 \
  --journal journal.md \
  --dry-run \
  --json
```

Output must show what would happen:

```json
{
  "ok": true,
  "data": {
    "dry_run": true,
    "operations": [
      {"type": "journal", "action": "create"},
      {"type": "decision", "action": "create", "count": 2},
      {"type": "task_status", "from": "in_progress", "to": "done"}
    ]
  }
}
```

---

# 22. Repository auto-detection

When run inside a git repository, the CLI SHOULD detect:

```bash
git remote get-url origin
```

and send the remote URL or normalized repository identity to FairMind.

Do not require the agent to manually provide repository IDs when a binding exists.

Resolution order:

```text
explicit --repo
    |
local git remote
    |
FairMind repository binding
    |
configured project default
```

If multiple matches exist, return an explicit ambiguity error.

---

# 23. Local configuration

Optional local file:

```text
.fairmind/config.json
```

Example:

```json
{
  "project": "PROJECT-123",
  "repository": "backend-api"
}
```

No credential may be stored in this file.

This file may be committed if it contains only non-sensitive identifiers.

---

# 24. Suggested REST API

The exact existing backend API is still to be confirmed.

Recommended public Agent API:

```text
GET  /v1/projects

POST /v1/context:get
POST /v1/context:for-task
POST /v1/context:for-code-change
POST /v1/context:for-repository

GET  /v1/search

POST /v1/work/{taskId}:start
POST /v1/work/{taskId}:complete

POST /v1/issues

GET  /v1/brain/nodes/{id}
GET  /v1/brain/nodes/{id}/timeline
```

Internal service endpoints may remain separate.

The public Agent API should be stable even if MCP internals change.

---

# 25. Do not directly expose these capabilities to Copilot

At minimum, keep these internal/admin-only:

- destructive erase/delete operations;
- `Insights_erase_subject`;
- credential/config retrieval;
- `General_get_mcp_configs_for_agent`;
- skill creation/administrative configuration;
- low-level Insights telemetry writers;
- raw session administration;
- duplicated decision-writer APIs;
- operations exposing third-party credentials;
- sentinel/internal operations unless explicitly part of a future product requirement.

---

# 26. Low-level CLI namespace

For diagnostics and advanced human use, the CLI MAY expose lower-level commands.

Examples:

```bash
fairmind brain search
fairmind brain get
fairmind brain context
fairmind brain timeline

fairmind studio task get
fairmind studio story get
fairmind studio need get

fairmind code search
fairmind code tree
fairmind code usages

fairmind docs search
```

These are secondary.

Agent Skills should normally reference only aggregate capabilities.

---

# 27. GitHub Copilot Agent Skill

Repository structure:

```text
.github/
  skills/
    fairmind-project-context/
      SKILL.md
```

Suggested content:

```markdown
---
name: fairmind-project-context
description: >
  Use FairMind Project Context when project requirements,
  architectural decisions, previous implementation knowledge,
  cross-repository context, tests, project documents, or
  implementation history may affect the task.
allowed-tools:
  - shell
---

Before implementing a non-trivial code change, retrieve the relevant
FairMind Project Context.

For a known FairMind task:

    fairmind context for-task <ID> --json

For a general request:

    fairmind context get --intent "<task>" --json

Before modifying existing code:

    fairmind context for-code-change --diff --json

Use FairMind as the authoritative project knowledge source for
requirements, decisions, historical project knowledge and project
workflow metadata.

Use the local checkout as the authoritative source for the current
working tree.

Never print, request, inspect or expose FAIRMIND_TOKEN.

If FairMind reports missing or ambiguous context, do not invent it.
State the uncertainty and continue using the available repository
evidence when safe.
```

---

# 28. GitHub Copilot Custom Agent

Recommended path:

```text
.github/agents/fairmind.agent.md
```

Suggested content:

```markdown
---
name: fairmind
description: >
  Software engineering agent augmented by FairMind Project Context.
tools:
  - read
  - edit
  - execute
---

You are a software engineering agent connected to FairMind.

Before planning a substantial code change:

1. Retrieve the FairMind context for the task or intent.
2. Retrieve context for files that will be modified.
3. Inspect relevant confirmed decisions and requirements.
4. Inspect the current local repository.
5. Create an implementation plan.
6. Implement the requested change.
7. Run relevant tests and validations.
8. Record out-of-scope findings with `fairmind record issue`.
9. At completion, call `fairmind work complete` when a FairMind task
   is associated with the work.

FairMind is authoritative for project knowledge and history.
The current checkout is authoritative for local code state.

Never expose credentials or tokens.
Never treat proposed knowledge as confirmed.
Never silently ignore superseded or conflicting decisions.
```

---

# 29. Recommended agent workflow

```text
USER REQUEST
     |
     v
Identify task / intent
     |
     v
fairmind context get / for-task
     |
     v
Plan
     |
     v
Identify files to change
     |
     v
fairmind context for-code-change
     |
     v
Implement
     |
     v
Test / validate
     |
     +--> finding outside scope?
     |       |
     |       v
     |  fairmind record issue
     |
     v
fairmind work complete
     |
     v
RESULT
```

---

# 30. Context source precedence

Do not create a single simplistic precedence rule between repository and FairMind.

Use source-specific authority:

```text
Current file contents
    -> local checkout

Requirements / accepted project intent
    -> FairMind confirmed knowledge

Architectural decisions / historical rationale
    -> FairMind Brain

Current git changes
    -> local checkout

FairMind task workflow state
    -> FairMind Studio

Historical implementation context
    -> FairMind

Repository metadata ingested into FairMind
    -> consider staleness metadata
```

When FairMind reports stale repository data, prefer local code for current implementation state while still using FairMind for rationale/history.

---

# 31. Observed inconsistencies that the bridge must hide

The MCP introspection identified several current inconsistencies.

The Agent API MUST normalize or isolate them:

1. IDs accepted differently by different endpoints.
2. Some list-by-parent queries appear incomplete.
3. Error envelopes are inconsistent.
4. No formal output schema is declared by MCP tools.
5. RAG result-count behavior is not always consistent.
6. Duplicate APIs exist for recording decisions.
7. Some referenced tools are not actually exposed.
8. Some session ACL behavior differs across tool families.
9. At least one document-content operation shows encoding/mojibake issues.
10. Some internal/config operations may expose third-party credential headers.

These issues must not leak into the public CLI contract.

---

# 32. Security requirements

The CLI MUST:

- never log JWT values;
- redact `Authorization`;
- redact cookies and API keys;
- avoid token command-line flags;
- use TLS certificate verification;
- validate API host;
- set explicit network timeouts;
- cap response size;
- protect against accidental secret echo;
- avoid uploading repository files unless required by the requested operation;
- make any file upload explicit in command semantics;
- identify itself with a client/version header.

The backend MUST:

- validate JWT signature and expiration;
- validate audience/issuer if applicable;
- enforce tenant isolation;
- enforce scopes;
- enforce project role;
- enforce session privacy;
- audit writes;
- apply rate limiting;
- validate payloads;
- avoid trusting CLI-provided tenant or user identity.

---

# 33. Privacy / data minimization

For code-change context, prefer sending:

```text
repository identity
file paths
symbol names
diff metadata
```

instead of full source files when FairMind already has the repository indexed.

If source upload is needed, make it an explicit capability with a clear data boundary.

Do not automatically transmit the entire repository.

---

# 34. Observability

Each call SHOULD propagate:

```text
request_id
client_version
agent_type
project
repository
operation
duration
status
```

Do not log:

```text
JWT
raw secret headers
credential configuration
unredacted third-party tokens
```

Suggested client identifier:

```http
X-FairMind-Client: fairmind-cli/1.0.0
X-FairMind-Agent: github-copilot
```

---

# 35. Implementation language

Choose a language suitable for producing a single lightweight cross-platform CLI.

Recommended options:

1. Go
2. Rust
3. Node/TypeScript

Preference for MVP: **Go** if distribution simplicity is important.

Reasons:

- single binary;
- easy macOS/Linux/Windows distribution;
- no runtime dependency;
- good HTTP/JSON support;
- simple secure credential-store integration;
- easy CI packaging.

TypeScript is acceptable if the FairMind team already has stronger Node tooling and npm distribution is desirable.

The CLI must not embed proprietary algorithms regardless of language.

---

# 36. Suggested repository structure

```text
fairmind-copilot-bridge/
├── cmd/
│   └── fairmind/
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── output/
│   ├── git/
│   └── commands/
├── schemas/
│   ├── context-pack.schema.json
│   ├── error.schema.json
│   ├── work-complete.schema.json
│   └── search.schema.json
├── github/
│   ├── SKILL.md
│   └── fairmind.agent.md
├── docs/
│   ├── CLI.md
│   ├── AUTH.md
│   └── SECURITY.md
├── tests/
└── README.md
```

---

# 37. CLI global flags

Recommended:

```text
--project <id|name>
--repo <id|name>
--json
--profile <name>
--timeout <seconds>
--verbose
--dry-run
```

In agent mode, JSON output should be default or mandatory.

Human-friendly output can remain available interactively.

---

# 38. Versioning

CLI:

```bash
fairmind version
```

API:

```text
/v1/
```

JSON contracts must be versioned independently from internal FairMind/MCP contracts.

Do not expose internal contract identifiers as the public compatibility boundary unless intentional.

---

# 39. MVP scope

## Phase 1 — read-only proof of concept

Implement:

```bash
fairmind auth status
fairmind context get
fairmind context for-task
fairmind context for-code-change
fairmind search
fairmind status
```

Deliver:

- HTTPS/JWT client;
- normalized JSON;
- repository auto-detection;
- Agent Skill;
- Custom Agent;
- macOS/Linux packages;
- integration tests.

This phase proves the approach without write-risk.

---

# 40. Phase 2 — governed write loop

Add:

```bash
fairmind work start
fairmind work complete
fairmind record issue
```

Requirements:

- write-scope enforcement;
- dry-run;
- idempotency;
- audit;
- normalized status transitions;
- transaction/partial-failure strategy.

---

# 41. Phase 3 — enterprise hardening

Add:

- Windows support;
- secure login/device flow;
- token refresh;
- project profiles;
- CI/cloud-agent support;
- richer telemetry;
- retry policy;
- offline diagnostics;
- signed releases;
- SBOM;
- checksum/signature verification;
- enterprise proxy support;
- optional mTLS if required.

---

# 42. Acceptance criteria for MVP

The MVP is accepted when:

1. a developer with GitHub Copilot CLI can install `fairmind`;
2. no MCP server is configured for FairMind;
3. MCP Registry Only policy remains untouched;
4. the developer authenticates using a FairMind JWT;
5. Copilot can execute:
   `fairmind context get --intent "..." --json`;
6. Copilot can execute:
   `fairmind context for-code-change --diff --json`;
7. the CLI returns normalized JSON;
8. no JWT appears in process arguments or logs;
9. all FairMind proprietary orchestration remains server-side;
10. the Agent Skill correctly instructs Copilot when to use FairMind;
11. failure modes are machine-readable;
12. tenant/project authorization remains enforced by FairMind.

---

# 43. Initial implementation task for Claude Code / Codex

Implement **Phase 1** only.

Before coding:

1. inspect the existing FairMind backend/API repository;
2. identify the application/service layer currently used by MCP;
3. do not duplicate MCP business logic;
4. determine whether existing REST endpoints can support the aggregate API;
5. propose minimal server additions where required;
6. document anything that is still UNKNOWN.

Then implement:

```text
Agent API
    +
fairmind CLI
    +
SKILL.md
    +
fairmind.agent.md
```

Start with:

```text
POST /v1/context:get
POST /v1/context:for-task
POST /v1/context:for-code-change
GET  /v1/search
```

and CLI commands:

```text
fairmind context get
fairmind context for-task
fairmind context for-code-change
fairmind search
```

Do not implement write commands until the read path is tested end-to-end.

---

# 44. Required tests

At minimum:

## Authentication

- missing JWT;
- malformed JWT;
- expired JWT;
- read-only JWT;
- project-scoped JWT;
- wrong tenant/project access.

## Context

- general intent;
- task by ObjectId;
- task by FairMind ID;
- unknown task;
- repository not bound;
- repository bound;
- file with no FairMind knowledge;
- file with decisions;
- superseded decision;
- proposed requirement;
- stale repository data.

## CLI

- JSON is valid;
- exit codes are correct;
- timeout handling;
- server 5xx;
- network unavailable;
- no JWT leakage in stderr/stdout;
- no JWT leakage in verbose mode.

## Agent

- Copilot chooses `context for-task` for a known task;
- Copilot chooses `context for-code-change` before modifying existing code;
- Copilot does not request/print credentials;
- Copilot handles empty context safely;
- Copilot distinguishes proposed vs confirmed knowledge.

---

# 45. Open questions to resolve during implementation

The following are still intentionally unresolved and must be verified in code/infrastructure:

1. exact existing FairMind REST endpoints;
2. exact JWT transport currently used by the MCP;
3. issuer/audience details;
4. whether the JWT is itself an access token or an API-key-like JWT;
5. refresh/login mechanism currently available;
6. existing repository binding service APIs;
7. which MCP orchestration logic can be directly extracted/reused;
8. whether aggregate context endpoints already partially exist;
9. how work sessions should map to Copilot CLI sessions;
10. cloud Copilot Agent outbound-network and secret configuration at the customer.

Do not invent answers to these questions.

---

# 46. Architectural rule

**The FairMind CLI is a protocol adapter, not the product.**

Anything that differentiates FairMind must remain behind the FairMind API.

The client should remain small enough that it could be audited or even open-sourced without exposing FairMind intellectual property.

---

# 47. Target user experience

Developer:

```bash
cd my-project
copilot
```

User:

```text
Implement TASK-123.
```

Copilot:

```text
fairmind context for-task TASK-123 --json
```

Before touching existing files:

```text
fairmind context for-code-change --diff --json
```

Then Copilot implements and tests.

Future Phase 2:

```text
fairmind work complete TASK-123 --journal - --json
```

The user should experience FairMind as the **project intelligence and memory layer** behind Copilot, without knowing or caring whether the integration uses MCP.

---

# 48. Definition of success

The project succeeds if FairMind can be used from GitHub Copilot CLI and GitHub Copilot Agents in a customer environment where third-party MCP servers are unavailable, **without bypassing the customer MCP security policy and without transferring FairMind proprietary logic into the customer environment**.

