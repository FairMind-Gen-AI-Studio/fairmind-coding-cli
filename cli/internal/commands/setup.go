package commands

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/assets"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

func init() {
	register(command{path: "setup", handler: setupCmd, usage: usageSetup,
		spec: flagSpec{"target": kindList, "project": kindString, "force": kindBool, "no-refresh": kindBool}})
}

var fairmindBlock = regexp.MustCompile(`(?s)<!-- fairmind:begin.*?<!-- fairmind:end -->\r?\n?`)

// setupTarget is one supported AI tool. SKILL.md is portable; only Copilot
// consumes the custom agent file.
type setupTarget struct {
	name, skillsDir, instructions, label string
	agent                                bool
}

var setupTargets = map[string]setupTarget{
	"copilot": {name: "copilot", skillsDir: ".github/skills", instructions: ".github/copilot-instructions.md", agent: true, label: "GitHub Copilot"},
	"claude":  {name: "claude", skillsDir: ".claude/skills", instructions: "CLAUDE.md", label: "Claude Code"},
	"codex":   {name: "codex", skillsDir: ".agents/skills", instructions: "AGENTS.md", label: "OpenAI Codex"},
}
var setupOrder = []string{"copilot", "claude", "codex"}

// setupCmd injects the FairMind integration into a repository for one or more
// AI tools: the portable skill, (for Copilot) the custom agent, the managed
// instructions block, an optional .fairmind/config.json, and a tool reference.
func setupCmd(r *run) (*output.Envelope, string, error) {
	if len(r.args.pos) != 1 {
		return nil, "", output.Validationf("expected exactly one target directory, e.g. `fairmind setup .`")
	}
	dir := r.args.pos[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", output.Validationf("cannot create %s: %v", dir, err)
	}
	selected, err := selectTargets(r.args.list("target"))
	if err != nil {
		return nil, "", err
	}
	force := r.args.bools["force"]
	written := []string{}
	skillDirs := []string{}

	block, err := assets.FS.ReadFile("templates/copilot-instructions.fairmind.md")
	if err != nil {
		return nil, "", output.NewError(output.CodeInternal, "missing embedded instructions block", nil)
	}

	for _, name := range selected {
		t := setupTargets[name]
		skillDst := filepath.Join(dir, filepath.FromSlash(t.skillsDir), "fairmind-project-context")
		_ = os.RemoveAll(skillDst)
		if err := copyTree("templates/skills/fairmind-project-context", skillDst); err != nil {
			return nil, "", err
		}
		skillDirs = append(skillDirs, skillDst)
		written = append(written, "["+t.label+"] "+t.skillsDir+"/fairmind-project-context/")
		if t.agent {
			if err := copyFile("templates/agents/fairmind.agent.md", filepath.Join(dir, ".github", "agents", "fairmind.agent.md")); err != nil {
				return nil, "", err
			}
			written = append(written, "["+t.label+"] .github/agents/fairmind.agent.md")
		}
		note := writeInstructions(filepath.Join(dir, filepath.FromSlash(t.instructions)), string(block))
		written = append(written, "["+t.label+"] "+t.instructions+" ("+note+")")
	}

	// Optional project default (no secrets; safe to commit), shared by all tools.
	if project := r.args.str("project"); project != "" {
		cfgPath := filepath.Join(dir, ".fairmind", "config.json")
		if _, statErr := os.Stat(cfgPath); statErr == nil && !force {
			written = append(written, ".fairmind/config.json (kept; use --force to overwrite)")
		} else {
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
				return nil, "", output.Validationf("cannot create .fairmind: %v", err)
			}
			b, _ := json.MarshalIndent(map[string]string{"project": project}, "", "  ")
			b = append(b, '\n')
			if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
				return nil, "", output.Validationf("cannot write .fairmind/config.json: %v", err)
			}
			written = append(written, ".fairmind/config.json (project: "+project+")")
		}
	}

	// Refresh the tool reference from the live catalog into every skill dir;
	// otherwise the embedded snapshot copied above remains.
	toolsNote := "bundled snapshot"
	if !r.args.bools["no-refresh"] {
		if err := r.loadContext(false); err == nil && !r.token.Empty() {
			if tools, err := r.catalog(true); err == nil {
				md := []byte(toolsMarkdown(tools, r.app.Now()))
				toolsNote = "live catalog"
				for _, d := range skillDirs {
					if err := os.WriteFile(filepath.Join(d, "reference", "tools.md"), md, 0o644); err != nil {
						toolsNote = "bundled snapshot"
					}
				}
			}
		}
	}
	written = append(written, "tool reference ("+toolsNote+")")

	env, err := output.Success(map[string]any{
		"target":  dir,
		"targets": selected,
		"written": written,
		"tools":   toolsNote,
	})
	return env, "", err
}

// selectTargets resolves the --target flag (default: all).
func selectTargets(flags []string) ([]string, error) {
	if len(flags) == 0 {
		return setupOrder, nil
	}
	want := map[string]bool{}
	for _, f := range flags {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "all" {
			return setupOrder, nil
		}
		if _, ok := setupTargets[f]; !ok {
			return nil, output.Validationf("unknown --target %q (use copilot, claude, codex or all)", f)
		}
		want[f] = true
	}
	var out []string
	for _, n := range setupOrder {
		if want[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

// writeInstructions inserts or refreshes only the managed FairMind block.
func writeInstructions(path, block string) string {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "error"
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		_ = os.WriteFile(path, []byte(block), 0o644)
		return "created"
	}
	if fairmindBlock.Match(existing) {
		_ = os.WriteFile(path, fairmindBlock.ReplaceAll(existing, []byte(block)), 0o644)
		return "FairMind block refreshed"
	}
	merged := string(existing)
	if !strings.HasSuffix(merged, "\n") {
		merged += "\n"
	}
	merged += "\n" + block
	_ = os.WriteFile(path, []byte(merged), 0o644)
	return "FairMind block appended, existing content kept"
}

func copyTree(embedDir, dst string) error {
	return fs.WalkDir(assets.FS, embedDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(embedDir, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		return copyFile(p, out)
	})
}

func copyFile(embedPath, dst string) error {
	b, err := assets.FS.ReadFile(embedPath)
	if err != nil {
		return output.NewError(output.CodeInternal, "missing embedded asset: "+embedPath, nil)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return output.Validationf("cannot create %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return output.Validationf("cannot write %s: %v", dst, err)
	}
	return nil
}
