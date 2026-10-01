package devbridge

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeMCP answers initialize / tools/call over SSE like a Streamable HTTP server.
func fakeMCP(t *testing.T, tools map[string]func(args map[string]any) (any, bool)) (*httptest.Server, *atomic.Int32) {
	var inits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-token-123" {
			w.WriteHeader(401)
			return
		}
		var msg struct {
			ID     *int64         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &msg)
		if msg.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-1" {
			w.WriteHeader(404)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			inits.Add(1)
			w.Header().Set("Mcp-Session-Id", "sess-1")
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "Studio_list_tasks_by_project", "description": "Lists tasks.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"project_id": map[string]any{"type": "string"}}, "required": []any{"project_id"}}},
				map[string]any{"name": "Brain_record_issue", "description": "Record an issue.", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "Insights_erase_subject", "description": "Erase.", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "General_get_mcp_configs_for_agent", "description": "Configs.", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "Code_analyze_repo", "description": "Analyze.", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "Brain_search", "description": "Search.", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			name, _ := msg.Params["name"].(string)
			args, _ := msg.Params["arguments"].(map[string]any)
			fn := tools[name]
			if fn == nil {
				result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Unknown tool " + name}}}
				break
			}
			out, isErr := fn(args)
			text, _ := json.Marshal(out)
			if s, ok := out.(string); ok {
				text = []byte(s)
			}
			result = map[string]any{"isError": isErr, "content": []any{map[string]any{"type": "text", "text": string(text)}}}
		}
		resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &inits
}

func bridge(t *testing.T, mcpURL string) *httptest.Server {
	c := NewMCPClient(mcpURL)
	c.HTTP = http.DefaultClient
	srv := httptest.NewServer(NewServer(c, log.New(io.Discard, "", 0)))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, method, url, body, token string) (int, map[string]any) {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, env
}

var tools = map[string]func(map[string]any) (any, bool){
	"General_list_projects": func(map[string]any) (any, bool) {
		return []any{map[string]any{"id": "p1", "name": "Community Pulse"}}, false
	},
	"Studio_get_user_story": func(a map[string]any) (any, bool) {
		if a["user_story_id"] != "US-2026-0600" && a["user_story_id"] != "oid-story" {
			return "User story not found", true
		}
		return map[string]any{"id": "oid-story", "mindstreamId": "US-2026-0600", "projectId": "p1", "needId": "oid-need",
			"title": "Login page", "status": "ready", "text": "As an admin I want a login page"}, false
	},
	"Studio_get_task": func(map[string]any) (any, bool) { return "Task not found", true },
	"Studio_get_need": func(map[string]any) (any, bool) {
		return map[string]any{"id": "oid-need", "mindstreamId": "NEED-2026-0276", "projectId": "p1", "title": "Auth layer"}, false
	},
	"Studio_list_tests_by_userstory": func(map[string]any) (any, bool) {
		return map[string]any{"items": []any{map[string]any{"id": "t1", "mindstreamId": "TEST-1", "title": "rejects bad password"}}}, false
	},
	"Brain_search": func(a map[string]any) (any, bool) {
		if a["project"] != nil && a["project"] != "p1" {
			return map[string]any{"results": []any{}}, false // like the real server: names silently match nothing
		}
		return map[string]any{"results": []any{
			map[string]any{"node_id": "n1", "kind": "decision", "title": "Use HttpOnly cookies", "status": "taken", "review_state": "confirmed", "score": 0.9},
			map[string]any{"node_id": "n2", "kind": "decision", "title": "Store JWT in localStorage", "status": "superseded", "review_state": "confirmed", "score": 0.5},
		}}, false
	},
	"Studio_list_user_stories_by_project": func(map[string]any) (any, bool) {
		return map[string]any{"items": []any{
			map[string]any{"id": "oid-story", "mindstreamId": "US-2026-0600", "title": "Use HttpOnly cookies for the login session"},
			map[string]any{"id": "oid-2", "mindstreamId": "US-2026-0599", "title": "Organizer seed script to migrate accounts from Supabase"},
		}, "hasMore": false}, false
	},
	"Studio_list_needs_by_project": func(map[string]any) (any, bool) { return map[string]any{"items": []any{}, "hasMore": false}, false },
	"Studio_list_tasks_by_project": func(a map[string]any) (any, bool) {
		return map[string]any{"items": []any{map[string]any{"id": "t1", "mindstreamId": "TASK-2026-0001", "title": "Do it", "status": "todo"}},
			"hasMore": false, "totalCount": 1, "received_project_id": a["project_id"]}, false
	},
	"Brain_record_issue":             func(a map[string]any) (any, bool) { return map[string]any{"recorded": true}, false },
	"General_rag_retrieve_documents": func(map[string]any) (any, bool) { return []any{}, false },
	"Brain_context": func(a map[string]any) (any, bool) {
		if a["file_path"] == "src/login.tsx" {
			return map[string]any{"items": []any{map[string]any{"node_id": "n3", "kind": "requirement", "title": "Generic login errors", "status": "accepted", "review_state": "proposed"}},
				"staleness": map[string]any{"repo": map[string]any{"catalog_id": "c1", "sync_state": "stale"}}}, false
		}
		return map[string]any{"items": []any{}}, false
	},
}

