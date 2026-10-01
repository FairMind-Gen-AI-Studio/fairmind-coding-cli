package mock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Claims accepted by the mock. Real FairMind JWT claims are still UNKNOWN
// (spec §45 Q2–Q4); these are placeholders.
type Claims struct {
	Subject string `json:"sub"`
	Company string `json:"company"`
	Scope   string `json:"scope"`
	Project string `json:"project,omitempty"`
	Exp     int64  `json:"exp"`
	Issuer  string `json:"iss,omitempty"`
}

// MintToken builds an UNSIGNED test JWT (alg "none"). Never valid anywhere
// except this mock.
func MintToken(c Claims) string {
	enc := base64.RawURLEncoding
	h := enc.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	p, _ := json.Marshal(c)
	return h + "." + enc.EncodeToString(p) + ".mock-signature"
}

// Request is a recorded inbound request (tests assert on it).
type Request struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

// Server is the mock Agent API.
type Server struct {
	Now func() time.Time

	mu       sync.Mutex
	requests []Request
	mux      *http.ServeMux
}

// New returns a ready mock server handler.
func New() *Server {
	s := &Server{Now: time.Now, mux: http.NewServeMux()}
	s.mux.HandleFunc("/v1/projects", s.authed("GET", s.listProjects))
	s.mux.HandleFunc("/v1/context:get", s.authed("POST", s.contextGet))
	s.mux.HandleFunc("/v1/context:for-task", s.authed("POST", s.contextForTask))
	s.mux.HandleFunc("/v1/context:for-code-change", s.authed("POST", s.contextForCodeChange))
	s.mux.HandleFunc("/v1/search", s.authed("GET", s.search))
	s.mux.HandleFunc("/v1/brain/nodes/", s.authed("GET", s.brainGet))
	s.mux.HandleFunc("/v1/tools", s.authed("GET", s.listTools))
	s.mux.HandleFunc("/v1/tools/", s.authed("*", s.toolsRoute))
	s.mux.HandleFunc("/v1/work", s.authed("GET", s.listWork))
	return s
}

// Requests returns a copy of all recorded requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body)})
	s.mu.Unlock()
	s.mux.ServeHTTP(w, r)
}

// ---- envelope helpers ----

type apiError struct {
	status  int
	code    string
	message string
	details map[string]any
}

func fail(status int, code, format string, a ...any) *apiError {
	return &apiError{status: status, code: code, message: fmt.Sprintf(format, a...)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data any, page any) {
	writeJSON(w, 200, map[string]any{"ok": true, "data": data, "page": page, "evidence": nil, "error": nil})
}

func writeErr(w http.ResponseWriter, e *apiError) {
	details := e.details
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, e.status, map[string]any{"ok": false, "data": nil, "error": map[string]any{
		"code": e.code, "message": e.message, "retryable": e.status >= 500, "details": details}})
}

type handlerFunc func(r *http.Request, c *Claims) (data any, page any, err *apiError)

func (s *Server) authed(method string, h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if method != "*" && r.Method != method {
			writeErr(w, fail(405, "METHOD_NOT_ALLOWED", "use %s", method))
			return
		}
		c, e := s.authenticate(r)
		if e != nil {
			writeErr(w, e)
			return
		}
		data, page, e := h(r, c)
		if e != nil {
			writeErr(w, e)
			return
		}
		writeOK(w, data, page)
	}
}

func (s *Server) authenticate(r *http.Request) (*Claims, *apiError) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, fail(401, "TOKEN_MISSING", "Authorization header is required")
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	parts := strings.Split(tok, ".")
	if tok == h || len(parts) != 3 {
		return nil, fail(401, "TOKEN_INVALID", "malformed bearer token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fail(401, "TOKEN_INVALID", "malformed token payload")
	}
	var c Claims
	if json.Unmarshal(raw, &c) != nil || c.Subject == "" || c.Company == "" {
		return nil, fail(401, "TOKEN_INVALID", "token payload is missing required claims")
	}
	if c.Exp != 0 && s.Now().Unix() >= c.Exp {
		return nil, fail(401, "TOKEN_EXPIRED", "token expired")
	}
	if !hasScope(c.Scope, "read") {
		return nil, fail(403, "SCOPE_REQUIRED", "scope 'read' is required")
	}
	return &c, nil
}

func hasScope(scopes, want string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == want || s == "admin" || (want == "read" && s == "write") {
			return true
		}
	}
	return false
}

// ---- resolution helpers ----

