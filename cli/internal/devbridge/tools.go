package devbridge

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tool access classes exposed to the CLI.
const (
	AccessRead        = "read"
	AccessWrite       = "write"
	AccessDestructive = "destructive"
	AccessSensitive   = "sensitive" // may return credentials or secret configuration
)

// ToolInfo is one FairMind capability as published at GET /v1/tools.
type ToolInfo struct {
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace"`
	Command     string          `json:"command"`
	Description string          `json:"description"`
	Access      string          `json:"access"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations struct {
		ReadOnlyHint    *bool `json:"readOnlyHint"`
		DestructiveHint *bool `json:"destructiveHint"`
	} `json:"annotations"`
}

// ListTools returns the MCP server's tools (all pages).
func (c *MCPClient) ListTools(ctx context.Context, auth string) ([]mcpTool, error) {
	var out []mcpTool
	cursor := ""
	for page := 0; page < 20; page++ {
		s, err := c.session(ctx, auth, false)
		if err != nil {
			return nil, err
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.rpc(ctx, auth, s, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []mcpTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return out, nil
}

var (
	readVerbs = map[string]bool{"get": true, "list": true, "search": true, "context": true, "expand": true, "timeline": true,
		"cat": true, "grep": true, "tree": true, "find": true, "rag": true, "analyze": true}
	sensitiveTools = map[string]bool{"General_get_mcp_configs_for_agent": true}
	camelBoundary  = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// Classify derives the access class from MCP annotations, falling back to the
// tool-name verb. Unknown verbs are treated as writes (fail safe).
func Classify(t mcpTool) string {
	lower := strings.ToLower(t.Name)
	if sensitiveTools[t.Name] {
		return AccessSensitive
	}
	if (t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint && !(t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint)) ||
		strings.Contains(lower, "erase") || strings.Contains(lower, "delete") {
		return AccessDestructive
	}
	if t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint {
		return AccessRead
	}
	_, rest := splitName(t.Name)
	verb := strings.SplitN(rest, "_", 2)[0]
	if readVerbs[strings.ToLower(verb)] {
		return AccessRead
	}
	return AccessWrite
}

func splitName(name string) (ns, rest string) {
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i], name[i+1:]
	}
	return "general", name
}

// CommandFor maps "Studio_list_tasks_by_project" to "studio list-tasks-by-project".
func CommandFor(name string) (namespace, command string) {
	ns, rest := splitName(name)
	ns = strings.ToLower(ns)
	sub := camelBoundary.ReplaceAllString(rest, "${1}_${2}")
	sub = strings.ToLower(strings.ReplaceAll(sub, "_", "-"))
	return ns, ns + " " + sub
}

type catalogEntry struct {
	tools   []ToolInfo
	fetched time.Time
}

type toolCatalog struct {
	mu      sync.Mutex
	entries map[string]catalogEntry
}

var catalogs = &toolCatalog{entries: map[string]catalogEntry{}}

func (c *call) catalog(refresh bool) ([]ToolInfo, error) {
	key := authKey(c.auth)
	catalogs.mu.Lock()
	e, ok := catalogs.entries[key]
	catalogs.mu.Unlock()
	if ok && !refresh && time.Since(e.fetched) < sessionTTL {
		return e.tools, nil
	}
	raw, err := c.s.MCP.ListTools(c.ctx, c.auth)
	if err != nil {
		return nil, err
	}
	var tools []ToolInfo
	for _, t := range raw {
		ns, cmd := CommandFor(t.Name)
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, ToolInfo{Name: t.Name, Namespace: ns, Command: cmd,
			Description: strings.TrimSpace(t.Description), Access: Classify(t), InputSchema: schema})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	catalogs.mu.Lock()
	catalogs.entries[key] = catalogEntry{tools: tools, fetched: time.Now()}
	catalogs.mu.Unlock()
	return tools, nil
}

func (s *Server) listTools(c *call) (any, any, error) {
	tools, err := c.catalog(c.r.URL.Query().Get("refresh") == "true")
	if err != nil {
		return nil, nil, err
	}
	counts := map[string]int{}
	for _, t := range tools {
		counts[t.Access]++
	}
	return map[string]any{"tools": tools, "counts": counts},
		map[string]any{"cursor": nil, "limit": len(tools), "total": len(tools), "has_more": false}, nil
}

// toolsRoute serves GET /v1/tools/{name} and POST /v1/tools/{name}:call.
func (s *Server) toolsRoute(c *call) (any, any, error) {
	rest := strings.TrimPrefix(c.r.URL.Path, "/v1/tools/")
	name, isCall := cutSuffix(rest, ":call")
	if name == "" || strings.Contains(name, "/") {
		return nil, nil, fail(404, "TOOL_NOT_FOUND", "unknown tool path")
	}
	tools, err := c.catalog(false)
	if err != nil {
		return nil, nil, err
	}
	var tool *ToolInfo
	for i := range tools {
		if tools[i].Name == name {
			tool = &tools[i]
		}
	}
	if tool == nil {
		return nil, nil, fail(404, "TOOL_NOT_FOUND", "FairMind tool %q does not exist or is not available to this token", name)
	}
	switch {
	case !isCall && c.r.Method == "GET":
		return tool, nil, nil
	case isCall && c.r.Method == "POST":
		return s.callTool(c, tool)
	}
	return nil, nil, fail(405, "METHOD_NOT_ALLOWED", "use GET /v1/tools/{name} or POST /v1/tools/{name}:call")
}

func (s *Server) callTool(c *call, tool *ToolInfo) (any, any, error) {
	var req struct {
		Arguments map[string]any `json:"arguments"`
		DryRun    bool           `json:"dry_run"`
	}
	if err := decode(c, &req); err != nil {
		return nil, nil, err
	}
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	if tool.Access != AccessRead && c.r.Header.Get("X-FairMind-Write-Intent") != "confirmed" {
		return nil, nil, fail(400, "WRITE_NOT_CONFIRMED", "%s modifies FairMind (%s); confirm the write intent (CLI: --yes)", tool.Name, tool.Access)
	}
	if (tool.Access == AccessDestructive || tool.Access == AccessSensitive) && c.r.Header.Get("X-FairMind-Allow-Destructive") != "1" {
		return nil, nil, fail(403, "DESTRUCTIVE_NOT_ALLOWED", "%s is %s and is disabled unless explicitly allowed (CLI: FAIRMIND_ALLOW_DESTRUCTIVE=1)", tool.Name, tool.Access)
	}
	if err := c.normalizeProjectArgs(req.Arguments); err != nil {
		return nil, nil, err
	}
	if req.DryRun {
		return map[string]any{"dry_run": true, "tool": tool.Name, "access": tool.Access, "arguments": req.Arguments}, nil, nil
	}
	raw, err := s.MCP.CallTool(c.ctx, c.auth, tool.Name, req.Arguments)
	if err != nil {
		return nil, nil, err
	}
	return raw, nil, nil
}

// normalizeProjectArgs turns project names into ids. Some tools only accept
// ids, and tools documented as "name or ID" (e.g. Brain_search) silently
// return nothing for a name, so ids are always sent.
func (c *call) normalizeProjectArgs(args map[string]any) error {
	for _, key := range []string{"project", "project_id", "projectId"} {
		v, ok := args[key].(string)
		if !ok || v == "" || oidPattern.MatchString(v) {
			continue
		}
		p, err := c.resolveProject(v, true)
		if err != nil {
			return err
		}
		args[key] = p.ID
	}
	return nil
}

// ---- GET /v1/work ----

var workTools = map[string]string{
	"task": "Studio_list_tasks_by_project", "story": "Studio_list_user_stories_by_project",
	"epic": "Studio_list_needs_by_project", "need": "Studio_list_needs_by_project",
}

func (s *Server) listWork(c *call) (any, any, error) {
	q := c.r.URL.Query()
	kind := strings.ToLower(q.Get("kind"))
	if kind == "" {
		kind = "task"
	}
	tool, ok := workTools[kind]
	if !ok {
		return nil, nil, fail(422, "VALIDATION_ERROR", "kind must be task, story or epic")
	}
	p, err := c.resolveProject(q.Get("project"), true)
	if err != nil {
		return nil, nil, err
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	skip, _ := strconv.Atoi(q.Get("cursor"))
	all := q.Get("all") == "true"
	status := strings.ToLower(q.Get("status"))
	// Filter by parent here instead of the list-by-parent tools, whose results
	// are known to be incomplete (spec §31.2).
	parent := strings.TrimSpace(q.Get("parent"))
	if parent != "" {
		all = true
		if !oidPattern.MatchString(parent) {
			_, ent, err := c.fetchWork(parent)
			if err != nil {
				return nil, nil, err
			}
			parent = str(ent, "id", "_id")
		}
	}

	items := []map[string]any{}
	total, hasMore := 0, false
	for {
		page, err := c.tool(tool, map[string]any{"project_id": p.ID, "fields": "full", "limit": limit, "skip": skip})
		if err != nil {
			return nil, nil, err
		}
		rows := asList(firstOf(page, "items", "result"))
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			if status != "" && strings.ToLower(str(m, "status")) != status {
				continue
			}
			if parent != "" && str(m, "needId") != parent && str(m, "userStoryId") != parent {
				continue
			}
			items = append(items, map[string]any{
				"id": str(m, "mindstreamId", "id"), "object_id": str(m, "id", "_id"), "kind": kind,
				"title": str(m, "title", "name"), "status": str(m, "status"),
				"parent": str(m, "userStoryId", "needId"), "priority": m["priority"],
				"excerpt": truncate(str(m, "text", "description"), 300),
			})
		}
		if t, ok := page["totalCount"].(float64); ok {
			total = int(t)
		}
		hasMore, _ = page["hasMore"].(bool)
		skip += len(rows)
		if !all || !hasMore || len(rows) == 0 || skip >= 1000 {
			break
		}
	}
	if parent != "" || status != "" {
		total = len(items) // the upstream total ignores our filters
		if !all && hasMore {
			total = -1
		}
	}
	var cursor any
	if hasMore {
		cursor = strconv.Itoa(skip)
	}
	return map[string]any{"project": map[string]string{"id": p.ID, "name": p.Name}, "kind": kind, "items": items, "warnings": []any{}},
		map[string]any{"cursor": cursor, "limit": limit, "total": nullIfNegative(total), "has_more": hasMore}, nil
}

func nullIfNegative(n int) any {
	if n < 0 {
		return nil
	}
	return n
}

func cutSuffix(s, suffix string) (string, bool) {
	if strings.HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}
