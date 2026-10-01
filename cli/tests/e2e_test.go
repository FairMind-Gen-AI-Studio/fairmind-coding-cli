// End-to-end tests: the real command tree against the in-process mock Agent
// API, covering the scenarios required by spec §44.
package tests

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/commands"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/git"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/mock"
)

const (
	demoProject    = "665f00000000000000000001"
	billingProject = "665f00000000000000000002"
)

type fakeKeychain struct{ items map[string]string }

func (k *fakeKeychain) Get(p string) (string, error) {
	if v, ok := k.items[p]; ok {
		return v, nil
	}
	return "", auth.ErrNotFound
}
func (k *fakeKeychain) Set(p, v string) error { k.items[p] = v; return nil }
func (k *fakeKeychain) Delete(p string) error {
	if _, ok := k.items[p]; !ok {
		return auth.ErrNotFound
	}
	delete(k.items, p)
	return nil
}

type harness struct {
	t     *testing.T
	mock  *mock.Server
	srv   *httptest.Server
	env   map[string]string
	dir   string
	kc    *fakeKeychain
	stdin string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	m := mock.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return &harness{t: t, mock: m, srv: srv, dir: t.TempDir(), kc: &fakeKeychain{items: map[string]string{}},
		env: map[string]string{"HOME": t.TempDir(), "FAIRMIND_API_URL": srv.URL, auth.EnvVar: readToken()}}
}

func token(c mock.Claims) string {
	if c.Subject == "" {
		c.Subject = "dev"
	}
	if c.Company == "" {
		c.Company = mock.Tenant
	}
	if c.Exp == 0 {
		c.Exp = time.Now().Add(time.Hour).Unix()
	}
	return mock.MintToken(c)
}

func readToken() string { return token(mock.Claims{Scope: "read"}) }

type result struct {
	stdout, stderr string
	exit           int
	env            map[string]any
}

func (h *harness) run(args ...string) result {
	h.t.Helper()
	var out, errb bytes.Buffer
	app := &commands.App{
		Stdin: strings.NewReader(h.stdin), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return h.env[k] }, Dir: h.dir,
		Keychain: h.kc, Git: git.Exec, Now: time.Now,
	}
	exit := app.Run(args)
	r := result{stdout: out.String(), stderr: errb.String(), exit: exit}
	if err := json.Unmarshal(out.Bytes(), &r.env); err != nil {
		h.t.Fatalf("stdout is not valid JSON (%v):\n%s", err, out.String())
	}
	// No credential may ever leak to stdout/stderr (spec §44 CLI).
	if tok := h.env[auth.EnvVar]; len(tok) > 8 && (strings.Contains(r.stdout, tok) || strings.Contains(r.stderr, tok)) {
		h.t.Fatalf("token leaked in output:\n%s\n%s", r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout+r.stderr, "eyJ") {
		h.t.Fatalf("JWT-shaped string leaked in output:\n%s\n%s", r.stdout, r.stderr)
	}
	return r
}

