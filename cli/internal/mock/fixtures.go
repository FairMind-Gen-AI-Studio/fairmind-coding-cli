// Package mock is an in-memory stand-in for the FairMind Agent API, built from
// the public contract in the spec. It exists only for CLI development and
// tests; it contains no FairMind logic and its naive keyword scoring is NOT a
// model of the real server-side ranking.
package mock

// Tenant is the only tenant with data in the mock.
const Tenant = "acme"

type project struct {
	ID, Name string
}

var projects = []project{
	{ID: "665f00000000000000000001", Name: "demo-project"},
	{ID: "665f00000000000000000002", Name: "billing"},
}

// entity is a Studio work item (task / user story / need).
type entity struct {
	ID, ObjectID, Kind, Title, Project, Parent, Status string
}

var entities = []entity{
	{ID: "NEED-7", ObjectID: "665f1c2e9b1d4a0012345501", Kind: "need", Title: "Users can recover access to their account", Project: projects[0].ID, Status: "approved"},
	{ID: "US-45", ObjectID: "665f1c2e9b1d4a0012345545", Kind: "story", Title: "As a user I can reset my password by email", Project: projects[0].ID, Parent: "NEED-7", Status: "in_progress"},
	{ID: "US-46", ObjectID: "665f1c2e9b1d4a0012345546", Kind: "story", Title: "As a user my account is protected from brute force", Project: projects[0].ID, Parent: "NEED-7", Status: "todo"},
	{ID: "TASK-123", ObjectID: "665f1c2e9b1d4a0012345601", Kind: "task", Title: "Implement password reset endpoint", Project: projects[0].ID, Parent: "US-45", Status: "todo"},
	{ID: "TASK-124", ObjectID: "665f1c2e9b1d4a0012345602", Kind: "task", Title: "Add rate limiting to login", Project: projects[0].ID, Parent: "US-46", Status: "todo"},
	{ID: "TASK-900", ObjectID: "665f1c2e9b1d4a0012345900", Kind: "task", Title: "Issue invoices as immutable PDF", Project: projects[1].ID, Parent: "", Status: "todo"},
}

// knowledge is any context item the mock can return.
type knowledge struct {
	Source, Kind, ID, Title, Status, ReviewState, Excerpt, Content string
	Project                                                        string
	Links                                                          []string // related Studio entity ids
	Anchors                                                        []string // repository-relative paths
	SupersededBy                                                   string
	Priority                                                       int // lower = more important (mock only)
}

