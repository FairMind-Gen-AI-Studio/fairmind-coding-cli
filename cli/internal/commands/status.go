package commands

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/config"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/version"
)

func init() {
	register(command{path: "version", handler: versionCmd, usage: usageVersion, spec: flagSpec{}})
	register(command{path: "status", handler: statusCmd, usage: usageStatus, spec: flagSpec{"offline": kindBool}})
	register(command{path: "auth status", handler: authStatus, usage: usageAuthStatus, spec: flagSpec{"offline": kindBool}})
	register(command{path: "auth login", handler: authLogin, usage: usageAuthLogin, spec: flagSpec{}})
	register(command{path: "auth logout", handler: authLogout, usage: usageAuthLogout, spec: flagSpec{}})
}

func versionCmd(r *run) (*output.Envelope, string, error) {
	env, err := output.Success(map[string]string{
		"version": version.Version, "commit": version.Commit, "api_contract": version.APIContract,
	})
	return env, "", err
}

type serverCheck struct {
	Checked         bool          `json:"checked"`
	OK              bool          `json:"ok"`
	LatencyMS       int64         `json:"latency_ms,omitempty"`
	ProjectsVisible *int          `json:"projects_visible,omitempty"`
	Error           *output.Error `json:"error,omitempty"`
}

// checkServer calls GET /v1/projects to prove the token is accepted.
func (r *run) checkServer() (serverCheck, error) {
	start := time.Now()
	env, err := r.call("GET", "/v1/projects", nil, nil)
	sc := serverCheck{Checked: true, LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		return sc, err
	}
	sc.OK = true
	var list []json.RawMessage
	if json.Unmarshal(env.Data, &list) != nil {
		var wrapped struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(env.Data, &wrapped)
		list = wrapped.Items
	}
	n := len(list)
	sc.ProjectsVisible = &n
	return sc, nil
}

type tokenInfo struct {
	Present bool         `json:"present"`
	Source  string       `json:"source,omitempty"`
	Claims  *auth.Claims `json:"claims,omitempty"`
	// ClaimsVerified is always false: claims are decoded locally for display
	// only; the server is the sole authority on validity and scope.
	ClaimsVerified bool   `json:"claims_verified"`
	Note           string `json:"note,omitempty"`
}

func (r *run) tokenInfo() tokenInfo {
	ti := tokenInfo{Present: !r.token.Empty(), Source: r.token.Source}
	if ti.Present {
		if c, ok := auth.PeekClaims(r.token, r.app.Now()); ok {
			ti.Claims = &c
		} else {
			ti.Note = "token is not JWT-shaped; the server will decide whether it is valid"
		}
	}
	return ti
}

func authStatus(r *run) (*output.Envelope, string, error) {
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	if r.token.Empty() {
		return nil, "", output.NewError(output.CodeTokenMissing,
			"no FairMind token found; run `fairmind auth login` or set "+auth.EnvVar, nil)
	}
	data := map[string]any{"profile": r.settings.Profile, "token": r.tokenInfo()}
	if r.args.bools["offline"] {
		data["server"] = serverCheck{}
	} else {
		sc, err := r.checkServer()
		if err != nil {
			return nil, "", err
		}
		data["server"] = sc
	}
	env, err := output.Success(data)
	return env, "", err
}

func statusCmd(r *run) (*output.Envelope, string, error) {
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	data := map[string]any{
		"version":  version.Version,
		"settings": r.settings,
		"token":    r.tokenInfo(),
	}
	if r.repo != nil {
		data["repository"] = map[string]string{
			"remote": r.repo.Remote, "branch": r.repo.Branch, "head_commit": r.repo.HeadCommit,
		}
	} else {
		data["repository"] = nil
	}
	// Endpoint: a server-side Agent API (api_url) if configured, else the
	// FairMind MCP server called directly (no devbridge).
	var apiInfo map[string]any
	var validateErr error
	if r.settings.APIURL != "" {
		apiInfo = map[string]any{"mode": "agent-api", "url": r.settings.APIURL}
		_, validateErr = config.ValidateAPIURL(r.settings.APIURL)
	} else {
		mcp := r.settings.MCPURL
		if mcp == "" {
			mcp = config.DefaultMCPURL
		}
		apiInfo = map[string]any{"mode": "direct-mcp", "url": mcp}
		_, validateErr = config.ValidateMCPURL(mcp)
	}
	if validateErr != nil {
		var ce *output.CLIError
		errors.As(validateErr, &ce)
		apiInfo["valid"] = false
		apiInfo["error"] = ce.Err
	} else {
		apiInfo["valid"] = true
		if !r.args.bools["offline"] && !r.token.Empty() {
			sc, err := r.checkServer()
			if err != nil {
				var ce *output.CLIError
				if errors.As(err, &ce) {
					sc.Error = &ce.Err
				}
			}
			apiInfo["check"] = sc
		}
	}
	data["api"] = apiInfo
	env, err := output.Success(data)
	return env, "", err
}

func authLogin(r *run) (*output.Envelope, string, error) {
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	token, err := readSecret(r)
	if err != nil {
		return nil, "", err
	}
	r.red.AddSecret(token)
	if token == "" || strings.ContainsAny(token, " \t") || len(token) > 16384 {
		return nil, "", output.Validationf("the token read from stdin is empty or malformed")
	}
	if err := r.app.Keychain.Set(r.settings.Profile, token); err != nil {
		return nil, "", output.NewError(output.CodeKeychain, err.Error(), nil)
	}
	data := map[string]any{"stored": true, "profile": r.settings.Profile, "source": auth.SourceKeychain}
	if r.app.Getenv(auth.EnvVar) != "" {
		data["warning"] = auth.EnvVar + " is set and takes precedence over the stored credential"
	}
	env, err := output.Success(data)
	return env, "", err
}

func authLogout(r *run) (*output.Envelope, string, error) {
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	err := r.app.Keychain.Delete(r.settings.Profile)
	if err != nil && !errors.Is(err, auth.ErrNotFound) {
		return nil, "", output.NewError(output.CodeKeychain, err.Error(), nil)
	}
	env, _ := output.Success(map[string]any{"deleted": err == nil, "profile": r.settings.Profile})
	return env, "", nil
}

// readSecret reads one line from stdin; on a terminal it disables echo.
func readSecret(r *run) (string, error) {
	in := r.app.Stdin
	if r.app.StdinIsTTY {
		_, _ = io.WriteString(r.app.Stderr, "Paste your FairMind token (input hidden) and press Enter: ")
		if f, ok := in.(*os.File); ok {
			if setEcho(f, false) == nil {
				defer func() {
					_ = setEcho(f, true)
					_, _ = io.WriteString(r.app.Stderr, "\n")
				}()
			}
		}
	}
	line, err := bufio.NewReader(io.LimitReader(in, 16385)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", output.Validationf("could not read token from stdin")
	}
	return strings.TrimSpace(line), nil
}

func setEcho(f *os.File, on bool) error {
	arg := "-echo"
	if on {
		arg = "echo"
	}
	cmd := exec.Command("stty", arg)
	cmd.Stdin = f
	return cmd.Run()
}