func (r result) errCode() string {
	e, _ := r.env["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (r result) data() map[string]any {
	d, _ := r.env["data"].(map[string]any)
	return d
}

func (r result) itemIDs() map[string]map[string]any {
	out := map[string]map[string]any{}
	items, _ := r.data()["items"].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func (r result) warningCodes() map[string]bool {
	out := map[string]bool{}
	ws, _ := r.data()["warnings"].([]any)
	for _, w := range ws {
		if m, ok := w.(map[string]any); ok {
			out[m["code"].(string)] = true
		}
	}
	return out
}

func expect(t *testing.T, r result, exit int, code string) {
	t.Helper()
	if r.exit != exit || (code != "" && r.errCode() != code) {
		t.Fatalf("want exit %d code %q, got exit %d code %q\nstdout: %s\nstderr: %s", exit, code, r.exit, r.errCode(), r.stdout, r.stderr)
	}
}

// ---- git fixtures ----

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initRepo creates a repo with origin=remote and committed files.
func (h *harness) initRepo(remote string, files map[string]string) {
	h.t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		h.t.Skip("git not installed")
	}
	gitRun(h.t, h.dir, "init", "-q")
	gitRun(h.t, h.dir, "remote", "add", "origin", remote)
	for p, c := range files {
		writeFile(h.t, filepath.Join(h.dir, p), c)
	}
	gitRun(h.t, h.dir, "add", ".")
	gitRun(h.t, h.dir, "commit", "-qm", "init")
}

var authFiles = map[string]string{
	"src/auth/reset.ts":  "export function confirmReset() {\n  return 1\n}\n",
	"src/auth/tokens.ts": "export function hashToken() {\n  return 1\n}\n",
	"src/misc/util.ts":   "export function util() {\n  return 1\n}\n",
}

// ---- Authentication (§44) ----

func TestMissingTokenFailsLocally(t *testing.T) {
	h := newHarness(t)
	delete(h.env, auth.EnvVar)
	r := h.run("context", "get", "--intent", "password reset", "--json")
	expect(t, r, 2, "TOKEN_MISSING")
	if n := len(h.mock.Requests()); n != 0 {
		t.Fatalf("no request expected without a token, got %d", n)
	}
}

func TestMalformedToken(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = "not-a-jwt-token"
	expect(t, h.run("context", "get", "--intent", "x", "--project", "demo-project"), 2, "TOKEN_INVALID")
}

func TestExpiredToken(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "read", Exp: time.Now().Add(-time.Minute).Unix()})
	expect(t, h.run("context", "for-task", "TASK-123"), 2, "TOKEN_EXPIRED")
}

func TestTokenWithoutReadScope(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "profile"})
	expect(t, h.run("search", "password"), 4, "SCOPE_REQUIRED")
}

func TestReadOnlyTokenCanRead(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("context", "for-task", "TASK-123"), 0, "")
}

func TestProjectScopedTokenCannotReadOtherProject(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "read", Project: billingProject})
	expect(t, h.run("context", "for-task", "TASK-123"), 4, "RESOURCE_FORBIDDEN")
	expect(t, h.run("context", "for-task", "TASK-900"), 0, "")
}

func TestWrongTenant(t *testing.T) {
	h := newHarness(t)
	h.env[auth.EnvVar] = token(mock.Claims{Scope: "read", Company: "other-co"})
	expect(t, h.run("context", "for-task", "TASK-123"), 3, "TASK_NOT_FOUND")
	expect(t, h.run("context", "get", "--intent", "reset", "--project", demoProject), 3, "PROJECT_NOT_FOUND")
}

func TestTokenFlagIsRejected(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("context", "get", "--intent", "x", "--token", "abc"), 5, "VALIDATION_ERROR")
	if len(h.mock.Requests()) != 0 {
		t.Fatal("no request expected")
	}
}

func TestAuthStatus(t *testing.T) {
	h := newHarness(t)
	r := h.run("auth", "status")
	expect(t, r, 0, "")
	tok := r.data()["token"].(map[string]any)
	if tok["source"] != "env" || tok["claims_verified"] != false {
		t.Fatalf("unexpected token info: %v", tok)
	}
	if srv := r.data()["server"].(map[string]any); srv["ok"] != true || srv["projects_visible"].(float64) != 2 {
		t.Fatalf("unexpected server check: %v", srv)
	}
}

func TestLoginStoresTokenInKeychain(t *testing.T) {
	h := newHarness(t)
	tok := h.env[auth.EnvVar]
	delete(h.env, auth.EnvVar)
	h.stdin = tok + "\n"
	expect(t, h.run("auth", "login"), 0, "")
	if h.kc.items["default"] != tok {
		t.Fatal("token not stored in keychain")
	}
	r := h.run("auth", "status")
	expect(t, r, 0, "")
	if r.data()["token"].(map[string]any)["source"] != "keychain" {
		t.Fatal("expected keychain source")
	}
	expect(t, h.run("auth", "logout"), 0, "")
	expect(t, h.run("auth", "status"), 2, "TOKEN_MISSING")
}

// ---- Context (§44) ----

