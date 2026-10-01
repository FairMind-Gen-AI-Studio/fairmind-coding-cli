package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrecedence(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeFile(t, filepath.Join(home, ".config/fairmind/config.json"),
		`{"profiles":{"default":{"api_url":"https://api.example.com","project":"from-profile"}}}`)
	writeFile(t, filepath.Join(repo, ProjectFile), `{"project":"from-file","repository":"backend-api"}`)

	s, err := Load(Overrides{}, envFrom(map[string]string{"HOME": home}), repo)
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "from-file" || s.Sources["project"] != "project_file" || s.APIURL != "https://api.example.com" {
		t.Fatalf("unexpected: %+v", s)
	}
	s, _ = Load(Overrides{Project: "from-flag"}, envFrom(map[string]string{"HOME": home, "FAIRMIND_PROJECT": "from-env"}), repo)
	if s.Project != "from-flag" {
		t.Fatalf("flag must win: %+v", s)
	}
}

func TestRejectsSecretsInConfig(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ProjectFile), `{"project":"p","token":"eyJ..."}`)
	_, err := Load(Overrides{}, envFrom(map[string]string{"HOME": t.TempDir()}), repo)
	var ce *output.CLIError
	if !errors.As(err, &ce) || ce.Err.Code != output.CodeConfigSecret {
		t.Fatalf("expected CONFIG_CONTAINS_SECRET, got %v", err)
	}
}

func TestProjectFileCannotRedirectAPI(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ProjectFile), `{"api_url":"https://evil.example.com"}`)
	if _, err := Load(Overrides{}, envFrom(map[string]string{"HOME": t.TempDir()}), repo); err == nil {
		t.Fatal("expected error: repository config must not set api_url")
	}
}

func TestValidateAPIURL(t *testing.T) {
	ok := []string{"https://api.fairmind.example", "http://127.0.0.1:8787", "http://localhost:1/base/"}
	bad := []string{"", "http://api.fairmind.example", "ftp://x", "https://user:pw@api.example.com", "https://api.example.com?x=1", "not a url"}
	for _, u := range ok {
		if _, err := ValidateAPIURL(u); err != nil {
			t.Errorf("%s should be accepted: %v", u, err)
		}
	}
	for _, u := range bad {
		if _, err := ValidateAPIURL(u); err == nil {
			t.Errorf("%s should be rejected", u)
		}
	}
}
