package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	r := &Redactor{}
	r.AddSecret("opaque-api-token-value")
	in := `{"a":"Bearer abc.def","jwt":"eyJhbGciOiJub25lIn0.eyJzdWIiOiJ4In0.sig","raw":"opaque-api-token-value","password":"hunter2","k":"x\"y"}`
	out := r.Redact(in)
	for _, leak := range []string{"abc.def", "eyJhbGciOiJub25lIn0", "opaque-api-token-value", "hunter2"} {
		if strings.Contains(out, leak) {
			t.Errorf("redacted output still contains %q: %s", leak, out)
		}
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("redaction broke JSON: %s", out)
	}
}

func TestRedactKeepsOrdinaryText(t *testing.T) {
	r := &Redactor{}
	in := `{"requested_tokens": 8000, "title": "Password reset tokens expire"}`
	if out := r.Redact(in); out != in {
		t.Errorf("unexpected change: %s", out)
	}
}

func TestExitMapping(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"TOKEN_EXPIRED", ExitAuth}, {"token_invalid", ExitAuth}, {"SCOPE_REQUIRED", ExitForbidden},
		{"TASK_NOT_FOUND", ExitNotFound}, {"repository_not_found", ExitNotFound},
		{"INVALID_STATE_TRANSITION", ExitConflict}, {"REPOSITORY_AMBIGUOUS", ExitValidation},
		{"TIMEOUT", ExitTimeout}, {"SERVICE_UNAVAILABLE", ExitUnavailable},
	}
	for _, c := range cases {
		if got, ok := ExitForCode(c.code); !ok || got != c.want {
			t.Errorf("ExitForCode(%s) = %d,%v want %d", c.code, got, ok, c.want)
		}
	}
	for status, want := range map[int]int{401: 2, 403: 4, 404: 3, 422: 5, 409: 6, 500: 7, 503: 7, 504: 8, 429: 7, 418: 1} {
		if got := ExitForStatus(status); got != want {
			t.Errorf("ExitForStatus(%d) = %d want %d", status, got, want)
		}
	}
}

func TestWarningAcceptsStringOrObject(t *testing.T) {
	var ws []Warning
	if err := json.Unmarshal([]byte(`["plain", {"code":"X","message":"m"}]`), &ws); err != nil {
		t.Fatal(err)
	}
	if ws[0].Message != "plain" || ws[1].Code != "X" {
		t.Fatalf("%+v", ws)
	}
}
