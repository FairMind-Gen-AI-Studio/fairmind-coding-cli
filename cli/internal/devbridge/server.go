package devbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server implements the proposed /v1 Agent API by orchestrating MCP tools.
type Server struct {
	MCP    *MCPClient
	Log    *log.Logger
	mux    *http.ServeMux
	routes []route
}

type route struct {
	method, path string
	h            handlerFunc
	prefix       bool
}

func NewServer(mcp *MCPClient, logger *log.Logger) *Server {
	s := &Server{MCP: mcp, Log: logger, mux: http.NewServeMux()}
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { writeOK(w, map[string]string{"status": "ok"}, nil) })
	s.handle("GET", "/v1/projects", s.projects)
	s.handle("GET", "/v1/search", s.search)
	s.handle("POST", "/v1/context:get", s.contextGet)
	s.handle("POST", "/v1/context:for-task", s.contextForTask)
	s.handle("POST", "/v1/context:for-code-change", s.contextForCodeChange)
	s.handle("GET", "/v1/brain/nodes/", s.brainGet)
	s.handle("GET", "/v1/tools", s.listTools)
	s.handle("*", "/v1/tools/", s.toolsRoute)
	s.handle("GET", "/v1/work", s.listWork)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Dispatch runs one /v1 request in-process and returns (data, page, error).
// It shares every handler with the HTTP server, so the CLI's direct-MCP mode
// behaves identically to going through the standalone bridge.
func (s *Server) Dispatch(r *http.Request) (any, any, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) < 16 {
		return nil, nil, fail(401, "TOKEN_MISSING", "Authorization: Bearer <token> is required")
	}
	c := &call{ctx: r.Context(), auth: auth, s: s, r: r}
	p := r.URL.Path
	for _, rt := range s.routes { // exact routes first
		if !rt.prefix && p == rt.path {
			if rt.method != "*" && r.Method != rt.method {
				return nil, nil, fail(405, "METHOD_NOT_ALLOWED", "use %s", rt.method)
			}
			return rt.h(c)
		}
	}
	for _, rt := range s.routes {
		if rt.prefix && strings.HasPrefix(p, rt.path) {
			if rt.method != "*" && r.Method != rt.method {
				return nil, nil, fail(405, "METHOD_NOT_ALLOWED", "use %s", rt.method)
			}
			return rt.h(c)
		}
	}
	return nil, nil, fail(404, "RESOURCE_NOT_FOUND", "unknown endpoint")
}

// ---- plumbing ----

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

func fail(status int, code, format string, a ...any) *apiError {
	return &apiError{status: status, code: code, message: fmt.Sprintf(format, a...)}
}

type call struct {
	ctx  context.Context
	auth string
	s    *Server
	r    *http.Request
}

type handlerFunc func(c *call) (data any, page any, err error)

func (s *Server) handle(method, path string, h handlerFunc) {
	s.routes = append(s.routes, route{method: method, path: path, h: h, prefix: strings.HasSuffix(path, "/")})
	s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		status := 200
		defer func() {
			// Never log headers or bodies: they may contain the token or project data.
			s.Log.Printf("%s %s -> %d (%dms) request_id=%s", r.Method, r.URL.Path, status, time.Since(start).Milliseconds(), r.Header.Get("X-Request-Id"))
		}()
		if method != "*" && r.Method != method {
			status = 405
			writeErr(w, fail(405, "METHOD_NOT_ALLOWED", "use %s", method))
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || len(auth) < 16 {
			status = 401
			writeErr(w, fail(401, "TOKEN_MISSING", "Authorization: Bearer <token> is required"))
			return
		}
		data, page, err := h(&call{ctx: r.Context(), auth: auth, s: s, r: r})
		if err != nil {
			e := mapError(err)
			status = e.status
			writeErr(w, e)
			return
		}
		writeOK(w, data, page)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data, page any) {
	writeJSON(w, 200, map[string]any{"ok": true, "data": data, "page": page, "evidence": nil, "error": nil})
}

func writeErr(w http.ResponseWriter, e *apiError) {
	writeJSON(w, e.status, map[string]any{"ok": false, "data": nil, "error": map[string]any{
		"code": e.code, "message": e.message, "retryable": e.status >= 500 || e.status == 429, "details": map[string]any{"via": "fairmind-devbridge"}}})
}

func mapError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	var ue *UpstreamError
	if errors.As(err, &ue) {
		switch ue.Status {
		case 401:
			return fail(401, "TOKEN_INVALID", "FairMind rejected the token")
		case 403:
			return fail(403, "RESOURCE_FORBIDDEN", "FairMind denied access")
		case 429:
			return fail(429, "RATE_LIMITED", "FairMind rate limit reached")
		}
		return fail(503, "SERVICE_UNAVAILABLE", "FairMind MCP server answered HTTP %d", ue.Status)
	}
	var te *ToolError
	if errors.As(err, &te) {
		msg := strings.ToLower(te.Message)
		switch {
		case strings.Contains(msg, "not found") || strings.Contains(msg, "no such") || strings.Contains(msg, "does not exist"):
			return fail(404, "RESOURCE_NOT_FOUND", "%s", truncate(te.Message, 300))
		case strings.Contains(msg, "permission") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "access denied") || strings.Contains(msg, "scope"):
			return fail(403, "RESOURCE_FORBIDDEN", "%s", truncate(te.Message, 300))
		case strings.Contains(msg, "expired"):
			return fail(401, "TOKEN_EXPIRED", "%s", truncate(te.Message, 300))
		case strings.Contains(msg, "required") || strings.Contains(msg, "invalid") || strings.Contains(msg, "refused"):
			return fail(422, "VALIDATION_ERROR", "%s", truncate(te.Message, 300))
		}
		return fail(502, "UPSTREAM_TOOL_ERROR", "%s", truncate(te.Message, 300))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fail(504, "TIMEOUT", "FairMind MCP server timed out")
	}
	return fail(503, "SERVICE_UNAVAILABLE", "FairMind MCP server unreachable")
}

