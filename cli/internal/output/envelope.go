// Package output defines the normalized JSON envelope, error codes, exit codes
// and redaction applied to everything the CLI prints.
package output

import "encoding/json"

// Envelope is the standard Agent API / CLI envelope (spec §14).
type Envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Page     json.RawMessage `json:"page"`
	Evidence json.RawMessage `json:"evidence"`
	Error    *Error          `json:"error"`
}

// Error is the machine-readable error object inside an envelope.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

// Success builds a success envelope around an arbitrary data value.
func Success(data any) (*Envelope, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &Envelope{OK: true, Data: raw}, nil
}

// Failure builds an error envelope.
func Failure(e Error) *Envelope {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	return &Envelope{OK: false, Error: &e}
}

// ContextPack mirrors the common aggregate context structure (spec §17).
// The CLI only decodes it for human rendering; JSON output passes the server
// payload through untouched.
type ContextPack struct {
	Summary  string        `json:"summary"`
	Intent   string        `json:"intent,omitempty"`
	Project  *ProjectRef   `json:"project,omitempty"`
	Items    []ContextItem `json:"items"`
	Warnings []Warning     `json:"warnings"`
	FollowUp []FollowUp    `json:"follow_up"`
	Budget   *Budget       `json:"budget,omitempty"`
}

type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ContextItem struct {
	Source      string  `json:"source"`
	Kind        string  `json:"kind"`
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Status      string  `json:"status,omitempty"`
	ReviewState string  `json:"review_state,omitempty"`
	WhyRelevant string  `json:"why_relevant,omitempty"`
	Excerpt     string  `json:"excerpt,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

// Warning is a non-fatal server notice. Servers may send plain strings or
// objects; UnmarshalJSON accepts both.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (w *Warning) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		w.Message = s
		return nil
	}
	type plain Warning
	return json.Unmarshal(b, (*plain)(w))
}

type FollowUp struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

type Budget struct {
	RequestedTokens int `json:"requested_tokens"`
	EstimatedTokens int `json:"estimated_tokens"`
	OmittedItems    int `json:"omitted_items"`
}
