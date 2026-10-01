package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/mock"
)

func lastToolCall(t *testing.T, h *harness) (mock.Request, map[string]any) {
	t.Helper()
	reqs := h.mock.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if strings.HasSuffix(reqs[i].Path, ":call") {
			var body struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal([]byte(reqs[i].Body), &body)
			return reqs[i], body.Arguments
		}
	}
	t.Fatal("no tool call recorded")
	return mock.Request{}, nil
}

func countCalls(h *harness) int {
	n := 0
	for _, r := range h.mock.Requests() {
		if strings.HasSuffix(r.Path, ":call") {
			n++
		}
	}
	return n
}

func TestToolsListAndCatalogCache(t *testing.T) {
	h := newHarness(t)
	r := h.run("tools", "list")
	expect(t, r, 0, "")
	if r.data()["count"].(float64) != 6 {
		t.Fatalf("expected 6 tools: %s", r.stdout)
	}
	counts := r.data()["counts"].(map[string]any)
	if counts["write"].(float64) != 1 || counts["destructive"].(float64) != 1 {
		t.Fatalf("unexpected access counts: %v", counts)
	}
	expect(t, h.run("tools", "list", "--namespace", "studio"), 0, "")
	expect(t, h.run("studio", "get-task", "TASK-123"), 0, "")
	if n := h.mock.ToolCatalogFetches(); n != 1 {
		t.Fatalf("catalog should be fetched once and cached, got %d fetches", n)
	}
	expect(t, h.run("tools", "list", "--refresh"), 0, "")
	if n := h.mock.ToolCatalogFetches(); n != 2 {
		t.Fatalf("--refresh must refetch, got %d", n)
	}
}

func TestDynamicCommandTypedFlagsAndProjectInjection(t *testing.T) {
	h := newHarness(t)
	r := h.run("studio", "list-tasks-by-project", "--limit", "5", "--fields", "summary", "--project", "demo-project")
	expect(t, r, 0, "")
	_, args := lastToolCall(t, h)
	if args["project_id"] != "demo-project" || args["limit"].(float64) != 5 || args["fields"] != "summary" {
		t.Fatalf("unexpected arguments: %v", args)
	}
	expect(t, h.run("studio", "list-tasks-by-project", "--limit", "five", "--project", "x"), 5, "VALIDATION_ERROR")
	expect(t, h.run("studio", "list-tasks-by-project", "--fields", "all", "--project", "x"), 5, "VALIDATION_ERROR")
}

func TestDynamicCommandFromFolderConfig(t *testing.T) {
	h := newHarness(t)
	h.initRepo("https://github.com/acme/backend-api.git", authFiles)
	writeFile(t, h.dir+"/.fairmind/config.json", `{"project":"demo-project"}`)
	expect(t, h.run("brain", "search", "--query", "reset", "--kinds", "decision"), 0, "")
	_, args := lastToolCall(t, h)
	if k, _ := args["kinds"].([]any); args["project"] != "demo-project" || args["git_remote"] != "https://github.com/acme/backend-api.git" || len(k) != 1 {
		t.Fatalf("context injection failed: %v", args)
	}
}

func TestPositionalRequiredAndMissing(t *testing.T) {
	h := newHarness(t)
	r := h.run("studio", "get-task", "TASK-123")
	expect(t, r, 0, "")
	if r.data()["mindstreamId"] != "TASK-123" {
		t.Fatalf("unexpected: %s", r.stdout)
	}
	expect(t, h.run("studio", "get-task"), 5, "VALIDATION_ERROR")
	expect(t, h.run("studio", "get-task", "a", "b"), 5, "VALIDATION_ERROR")
	expect(t, h.run("studio", "get-task", "TASK-404"), 3, "TASK_NOT_FOUND")
	expect(t, h.run("brain", "get", "DEC-7", "--max-tokens", "100"), 0, "")
}

func TestUnknownDynamicCommand(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("studio", "frobnicate"), 5, "UNKNOWN_COMMAND")
	expect(t, h.run("studio", "get-task", "--nope", "x"), 5, "VALIDATION_ERROR")
}

func TestWriteToolRequiresYes(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "read write"})
	args := []string{"brain", "record-issue", "--title", "Sessions survive reset", "--kind", "bug", "--anchors", "src/a.ts,src/b.ts", "--project", "demo-project"}

	expect(t, h.run(args...), 5, "WRITE_NOT_CONFIRMED")
	if countCalls(h) != 0 {
		t.Fatal("no request may be sent without --yes")
	}
	dry := h.run(append(args, "--dry-run")...)
	expect(t, dry, 0, "")
	if dry.data()["dry_run"] != true || countCalls(h) != 0 {
		t.Fatalf("dry-run must not call FairMind: %s", dry.stdout)
	}
	expect(t, h.run(append(args, "--yes")...), 0, "")
	req, sent := lastToolCall(t, h)
	if req.Header.Get("X-FairMind-Write-Intent") != "confirmed" || req.Header.Get("Idempotency-Key") == "" {
		t.Fatalf("write headers missing: %v", req.Header)
	}
	if anchors, _ := sent["anchors"].([]any); len(anchors) != 2 || sent["kind"] != "bug" {
		t.Fatalf("unexpected arguments: %v", sent)
	}
	expect(t, h.run("brain", "record-issue", "--title", "x", "--kind", "oops", "--yes"), 5, "VALIDATION_ERROR")
}