var knowledgeBase = []knowledge{
	{Source: "brain", Kind: "requirement", ID: "REQ-12", Project: projects[0].ID, Priority: 1,
		Title: "Password reset tokens expire after 30 minutes", Status: "active", ReviewState: "confirmed",
		Excerpt: "A password reset token MUST expire 30 minutes after issue and MUST be single-use.",
		Content: "Functional requirement approved by the product owner. Expiry is measured server-side from issue time. Reuse of a consumed token must return 410.",
		Links:   []string{"US-45"}, Anchors: []string{"src/auth/reset.ts"}},
	{Source: "brain", Kind: "requirement", ID: "REQ-13", Project: projects[0].ID, Priority: 1,
		Title: "Reset requests must not reveal whether an account exists", Status: "active", ReviewState: "proposed",
		Excerpt: "Inferred by an agent from the security guidelines; awaiting human review.",
		Content: "Proposed requirement: POST /password-reset always answers 202 regardless of whether the email exists.",
		Links:   []string{"US-45"}, Anchors: []string{"src/auth/reset.ts"}},
	{Source: "brain", Kind: "decision", ID: "DEC-7", Project: projects[0].ID, Priority: 2,
		Title: "Reset tokens are signed, single-use and stored hashed", Status: "taken", ReviewState: "confirmed",
		Excerpt: "Tokens are HMAC-signed; only a SHA-256 hash is persisted. Supersedes DEC-3.",
		Content: "Context: plaintext tokens in Redis were a leak risk (see DEC-3). Decision: sign tokens, store only hashes, delete on use.",
		Links:   []string{"NEED-7"}, Anchors: []string{"src/auth/reset.ts", "src/auth/tokens.ts"}},
	{Source: "brain", Kind: "decision", ID: "DEC-3", Project: projects[0].ID, Priority: 8,
		Title: "Store reset tokens in plaintext in Redis", Status: "superseded", ReviewState: "confirmed", SupersededBy: "DEC-7",
		Excerpt: "Historical decision, superseded by DEC-7. Kept for rationale; do not follow.",
		Content: "Original 2025 decision to keep tokens in Redis with a TTL.",
		Links:   []string{"NEED-7"}, Anchors: []string{"src/auth/tokens.ts"}},
	{Source: "brain", Kind: "decision", ID: "DEC-9", Project: projects[0].ID, Priority: 2,
		Title: "Rate limit login to 5 attempts per minute per IP", Status: "taken", ReviewState: "confirmed",
		Excerpt: "Sliding window limiter at the API gateway, with a per-account lockout after 20 failures.",
		Links:   []string{"US-46"}, Anchors: []string{"src/auth/login.ts"}},
	{Source: "brain", Kind: "issue", ID: "ISS-21", Project: projects[0].ID, Priority: 4,
		Title: "Sessions are not invalidated after a password change", Status: "open", ReviewState: "proposed",
		Excerpt: "Discovered during review of US-45: existing sessions survive a reset.",
		Links:   []string{"US-45"}, Anchors: []string{"src/auth/session.ts", "src/auth/reset.ts"}},
	{Source: "studio", Kind: "test", ID: "TEST-88", Project: projects[0].ID, Priority: 5,
		Title: "Reset token is rejected after expiry", Status: "defined", ReviewState: "confirmed",
		Excerpt: "Given a token issued 31 minutes ago, when it is used, then the API answers 410.", Links: []string{"US-45"}},
	{Source: "studio", Kind: "test", ID: "TEST-89", Project: projects[0].ID, Priority: 5,
		Title: "Reset request for unknown email returns 202", Status: "defined", ReviewState: "confirmed",
		Excerpt: "Given an unknown email, when a reset is requested, then the API answers 202 with no body.", Links: []string{"US-45"}},
	{Source: "document", Kind: "specification", ID: "DOC-4", Project: projects[0].ID, Priority: 7,
		Title: "Security guidelines v2", Status: "published", ReviewState: "confirmed",
		Excerpt: "Credentials, tokens and reset links must never be logged. Rotate signing keys every 90 days.", Links: []string{"NEED-7"}},
	{Source: "code", Kind: "code", ID: "CODE-reset", Project: projects[0].ID, Priority: 6,
		Title: "src/auth/reset.ts", Status: "indexed",
		Excerpt: "Password reset controller: requestReset, confirmReset.", Anchors: []string{"src/auth/reset.ts"}},
	{Source: "brain", Kind: "decision", ID: "DEC-50", Project: projects[1].ID, Priority: 2,
		Title: "Invoices are immutable after issue", Status: "taken", ReviewState: "confirmed",
		Excerpt: "Corrections are issued as credit notes.", Links: []string{"TASK-900"}, Anchors: []string{"src/invoices/issue.ts"}},
}

// binding links a normalized git remote to projects.
type binding struct {
	Remote          string
	Projects        []string
	Stale           bool
	IngestedCommit  string
	IngestedAt      string
	RepositoryID    string
	RepositoryLabel string
}

var bindings = []binding{
	{Remote: "github.com/acme/backend-api", Projects: []string{projects[0].ID}, RepositoryID: "repo-backend-api", RepositoryLabel: "backend-api",
		IngestedCommit: "", IngestedAt: "2026-09-20T10:00:00Z"},
	{Remote: "github.com/acme/legacy-app", Projects: []string{projects[0].ID}, RepositoryID: "repo-legacy-app", RepositoryLabel: "legacy-app",
		Stale: true, IngestedCommit: "0badc0de", IngestedAt: "2026-03-01T08:00:00Z"},
	{Remote: "github.com/acme/shared-lib", Projects: []string{projects[0].ID, projects[1].ID}, RepositoryID: "repo-shared-lib", RepositoryLabel: "shared-lib",
		IngestedAt: "2026-09-01T08:00:00Z"},
}
