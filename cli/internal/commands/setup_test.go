package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/git"
)

func setupTestApp(dir string) *App {
	return &App{
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Getenv: func(string) string { return "" }, Dir: dir,
		Git: git.Exec, Now: time.Now,
	}
}

func TestSetupInjectsIntoRepo(t *testing.T) {
	dir := t.TempDir()
	// Pre-existing instructions must be preserved.
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "copilot-instructions.md"), []byte("# Team rules\nUse pnpm.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := setupTestApp(dir) // no token -> offline, bundled snapshot
	if code := app.Run([]string{"setup", dir, "--target", "all", "--project", "Community Pulse", "--no-refresh", "--json"}); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	for _, f := range []string{
		".github/skills/fairmind-project-context/SKILL.md",
		".github/skills/fairmind-project-context/reference/tools.md",
		".github/agents/fairmind.agent.md",
		".github/copilot-instructions.md",
		".claude/skills/fairmind-project-context/SKILL.md",
		"CLAUDE.md",
		".agents/skills/fairmind-project-context/SKILL.md",
		"AGENTS.md",
		".fairmind/config.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	instr, _ := os.ReadFile(filepath.Join(dir, ".github", "copilot-instructions.md"))
	if !strings.Contains(string(instr), "Team rules") || !strings.Contains(string(instr), "fairmind:begin") {
		t.Fatalf("instructions not merged: %s", instr)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".fairmind", "config.json"))
	if !strings.Contains(string(cfg), "Community Pulse") {
		t.Fatalf("config not written: %s", cfg)
	}

	// Idempotent: second run keeps the team content and a single managed block.
	if code := app.Run([]string{"setup", dir, "--no-refresh", "--json"}); code != 0 {
		t.Fatalf("second setup exit %d", code)
	}
	instr2, _ := os.ReadFile(filepath.Join(dir, ".github", "copilot-instructions.md"))
	if n := strings.Count(string(instr2), "fairmind:begin"); n != 1 {
		t.Fatalf("expected 1 managed block, got %d", n)
	}
	if !strings.Contains(string(instr2), "Team rules") {
		t.Fatal("team content lost on rerun")
	}
}

func TestSetupSingleTarget(t *testing.T) {
	dir := t.TempDir()
	app := setupTestApp(dir)
	if code := app.Run([]string{"setup", dir, "--target", "claude", "--no-refresh", "--json"}); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "fairmind-project-context", "SKILL.md")); err != nil {
		t.Error("claude skill missing")
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); err == nil {
		t.Error(".github should not be created for --target claude")
	}
	app2 := setupTestApp(dir)
	if code := app2.Run([]string{"setup", dir, "--target", "bogus", "--json"}); code != 5 {
		t.Fatalf("unknown target should be validation error, got exit %d", code)
	}
}