func TestReadOnlyTokenCannotWrite(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("brain", "record-issue", "--title", "x", "--kind", "bug", "--project", "demo-project", "--yes"), 4, "SCOPE_REQUIRED")
}

func TestDestructiveToolNeedsHumanOptIn(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "read write admin"})
	expect(t, h.run("insights", "erase-subject", "user-1", "--yes"), 4, "DESTRUCTIVE_NOT_ALLOWED")
	if countCalls(h) != 0 {
		t.Fatal("destructive tool must not reach the server without opt-in")
	}
	h.env["FAIRMIND_ALLOW_DESTRUCTIVE"] = "1"
	expect(t, h.run("insights", "erase-subject", "user-1", "--yes"), 0, "")
	req, _ := lastToolCall(t, h)
	if req.Header.Get("X-FairMind-Allow-Destructive") != "1" {
		t.Fatal("destructive header missing")
	}
}

func TestToolsCallAndDescribe(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("tools", "call", "Studio_list_tasks_by_project", "--arg", "project_id=demo-project", "--arg", "limit=3"), 0, "")
	_, args := lastToolCall(t, h)
	if args["limit"].(float64) != 3 {
		t.Fatalf("--arg must use schema types: %v", args)
	}
	expect(t, h.run("tools", "call", "Studio_list_tasks_by_project", "--args-json", `{"project_id":"demo-project","skip":2}`), 0, "")
	expect(t, h.run("tools", "call", "Nope_tool"), 3, "TOOL_NOT_FOUND")
	expect(t, h.run("tools", "call", "Studio_get_task", "--arg", "bogus=1"), 5, "VALIDATION_ERROR")

	d := h.run("tools", "describe", "studio", "list-tasks-by-project")
	expect(t, d, 0, "")
	ps := d.data()["parameters"].([]any)
	if first := ps[0].(map[string]any); first["name"] != "project_id" || first["required"] != true {
		t.Fatalf("required parameters must come first: %v", ps)
	}
}

func TestToolsDocsMarkdown(t *testing.T) {
	h := newHarness(t)
	out := h.dir + "/tools.md"
	expect(t, h.run("tools", "docs", "--output", out), 0, "")
	b := readFile(t, out)
	for _, want := range []string{"# FairMind tools reference", "### `fairmind studio list-tasks-by-project` — read", "| `--project-id` | string | yes |", "destructive"} {
		if !strings.Contains(b, want) {
			t.Errorf("docs missing %q", want)
		}
	}
}

func TestWorkList(t *testing.T) {
	h := newHarness(t)
	r := h.run("work", "list", "--project", "demo-project")
	expect(t, r, 0, "")
	items := r.data()["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected the 2 demo tasks, got %d: %s", len(items), r.stdout)
	}
	s := h.run("work", "list", "--kind", "story", "--project", "demo-project", "--status", "todo")
	expect(t, s, 0, "")
	if got := s.data()["items"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != "US-46" {
		t.Fatalf("status filter failed: %s", s.stdout)
	}
	expect(t, h.run("work", "list", "--kind", "bug"), 5, "VALIDATION_ERROR")
}

func TestNamespaceHelp(t *testing.T) {
	h := newHarness(t)
	var out strings.Builder
	r := h.runRaw("studio", "--help")
	out.WriteString(r)
	for _, want := range []string{"list-tasks-by-project", "get-task"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("namespace help missing %s:\n%s", want, out.String())
		}
	}
	if s := h.runRaw("brain", "record-issue", "--help"); !strings.Contains(s, "requires --yes") || !strings.Contains(s, "--kind <bug|feature|question|tech_debt>") {
		t.Fatalf("tool help incomplete:\n%s", s)
	}
}

func TestAtFileValues(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "read write"})
	writeFile(t, h.dir+"/body.md", "Found while testing\nline 2")
	expect(t, h.run("brain", "record-issue", "--title", "@@literal", "--kind", "bug", "--body", "@body.md", "--project", "demo-project", "--yes"), 0, "")
	_, args := lastToolCall(t, h)
	if args["body"] != "Found while testing\nline 2" || args["title"] != "@literal" {
		t.Fatalf("@file expansion failed: %v", args)
	}
	h.stdin = "from stdin"
	expect(t, h.run("brain", "record-issue", "--title", "x", "--kind", "bug", "--body", "@-", "--project", "demo-project", "--yes"), 0, "")
	if _, args := lastToolCall(t, h); args["body"] != "from stdin" {
		t.Fatalf("@- expansion failed: %v", args)
	}
	expect(t, h.run("brain", "record-issue", "--title", "x", "--kind", "bug", "--body", "@missing.md", "--yes"), 5, "VALIDATION_ERROR")
}

func TestWorkListParentAndFolderConfigOutsideGit(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dir+"/.fairmind/config.json", `{"project":"demo-project"}`)
	r := h.run("work", "list", "--kind", "story", "--parent", "NEED-7")
	expect(t, r, 0, "")
	if got := r.data()["items"].([]any); len(got) != 2 {
		t.Fatalf("expected the 2 stories of NEED-7 (project from folder config, no git): %s", r.stdout)
	}
}

func TestStringOrArrayParamIsSentAsArray(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("brain", "search", "--query", "x", "--kinds", "decision,issue", "--project", "demo-project"), 0, "")
	_, args := lastToolCall(t, h)
	if k, ok := args["kinds"].([]any); !ok || len(k) != 2 {
		t.Fatalf("kinds must be an array: %v", args["kinds"])
	}
}