func tenantProjects(c *Claims) []project {
	if c.Company != Tenant {
		return nil
	}
	var out []project
	for _, p := range projects {
		if c.Project == "" || c.Project == p.ID {
			out = append(out, p)
		}
	}
	return out
}

func findProject(c *Claims, ref string) (*project, *apiError) {
	if c.Company == Tenant {
		for i := range projects {
			p := &projects[i]
			if p.ID == ref || strings.EqualFold(p.Name, ref) {
				if c.Project != "" && c.Project != p.ID {
					return nil, fail(403, "RESOURCE_FORBIDDEN", "token is scoped to a different project")
				}
				return p, nil
			}
		}
	}
	return nil, fail(404, "PROJECT_NOT_FOUND", "project %q was not found", ref)
}

type repoRef struct {
	Explicit   string `json:"explicit"`
	Remote     string `json:"remote"`
	Branch     string `json:"branch"`
	HeadCommit string `json:"head_commit"`
	Configured string `json:"configured"`
}

// resolveBinding follows explicit → remote → configured (spec §22).
func resolveBinding(ref *repoRef) *binding {
	if ref == nil {
		return nil
	}
	for _, key := range []string{ref.Explicit, ref.Remote, ref.Configured} {
		if key == "" {
			continue
		}
		for i := range bindings {
			b := &bindings[i]
			if b.Remote == key || b.RepositoryID == key || b.RepositoryLabel == key {
				return b
			}
		}
		if key == ref.Explicit {
			return nil // an explicit but unknown repo must not silently fall through
		}
	}
	return nil
}

func resolveProject(c *Claims, projectRef string, ref *repoRef) (*project, *binding, *apiError) {
	b := resolveBinding(ref)
	if projectRef != "" {
		p, e := findProject(c, projectRef)
		return p, b, e
	}
	if b != nil {
		var visible []string
		for _, pid := range b.Projects {
			if c.Company == Tenant && (c.Project == "" || c.Project == pid) {
				visible = append(visible, pid)
			}
		}
		if len(visible) > 1 {
			e := fail(409, "REPOSITORY_AMBIGUOUS", "repository %s is bound to several projects; pass --project", b.Remote)
			e.details = map[string]any{"projects": visible}
			return nil, b, e
		}
		if len(visible) == 1 {
			p, e := findProject(c, visible[0])
			return p, b, e
		}
	}
	tp := tenantProjects(c)
	switch len(tp) {
	case 0:
		return nil, b, fail(404, "PROJECT_NOT_FOUND", "no accessible project")
	case 1:
		return &tp[0], b, nil
	}
	return nil, b, fail(400, "PROJECT_REQUIRED", "several projects are accessible; pass --project or add .fairmind/config.json")
}

func decodeBody(r *http.Request, v any) *apiError {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fail(400, "VALIDATION_ERROR", "invalid JSON body")
	}
	return nil
}

func findEntity(id string) *entity {
	for i := range entities {
		e := &entities[i]
		if strings.EqualFold(e.ID, id) || e.ObjectID == id {
			return e
		}
	}
	return nil
}

// ---- context pack assembly ----

type item struct {
	Source       string         `json:"source"`
	Kind         string         `json:"kind"`
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	ReviewState  string         `json:"review_state,omitempty"`
	WhyRelevant  string         `json:"why_relevant"`
	Excerpt      string         `json:"excerpt"`
	Anchors      []string       `json:"anchors"`
	Provenance   map[string]any `json:"provenance"`
	SupersededBy string         `json:"superseded_by,omitempty"`
	Score        float64        `json:"score,omitempty"`
	priority     int
}

func fromKnowledge(k knowledge, why string) item {
	anchors := k.Anchors
	if anchors == nil {
		anchors = []string{}
	}
	prov := map[string]any{"project": k.Project, "origin": "fairmind-mock"}
	if k.ReviewState == "proposed" {
		prov["created_by"] = "agent"
	}
	return item{Source: k.Source, Kind: k.Kind, ID: k.ID, Title: k.Title, Status: k.Status, ReviewState: k.ReviewState,
		WhyRelevant: why, Excerpt: k.Excerpt, Anchors: anchors, Provenance: prov, SupersededBy: k.SupersededBy, priority: k.Priority}
}

func fromEntity(e *entity, why string) item {
	return item{Source: "studio", Kind: e.Kind, ID: e.ID, Title: e.Title, Status: e.Status, WhyRelevant: why,
		Anchors: []string{}, Provenance: map[string]any{"project": e.Project, "object_id": e.ObjectID}, priority: 3}
}

