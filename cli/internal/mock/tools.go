package mock

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// mockTool mirrors the /v1/tools catalog entry.
type mockTool struct {
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace"`
	Command     string          `json:"command"`
	Description string          `json:"description"`
	Access      string          `json:"access"`
	InputSchema json.RawMessage `json:"input_schema"`
}

var mockTools = []mockTool{
	{Name: "Brain_get", Namespace: "brain", Command: "brain get", Access: "read",
		Description: "Read one piece of knowledge in full.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"knowledge_id":{"type":"string","description":"The node id."},"max_tokens":{"type":"integer","default":2000}},"required":["knowledge_id"]}`)},
	{Name: "Brain_search", Namespace: "brain", Command: "brain search", Access: "read",
		Description: "Find knowledge by meaning and by words.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"anyOf":[{"type":"string"},{"type":"null"}]},"k":{"type":"integer"},"project":{"anyOf":[{"type":"string"},{"type":"null"}]},"git_remote":{"anyOf":[{"type":"string"},{"type":"null"}]},"include_invalidated":{"type":"boolean","default":false},"kinds":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}},{"type":"null"}]}}}`)},
	{Name: "Brain_record_issue", Namespace: "brain", Command: "brain record-issue", Access: "write",
		Description: "Record an issue found while working.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"kind":{"type":"string","enum":["bug","feature","question","tech_debt"]},"project":{"type":"string"},"anchors":{"type":"array","items":{"type":"string"}}},"required":["title","kind"]}`)},
	{Name: "Studio_get_task", Namespace: "studio", Command: "studio get-task", Access: "read",
		Description: "Get detailed information for a specific task.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string"}},"required":["task_id"]}`)},
	{Name: "Studio_list_tasks_by_project", Namespace: "studio", Command: "studio list-tasks-by-project", Access: "read",
		Description: "Lists tasks for a project with pagination.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":{"type":"string"},"limit":{"type":"integer","default":20},"skip":{"type":"integer"},"fields":{"type":"string","enum":["full","summary"]}},"required":["project_id"]}`)},
	{Name: "Insights_erase_subject", Namespace: "insights", Command: "insights erase-subject", Access: "destructive",
		Description: "Erase every record about a subject.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"subject":{"type":"string"}},"required":["subject"]}`)},
}

// ToolCatalogFetches counts GET /v1/tools calls (cache tests).
func (s *Server) ToolCatalogFetches() int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == "GET" && r.Path == "/v1/tools" {
			n++
		}
	}
	return n
}

func (s *Server) listTools(r *http.Request, c *Claims) (any, any, *apiError) {
	counts := map[string]int{}
	for _, t := range mockTools {
		counts[t.Access]++
	}
	return map[string]any{"tools": mockTools, "counts": counts}, nil, nil
}

func (s *Server) toolsRoute(r *http.Request, c *Claims) (any, any, *apiError) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/tools/")
	isCall := strings.HasSuffix(name, ":call")
	name = strings.TrimSuffix(name, ":call")
	var tool *mockTool
	for i := range mockTools {
		if mockTools[i].Name == name {
			tool = &mockTools[i]
		}
	}
	if tool == nil {
		return nil, nil, fail(404, "TOOL_NOT_FOUND", "unknown tool %s", name)
	}
	if !isCall {
		return tool, nil, nil
	}
	if r.Method != "POST" {
		return nil, nil, fail(405, "METHOD_NOT_ALLOWED", "use POST")
	}
	if tool.Access != "read" {
		if !hasScope(c.Scope, "write") {
			return nil, nil, fail(403, "SCOPE_REQUIRED", "scope 'write' is required")
		}
		if r.Header.Get("X-FairMind-Write-Intent") != "confirmed" {
			return nil, nil, fail(400, "WRITE_NOT_CONFIRMED", "write intent not confirmed")
		}
	}
	if (tool.Access == "destructive" || tool.Access == "sensitive") && r.Header.Get("X-FairMind-Allow-Destructive") != "1" {
		return nil, nil, fail(403, "DESTRUCTIVE_NOT_ALLOWED", "destructive tools are disabled")
	}
	var req struct {
		Arguments map[string]any `json:"arguments"`
	}
	if e := decodeBody(r, &req); e != nil {
		return nil, nil, e
	}
	switch tool.Name {
	case "Brain_get":
		for _, k := range knowledgeBase {
			if k.ID == req.Arguments["knowledge_id"] {
				return map[string]any{"node": fromKnowledge(k, ""), "content": k.Content}, nil, nil
			}
		}
		return nil, nil, fail(404, "KNOWLEDGE_NOT_FOUND", "knowledge node %v was not found", req.Arguments["knowledge_id"])
	case "Studio_get_task":
		if e := findEntity(str(req.Arguments["task_id"])); e != nil && e.Kind == "task" {
			return map[string]any{"id": e.ObjectID, "mindstreamId": e.ID, "title": e.Title, "status": e.Status}, nil, nil
		}
		return nil, nil, fail(404, "TASK_NOT_FOUND", "task not found")
	}
	// Other tools echo their arguments so tests can assert on conversion.
	return map[string]any{"tool": tool.Name, "received": req.Arguments}, nil, nil
}

func str(v any) string { s, _ := v.(string); return s }

func (s *Server) listWork(r *http.Request, c *Claims) (any, any, *apiError) {
	q := r.URL.Query()
	p, _, e := resolveProject(c, q.Get("project"), nil)
	if e != nil {
		return nil, nil, e
	}
	kind := q.Get("kind")
	if kind == "epic" {
		kind = "need"
	}
	if kind == "" {
		kind = "task"
	}
	if kind != "task" && kind != "story" && kind != "need" {
		return nil, nil, fail(422, "VALIDATION_ERROR", "kind must be task, story or epic")
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	items := []map[string]any{}
	for _, en := range entities {
		if parent := q.Get("parent"); parent != "" {
			pe := findEntity(parent)
			if pe == nil || en.Parent != pe.ID {
				continue
			}
		}
		if en.Project != p.ID || en.Kind != kind || (q.Get("status") != "" && en.Status != q.Get("status")) {
			continue
		}
		items = append(items, map[string]any{"id": en.ID, "object_id": en.ObjectID, "kind": q.Get("kind"), "title": en.Title, "status": en.Status, "parent": en.Parent})
	}
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return map[string]any{"project": map[string]string{"id": p.ID, "name": p.Name}, "kind": kind, "items": items, "warnings": []any{}},
		map[string]any{"cursor": nil, "limit": limit, "total": total, "has_more": total > limit}, nil
}
