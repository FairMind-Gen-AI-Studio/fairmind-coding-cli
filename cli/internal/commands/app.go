// Package commands implements the fairmind command tree.
package commands

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/api"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/config"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/git"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

// DefaultTimeout applies when --timeout / FAIRMIND_TIMEOUT are not set.
const DefaultTimeout = 30 * time.Second

// App holds process dependencies so tests can run commands in-process.
type App struct {
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Getenv   func(string) string
	Dir      string
	Keychain auth.Keychain
	Git      git.Runner
	HTTP     *http.Client
	Now      func() time.Time
	// StdoutIsTTY selects human output when --json is not given.
	StdoutIsTTY bool
	StdinIsTTY  bool
}

// NewApp wires the real process environment.
func NewApp() *App {
	dir, _ := os.Getwd()
	return &App{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv: os.Getenv, Dir: dir, Keychain: auth.SystemKeychain{},
		Git: git.Exec, Now: time.Now,
		StdoutIsTTY: isTerminal(os.Stdout), StdinIsTTY: isTerminal(os.Stdin),
	}
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// run is the per-invocation state shared by command handlers.
type run struct {
	app     *App
	args    *parsed
	printer *output.Printer
	red     *output.Redactor
	verbose bool
	timeout time.Duration

	settings *config.Settings
	repo     *git.Repo
	token    auth.Token
}

type handler func(r *run) (*output.Envelope, string, error)

type command struct {
	path    string
	spec    flagSpec
	handler handler
	usage   string
}

var commandTable []command

// toolNamespaces are the FairMind tool families exposed as dynamic commands
// (`fairmind <namespace> <command>`), generated from the live tool catalog.
var toolNamespaces = map[string]bool{"brain": true, "code": true, "general": true, "insights": true, "studio": true}

func register(c command) { commandTable = append(commandTable, c) }

// Run executes one CLI invocation and returns the process exit code.
func (a *App) Run(argv []string) int {
	red := &output.Redactor{}
	pr := &output.Printer{Stdout: a.Stdout, Stderr: a.Stderr, Redactor: red,
		JSON: !a.StdoutIsTTY || strings.EqualFold(a.Getenv("FAIRMIND_OUTPUT"), "json")}

	cmd, rest := lookup(argv)
	if cmd == nil {
		if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" || argv[0] == "-h" {
			pr.Raw(rootUsage)
			return output.ExitOK
		}
		if toolNamespaces[argv[0]] {
			return a.runDynamic(pr, red, argv)
		}
		if hasFlag(argv, "--json") {
			pr.JSON = true
		}
		return a.fail(pr, output.NewError(output.CodeUnknownCommand, "unknown command: "+strings.Join(leadingWords(argv), " ")+"; run `fairmind help`", nil))
	}

	p, err := parseArgs(rest, cmd.spec)
	if err != nil {
		if hasFlag(argv, "--json") {
			pr.JSON = true
		}
		return a.fail(pr, err)
	}
	if p.bools["json"] {
		pr.JSON = true
	}
	if p.bools["help"] {
		pr.Raw(cmd.usage)
		return output.ExitOK
	}

	r := &run{app: a, args: p, printer: pr, red: red, verbose: p.bools["verbose"]}
	if r.timeout, err = parseTimeout(firstNonEmpty(p.str("timeout"), a.Getenv("FAIRMIND_TIMEOUT"))); err != nil {
		return a.fail(pr, err)
	}
	if p.bools["dry-run"] {
		// Phase 1 commands are read-only; the flag is accepted for contract stability.
		r.logf("--dry-run has no effect on read-only commands")
	}

	env, kind, err := cmd.handler(r)
	if err != nil {
		return a.fail(pr, err)
	}
	if env != nil { // nil: the handler already wrote raw output (e.g. tools docs)
		pr.Envelope(env, kind)
	}
	return output.ExitOK
}

func (a *App) fail(pr *output.Printer, err error) int {
	var ce *output.CLIError
	if !errors.As(err, &ce) {
		ce = output.NewError(output.CodeInternal, err.Error(), nil)
	}
	pr.Envelope(output.Failure(ce.Err), "")
	return ce.Exit
}

func lookup(argv []string) (*command, []string) {
	words := leadingWords(argv)
	var best *command
	bestLen := 0
	for i := range commandTable {
		c := &commandTable[i]
		parts := strings.Fields(c.path)
		if len(parts) <= len(words) && len(parts) > bestLen && equalWords(parts, words[:len(parts)]) {
			best, bestLen = c, len(parts)
		}
	}
	if best == nil {
		return nil, nil
	}
	// Command words are always the leading tokens.
	rest := append([]string(nil), argv[bestLen:]...)
	return best, rest
}

// leadingWords returns non-flag tokens up to the first flag, e.g. "context get".
func leadingWords(argv []string) []string {
	var w []string
	for _, a := range argv {
		if strings.HasPrefix(a, "-") {
			break
		}
		w = append(w, a)
	}
	return w
}