type pack struct {
	Summary  string           `json:"summary"`
	Intent   string           `json:"intent,omitempty"`
	Project  map[string]any   `json:"project"`
	Items    []item           `json:"items"`
	Warnings []map[string]any `json:"warnings"`
	FollowUp []map[string]any `json:"follow_up"`
	Evidence map[string]any   `json:"evidence"`
	Budget   map[string]int   `json:"budget"`
}

func newPack(p *project) *pack {
	return &pack{Project: map[string]any{"id": p.ID, "name": p.Name}, Items: []item{},
		Warnings: []map[string]any{}, FollowUp: []map[string]any{}, Evidence: map[string]any{}}
}

func (pk *pack) warn(code, msg string, details map[string]any) {
	w := map[string]any{"code": code, "message": msg}
	if details != nil {
		w["details"] = details
	}
	pk.Warnings = append(pk.Warnings, w)
}

// finish orders by (mock) priority and trims to the token budget.
func (pk *pack) finish(budget int) {
	if budget <= 0 {
		budget = 8000
	}
	sort.SliceStable(pk.Items, func(i, j int) bool { return pk.Items[i].priority < pk.Items[j].priority })
	used, kept, omitted, omittedHistory := 0, []item{}, 0, false
	for _, it := range pk.Items {
		b, _ := json.Marshal(it)
		cost := len(b)/4 + 1
		if used+cost > budget {
			omitted++
			if it.Status == "superseded" {
				omittedHistory = true
			}
			continue
		}
		used += cost
		kept = append(kept, it)
	}
	pk.Items = kept
	if omittedHistory {
		pk.warn("HISTORY_OMITTED", "superseded knowledge was omitted by the budget; raise --budget to include it", nil)
	}
	for _, it := range kept {
		if it.Kind == "decision" || it.Kind == "requirement" {
			pk.FollowUp = append(pk.FollowUp, map[string]any{
				"command": "fairmind brain get " + it.ID + " --json", "reason": "full " + it.Kind + " content"})
		}
		if it.Status == "superseded" {
			pk.warn("SUPERSEDED_KNOWLEDGE", fmt.Sprintf("%s is superseded by %s; follow the newer decision", it.ID, it.SupersededBy), nil)
		}
		if it.ReviewState == "proposed" {
			pk.warn("PROPOSED_KNOWLEDGE", fmt.Sprintf("%s is proposed (not human-confirmed); do not treat it as an accepted requirement", it.ID), nil)
		}
	}
	pk.Budget = map[string]int{"requested_tokens": budget, "estimated_tokens": used, "omitted_items": omitted}
	pk.Summary = fmt.Sprintf("%d relevant item(s) from FairMind for project %s.", len(kept), pk.Project["name"])
	if len(kept) == 0 {
		pk.Summary = "FairMind has no recorded knowledge relevant to this request."
	}
}

func (pk *pack) bindingWarnings(b *binding) {
	if b == nil {
		return
	}
	pk.Evidence["repository"] = map[string]any{"id": b.RepositoryID, "remote": b.Remote, "ingested_at": b.IngestedAt}
	if b.Stale {
		pk.warn("STALE_REPOSITORY_DATA", "FairMind's copy of this repository is outdated; trust the local checkout for current code, FairMind for rationale",
			map[string]any{"ingested_commit": b.IngestedCommit, "ingested_at": b.IngestedAt})
	}
}

var stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true, "implement": true, "add": true, "fix": true}

