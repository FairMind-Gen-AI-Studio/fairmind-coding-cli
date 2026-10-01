package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Acme/backend-api.git":                 "github.com/Acme/backend-api",
		"https://user:ghp_secret@GitHub.com/acme/backend-api.git": "github.com/acme/backend-api",
		"git@github.com:acme/backend-api.git":                     "github.com/acme/backend-api",
		"ssh://git@github.example.com:2222/acme/backend-api.git":  "github.example.com/acme/backend-api",
		"https://dev.azure.com/org/project/_git/repo":             "dev.azure.com/org/project/_git/repo",
		"https://oauth2:tok@gitlab.com/group/sub/repo/":           "gitlab.com/group/sub/repo",
		"/local/path/repo.git":                                    "",
		"":                                                        "",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSymbol(t *testing.T) {
	cases := map[string]string{
		"export function confirmReset(token: string) {":    "confirmReset",
		"func (s *Server) Handle(w http.ResponseWriter) {": "Handle",
		"def reset_password(user):":                        "reset_password",
		"class TokenStore {":                               "TokenStore",
		"const handler = async (req, res) => {":            "handler",
		"public void resetPassword(String email) {":        "resetPassword",
		"if (x) {": "",
		"":         "",
	}
	for in, want := range cases {
		if got := ExtractSymbol(in); got != want {
			t.Errorf("ExtractSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiffMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "src/a.ts"), "export function alpha() {\n  return 1\n}\n")
	write(t, filepath.Join(dir, "src/old.ts"), "export const x = 1\nexport const y = 2\nexport const z = 3\n")
	write(t, filepath.Join(dir, "src/gone.ts"), "bye\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-qm", "init")

	write(t, filepath.Join(dir, "src/a.ts"), "export function alpha() {\n  return 2\n}\n")
	gitCmd(t, dir, "mv", "src/old.ts", "src/new.ts")
	gitCmd(t, dir, "rm", "-q", "src/gone.ts")
	write(t, filepath.Join(dir, "src/untracked.ts"), "SECRET_SOURCE_CONTENT\n")

	changes, err := DiffMetadata(Exec, dir, DiffOptions{Symbols: true})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileChange{}
	for _, c := range changes {
		byPath[c.Path] = c
	}
	if c := byPath["src/a.ts"]; c.Status != "modified" || c.Additions == nil || *c.Additions != 1 || len(c.Hunks) != 1 || len(c.Symbols) != 1 || c.Symbols[0] != "alpha" {
		t.Errorf("modified file metadata wrong: %+v", c)
	}
	if c := byPath["src/new.ts"]; c.Status != "renamed" || c.OldPath != "src/old.ts" {
		t.Errorf("rename metadata wrong: %+v", c)
	}
	if c := byPath["src/gone.ts"]; c.Status != "deleted" {
		t.Errorf("delete metadata wrong: %+v", c)
	}
	if c := byPath["src/untracked.ts"]; c.Status != "untracked" {
		t.Errorf("untracked metadata wrong: %+v", c)
	}
}

func TestDiffMetadataNoCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	gitCmd(t, dir, "add", ".")
	changes, err := DiffMetadata(Exec, dir, DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Status != "added" {
		t.Fatalf("unexpected changes: %+v", changes)
	}
}

func TestRelToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := RelToRoot(root, filepath.Join(root, "src"), "auth.ts")
	if err != nil || got != "src/auth.ts" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := RelToRoot(root, root, "../outside.ts"); err == nil {
		t.Fatal("expected error for path outside repository")
	}
}

func TestDiffMetadataRejectsOptionLikeBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.txt"), "x\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-qm", "init")
	for _, bad := range []string{"--output=/tmp/pwn", "-ext-diff", "--upload-pack=x"} {
		if _, err := DiffMetadata(Exec, dir, DiffOptions{Base: bad}); err == nil {
			t.Errorf("base %q must be rejected", bad)
		}
	}
}