func equalWords(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasFlag(argv []string, f string) bool {
	for _, a := range argv {
		if a == f || strings.HasPrefix(a, f+"=") {
			return true
		}
	}
	return false
}

func parseTimeout(s string) (time.Duration, error) {
	if s == "" {
		return DefaultTimeout, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || f > 600 {
		return 0, output.Validationf("--timeout must be a number of seconds between 0 and 600")
	}
	return time.Duration(f * float64(time.Second)), nil
}

func (r *run) logf(format string, a ...any) {
	if r.verbose {
		r.printer.Logf("[fairmind] "+format, a...)
	}
}

// loadContext resolves settings, repository identity and token.
func (r *run) loadContext(needRepo bool) error {
	repo, err := git.Detect(r.app.Git, r.app.Dir)
	switch {
	case err == nil:
		r.repo = repo
	case errors.Is(err, git.ErrNotRepo):
		if needRepo {
			return output.NewError(output.CodeNotGitRepo, "this command must run inside a git repository", nil)
		}
	default:
		if needRepo {
			return output.NewError(output.CodeGit, err.Error(), nil)
		}
		r.logf("git detection skipped: %v", err)
	}
	root := ""
	if r.repo != nil {
		root = r.repo.Root
	} else {
		root = findConfigDir(r.app.Dir)
	}
	r.settings, err = config.Load(config.Overrides{
		Profile: r.args.str("profile"), Project: r.args.str("project"), Repository: r.args.str("repo"),
	}, r.app.Getenv, root)
	if err != nil {
		return err
	}
	r.token, err = auth.Resolve(r.settings.Profile, r.app.Getenv, r.app.Keychain)
	if err != nil {
		return output.NewError(output.CodeKeychain, "could not read the OS credential store: "+err.Error(), nil)
	}
	r.red.AddSecret(r.token.Value())
	return nil
}

// endpoint identifies the target for the tool-catalog cache key.
func (r *run) endpoint() string {
	if r.settings.APIURL != "" {
		return r.settings.APIURL
	}
	if r.settings.MCPURL != "" {
		return r.settings.MCPURL
	}
	return "direct:" + config.DefaultMCPURL
}

func (r *run) client() (apiDoer, error) {
	var logf func(string, ...any)
	if r.verbose {
		logf = r.logf
	}
	if r.token.Empty() {
		return nil, output.NewError(output.CodeTokenMissing,
			"no FairMind token found; run `fairmind auth login` or set "+auth.EnvVar, nil)
	}
	// A server-side Agent API is used only when api_url is explicitly set.
	// Otherwise the CLI talks to the FairMind MCP server directly (no devbridge).
	if r.settings.APIURL != "" {
		base, err := config.ValidateAPIURL(r.settings.APIURL)
		if err != nil {
			return nil, err
		}
		c := &api.Client{BaseURL: base, Token: r.token, Project: r.settings.Project,
			Agent: r.settings.Agent, Timeout: r.timeout, HTTP: r.app.HTTP, Logf: logf}
		return c, nil
	}
	mcp := r.settings.MCPURL
	if mcp == "" {
		mcp = config.DefaultMCPURL
	}
	u, err := config.ValidateMCPURL(mcp)
	if err != nil {
		return nil, err
	}
	return newDirectClient(u.String(), r.token, r.settings.Project, r.settings.Agent, logf), nil
}

// call performs one API request.
func (r *run) call(method, path string, query map[string][]string, body any) (*output.Envelope, error) {
	return r.callWith(nil, method, path, query, body)
}

// callWith performs one API request with extra headers.
func (r *run) callWith(headers map[string]string, method, path string, query map[string][]string, body any) (*output.Envelope, error) {
	c, err := r.client()
	if err != nil {
		return nil, err
	}
	c.SetHeaders(headers)
	return c.Do(context.Background(), method, path, query, body)
}

// repositoryRef is what the server uses to resolve the repository binding:
// explicit --repo, then local git remote, then configured default (spec §22).
type repositoryRef struct {
	Explicit   string `json:"explicit,omitempty"`
	Remote     string `json:"remote,omitempty"`
	Branch     string `json:"branch,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
	Configured string `json:"configured,omitempty"`
}

func (r *run) repositoryRef() *repositoryRef {
	ref := &repositoryRef{}
	switch r.settings.Sources["repository"] {
	case "flag", "env":
		ref.Explicit = r.settings.Repository
	default:
		ref.Configured = r.settings.Repository
	}
	if r.repo != nil {
		ref.Remote, ref.Branch, ref.HeadCommit = r.repo.Remote, r.repo.Branch, r.repo.HeadCommit
	}
	if *ref == (repositoryRef{}) {
		return nil
	}
	return ref
}

// findConfigDir looks for .fairmind/config.json from dir upwards (used outside
// git repositories).
func findConfigDir(dir string) string {
	for d := dir; d != "" && d != string(filepath.Separator); d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, config.ProjectFile)); err == nil && !st.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
