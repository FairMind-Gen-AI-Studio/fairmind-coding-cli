// Package api is the HTTPS client for the FairMind Agent API. It only
// transports requests: all orchestration, ranking and authorization happen
// server-side (spec §2, §13).
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/auth"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/version"
)

// MaxResponseBytes caps any response body the CLI will read.
const MaxResponseBytes = 8 << 20

// Client talks to one FairMind Agent API base URL.
type Client struct {
	BaseURL *url.URL
	Token   auth.Token
	Project string
	Agent   string
	Timeout time.Duration
	HTTP    *http.Client
	// Headers are extra per-request headers (write intent, idempotency key).
	Headers map[string]string
	// Logf, when set, receives verbose diagnostics (already free of secrets;
	// the printer redacts again as defence in depth).
	Logf func(format string, a ...any)
}

// NewHTTPClient returns an http.Client with TLS verification, proxy support
// and redirects disabled (a redirect could forward the bearer token).
func NewHTTPClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Do performs a request and returns the normalized envelope. Any failure —
// transport, HTTP or application — is returned as *output.CLIError.
// SetHeaders sets extra per-request headers (write intent, idempotency key).
func (c *Client) SetHeaders(h map[string]string) { c.Headers = h }

func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any) (*output.Envelope, error) {
	if c.Token.Empty() {
		return nil, output.NewError(output.CodeTokenMissing,
			"no FairMind token found; run `fairmind auth login` or set "+auth.EnvVar, nil)
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	u := *c.BaseURL
	u.Path = c.BaseURL.Path + path
	// Keep ':' literal in custom-method paths such as /v1/context:get.
	u.RawPath = ""
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
	reqID := NewUUID()
	req.Header.Set("Authorization", "Bearer "+c.Token.Value())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.ClientID())
	req.Header.Set("X-FairMind-Client", version.ClientID())
	req.Header.Set("X-Request-Id", reqID)
	if c.Agent != "" {
		req.Header.Set("X-FairMind-Agent", c.Agent)
	}
	if c.Project != "" {
		req.Header.Set("X-FairMind-Project", c.Project)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	c.logf("-> %s %s request_id=%s", method, u.Redacted(), reqID)
	c.logHeaders(req.Header)

	httpc := c.HTTP
	if httpc == nil {
		httpc = NewHTTPClient()
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	dur := time.Since(start)
	c.logf("<- %d %s in %dms request_id=%s", resp.StatusCode, http.StatusText(resp.StatusCode), dur.Milliseconds(), reqID)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	if len(raw) > MaxResponseBytes {
		return nil, output.NewError(output.CodeResponseTooLarge,
			fmt.Sprintf("response exceeded %d bytes", MaxResponseBytes), map[string]any{"request_id": reqID})
	}
	return decode(resp, raw, reqID)
}

func decode(resp *http.Response, raw []byte, reqID string) (*output.Envelope, error) {
	status := resp.StatusCode
	if status >= 300 && status < 400 {
		return nil, output.NewError(output.CodeUnexpectedRedirect,
			"the FairMind API answered with a redirect; check the configured API URL", map[string]any{"status": status})
	}

	var env output.Envelope
	decodeErr := json.Unmarshal(raw, &env)
	validEnvelope := decodeErr == nil && (env.OK || env.Error != nil)

	if status >= 200 && status < 300 {
		if !validEnvelope {
			return nil, output.NewError(output.CodeInvalidResponse,
				"the FairMind API returned a response that is not a valid envelope", map[string]any{"status": status, "request_id": reqID})
		}
		if env.OK {
			return &env, nil
		}
	}

	// Error path: prefer the server's normalized error, else synthesize one.
	var e output.Error
	if validEnvelope && env.Error != nil {
		e = *env.Error
		e.Code = strings.ToUpper(e.Code)
	} else {
		e = output.Error{Code: statusCode(status), Message: fmt.Sprintf("FairMind API returned HTTP %d", status)}
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details["http_status"] = status
	e.Details["request_id"] = reqID

	exit, ok := output.ExitForCode(e.Code)
	if !ok {
		exit = output.ExitForStatus(status)
		if status < 400 {
			exit = output.ExitGeneric
		}
	}
	if !validEnvelope {
		e.Retryable = output.IsRetryableExit(exit)
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			e.Details["retry_after"] = ra
		}
	}
	return nil, &output.CLIError{Err: e, Exit: exit}
}

func statusCode(status int) string {
	switch {
	case status == 401:
		return "TOKEN_INVALID"
	case status == 403:
		return "RESOURCE_FORBIDDEN"
	case status == 404:
		return "RESOURCE_NOT_FOUND"
	case status == 429:
		return output.CodeRateLimited
	case status == 408 || status == 504:
		return output.CodeTimeout
	case status >= 500:
		return output.CodeServiceUnavailable
	case status == 400 || status == 422:
		return output.CodeValidation
	}
	return fmt.Sprintf("HTTP_%d", status)
}

func transportError(ctx context.Context, err error) error {
	var netErr net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return output.NewError(output.CodeTimeout, "request to the FairMind API timed out", nil)
	}
	if strings.Contains(err.Error(), "x509:") {
		return &output.CLIError{Exit: output.ExitUnavailable, Err: output.Error{
			Code: "TLS_VERIFICATION_FAILED", Message: "TLS certificate verification failed for the FairMind API",
			Retryable: false, Details: map[string]any{}}}
	}
	return output.NewError(output.CodeServiceUnavailable, "the FairMind API is unreachable", map[string]any{"cause": sanitizeCause(err)})
}

// sanitizeCause keeps the error class without echoing request details.
func sanitizeCause(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return err.Error()
}

func (c *Client) logf(format string, a ...any) {
	if c.Logf != nil {
		c.Logf(format, a...)
	}
}

func (c *Client) logHeaders(h http.Header) {
	if c.Logf == nil {
		return
	}
	for k, vs := range h {
		v := strings.Join(vs, ",")
		switch strings.ToLower(k) {
		case "authorization", "cookie", "x-api-key":
			v = "[REDACTED]"
		}
		c.Logf("   %s: %s", k, v)
	}
}

func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