func TestContextGetGeneralIntent(t *testing.T) {
	h := newHarness(t)
	r := h.run("context", "get", "--intent", "password reset expiry", "--project", "demo-project", "--budget", "8000")
	expect(t, r, 0, "")
	ids := r.itemIDs()
	if ids["REQ-12"] == nil {
		t.Fatalf("expected REQ-12 in %v", r.stdout)
	}
	if b := r.data()["budget"].(map[string]any); b["requested_tokens"].(float64) != 8000 {
		t.Fatalf("budget not forwarded: %v", b)
	}
}

func TestContextForTaskByFairMindIDAndObjectID(t *testing.T) {
	h := newHarness(t)
	byID := h.run("context", "for-task", "TASK-123")
	byOID := h.run("context", "for-task", "665f1c2e9b1d4a0012345601")
	expect(t, byID, 0, "")
	expect(t, byOID, 0, "")
	a, b := byID.itemIDs(), byOID.itemIDs()
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("ObjectId and TASK id should resolve identically: %d vs %d", len(a), len(b))
	}
	for _, want := range []string{"TASK-123", "US-45", "NEED-7", "REQ-12", "TEST-88", "DEC-7"} {
		if a[want] == nil {
			t.Errorf("traversal missing %s", want)
		}
	}
}

func TestContextForUnknownTask(t *testing.T) {
	h := newHarness(t)
	r := h.run("context", "for-task", "TASK-404")
	expect(t, r, 3, "TASK_NOT_FOUND")
	if r.env["error"].(map[string]any)["retryable"] != false {
		t.Fatal("not-found must not be retryable")
	}
}

func TestSupersededAndProposedKnowledgeAreLabelled(t *testing.T) {
	h := newHarness(t)
	r := h.run("context", "for-task", "TASK-123")
	ids := r.itemIDs()
	if dec := ids["DEC-3"]; dec == nil || dec["status"] != "superseded" || dec["superseded_by"] != "DEC-7" {
		t.Fatalf("superseded decision must be present and labelled: %v", dec)
	}
	if req := ids["REQ-13"]; req == nil || req["review_state"] != "proposed" {
		t.Fatalf("proposed requirement must keep review_state=proposed: %v", req)
	}
	w := r.warningCodes()
	if !w["SUPERSEDED_KNOWLEDGE"] || !w["PROPOSED_KNOWLEDGE"] {
		t.Fatalf("expected trust warnings, got %v", w)
	}
}

func TestCodeChangeBoundRepository(t *testing.T) {
	h := newHarness(t)
	h.initRepo("https://ci-user:ghp_embeddedsecret@github.com/acme/backend-api.git", authFiles)
	writeFile(t, filepath.Join(h.dir, "src/auth/reset.ts"), "export function confirmReset() {\n  return 2 // SECRET_SOURCE_CONTENT\n}\n")
	writeFile(t, filepath.Join(h.dir, "src/auth/tokens.ts"), "export function hashToken() {\n  return 2\n}\n")

	r := h.run("context", "for-code-change", "--diff", "--symbols", "--json")
	expect(t, r, 0, "")
	ids := r.itemIDs()
	for _, want := range []string{"DEC-7", "REQ-12", "DEC-3", "ISS-21"} {
		if ids[want] == nil {
			t.Errorf("expected %s for changed auth files", want)
		}
	}

	// Data minimization (§33): metadata only, no source, no credentials.
	reqs := h.mock.Requests()
	body := reqs[len(reqs)-1].Body
	for _, leak := range []string{"SECRET_SOURCE_CONTENT", "return 2", "ghp_embeddedsecret", "ci-user"} {
		if strings.Contains(body, leak) {
			t.Fatalf("request body leaked %q: %s", leak, body)
		}
	}
	for _, want := range []string{`"remote":"github.com/acme/backend-api"`, `"path":"src/auth/reset.ts"`, `"symbols":["confirmReset"]`} {
		if !strings.Contains(body, want) {
			t.Errorf("request body missing %s: %s", want, body)
		}
	}
}

