// Package config loads non-sensitive CLI configuration. Credentials are never
// read from or written to config files (spec §23).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

// ProjectFile is the repository-local config path, relative to the git root.
const ProjectFile = ".fairmind/config.json"

// Settings is the resolved, non-sensitive configuration.
type Settings struct {
	Profile    string `json:"profile"`
	APIURL     string `json:"api_url"`
	MCPURL     string `json:"mcp_url,omitempty"`
	Project    string `json:"project,omitempty"`
	Repository string `json:"repository,omitempty"`
	Agent      string `json:"agent,omitempty"`
	// Sources records where each value came from (for `status`).
	Sources map[string]string `json:"sources"`
}

type fileConfig struct {
	APIURL     string `json:"api_url"`
	MCPURL     string `json:"mcp_url"`
	Project    string `json:"project"`
	Repository string `json:"repository"`
	Agent      string `json:"agent"`
}

type userFile struct {
	DefaultProfile string                `json:"default_profile"`
	Profiles       map[string]fileConfig `json:"profiles"`
}

// Overrides are values given explicitly on the command line.
type Overrides struct {
	Profile    string
	Project    string
	Repository string
}

var secretKey = regexp.MustCompile(`(?i)token|secret|password|passwd|jwt|authorization|bearer|api[_-]?key|credential|cookie`)

// Load resolves settings with precedence: flags > env > project file > user profile.
func Load(o Overrides, getenv func(string) string, repoRoot string) (*Settings, error) {
	s := &Settings{Sources: map[string]string{}}

	uf, userPath, err := loadUserFile(getenv)
	if err != nil {
		return nil, err
	}
	s.Profile = firstNonEmpty(o.Profile, getenv("FAIRMIND_PROFILE"), uf.DefaultProfile, "default")
	prof := uf.Profiles[s.Profile]
	if o.Profile != "" && uf.Profiles != nil {
		if _, ok := uf.Profiles[o.Profile]; !ok && o.Profile != "default" {
			return nil, output.NewError(output.CodeConfigInvalid,
				fmt.Sprintf("profile %q not found in %s", o.Profile, userPath), nil)
		}
	}

	var pf fileConfig
	if repoRoot != "" {
		p := filepath.Join(repoRoot, ProjectFile)
		if err := readJSON(p, &pf); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if pf.APIURL != "" {
			// A committed file must not be able to redirect the token elsewhere.
			return nil, output.NewError(output.CodeConfigInvalid,
				ProjectFile+" must not set api_url; configure it per user (profile or FAIRMIND_API_URL)", nil)
		}
		if pf.MCPURL != "" {
			return nil, output.NewError(output.CodeConfigInvalid,
				ProjectFile+" must not set mcp_url; configure it per user (profile or FAIRMIND_MCP_URL)", nil)
		}
	}

	set := func(field string, candidates ...[2]string) string {
		for _, c := range candidates {
			if c[0] != "" {
				s.Sources[field] = c[1]
				return c[0]
			}
		}
		return ""
	}
	s.APIURL = set("api_url", [2]string{getenv("FAIRMIND_API_URL"), "env"}, [2]string{prof.APIURL, "profile"})
	s.MCPURL = set("mcp_url", [2]string{getenv("FAIRMIND_MCP_URL"), "env"}, [2]string{prof.MCPURL, "profile"})
	s.Project = set("project", [2]string{o.Project, "flag"}, [2]string{getenv("FAIRMIND_PROJECT"), "env"},
		[2]string{pf.Project, "project_file"}, [2]string{prof.Project, "profile"})
	s.Repository = set("repository", [2]string{o.Repository, "flag"}, [2]string{getenv("FAIRMIND_REPOSITORY"), "env"},
		[2]string{pf.Repository, "project_file"}, [2]string{prof.Repository, "profile"})
	s.Agent = set("agent", [2]string{getenv("FAIRMIND_AGENT"), "env"}, [2]string{pf.Agent, "project_file"},
		[2]string{prof.Agent, "profile"}, [2]string{"cli", "default"})
	return s, nil
}

// DefaultMCPURL is the FairMind MCP endpoint used when nothing else is set.
const DefaultMCPURL = "https://project-context.fairmind.ai/mcp/mcp"

// ValidateMCPURL validates the MCP endpoint used by the CLI's direct-MCP mode.
func ValidateMCPURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, output.NewError(output.CodeAPIURLMissing,
			"FairMind MCP URL is not configured; set FAIRMIND_MCP_URL or mcp_url in your user profile", nil)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, output.NewError(output.CodeAPIURLInsecure, "FairMind MCP URL is not a valid absolute URL", nil)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, output.NewError(output.CodeAPIURLInsecure, "FairMind MCP URL must not contain credentials, query or fragment", nil)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, output.NewError(output.CodeAPIURLInsecure, "FairMind MCP URL must use https (http is allowed only for localhost)", nil)
		}
	default:
		return nil, output.NewError(output.CodeAPIURLInsecure, "FairMind MCP URL must use https", nil)
	}
	return u, nil
}

// ValidateAPIURL enforces https (plain http only for loopback, e.g. a local
// mock) and rejects embedded credentials, query strings and fragments.
func ValidateAPIURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, output.NewError(output.CodeAPIURLMissing,
			"FairMind API URL is not configured; set FAIRMIND_API_URL or api_url in your user profile", nil)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, output.NewError(output.CodeAPIURLInsecure, "FairMind API URL is not a valid absolute URL", nil)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, output.NewError(output.CodeAPIURLInsecure,
			"FairMind API URL must not contain credentials, query or fragment", nil)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, output.NewError(output.CodeAPIURLInsecure,
				"FairMind API URL must use https (http is allowed only for localhost)", nil)
		}
	default:
		return nil, output.NewError(output.CodeAPIURLInsecure, "FairMind API URL must use https", nil)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// UserConfigPath returns the per-user config file path.
func UserConfigPath(getenv func(string) string) string {
	if p := getenv("FAIRMIND_CONFIG"); p != "" {
		return p
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "fairmind", "config.json")
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "fairmind", "config.json")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "fairmind", "config.json")
	}
	return ""
}

func loadUserFile(getenv func(string) string) (userFile, string, error) {
	var uf userFile
	p := UserConfigPath(getenv)
	if p == "" {
		return uf, p, nil
	}
	if err := readJSON(p, &uf); err != nil && !errors.Is(err, os.ErrNotExist) {
		return uf, p, err
	}
	return uf, p, nil
}

// readJSON decodes a config file after rejecting any credential-like key.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return output.NewError(output.CodeConfigInvalid, fmt.Sprintf("%s is not valid JSON", path), nil)
	}
	if key := findSecretKey(generic); key != "" {
		return output.NewError(output.CodeConfigSecret,
			fmt.Sprintf("%s contains credential-like key %q; credentials must not be stored in config files", path, key), nil)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return output.NewError(output.CodeConfigInvalid, fmt.Sprintf("%s has an invalid structure", path), nil)
	}
	return nil
}

func findSecretKey(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if secretKey.MatchString(k) {
				return k
			}
			if found := findSecretKey(child); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range x {
			if found := findSecretKey(child); found != "" {
				return found
			}
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
