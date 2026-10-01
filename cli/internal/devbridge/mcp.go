// Package devbridge is a DEVELOPMENT-ONLY local implementation of the
// proposed Agent API v1 on top of the existing FairMind MCP server. It lets the
// fairmind CLI run against real FairMind data before the server-side Agent API
// exists. It is NOT part of the CLI, must never be shipped to customers, and
// its orchestration is a throwaway prototype for the backend team: the real
// orchestration, ranking and budgeting belong server-side (spec §2, §46).
package devbridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	protocolVersion = "2025-06-18"
	sessionTTL      = 10 * time.Minute
	maxMCPResponse  = 16 << 20
)

// MCPClient speaks MCP Streamable HTTP to one server. It never stores tokens:
// each call uses the caller's Authorization header; sessions are cached by a
// hash of that header.
type MCPClient struct {
	URL  string
	HTTP *http.Client

	nextID   atomic.Int64
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	id      string
	version string
	created time.Time
}

// UpstreamError is an HTTP-level failure from the MCP server.
type UpstreamError struct {
	Status int
	Body   string
}

func (e *UpstreamError) Error() string { return fmt.Sprintf("MCP server HTTP %d", e.Status) }

// ToolError is a tool result with isError=true.
type ToolError struct {
	Tool    string
	Message string
}

func (e *ToolError) Error() string { return e.Tool + ": " + e.Message }

func NewMCPClient(url string) *MCPClient {
	return &MCPClient{URL: url, sessions: map[string]*session{},
		HTTP: &http.Client{Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func authKey(auth string) string {
	h := sha256.Sum256([]byte(auth))
	return hex.EncodeToString(h[:])
}

// CallTool invokes an MCP tool and returns its JSON payload (structured
// content if present, else the first text block parsed as JSON, else the
// text as a JSON string).
func (c *MCPClient) CallTool(ctx context.Context, auth, tool string, args map[string]any) (json.RawMessage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		s, err := c.session(ctx, auth, attempt > 0)
		if err != nil {
			return nil, err
		}
		res, err := c.rpc(ctx, auth, s, "tools/call", map[string]any{"name": tool, "arguments": args})
		var ue *UpstreamError
		if errors.As(err, &ue) && (ue.Status == 404 || (ue.Status == 400 && strings.Contains(strings.ToLower(ue.Body), "session"))) && attempt == 0 {
			continue // session expired server-side: re-initialize once
		}
		if err != nil {
			return nil, err
		}
		return decodeToolResult(tool, res)
	}
	return nil, errors.New("MCP session could not be established")
}

func decodeToolResult(tool string, raw json.RawMessage) (json.RawMessage, error) {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("%s: undecodable tool result", tool)
	}
	var text string
	for _, c := range r.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	if r.IsError {
		return nil, &ToolError{Tool: tool, Message: strings.TrimSpace(text)}
	}
	if len(r.StructuredContent) > 0 && string(r.StructuredContent) != "null" {
		// FastMCP wraps non-object returns as {"result": ...}.
		var wrapped map[string]json.RawMessage
		if json.Unmarshal(r.StructuredContent, &wrapped) == nil && len(wrapped) == 1 && wrapped["result"] != nil {
			return wrapped["result"], nil
		}
		return r.StructuredContent, nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text), nil
	}
	b, _ := json.Marshal(text)
	return b, nil
}

func (c *MCPClient) session(ctx context.Context, auth string, fresh bool) (*session, error) {
	key := authKey(auth)
	c.mu.Lock()
	s := c.sessions[key]
	if s != nil && !fresh && time.Since(s.created) < sessionTTL {
		c.mu.Unlock()
		return s, nil
	}
	delete(c.sessions, key)
	c.mu.Unlock()

	s = &session{version: protocolVersion, created: time.Now()}
	res, sid, err := c.post(ctx, auth, s, rpcRequest{JSONRPC: "2.0", ID: c.id(), Method: "initialize", Params: map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "fairmind-devbridge", "version": "0.1.0"},
	}})
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(res, &init)
	if init.ProtocolVersion != "" {
		s.version = init.ProtocolVersion
	}
	s.id = sid
	if _, _, err := c.post(ctx, auth, s, rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.sessions[key] = s
	c.mu.Unlock()
	return s, nil
}

func (c *MCPClient) id() *int64 { n := c.nextID.Add(1); return &n }

func (c *MCPClient) rpc(ctx context.Context, auth string, s *session, method string, params any) (json.RawMessage, error) {
	res, _, err := c.post(ctx, auth, s, rpcRequest{JSONRPC: "2.0", ID: c.id(), Method: method, Params: params})
	return res, err
}

// post sends one JSON-RPC message and returns the matching result. It handles
// both application/json and text/event-stream responses.
func (c *MCPClient) post(ctx context.Context, auth string, s *session, msg rpcRequest) (json.RawMessage, string, error) {
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", s.version)
	if s.id != "" {
		req.Header.Set("Mcp-Session-Id", s.id)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, sid, &UpstreamError{Status: resp.StatusCode, Body: string(b)}
	}
	if msg.ID == nil { // notification
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, sid, nil
	}
	body2 := io.LimitReader(resp.Body, maxMCPResponse)
	var candidates [][]byte
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		candidates, err = readSSE(body2, *msg.ID)
	} else {
		b, rerr := io.ReadAll(body2)
		candidates, err = [][]byte{b}, rerr
	}
	if err != nil {
		return nil, sid, err
	}
	for _, cand := range candidates {
		var r rpcResponse
		if json.Unmarshal(cand, &r) != nil || r.ID == nil || *r.ID != *msg.ID {
			continue
		}
		if r.Error != nil {
			return nil, sid, &ToolError{Tool: msg.Method, Message: r.Error.Message}
		}
		return r.Result, sid, nil
	}
	return nil, sid, fmt.Errorf("no JSON-RPC response for %s", msg.Method)
}

// readSSE collects event data payloads until the one carrying wantID arrives.
func readSSE(r io.Reader, wantID int64) ([][]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxMCPResponse)
	var out [][]byte
	var data strings.Builder
	flush := func() bool {
		if data.Len() == 0 {
			return false
		}
		b := []byte(data.String())
		data.Reset()
		out = append(out, b)
		var r rpcResponse
		return json.Unmarshal(b, &r) == nil && r.ID != nil && *r.ID == wantID
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if flush() {
				return out, nil
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return out, sc.Err()
}