func TestCodeChangeUnboundRepository(t *testing.T) {
	h := newHarness(t)
	h.initRepo("git@github.com:acme/unknown-repo.git", authFiles)
	writeFile(t, filepath.Join(h.dir, "src/auth/reset.ts"), "changed\n")
	expect(t, h.run("context", "for-code-change", "--diff"), 3, "REPOSITORY_NOT_FOUND")

	r := h.run("context", "for-code-change", "--diff", "--project", "demo-project")
	expect(t, r, 0, "")
	if !r.warningCodes()["REPOSITORY_NOT_BOUND"] {
		t.Fatal("expected REPOSITORY_NOT_BOUND warning")
	}
}

func TestCodeChangeFileWithoutKnowledge(t *testing.T) {
	h := newHarness(t)
	h.initRepo("git@github.com:acme/backend-api.git", authFiles)
	r := h.run("context", "for-code-change", "--files", "src/misc/util.ts")
	expect(t, r, 0, "")
	if len(r.itemIDs()) != 0 || !r.warningCodes()["NO_KNOWLEDGE_FOR_FILES"] {
		t.Fatalf("expected empty context with warning: %s", r.stdout)
	}
}

func TestCodeChangeStaleRepository(t *testing.T) {
	h := newHarness(t)
	h.initRepo("https://github.com/acme/legacy-app.git", authFiles)
	r := h.run("context", "for-code-change", "--files", "src/auth/reset.ts")
	expect(t, r, 0, "")
	if !r.warningCodes()["STALE_REPOSITORY_DATA"] {
		t.Fatal("expected STALE_REPOSITORY_DATA warning")
	}
}

func TestCodeChangeAmbiguousRepository(t *testing.T) {
	h := newHarness(t)
	h.initRepo("https://github.com/acme/shared-lib.git", authFiles)
	expect(t, h.run("context", "for-code-change", "--files", "src/auth/reset.ts"), 5, "REPOSITORY_AMBIGUOUS")
	expect(t, h.run("context", "for-code-change", "--files", "src/auth/reset.ts", "--project", "demo-project"), 0, "")
}

func TestCodeChangeWithoutLocalChangesSkipsNetwork(t *testing.T) {
	h := newHarness(t)
	h.initRepo("https://github.com/acme/backend-api.git", authFiles)
	r := h.run("context", "for-code-change", "--diff")
	expect(t, r, 0, "")
	if !r.warningCodes()["NO_LOCAL_CHANGES"] || len(h.mock.Requests()) != 0 {
		t.Fatalf("expected local short-circuit: %s", r.stdout)
	}
}

func TestCodeChangeDiffRequiresRepo(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("context", "for-code-change", "--diff"), 5, "NOT_A_GIT_REPOSITORY")
}

func TestProjectFileConfig(t *testing.T) {
	h := newHarness(t)
	h.initRepo("https://github.com/acme/shared-lib.git", authFiles)
	writeFile(t, filepath.Join(h.dir, ".fairmind/config.json"), `{"project":"demo-project"}`)
	expect(t, h.run("context", "for-code-change", "--files", "src/auth/reset.ts"), 0, "")
}

// ---- Search ----

func TestSearchFiltersSources(t *testing.T) {
	h := newHarness(t)
	r := h.run("search", "password reset", "--in", "docs,brain", "--k", "3", "--project", "demo-project")
	expect(t, r, 0, "")
	items := r.data()["items"].([]any)
	if len(items) == 0 || len(items) > 3 {
		t.Fatalf("unexpected result count %d", len(items))
	}
	for _, it := range items {
		src := it.(map[string]any)["source"]
		if src != "brain" && src != "document" {
			t.Fatalf("unexpected source %v", src)
		}
	}
	if page, _ := r.env["page"].(map[string]any); page == nil || page["limit"].(float64) != 3 {
		t.Fatalf("expected normalized page: %v", r.env["page"])
	}
	expect(t, h.run("search", "x", "--in", "jira"), 5, "VALIDATION_ERROR")
}

// ---- CLI robustness (§44) ----

func TestInsecureAPIURLRejected(t *testing.T) {
	h := newHarness(t)
	h.env["FAIRMIND_API_URL"] = "http://api.fairmind.example"
	expect(t, h.run("search", "reset"), 5, "API_URL_INSECURE")
}

