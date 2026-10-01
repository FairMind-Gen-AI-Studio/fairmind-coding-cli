package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

// catalogTTL bounds how long the local tool catalog cache is trusted.
const catalogTTL = 24 * time.Hour

// Access classes published by the Agent API for each tool.
const (
	accessRead        = "read"
	accessWrite       = "write"
	accessDestructive = "destructive"
	accessSensitive   = "sensitive"
)

// toolInfo is one entry of GET /v1/tools. The CLI treats the catalog as data:
// it contains no FairMind logic, only names, descriptions and input schemas.
type toolInfo struct {
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace"`
	Command     string          `json:"command"`
	Description string          `json:"description"`
	Access      string          `json:"access"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type catalogFile struct {
	FetchedAt time.Time  `json:"fetched_at"`
	APIURL    string     `json:"api_url"`
	Tools     []toolInfo `json:"tools"`
}

func (r *run) cachePath() string {
	base := r.app.Getenv("XDG_CACHE_HOME")
	if base == "" {
		if home := r.app.Getenv("HOME"); home != "" {
			base = filepath.Join(home, ".cache")
		} else if dir, err := os.UserCacheDir(); err == nil { // Windows: %LocalAppData%
			base = dir
		} else {
			return ""
		}
	}
	return filepath.Join(base, "fairmind", "tools-"+r.settings.Profile+".json")
}

// catalog returns the tool catalog, from a fresh local cache when possible.
func (r *run) catalog(refresh bool) ([]toolInfo, error) {
	path := r.cachePath()
	if !refresh && path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var cf catalogFile
			if json.Unmarshal(b, &cf) == nil && cf.APIURL == r.endpoint() &&
				r.app.Now().Sub(cf.FetchedAt) < catalogTTL && len(cf.Tools) > 0 {
				return cf.Tools, nil
			}
		}
	}
	q := map[string][]string{}
	if refresh {
		q["refresh"] = []string{"true"}
	}
	env, err := r.call("GET", "/v1/tools", q, nil)
	if err != nil {
		return nil, err
	}
	var data struct {
		Tools []toolInfo `json:"tools"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || len(data.Tools) == 0 {
		return nil, output.NewError(output.CodeInvalidResponse, "the FairMind API returned an empty or invalid tool catalog", nil)
	}
	sort.Slice(data.Tools, func(i, j int) bool { return data.Tools[i].Name < data.Tools[j].Name })
	if path != "" {
		if b, err := json.Marshal(catalogFile{FetchedAt: r.app.Now(), APIURL: r.endpoint(), Tools: data.Tools}); err == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			_ = os.WriteFile(path, b, 0o600)
		}
	}
	return data.Tools, nil
}

func findTool(tools []toolInfo, nameOrCommand string) *toolInfo {
	for i := range tools {
		if tools[i].Name == nameOrCommand || tools[i].Command == nameOrCommand {
			return &tools[i]
		}
	}
	for i := range tools { // case-insensitive tool name
		if strings.EqualFold(tools[i].Name, nameOrCommand) {
			return &tools[i]
		}
	}
	return nil
}

// ---- JSON-schema parameters ----

type param struct {
	Name        string   `json:"name"`
	Flag        string   `json:"flag"`
	Type        string   `json:"type"` // string|integer|number|boolean|array|object|any
	ItemType    string   `json:"item_type,omitempty"`
	Required    bool     `json:"required"`
	Nullable    bool     `json:"nullable,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
}

type schemaNode struct {
	Type        json.RawMessage `json:"type"`
	AnyOf       []schemaNode    `json:"anyOf"`
	OneOf       []schemaNode    `json:"oneOf"`
	Items       *schemaNode     `json:"items"`
	Enum        []any           `json:"enum"`
	Default     any             `json:"default"`
	Description string          `json:"description"`
}

func (n schemaNode) types() []string {
	var one string
	if json.Unmarshal(n.Type, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(n.Type, &many)
	return many
}

// resolve collapses anyOf/oneOf/type arrays to one primary type.
func (n schemaNode) resolve() (typ string, nullable bool, item string, enum []string) {
	cands := append([]schemaNode{n}, append(n.AnyOf, n.OneOf...)...)
	for _, c := range cands {
		for _, t := range c.types() {
			if t == "null" {
				nullable = true
				continue
			}
			if typ == "" {
				typ = t
				if t == "array" && c.Items != nil {
					item, _, _, _ = c.Items.resolve()
				}
			}
		}
		for _, e := range c.Enum {
			if s, ok := e.(string); ok {
				enum = append(enum, s)
			}
		}
	}
	// "string or array" parameters (e.g. kinds) are sent as arrays so that
	// comma-separated values keep their meaning.
	for _, c := range cands {
		for _, t := range c.types() {
			if t == "array" && typ != "array" {
				typ = "array"
				if c.Items != nil {
					item, _, _, _ = c.Items.resolve()
				}
			}
		}
	}
	if typ == "" {
		typ = "any"
	}
	return typ, nullable, item, enum
}

// params lists a tool's parameters: required ones first (schema order), then optional ones alphabetically.
func (t *toolInfo) params() []param {
	var s struct {
		Properties map[string]schemaNode `json:"properties"`
		Required   []string              `json:"required"`
	}
	_ = json.Unmarshal(t.InputSchema, &s)
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	var out []param
	add := func(name string) {
		n := s.Properties[name]
		typ, nullable, item, enum := n.resolve()
		out = append(out, param{Name: name, Flag: flagName(name), Type: typ, ItemType: item, Required: req[name],
			Nullable: nullable, Enum: enum, Default: n.Default, Description: strings.TrimSpace(n.Description)})
	}
	for _, r := range s.Required {
		if _, ok := s.Properties[r]; ok {
			add(r)
		}
	}
	var optional []string
	for name := range s.Properties {
		if !req[name] {
			optional = append(optional, name)
		}
	}
	sort.Strings(optional)
	for _, name := range optional {
		add(name)
	}
	return out
}

func flagName(param string) string { return strings.ReplaceAll(param, "_", "-") }