func (c *call) tool(name string, args map[string]any) (map[string]any, error) {
	for k, v := range args { // drop empty optional arguments
		if v == nil || v == "" {
			delete(args, k)
		}
	}
	raw, err := c.s.MCP.CallTool(c.ctx, c.auth, name, args)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		return obj, nil
	}
	var arr []any
	if json.Unmarshal(raw, &arr) == nil {
		return map[string]any{"result": arr}, nil
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	return map[string]any{"text": text}, nil
}

// ---- projects ----

type project struct{ ID, Name string }

func (c *call) listProjects() ([]project, error) {
	res, err := c.tool("General_list_projects", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out []project
	for _, p := range asList(firstOf(res, "result", "projects", "items")) {
		m, _ := p.(map[string]any)
		out = append(out, project{ID: str(m, "id", "_id", "project_id"), Name: str(m, "name")})
	}
	return out, nil
}

// resolveProject maps a name or id to a project; empty means "the only one".
func (c *call) resolveProject(ref string, required bool) (*project, error) {
	ps, err := c.listProjects()
	if err != nil {
		return nil, err
	}
	if ref == "" {
		if len(ps) == 1 {
			return &ps[0], nil
		}
		if !required {
			return nil, nil
		}
		if len(ps) == 0 {
			return nil, fail(404, "PROJECT_NOT_FOUND", "no FairMind project is accessible with this token")
		}
		return nil, fail(400, "PROJECT_REQUIRED", "%d projects are accessible; pass --project or add .fairmind/config.json", len(ps))
	}
	for i := range ps {
		if ps[i].ID == ref || strings.EqualFold(ps[i].Name, ref) {
			return &ps[i], nil
		}
	}
	return nil, fail(404, "PROJECT_NOT_FOUND", "project %q was not found or is not accessible", ref)
}

func (c *call) projects() (any, any, error) {
	ps, err := c.listProjects()
	if err != nil {
		return nil, nil, err
	}
	out := []map[string]string{}
	for _, p := range ps {
		out = append(out, map[string]string{"id": p.ID, "name": p.Name})
	}
	return out, map[string]any{"cursor": nil, "limit": len(out), "total": len(out), "has_more": false}, nil
}

func (s *Server) projects(c *call) (any, any, error) { return c.projects() }

// ---- item mapping ----

type item struct {
	Source       string         `json:"source"`
	Kind         string         `json:"kind"`
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	ReviewState  string         `json:"review_state,omitempty"`
	WhyRelevant  string         `json:"why_relevant"`
	Excerpt      string         `json:"excerpt"`
	Score        float64        `json:"score,omitempty"`
	Anchors      []string       `json:"anchors"`
	Provenance   map[string]any `json:"provenance"`
	SupersededBy string         `json:"superseded_by,omitempty"`
	rank         int
}

// brainItem maps a Brain_search / Brain_context result row.
func brainItem(m map[string]any, why string) item {
	it := item{Source: "brain", Kind: str(m, "kind"), ID: str(m, "node_id", "id"), Title: str(m, "title"),
		Status: str(m, "status"), ReviewState: str(m, "review_state"), WhyRelevant: why,
		Excerpt: truncate(str(m, "summary", "excerpt"), 600), Anchors: []string{}, Provenance: map[string]any{}}
	if sub := str(m, "subtype"); sub != "" {
		it.Provenance["subtype"] = sub
	}
	if p, ok := m["provenance"].(map[string]any); ok {
		for k, v := range p {
			it.Provenance[k] = v
		}
	}
	if r := str(m, "reason"); r != "" {
		it.Provenance["reason"] = r
	}
	if sb := str(m, "superseded_by", "supersededBy"); sb != "" {
		it.SupersededBy = sb
	}
	if f, ok := m["score"].(float64); ok {
		it.Score = f
	}
	for _, a := range asList(m["anchors"]) {
		if am, ok := a.(map[string]any); ok {
			if p := str(am, "file_path", "path"); p != "" {
				it.Anchors = append(it.Anchors, p)
			}
		} else if s, ok := a.(string); ok {
			it.Anchors = append(it.Anchors, s)
		}
	}
	it.rank = rankFor(it)
	return it
}

// rankFor is a placeholder ordering (spec §18 priorities), NOT FairMind ranking.
func rankFor(it item) int {
	r := map[string]int{"requirement": 1, "decision": 2, "task": 3, "user_story": 3, "story": 3, "need": 3, "issue": 4, "test": 5, "document": 7, "code": 6}[it.Kind]
	if r == 0 {
		r = 6
	}
	if it.ReviewState == "confirmed" {
		r *= 10
	} else {
		r = r*10 + 5
	}
	if it.Status == "superseded" || it.Status == "retired" {
		r += 100
	}
	return r
}

func studioItem(kind string, m map[string]any, why string) item {
	id := str(m, "mindstreamId", "id")
	it := item{Source: "studio", Kind: kind, ID: id, Title: str(m, "title", "name"), Status: str(m, "status"),
		WhyRelevant: why, Excerpt: truncate(str(m, "text", "description", "content"), 1200), Anchors: []string{},
		Provenance: map[string]any{"object_id": str(m, "id", "_id"), "project": str(m, "projectId")}}
	if sb := str(m, "supersededBy"); sb != "" {
		it.SupersededBy = sb
		it.Status = "superseded"
	}
	it.rank = rankFor(it)
	return it
}

// ---- context pack ----

type pack struct {
	Summary  string           `json:"summary"`
	Intent   string           `json:"intent,omitempty"`
	Project  map[string]any   `json:"project,omitempty"`
	Items    []item           `json:"items"`
	Warnings []map[string]any `json:"warnings"`
	FollowUp []map[string]any `json:"follow_up"`
	Evidence map[string]any   `json:"evidence"`
	Budget   map[string]int   `json:"budget"`
	seen     map[string]bool
}

func newPack(p *project) *pack {
	pk := &pack{Items: []item{}, Warnings: []map[string]any{}, FollowUp: []map[string]any{},
		Evidence: map[string]any{"via": "fairmind-devbridge (prototype over MCP)"}, seen: map[string]bool{}}
	if p != nil {
		pk.Project = map[string]any{"id": p.ID, "name": p.Name}
	}
	return pk
}

func (pk *pack) add(it item) {
	if it.ID == "" || pk.seen[it.Source+"/"+it.ID] {
		return
	}
	pk.seen[it.Source+"/"+it.ID] = true
	pk.Items = append(pk.Items, it)
}

func (pk *pack) warn(code, msg string) {
	for _, w := range pk.Warnings {
		if w["code"] == code {
			return
		}
	}
	pk.Warnings = append(pk.Warnings, map[string]any{"code": code, "message": msg})
}

func (pk *pack) finish(budget int) {
	if budget <= 0 {
		budget = 8000
	}
	sort.SliceStable(pk.Items, func(i, j int) bool { return pk.Items[i].rank < pk.Items[j].rank })
	used, omitted, kept := 0, 0, []item{}
	for _, it := range pk.Items {
		b, _ := json.Marshal(it)
		cost := len(b)/4 + 1
		if used+cost > budget {
			omitted++
			continue
		}
		used += cost
		kept = append(kept, it)
	}
	pk.Items = kept
	for _, it := range kept {
		switch {
		case it.Status == "superseded" || it.Status == "retired":
			pk.warn("SUPERSEDED_KNOWLEDGE", "some items are superseded/retired: history only, follow the newer knowledge")
		case it.ReviewState == "proposed":
			pk.warn("PROPOSED_KNOWLEDGE", "some items are proposed (not human-confirmed)")
		}
		if it.Source == "brain" && (it.Kind == "decision" || it.Kind == "requirement" || it.Kind == "issue") && len(pk.FollowUp) < 5 {
			pk.FollowUp = append(pk.FollowUp, map[string]any{"command": "fairmind brain get " + it.ID + " --json", "reason": "full " + it.Kind + " content"})
		}
	}
	if omitted > 0 {
		pk.warn("BUDGET_TRIMMED", fmt.Sprintf("%d item(s) omitted to fit the budget", omitted))
	}
	pk.Budget = map[string]int{"requested_tokens": budget, "estimated_tokens": used, "omitted_items": omitted}
	pk.Summary = fmt.Sprintf("%d relevant item(s) from FairMind.", len(kept))
	if len(kept) == 0 {
		pk.Summary = "FairMind returned no knowledge relevant to this request."
	}
}

type repoRef struct {
	Explicit   string `json:"explicit"`
	Remote     string `json:"remote"`
	Branch     string `json:"branch"`
	HeadCommit string `json:"head_commit"`
	Configured string `json:"configured"`
}

func (r *repoRef) repository() string {
	if r == nil {
		return ""
	}
	if r.Explicit != "" {
		return r.Explicit
	}
	return r.Configured
}

func (r *repoRef) gitRemote() string {
	if r == nil || r.Remote == "" {
		return ""
	}
	return "https://" + r.Remote + ".git"
}

func decode(c *call, v any) error {
	if err := json.NewDecoder(c.r.Body).Decode(v); err != nil {
		return fail(400, "VALIDATION_ERROR", "invalid JSON body")
	}
	return nil
}

// ---- search ----

func (s *Server) search(c *call) (any, any, error) {
	q := c.r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		return nil, nil, fail(422, "VALIDATION_ERROR", "q is required")
	}
	k, _ := strconv.Atoi(q.Get("k"))
	if k <= 0 || k > 50 {
		k = 10
	}
	in := map[string]bool{}
	for _, v := range strings.Split(q.Get("in"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			in[v] = true
		}
	}
	all := len(in) == 0
	p, err := c.resolveProject(q.Get("project"), true)
	if err != nil {
		return nil, nil, err
	}
	ref := &repoRef{Explicit: q.Get("repository"), Remote: q.Get("repository_remote")}

	var mu sync.Mutex
	var items []item
	var warnings []map[string]any
	var wg sync.WaitGroup
	addWarn := func(code, msg string) {
		mu.Lock()
		warnings = append(warnings, map[string]any{"code": code, "message": msg})
		mu.Unlock()
	}
	if all || in["brain"] || in["studio"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.tool("Brain_search", map[string]any{"query": query, "k": k, "project": p.ID})
			if err != nil {
				addWarn("SOURCE_UNAVAILABLE", "brain: "+mapError(err).message)
				return
			}
			for _, row := range asList(res["results"]) {
				m, _ := row.(map[string]any)
				it := brainItem(m, "matched search")
				isStudio := it.Provenance["source"] == "studio"
				if all || (in["brain"] && !isStudio) || (in["studio"] && isStudio) {
					mu.Lock()
					items = append(items, it)
					mu.Unlock()
				}
			}
		}()
	}
	var studio []item
	if all || in["studio"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found, err := c.studioSearch(p.ID, query)
			if err != nil {
				addWarn("SOURCE_UNAVAILABLE", "studio: "+mapError(err).message)
			}
			mu.Lock()
			studio = found
			mu.Unlock()
		}()
	}
	if all || in["docs"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			docs, err := c.ragDocs(query, p.ID, k)
			if err != nil {
				addWarn("SOURCE_UNAVAILABLE", "docs: "+mapError(err).message)
				return
			}
			mu.Lock()
			items = append(items, docs...)
			mu.Unlock()
		}()
	}
	if in["code"] || (all && ref.repository() != "") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			repo := ref.repository()
			if repo == "" {
				addWarn("CODE_SEARCH_SKIPPED", "code search needs --repo <repository name or id> in this prototype")
				return
			}
			res, err := c.tool("Code_search", map[string]any{"query": query, "project": p.ID, "repository": repo, "top_k": k})
			if err != nil {
				addWarn("SOURCE_UNAVAILABLE", "code: "+mapError(err).message)
				return
			}
			mu.Lock()
			items = append(items, codeItems(res)...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if extra := notInBrain(studio, items); len(extra) > 0 {
		items = append(items, extra...)
		warnings = append(warnings, brainSparseWarning(len(extra)))
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	total := len(items)
	if len(items) > k {
		items = items[:k]
	}
	if items == nil {
		items = []item{}
	}
	if warnings == nil {
		warnings = []map[string]any{}
	}
	return map[string]any{"items": items, "warnings": warnings},
		map[string]any{"cursor": nil, "limit": k, "total": total, "has_more": total > k}, nil
}

func (c *call) ragDocs(query, projectID string, k int) ([]item, error) {
	res, err := c.tool("General_rag_retrieve_documents", map[string]any{"query": query, "project_id": projectID, "k": k})
	if err != nil {
		return nil, err
	}
	var out []item
	for i, row := range asList(firstOf(res, "result", "documents", "results", "items")) {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		title := str(meta, "title", "file_name", "filename", "source", "name")
		if title == "" {
			title = str(m, "title", "name", "source")
		}
		id := str(meta, "document_id", "doc_id", "id", "source")
		if id == "" {
			id = str(m, "id", "document_id")
		}
		if id == "" {
			id = fmt.Sprintf("doc-%d", i+1)
		}
		score, _ := m["score"].(float64)
		it := item{Source: "document", Kind: "document", ID: id, Title: title, Status: "indexed", WhyRelevant: "matched document search",
			Excerpt: truncate(str(m, "page_content", "content", "text", "chunk"), 600), Score: score, Anchors: []string{}, Provenance: map[string]any{}}
		it.rank = rankFor(it)
		out = append(out, it)
	}
	return out, nil
}

// studioSearch matches the query against the titles of every need, user story
// and task of the project. It compensates for Studio items that FairMind Brain
// has not indexed (Brain_search only sees what was delivered to it). Naive
// title matching is prototype-only; the real Agent API should query one index.
func (c *call) studioSearch(projectID, query string) ([]item, error) {
	ts := terms(query)
	if len(ts) == 0 {
		return nil, nil
	}
	lists := []struct{ kind, tool string }{
		{"need", "Studio_list_needs_by_project"},
		{"story", "Studio_list_user_stories_by_project"},
		{"task", "Studio_list_tasks_by_project"},
	}
	type res struct {
		items []item
		err   error
	}
	out := make([]res, len(lists))
	var wg sync.WaitGroup
	for i, l := range lists {
		wg.Add(1)
		go func(i int, kind, tool string) {
			defer wg.Done()
			for skip := 0; skip < 500; skip += 100 {
				page, err := c.tool(tool, map[string]any{"project_id": projectID, "fields": "summary", "limit": 100, "skip": skip})
				if err != nil {
					out[i].err = err
					return
				}
				for _, row := range asList(firstOf(page, "items", "result")) {
					m, ok := row.(map[string]any)
					if !ok {
						continue
					}
					title := str(m, "title")
					hit := matchTerms(ts, title)
					if len(hit) == 0 {
						continue
					}
					it := studioItem(kind, m, "title matches: "+strings.Join(hit, ", "))
					it.Score = float64(int(float64(len(hit))/float64(len(ts))*100)) / 100
					out[i].items = append(out[i].items, it)
				}
				if more, _ := page["hasMore"].(bool); !more {
					break
				}
			}
		}(i, l.kind, l.tool)
	}
	wg.Wait()
	var items []item
	var firstErr error
	for _, r := range out {
		items = append(items, r.items...)
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
	}
	return items, firstErr
}

// notInBrain drops Studio items already returned by Brain (Brain titles are
// truncated copies of the Studio title).
func notInBrain(studio, brain []item) []item {
	var out []item
	for _, s := range studio {
		dup := false
		for _, b := range brain {
			if b.Source == "brain" && len(b.Title) >= 30 && strings.HasPrefix(s.Title, strings.TrimSuffix(b.Title, "…")) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "that": true, "this": true, "into": true,
	"gli": true, "degli": true, "delle": true, "della": true, "dello": true, "dei": true, "del": true, "per": true,
	"con": true, "che": true, "una": true, "uno": true, "nel": true, "nella": true, "sul": true, "sulla": true,
}

func terms(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(w) >= 3 && !stopWords[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func matchTerms(ts []string, text string) []string {
	text = strings.ToLower(text)
	var hit []string
	for _, t := range ts {
		if strings.Contains(text, t) {
			hit = append(hit, t)
		}
	}
	return hit
}

func codeItems(res map[string]any) []item {
	var out []item
	for _, row := range asList(firstOf(res, "result", "results", "items", "matches")) {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		path := str(m, "file_path", "path", "file")
		score, _ := m["score"].(float64)
		it := item{Source: "code", Kind: "code", ID: path, Title: path, Status: "indexed", WhyRelevant: "matched code search",
			Excerpt: truncate(str(m, "summary", "description", "snippet", "content"), 400), Score: score, Anchors: []string{path}, Provenance: map[string]any{}}
		it.rank = rankFor(it)
		out = append(out, it)
	}
	return out
}

// ---- context:get ----

func (s *Server) contextGet(c *call) (any, any, error) {
	var req struct {
		Intent       string   `json:"intent"`
		Project      string   `json:"project"`
		Task         string   `json:"task"`
		Paths        []string `json:"paths"`
		BudgetTokens int      `json:"budget_tokens"`
		Repository   *repoRef `json:"repository"`
	}
	if err := decode(c, &req); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(req.Intent) == "" {
		return nil, nil, fail(422, "VALIDATION_ERROR", "intent is required")
	}
	p, err := c.resolveProject(req.Project, true)
	if err != nil {
		return nil, nil, err
	}
	pk := newPack(p)
	pk.Intent = req.Intent
	if req.Task != "" {
		if err := c.traverse(pk, req.Task); err != nil {
			return nil, nil, err
		}
	}
	if res, err := c.tool("Brain_search", map[string]any{"query": req.Intent, "k": 10, "project": p.ID,
		"repository": req.Repository.repository(), "git_remote": req.Repository.gitRemote()}); err == nil {
		for _, row := range asList(res["results"]) {
			m, _ := row.(map[string]any)
			pk.add(brainItem(m, "semantically related to the intent"))
		}
	} else {
		pk.warn("SOURCE_UNAVAILABLE", "brain search failed: "+mapError(err).message)
	}
	if found, err := c.studioSearch(p.ID, req.Intent); err == nil {
		if extra := notInBrain(found, pk.Items); len(extra) > 0 {
			sort.SliceStable(extra, func(i, j int) bool { return extra[i].Score > extra[j].Score })
			for _, it := range limit2(extra, 10) {
				pk.add(it)
			}
			w := brainSparseWarning(len(extra))
			pk.warn(w["code"].(string), w["message"].(string))
		}
	}
	if docs, err := c.ragDocs(req.Intent, p.ID, 5); err == nil {
		for _, d := range docs {
			pk.add(d)
		}
	}
	for _, path := range limit(req.Paths, 10) {
		c.fileContext(pk, p, req.Repository, path, "")
	}
	if len(pk.Items) == 0 {
		pk.warn("NO_RELEVANT_CONTEXT", "FairMind returned nothing for this intent")
	}
	pk.finish(req.BudgetTokens)
	return pk, nil, nil
}

// ---- context:for-task ----

var (
	oidPattern = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)
)

// fetchWork resolves an id to a Studio entity. Prefixes pick the tool; a bare
// ObjectId is tried as task, then story, then need.
func (c *call) fetchWork(id string) (string, map[string]any, error) {
	up := strings.ToUpper(id)
	type attempt struct{ kind, tool, arg string }
	var tries []attempt
	switch {
	case strings.HasPrefix(up, "TASK-"):
		tries = []attempt{{"task", "Studio_get_task", "task_id"}}
	case strings.HasPrefix(up, "US-"):
		tries = []attempt{{"story", "Studio_get_user_story", "user_story_id"}}
	case strings.HasPrefix(up, "EPIC-"), strings.HasPrefix(up, "NEED-"):
		tries = []attempt{{"need", "Studio_get_need", "need_id"}}
	case oidPattern.MatchString(id):
		tries = []attempt{{"task", "Studio_get_task", "task_id"}, {"story", "Studio_get_user_story", "user_story_id"}, {"need", "Studio_get_need", "need_id"}}
	default:
		return "", nil, fail(422, "VALIDATION_ERROR", "unrecognized id %q: expected TASK-, US-, EPIC-/NEED- or an ObjectId", id)
	}
	var lastErr error
	for _, t := range tries {
		res, err := c.tool(t.tool, map[string]any{t.arg: id})
		if err == nil && (str(res, "id", "_id") != "" || str(res, "mindstreamId") != "") {
			return t.kind, res, nil
		}
		if err != nil && mapError(err).status != 404 && mapError(err).status != 422 && mapError(err).status != 502 {
			return "", nil, err
		}
		lastErr = err
	}
	_ = lastErr
	return "", nil, fail(404, "TASK_NOT_FOUND", "no task, user story or need %s is accessible", id)
}

// traverse adds task -> story -> need, tests and related Brain knowledge.
func (c *call) traverse(pk *pack, id string) error {
	kind, ent, err := c.fetchWork(id)
	if err != nil {
		return err
	}
	pk.Evidence["resolved_id"] = map[string]string{"input": id, "id": str(ent, "mindstreamId"), "object_id": str(ent, "id", "_id"), "kind": kind}
	pk.add(studioItem(kind, ent, "requested item"))
	storyID, needID := "", ""
	switch kind {
	case "task":
		storyID = str(ent, "userStoryId", "user_story_id", "storyId")
		needID = str(ent, "needId", "need_id")
	case "story":
		storyID = str(ent, "id", "_id")
		needID = str(ent, "needId", "need_id")
	case "need":
		needID = ""
	}
	if kind == "task" && storyID != "" {
		if st, err := c.tool("Studio_get_user_story", map[string]any{"user_story_id": storyID}); err == nil {
			pk.add(studioItem("story", st, "parent user story of "+str(ent, "mindstreamId")))
			if needID == "" {
				needID = str(st, "needId", "need_id")
			}
		}
	}
	if needID != "" {
		if nd, err := c.tool("Studio_get_need", map[string]any{"need_id": needID}); err == nil {
			pk.add(studioItem("need", nd, "parent need/epic"))
		}
	}
	if storyID != "" {
		if tests, err := c.tool("Studio_list_tests_by_userstory", map[string]any{"user_story_id": storyID, "fields": "full", "limit": 20}); err == nil {
			for _, t := range asList(firstOf(tests, "items", "result")) {
				if m, ok := t.(map[string]any); ok {
					pk.add(studioItem("test", m, "test case of the user story"))
				}
			}
		}
	}
	title := str(ent, "title")
	if title != "" {
		if res, err := c.tool("Brain_search", map[string]any{"query": title, "k": 8, "project": str(ent, "projectId"),
			"kinds": []string{"decision", "requirement", "issue"}}); err == nil {
			for _, row := range asList(res["results"]) {
				m, _ := row.(map[string]any)
				pk.add(brainItem(m, "related knowledge for "+str(ent, "mindstreamId")))
			}
		} else {
			pk.warn("SOURCE_UNAVAILABLE", "brain search failed: "+mapError(err).message)
		}
	}
	return nil
}

func (s *Server) contextForTask(c *call) (any, any, error) {
	var req struct {
		ID           string   `json:"id"`
		Project      string   `json:"project"`
		BudgetTokens int      `json:"budget_tokens"`
		Repository   *repoRef `json:"repository"`
	}
	if err := decode(c, &req); err != nil {
		return nil, nil, err
	}
	pk := newPack(nil)
	if err := c.traverse(pk, strings.TrimSpace(req.ID)); err != nil {
		return nil, nil, err
	}
	if ps, err := c.listProjects(); err == nil && len(pk.Items) > 0 {
		pid, _ := pk.Items[0].Provenance["project"].(string)
		for _, p := range ps {
			if p.ID == pid {
				pk.Project = map[string]any{"id": p.ID, "name": p.Name}
			}
		}
	}
	pk.finish(req.BudgetTokens)
	return pk, nil, nil
}

// ---- context:for-code-change ----

func (c *call) fileContext(pk *pack, p *project, ref *repoRef, path, symbol string) {
	args := map[string]any{"file_path": path, "repository": ref.repository(), "git_remote": ref.gitRemote(),
		"max_items": 10, "detail_level": "standard", "symbol_name": symbol}
	if p != nil {
		args["project"] = p.ID
	}
	res, err := c.tool("Brain_context", args)
	if err != nil {
		e := mapError(err)
		if strings.Contains(strings.ToLower(e.message), "repositor") {
			pk.warn("REPOSITORY_NOT_BOUND", "FairMind could not resolve this repository: "+e.message)
		} else {
			pk.warn("SOURCE_UNAVAILABLE", "context for "+path+": "+e.message)
		}
		return
	}
	for _, row := range asList(res["items"]) {
		m, _ := row.(map[string]any)
		why := "anchored to " + path
		if symbol != "" {
			why += "#" + symbol
		}
		pk.add(brainItem(m, why))
	}
	if st, ok := res["staleness"].(map[string]any); ok {
		if repo, ok := st["repo"].(map[string]any); ok {
			state := str(repo, "sync_state")
			pk.Evidence["repository_sync"] = repo
			if state == "stale" || state == "outdated" {
				pk.warn("STALE_REPOSITORY_DATA", "FairMind's copy of this repository is outdated; trust the local checkout for current code")
			}
			if str(repo, "catalog_id") == "" && state == "unknown" {
				pk.warn("REPOSITORY_NOT_BOUND", "repository is not bound/ingested in FairMind; results rely on knowledge anchors only")
			}
		}
	}
}

func (s *Server) contextForCodeChange(c *call) (any, any, error) {
	var req struct {
		Project      string   `json:"project"`
		Repository   *repoRef `json:"repository"`
		BudgetTokens int      `json:"budget_tokens"`
		Changes      []struct {
			Path    string   `json:"path"`
			OldPath string   `json:"old_path"`
			Symbols []string `json:"symbols"`
		} `json:"changes"`
	}
	if err := decode(c, &req); err != nil {
		return nil, nil, err
	}
	if len(req.Changes) == 0 {
		return nil, nil, fail(422, "VALIDATION_ERROR", "changes must not be empty")
	}
	if req.Repository.repository() == "" && req.Repository.gitRemote() == "" {
		return nil, nil, fail(404, "REPOSITORY_NOT_FOUND", "no repository identity: run inside a git checkout or pass --repo")
	}
	p, err := c.resolveProject(req.Project, false)
	if err != nil {
		return nil, nil, err
	}
	pk := newPack(p)
	type job struct{ path, symbol string }
	var jobs []job
	for _, ch := range req.Changes {
		jobs = append(jobs, job{ch.Path, ""})
		if ch.OldPath != "" {
			jobs = append(jobs, job{ch.OldPath, ""})
		}
		for _, sym := range limit(ch.Symbols, 3) {
			jobs = append(jobs, job{ch.Path, sym})
		}
	}
	if len(jobs) > 30 {
		pk.warn("CHANGESET_TRUNCATED", fmt.Sprintf("only the first 30 of %d file/symbol lookups were performed", len(jobs)))
		jobs = jobs[:30]
	}
	// Brain_context calls run concurrently; each gets its own pack, merged in order.
	parts := make([]*pack, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			part := newPack(p)
			c.fileContext(part, p, req.Repository, j.path, j.symbol)
			parts[i] = part
		}(i, j)
	}
	wg.Wait()
	for _, part := range parts {
		for _, it := range part.Items {
			pk.add(it)
		}
		for _, w := range part.Warnings {
			pk.warn(w["code"].(string), w["message"].(string))
		}
		if rs, ok := part.Evidence["repository_sync"]; ok {
			pk.Evidence["repository_sync"] = rs
		}
	}
	if len(pk.Items) == 0 {
		pk.warn("NO_KNOWLEDGE_FOR_FILES", "FairMind has no decisions, requirements or issues anchored to these files")
	}
	pk.finish(req.BudgetTokens)
	return pk, nil, nil
}

// ---- brain get ----

func (s *Server) brainGet(c *call) (any, any, error) {
	id := strings.TrimPrefix(c.r.URL.Path, "/v1/brain/nodes/")
	if id == "" || strings.Contains(id, "/") {
		return nil, nil, fail(422, "VALIDATION_ERROR", "invalid node id")
	}
	res, err := c.tool("Brain_get", map[string]any{"knowledge_id": id, "max_tokens": 4000})
	if err != nil {
		if e := mapError(err); e.status == 404 {
			return nil, nil, fail(404, "KNOWLEDGE_NOT_FOUND", "knowledge node %s was not found", id)
		}
		return nil, nil, err
	}
	return res, nil, nil
}

// ---- small helpers ----

func str(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 { // do not split a UTF-8 sequence
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1] >= 0xC0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

func limit2(xs []item, n int) []item {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func brainSparseWarning(n int) map[string]any {
	return map[string]any{"code": "BRAIN_INDEX_SPARSE", "message": fmt.Sprintf(
		"%d Studio item(s) matched by title only: FairMind Brain has not indexed them, so linked decisions/anchors may be missing; use `fairmind context for-task <id>` for details", n)}
}

func limit(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// ErrorFields returns the normalized HTTP status, code and message for an error
// returned by Dispatch, so callers outside this package (the CLI's direct-MCP
// client) can map it without depending on the unexported error type.
func ErrorFields(err error) (int, string, string) {
	e := mapError(err)
	return e.status, e.code, e.message
}
