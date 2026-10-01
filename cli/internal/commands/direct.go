package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/devbridge"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

// apiDoer is implemented by both the HTTP api.Client and the in-process
// directClient, so the command handlers do not care which transport is used.
type apiDoer interface {
	Do(ctx context.Context, method, path string, query url.Values, body any) (*output.Envelope, error)
	SetHeaders(map[string]string)
}

// directClient runs the /v1 orchestration in-process and calls the FairMind
// MCP server directly — no devbridge process, no /v1 HTTP endpoint. The
// FairMind proprietary logic stays inside the MCP tools; the CLI only does the
// light "call tool A, then B, assemble" orchestration.
type directClient struct {
	srv     *devbridge.Server
	token   auth.Token
	project string
	agent   string
	headers map[string]string
	logf    func(string, ...any)
}

func newDirectClient(mcpURL string, token auth.Token, project, agent string, logf func(string, ...any)) *directClient {
	logger := log.New(io.Discard, "", 0)
	return &directClient{srv: devbridge.NewServer(devbridge.NewMCPClient(mcpURL), logger),
		token: token, project: project, agent: agent, logf: logf}
}

func (d *directClient) SetHeaders(h map[string]string) { d.headers = h }

func (d *directClient) Do(ctx context.Context, method, path string, query url.Values, body any) (*output.Envelope, error) {
	if d.token.Empty() {
		return nil, output.NewError(output.CodeTokenMissing,
			"no FairMind token found; run `fairmind auth login` or set "+auth.EnvVar, nil)
	}
	u := &url.URL{Scheme: "http", Host: "fairmind-direct", Path: path}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, output.NewError(output.CodeInternal, "could not encode request", nil)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, output.NewError(output.CodeInternal, "could not build request", nil)
	}
	req.Header.Set("Authorization", "Bearer "+d.token.Value())
	if d.agent != "" {
		req.Header.Set("X-FairMind-Agent", d.agent)
	}
	if d.project != "" {
		req.Header.Set("X-FairMind-Project", d.project)
	}
	for k, v := range d.headers {
		req.Header.Set(k, v)
	}
	if d.logf != nil {
		d.logf("-> %s %s (direct MCP)", method, path)
	}

	data, page, derr := d.srv.Dispatch(req)
	if derr != nil {
		return nil, directError(derr)
	}
	rawData, _ := json.Marshal(data)
	rawPage, _ := json.Marshal(page)
	return &output.Envelope{OK: true, Data: rawData, Page: rawPage}, nil
}

func directError(err error) error {
	status, code, message := devbridge.ErrorFields(err)
	exit, ok := output.ExitForCode(code)
	if !ok {
		exit = output.ExitForStatus(status)
		if status < 400 {
			exit = output.ExitGeneric
		}
	}
	return &output.CLIError{Exit: exit, Err: output.Error{
		Code: code, Message: message, Retryable: status >= 500 || status == 429,
		Details: map[string]any{"via": "fairmind-cli-direct", "http_status": status},
	}}
}
