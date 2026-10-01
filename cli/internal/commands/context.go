package commands

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/git"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

const (
	minBudget = 256
	maxBudget = 200000
)

func init() {
	register(command{path: "context get", handler: contextGet, usage: usageContextGet,
		spec: flagSpec{"intent": kindString, "task": kindString, "path": kindList, "budget": kindString}})
	register(command{path: "context for-task", handler: contextForTask, usage: usageContextForTask,
		spec: flagSpec{"budget": kindString}})
	register(command{path: "context for-code-change", handler: contextForCodeChange, usage: usageContextForCodeChange,
		spec: flagSpec{"diff": kindBool, "files": kindList, "symbols": kindBool, "base": kindString, "budget": kindString}})
	register(command{path: "search", handler: search, usage: usageSearch,
		spec: flagSpec{"in": kindList, "k": kindString}})
}

type contextGetRequest struct {
	Intent       string         `json:"intent"`
	Project      string         `json:"project,omitempty"`
	Task         string         `json:"task,omitempty"`
	Paths        []string       `json:"paths,omitempty"`
	BudgetTokens int            `json:"budget_tokens,omitempty"`
	Repository   *repositoryRef `json:"repository,omitempty"`
}

func contextGet(r *run) (*output.Envelope, string, error) {
	intent := strings.TrimSpace(r.args.str("intent"))
	if intent == "" && len(r.args.pos) > 0 {
		intent = strings.TrimSpace(strings.Join(r.args.pos, " "))
	}
	if intent == "" {
		return nil, "", output.Validationf("--intent is required")
	}
	if len(intent) > 4000 {
		return nil, "", output.Validationf("--intent must be at most 4000 characters")
	}
	budget, err := r.args.intFlag("budget", 0, minBudget, maxBudget)
	if err != nil {
		return nil, "", err
	}
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	paths, err := r.repoPaths(r.args.list("path"))
	if err != nil {
		return nil, "", err
	}
	env, err := r.call("POST", "/v1/context:get", nil, contextGetRequest{
		Intent: intent, Project: r.settings.Project, Task: r.args.str("task"),
		Paths: paths, BudgetTokens: budget, Repository: r.repositoryRef(),
	})
	return env, "context", err
}

type contextForTaskRequest struct {
	ID           string         `json:"id"`
	Project      string         `json:"project,omitempty"`
	BudgetTokens int            `json:"budget_tokens,omitempty"`
	Repository   *repositoryRef `json:"repository,omitempty"`
}

func contextForTask(r *run) (*output.Envelope, string, error) {
	if len(r.args.pos) != 1 {
		return nil, "", output.Validationf("expected exactly one TASK/STORY/NEED id")
	}
	id := strings.TrimSpace(r.args.pos[0])
	if !idPattern.MatchString(id) {
		return nil, "", output.Validationf("invalid id %q: expected an ObjectId or a FairMind id such as TASK-123", id)
	}
	budget, err := r.args.intFlag("budget", 0, minBudget, maxBudget)
	if err != nil {
		return nil, "", err
	}
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	// The raw id is sent as-is: ObjectId vs TASK-/US-/NEED- normalization is server-side (spec §7.2).
	env, err := r.call("POST", "/v1/context:for-task", nil, contextForTaskRequest{
		ID: id, Project: r.settings.Project, BudgetTokens: budget, Repository: r.repositoryRef(),
	})
	return env, "context", err
}

type codeChangeRequest struct {
	Mode         string           `json:"mode"` // diff | files
	Project      string           `json:"project,omitempty"`
	Repository   *repositoryRef   `json:"repository,omitempty"`
	Changes      []git.FileChange `json:"changes"`
	BudgetTokens int              `json:"budget_tokens,omitempty"`
}