func TestForTaskTraversalOverMCP(t *testing.T) {
	mcp, inits := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	status, env := get(t, "POST", b.URL+"/v1/context:for-task", `{"id":"US-2026-0600"}`, "good-token-123")
	if status != 200 || env["ok"] != true {
		t.Fatalf("status %d: %v", status, env)
	}
	data := env["data"].(map[string]any)
	ids := map[string]bool{}
	for _, it := range data["items"].([]any) {
		ids[it.(map[string]any)["id"].(string)] = true
	}
	for _, want := range []string{"US-2026-0600", "NEED-2026-0276", "TEST-1", "n1", "n2"} {
		if !ids[want] {
			t.Errorf("missing %s in %v", want, ids)
		}
	}
	if data["project"].(map[string]any)["name"] != "Community Pulse" {
		t.Errorf("project not resolved: %v", data["project"])
	}
	get(t, "POST", b.URL+"/v1/context:for-task", `{"id":"US-2026-0600"}`, "good-token-123")
	if inits.Load() != 1 {
		t.Errorf("MCP session should be reused, got %d initializations", inits.Load())
	}
}

func TestErrorsAreNormalized(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	if status, env := get(t, "POST", b.URL+"/v1/context:for-task", `{"id":"TASK-2099-0001"}`, "good-token-123"); status != 404 ||
		env["error"].(map[string]any)["code"] != "TASK_NOT_FOUND" {
		t.Fatalf("unknown task: %d %v", status, env)
	}
	if status, env := get(t, "GET", b.URL+"/v1/projects", "", "wrong-token-456"); status != 401 ||
		env["error"].(map[string]any)["code"] != "TOKEN_INVALID" {
		t.Fatalf("bad token: %d %v", status, env)
	}
	if status, _ := get(t, "GET", b.URL+"/v1/projects", "", ""); status != 401 {
		t.Fatalf("missing token must be 401, got %d", status)
	}
}

func TestCodeChangeWarnings(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	body := `{"mode":"files","repository":{"remote":"github.com/acme/web"},"changes":[{"path":"src/login.tsx","status":"modified"}]}`
	status, env := get(t, "POST", b.URL+"/v1/context:for-code-change", body, "good-token-123")
	if status != 200 {
		t.Fatalf("status %d: %v", status, env)
	}
	data := env["data"].(map[string]any)
	codes := map[string]bool{}
	for _, w := range data["warnings"].([]any) {
		codes[w.(map[string]any)["code"].(string)] = true
	}
	if !codes["STALE_REPOSITORY_DATA"] || !codes["PROPOSED_KNOWLEDGE"] {
		t.Fatalf("expected stale + proposed warnings, got %v", codes)
	}
	if status, _ := get(t, "POST", b.URL+"/v1/context:for-code-change", `{"changes":[{"path":"a"}]}`, "good-token-123"); status != 404 {
		t.Fatalf("no repository identity should be 404, got %d", status)
	}
}

func TestReadSSEPicksMatchingID(t *testing.T) {
	stream := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" +
		"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"ok\":1}}\n\n"
	got, err := readSSE(strings.NewReader(stream), 7)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d events, %v", len(got), err)
	}
}