func terms(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(w) >= 3 && !stop[w] {
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

// ---- handlers ----

func (s *Server) listProjects(r *http.Request, c *Claims) (any, any, *apiError) {
	out := []map[string]string{}
	for _, p := range tenantProjects(c) {
		out = append(out, map[string]string{"id": p.ID, "name": p.Name})
	}
	return out, map[string]any{"cursor": nil, "limit": 50, "total": len(out), "has_more": false}, nil
}

func (s *Server) contextGet(r *http.Request, c *Claims) (any, any, *apiError) {
	var req struct {
		Intent       string   `json:"intent"`
		Project      string   `json:"project"`
		Task         string   `json:"task"`
		Paths        []string `json:"paths"`
		BudgetTokens int      `json:"budget_tokens"`
		Repository   *repoRef `json:"repository"`
	}
	if e := decodeBody(r, &req); e != nil {
		return nil, nil, e
	}
	if strings.TrimSpace(req.Intent) == "" {
		return nil, nil, fail(422, "VALIDATION_ERROR", "intent is required")
	}
	p, b, e := resolveProject(c, req.Project, req.Repository)
	if e != nil {
		return nil, nil, e
	}
	pk := newPack(p)
	pk.Intent = req.Intent
	seen := map[string]bool{}
	if req.Task != "" {
		ent := findEntity(req.Task)
		if ent == nil || ent.Project != p.ID {
			return nil, nil, fail(404, "TASK_NOT_FOUND", "task %s was not found", req.Task)
		}
		for _, it := range traverse(ent) {
			seen[it.ID] = true
			pk.Items = append(pk.Items, it)
		}
	}
	ts := terms(req.Intent)
	for _, k := range knowledgeBase {
		if k.Project != p.ID || seen[k.ID] {
			continue
		}
		if hit := matchTerms(ts, k.Title+" "+k.Excerpt); len(hit) > 0 {
			seen[k.ID] = true
			pk.Items = append(pk.Items, fromKnowledge(k, "matches intent terms: "+strings.Join(hit, ", ")))
			continue
		}
		for _, path := range req.Paths {
			if anchored(k, path) {
				seen[k.ID] = true
				pk.Items = append(pk.Items, fromKnowledge(k, "anchored to "+path))
				break
			}
		}
	}
	if len(pk.Items) == 0 {
		pk.warn("NO_RELEVANT_CONTEXT", "no FairMind knowledge matched this intent", nil)
	}
	pk.bindingWarnings(b)
	pk.finish(req.BudgetTokens)
	return pk, nil, nil
}

// traverse walks task → story → need and collects linked knowledge (spec §7.2).
func traverse(start *entity) []item {
	chain := []*entity{}
	for e := start; e != nil; {
		chain = append(chain, e)
		if e.Parent == "" {
			break
		}
		e = findEntity(e.Parent)
	}
	ids := map[string]bool{}
	var out []item
	for i, e := range chain {
		ids[e.ID] = true
		why := "requested item"
		if i > 0 {
			why = fmt.Sprintf("parent %s of %s", e.Kind, chain[i-1].ID)
		}
		out = append(out, fromEntity(e, why))
	}
	// A need also brings its other stories as related context.
	if start.Kind == "need" {
		for i := range entities {
			if entities[i].Parent == start.ID && entities[i].Kind == "story" {
				ids[entities[i].ID] = true
				out = append(out, fromEntity(&entities[i], "story of "+start.ID))
			}
		}
	}
	for _, k := range knowledgeBase {
		for _, l := range k.Links {
			if ids[l] {
				out = append(out, fromKnowledge(k, "linked to "+l))
				break
			}
		}
	}
	return out
}

func anchored(k knowledge, path string) bool {
	for _, a := range k.Anchors {
		if a == path || strings.HasPrefix(path, strings.TrimSuffix(a, "/")+"/") {
			return true
		}
	}
	return false
}

func (s *Server) contextForTask(r *http.Request, c *Claims) (any, any, *apiError) {
	var req struct {
		ID           string   `json:"id"`
		Project      string   `json:"project"`
		BudgetTokens int      `json:"budget_tokens"`
		Repository   *repoRef `json:"repository"`
	}
	if e := decodeBody(r, &req); e != nil {
		return nil, nil, e
	}
	ent := findEntity(strings.TrimSpace(req.ID))
	if ent == nil || c.Company != Tenant {
		return nil, nil, fail(404, "TASK_NOT_FOUND", "task %s was not found", req.ID)
	}
	p, e := findProject(c, ent.Project)
	if e != nil {
		return nil, nil, e
	}
	if req.Project != "" {
		want, e := findProject(c, req.Project)
		if e != nil {
			return nil, nil, e
		}
		if want.ID != p.ID {
			return nil, nil, fail(404, "TASK_NOT_FOUND", "task %s was not found in project %s", req.ID, req.Project)
		}
	}
	pk := newPack(p)
	pk.Items = traverse(ent)
	pk.Evidence["resolved_id"] = map[string]string{"input": req.ID, "id": ent.ID, "object_id": ent.ObjectID, "kind": ent.Kind}
	pk.bindingWarnings(resolveBinding(req.Repository))
	pk.finish(req.BudgetTokens)
	return pk, nil, nil
}

func (s *Server) contextForCodeChange(r *http.Request, c *Claims) (any, any, *apiError) {
	var req struct {
		Mode         string   `json:"mode"`
		Project      string   `json:"project"`
		Repository   *repoRef `json:"repository"`
		BudgetTokens int      `json:"budget_tokens"`
		Changes      []struct {
			Path    string   `json:"path"`
			OldPath string   `json:"old_path"`
			Status  string   `json:"status"`
			Symbols []string `json:"symbols"`
		} `json:"changes"`
	}
	if e := decodeBody(r, &req); e != nil {
		return nil, nil, e
	}
	if len(req.Changes) == 0 {
		return nil, nil, fail(422, "VALIDATION_ERROR", "changes must not be empty")
	}
	b := resolveBinding(req.Repository)
	if b == nil && req.Project == "" {
		return nil, nil, fail(404, "REPOSITORY_NOT_FOUND", "repository is not bound to any FairMind project; pass --project or bind it")
	}
	p, b, e := resolveProject(c, req.Project, req.Repository)
	if e != nil {
		return nil, nil, e
	}
	pk := newPack(p)
	if b == nil {
		pk.warn("REPOSITORY_NOT_BOUND", "repository is not bound in FairMind; results rely on path anchors only", nil)
	}
	pk.bindingWarnings(b)
	seen := map[string]bool{}
	for _, ch := range req.Changes {
		for _, k := range knowledgeBase {
			if k.Project != p.ID || seen[k.ID] {
				continue
			}
			for _, path := range []string{ch.Path, ch.OldPath} {
				if path != "" && anchored(k, path) {
					seen[k.ID] = true
					pk.Items = append(pk.Items, fromKnowledge(k, "anchored to "+path))
					break
				}
			}
		}
	}
	if len(pk.Items) == 0 {
		pk.warn("NO_KNOWLEDGE_FOR_FILES", "FairMind has no decisions, requirements or issues anchored to these files", nil)
	}
	pk.finish(req.BudgetTokens)
	return pk, nil, nil
}

var searchSourceMap = map[string]string{"brain": "brain", "docs": "document", "code": "code", "studio": "studio"}

func (s *Server) search(r *http.Request, c *Claims) (any, any, *apiError) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		return nil, nil, fail(422, "VALIDATION_ERROR", "q is required")
	}
	k, _ := strconv.Atoi(q.Get("k"))
	if k <= 0 {
		k = 10
	}
	var ref *repoRef
	if q.Get("repository") != "" || q.Get("repository_remote") != "" {
		ref = &repoRef{Explicit: q.Get("repository"), Remote: q.Get("repository_remote")}
	}
	p, _, e := resolveProject(c, q.Get("project"), ref)
	if e != nil {
		return nil, nil, e
	}
	allowed := map[string]bool{}
	for _, s := range strings.Split(q.Get("in"), ",") {
		if src, ok := searchSourceMap[strings.TrimSpace(s)]; ok {
			allowed[src] = true
		}
	}
	ts := terms(query)
	var results []item
	for _, kn := range knowledgeBase {
		if kn.Project != p.ID || (len(allowed) > 0 && !allowed[kn.Source]) {
			continue
		}
		hit := matchTerms(ts, kn.Title+" "+kn.Excerpt)
		if len(hit) == 0 || len(ts) == 0 {
			continue
		}
		it := fromKnowledge(kn, "matches: "+strings.Join(hit, ", "))
		it.Score = float64(int(float64(len(hit))/float64(len(ts))*100)) / 100
		results = append(results, it)
	}
	if len(allowed) == 0 || allowed["studio"] {
		for i := range entities {
			e := &entities[i]
			if e.Project != p.ID {
				continue
			}
			if hit := matchTerms(ts, e.Title); len(hit) > 0 && len(ts) > 0 {
				it := fromEntity(e, "matches: "+strings.Join(hit, ", "))
				it.Score = float64(int(float64(len(hit))/float64(len(ts))*100)) / 100
				results = append(results, it)
			}
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	total := len(results)
	if len(results) > k {
		results = results[:k]
	}
	if results == nil {
		results = []item{}
	}
	return map[string]any{"items": results, "warnings": []any{}},
		map[string]any{"cursor": nil, "limit": k, "total": total, "has_more": total > k}, nil
}

func (s *Server) brainGet(r *http.Request, c *Claims) (any, any, *apiError) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/brain/nodes/")
	for _, k := range knowledgeBase {
		if k.ID != id {
			continue
		}
		if _, e := findProject(c, k.Project); e != nil {
			if e.status == 403 {
				return nil, nil, e
			}
			break
		}
		it := fromKnowledge(k, "")
		return map[string]any{"node": it, "content": k.Content}, nil, nil
	}
	return nil, nil, fail(404, "KNOWLEDGE_NOT_FOUND", "knowledge node %s was not found", id)
}