func contextForCodeChange(r *run) (*output.Envelope, string, error) {
	useDiff := r.args.bools["diff"]
	files := r.args.list("files")
	if !useDiff && len(files) == 0 {
		return nil, "", output.Validationf("specify --diff and/or --files <path>...")
	}
	if len(r.args.pos) > 0 {
		return nil, "", output.Validationf("unexpected argument %q (use --files to list paths)", r.args.pos[0])
	}
	if r.args.str("base") != "" && !useDiff {
		return nil, "", output.Validationf("--base requires --diff")
	}
	budget, err := r.args.intFlag("budget", 0, minBudget, maxBudget)
	if err != nil {
		return nil, "", err
	}
	if err := r.loadContext(useDiff); err != nil {
		return nil, "", err
	}
	selected, err := r.repoPaths(files)
	if err != nil {
		return nil, "", err
	}

	req := codeChangeRequest{Mode: "files", Project: r.settings.Project, Repository: r.repositoryRef(), BudgetTokens: budget}
	if useDiff {
		req.Mode = "diff"
		changes, err := git.DiffMetadata(r.app.Git, r.repo.Root, git.DiffOptions{Base: r.args.str("base"), Symbols: r.args.bools["symbols"]})
		if err != nil {
			return nil, "", output.NewError(output.CodeGit, err.Error(), nil)
		}
		if len(selected) > 0 {
			changes = filterChanges(changes, selected)
		}
		req.Changes = changes
	} else {
		if r.args.bools["symbols"] {
			r.logf("--symbols only applies together with --diff")
		}
		for _, p := range selected {
			req.Changes = append(req.Changes, git.FileChange{Path: p, Status: "unspecified"})
		}
	}
	if len(req.Changes) == 0 {
		// Nothing changed locally: answer without a network round trip.
		env, _ := output.Success(output.ContextPack{
			Summary:  "No local changes detected; no code-change context to retrieve.",
			Items:    []output.ContextItem{},
			Warnings: []output.Warning{{Code: "NO_LOCAL_CHANGES", Message: "the working tree has no changes relative to the base ref"}},
			FollowUp: []output.FollowUp{{Command: "fairmind context for-code-change --files <path>... --json", Reason: "request context for files you are about to modify"}},
		})
		return env, "context", nil
	}
	if len(req.Changes) > 500 {
		return nil, "", output.Validationf("too many changed files (%d); narrow the request with --files", len(req.Changes))
	}
	env, err := r.call("POST", "/v1/context:for-code-change", nil, req)
	return env, "context", err
}

func filterChanges(changes []git.FileChange, keep []string) []git.FileChange {
	want := map[string]bool{}
	for _, k := range keep {
		want[k] = true
	}
	var out []git.FileChange
	for _, c := range changes {
		if want[c.Path] || (c.OldPath != "" && want[c.OldPath]) {
			out = append(out, c)
		}
	}
	return out
}

// repoPaths normalizes user paths relative to the repository root when inside
// a repository; outside one they are sent as given (slash-separated).
func (r *run) repoPaths(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if r.repo == nil {
			out = append(out, strings.ReplaceAll(p, "\\", "/"))
			continue
		}
		rel, err := git.RelToRoot(r.repo.Root, r.app.Dir, p)
		if err != nil {
			return nil, output.Validationf("%v", err)
		}
		out = append(out, rel)
	}
	return out, nil
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

var searchSources = map[string]bool{"brain": true, "docs": true, "code": true, "studio": true}

func search(r *run) (*output.Envelope, string, error) {
	q := strings.TrimSpace(strings.Join(r.args.pos, " "))
	if q == "" {
		return nil, "", output.Validationf("a search query is required")
	}
	if len(q) > 1000 {
		return nil, "", output.Validationf("query must be at most 1000 characters")
	}
	for _, s := range r.args.list("in") {
		if !searchSources[s] {
			return nil, "", output.Validationf("--in accepts brain,docs,code,studio (got %q)", s)
		}
	}
	k, err := r.args.intFlag("k", 10, 1, 100)
	if err != nil {
		return nil, "", err
	}
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	query := url.Values{"q": {q}, "k": {strconv.Itoa(k)}}
	if in := r.args.list("in"); len(in) > 0 {
		query.Set("in", strings.Join(in, ","))
	}
	if r.settings.Project != "" {
		query.Set("project", r.settings.Project)
	}
	if ref := r.repositoryRef(); ref != nil {
		if ref.Explicit != "" {
			query.Set("repository", ref.Explicit)
		} else if ref.Remote != "" {
			query.Set("repository_remote", ref.Remote)
		}
	}
	env, err := r.call("GET", "/v1/search", query, nil)
	return env, "search", err
}