func TestSearchIncludesStudioItemsMissingFromBrain(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	status, env := get(t, "GET", b.URL+"/v1/search?q=migrare+account+Supabase&k=10", "", "good-token-123")
	if status != 200 {
		t.Fatalf("status %d: %v", status, env)
	}
	data := env["data"].(map[string]any)
	ids := map[string]bool{}
	for _, it := range data["items"].([]any) {
		ids[it.(map[string]any)["id"].(string)] = true
	}
	if !ids["US-2026-0599"] {
		t.Fatalf("Studio story not indexed in Brain must still be found: %v", ids)
	}
	codes := map[string]bool{}
	for _, w := range data["warnings"].([]any) {
		codes[w.(map[string]any)["code"].(string)] = true
	}
	if !codes["BRAIN_INDEX_SPARSE"] {
		t.Fatalf("expected BRAIN_INDEX_SPARSE warning, got %v", codes)
	}
}

func TestToolCatalogClassification(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	status, env := get(t, "GET", b.URL+"/v1/tools", "", "good-token-123")
	if status != 200 {
		t.Fatalf("status %d: %v", status, env)
	}
	got := map[string]string{}
	for _, tl := range env["data"].(map[string]any)["tools"].([]any) {
		m := tl.(map[string]any)
		got[m["name"].(string)] = m["access"].(string) + "|" + m["command"].(string)
	}
	want := map[string]string{
		"Studio_list_tasks_by_project":      "read|studio list-tasks-by-project",
		"Brain_record_issue":                "write|brain record-issue",
		"Insights_erase_subject":            "destructive|insights erase-subject",
		"General_get_mcp_configs_for_agent": "sensitive|general get-mcp-configs-for-agent",
		"Code_analyze_repo":                 "read|code analyze-repo",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestToolCallGuardsAndProjectResolution(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	status, env := get(t, "POST", b.URL+"/v1/tools/Studio_list_tasks_by_project:call", `{"arguments":{"project_id":"Community Pulse"}}`, "good-token-123")
	if status != 200 || env["data"].(map[string]any)["received_project_id"] != "p1" {
		t.Fatalf("project name must resolve to id: %d %v", status, env)
	}
	if status, _ := get(t, "POST", b.URL+"/v1/tools/Brain_record_issue:call", `{"arguments":{}}`, "good-token-123"); status != 400 {
		t.Fatalf("unconfirmed write must be refused, got %d", status)
	}
	req, _ := http.NewRequest("POST", b.URL+"/v1/tools/Brain_record_issue:call", strings.NewReader(`{"arguments":{}}`))
	req.Header.Set("Authorization", "Bearer good-token-123")
	req.Header.Set("X-FairMind-Write-Intent", "confirmed")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("confirmed write should pass: %v %v", err, resp)
	}
	resp.Body.Close()
	req, _ = http.NewRequest("POST", b.URL+"/v1/tools/Insights_erase_subject:call", strings.NewReader(`{"arguments":{}}`))
	req.Header.Set("Authorization", "Bearer good-token-123")
	req.Header.Set("X-FairMind-Write-Intent", "confirmed")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("destructive tool without opt-in must be 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if status, _ := get(t, "POST", b.URL+"/v1/tools/Nope:call", `{}`, "good-token-123"); status != 404 {
		t.Fatalf("unknown tool must be 404, got %d", status)
	}
}

func TestWorkListOverMCP(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	status, env := get(t, "GET", b.URL+"/v1/work?kind=task", "", "good-token-123")
	if status != 200 {
		t.Fatalf("status %d: %v", status, env)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "TASK-2026-0001" {
		t.Fatalf("unexpected items: %v", items)
	}
}

func TestCommandFor(t *testing.T) {
	cases := map[string]string{
		"Studio_list_tasks_by_project":   "studio list-tasks-by-project",
		"General_rag_retrieve_documents": "general rag-retrieve-documents",
		"Brain_get":                      "brain get",
	}
	for in, want := range cases {
		if _, got := CommandFor(in); got != want {
			t.Errorf("CommandFor(%s) = %s want %s", in, got, want)
		}
	}
}

func TestProjectNameNormalizedForNameOrIDParams(t *testing.T) {
	mcp, _ := fakeMCP(t, tools)
	b := bridge(t, mcp.URL)
	req, _ := http.NewRequest("POST", b.URL+"/v1/tools/Brain_search:call", strings.NewReader(`{"arguments":{"project":"Community Pulse","query":"x"}}`))
	req.Header.Set("Authorization", "Bearer good-token-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if res := env["data"].(map[string]any)["results"].([]any); len(res) == 0 {
		t.Fatalf("project name must be converted to its id before calling Brain_search: %v", env)
	}
}