func TestTimeout(t *testing.T) {
	h := newHarness(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer slow.Close()
	h.env["FAIRMIND_API_URL"] = slow.URL
	r := h.run("search", "reset", "--timeout", "0.2")
	expect(t, r, 8, "TIMEOUT")
	if r.env["error"].(map[string]any)["retryable"] != true {
		t.Fatal("timeout must be retryable")
	}
}

func TestServer5xxWithoutEnvelope(t *testing.T) {
	h := newHarness(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>Bad gateway</html>"))
	}))
	defer bad.Close()
	h.env["FAIRMIND_API_URL"] = bad.URL
	r := h.run("context", "for-task", "TASK-123")
	expect(t, r, 7, "SERVICE_UNAVAILABLE")
	if r.env["error"].(map[string]any)["retryable"] != true {
		t.Fatal("5xx must be retryable")
	}
}

func TestNetworkUnavailable(t *testing.T) {
	h := newHarness(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	h.env["FAIRMIND_API_URL"] = "http://" + addr
	expect(t, h.run("search", "reset"), 7, "SERVICE_UNAVAILABLE")
}

func TestRedirectIsNotFollowed(t *testing.T) {
	h := newHarness(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/steal", http.StatusFound)
	}))
	defer redir.Close()
	h.env["FAIRMIND_API_URL"] = redir.URL
	expect(t, h.run("search", "reset"), 1, "UNEXPECTED_REDIRECT")
}

func TestNoTokenLeakInVerboseModeOrEchoedResponses(t *testing.T) {
	h := newHarness(t)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{
			"items": []any{}, "echo": r.Header.Get("Authorization")}})
	}))
	defer echo.Close()
	h.env["FAIRMIND_API_URL"] = echo.URL
	r := h.run("search", "reset", "--verbose") // run() fails the test on any leak
	expect(t, r, 0, "")
	if !strings.Contains(r.stderr, "Authorization: [REDACTED]") {
		t.Fatalf("verbose log should show a redacted Authorization header:\n%s", r.stderr)
	}
}

func TestTokenNeverInURL(t *testing.T) {
	h := newHarness(t)
	h.run("search", "reset", "--project", "demo-project")
	tok := h.env[auth.EnvVar]
	for _, req := range h.mock.Requests() {
		if strings.Contains(req.Path+req.Query+req.Body, tok) {
			t.Fatal("token must travel only in the Authorization header")
		}
		if req.Header.Get("X-FairMind-Client") == "" || req.Header.Get("X-Request-Id") == "" {
			t.Fatal("client identification headers missing")
		}
	}
}

func TestUnknownCommandAndValidation(t *testing.T) {
	h := newHarness(t)
	expect(t, h.run("frobnicate", "--json"), 5, "UNKNOWN_COMMAND")
	expect(t, h.run("context", "get"), 5, "VALIDATION_ERROR")
	expect(t, h.run("context", "for-task"), 5, "VALIDATION_ERROR")
	expect(t, h.run("context", "for-task", "../../etc"), 5, "VALIDATION_ERROR")
	expect(t, h.run("context", "get", "--intent", "x", "--budget", "10"), 5, "VALIDATION_ERROR")
}

func TestBrainGet(t *testing.T) {
	h := newHarness(t)
	r := h.run("brain", "get", "DEC-7")
	expect(t, r, 0, "")
	if r.data()["content"] == "" {
		t.Fatal("expected full content")
	}
	expect(t, h.run("brain", "get", "DEC-999"), 3, "KNOWLEDGE_NOT_FOUND")
}

// runRaw runs a command whose stdout is plain text (help, docs).
func (h *harness) runRaw(args ...string) string {
	h.t.Helper()
	var out, errb bytes.Buffer
	app := &commands.App{
		Stdin: strings.NewReader(h.stdin), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return h.env[k] }, Dir: h.dir,
		Keychain: h.kc, Git: git.Exec, Now: time.Now,
	}
	if exit := app.Run(args); exit != 0 {
		h.t.Fatalf("exit %d: %s %s", exit, out.String(), errb.String())
	}
	return out.String()
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
