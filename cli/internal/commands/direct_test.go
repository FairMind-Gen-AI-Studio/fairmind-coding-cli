package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
)

// fakeMCP is a minimal MCP Streamable-HTTP server for direct-mode tests.
func fakeMCP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer direct-token-123" {
			w.WriteHeader(401)
			return
		}
		var msg struct {
			ID     *int64         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &msg)
		if msg.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "s1" {
			w.WriteHeader(404)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "General_list_projects", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "Brain_record_issue", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			name, _ := msg.Params["name"].(string)
			var payload any
			switch name {
			case "General_list_projects":
				payload = []any{map[string]any{"id": "p1", "name": "Community Pulse"}}
			case "Brain_record_issue":
				payload = map[string]any{"recorded": true}
			default:
				payload = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Unknown tool"}}}
				text, _ := json.Marshal("Unknown tool " + name)
				result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": string(text)}}}
			}
			if result == nil {
				text, _ := json.Marshal(payload)
				result = map[string]any{"isError": false, "content": []any{map[string]any{"type": "text", "text": string(text)}}}
			}
		}
		resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDirectClientProjects(t *testing.T) {
	mcp := fakeMCP(t)
	dc := newDirectClient(mcp.URL, auth.NewToken("direct-token-123", "env"), "", "cli", nil)
	env, err := dc.Do(context.Background(), "GET", "/v1/projects", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var projects []map[string]any
	if e := json.Unmarshal(env.Data, &projects); e != nil || len(projects) != 1 || projects[0]["name"] != "Community Pulse" {
		t.Fatalf("unexpected projects: %s (%v)", env.Data, e)
	}
}

func TestDirectClientMissingToken(t *testing.T) {
	dc := newDirectClient("https://example/mcp", auth.Token{}, "", "cli", nil)
	if _, err := dc.Do(context.Background(), "GET", "/v1/projects", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "TOKEN_MISSING") {
		t.Fatalf("expected TOKEN_MISSING, got %v", err)
	}
}

func TestDirectClientUnknownTool(t *testing.T) {
	mcp := fakeMCP(t)
	dc := newDirectClient(mcp.URL, auth.NewToken("direct-token-123", "env"), "", "cli", nil)
	_, err := dc.Do(context.Background(), "POST", "/v1/tools/Nope:call", nil, map[string]any{"arguments": map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "TOOL_NOT_FOUND") {
		t.Fatalf("expected TOOL_NOT_FOUND, got %v", err)
	}
}

func TestDirectClientWriteHeadersPropagate(t *testing.T) {
	mcp := fakeMCP(t)
	dc := newDirectClient(mcp.URL, auth.NewToken("direct-token-123", "env"), "Community Pulse", "cli", nil)
	// Without write intent the dispatch must refuse.
	if _, err := dc.Do(context.Background(), "POST", "/v1/tools/Brain_record_issue:call", nil, map[string]any{"arguments": map[string]any{"title": "x"}}); err == nil ||
		!strings.Contains(err.Error(), "WRITE_NOT_CONFIRMED") {
		t.Fatalf("expected WRITE_NOT_CONFIRMED, got %v", err)
	}
	dc.SetHeaders(map[string]string{"X-FairMind-Write-Intent": "confirmed"})
	env, err := dc.Do(context.Background(), "POST", "/v1/tools/Brain_record_issue:call", url.Values(nil), map[string]any{"arguments": map[string]any{"title": "x"}})
	if err != nil {
		t.Fatalf("confirmed write should pass: %v", err)
	}
	if !strings.Contains(string(env.Data), "recorded") {
		t.Fatalf("unexpected result: %s", env.Data)
	}
}
