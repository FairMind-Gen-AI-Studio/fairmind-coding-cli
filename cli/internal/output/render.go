package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Printer writes envelopes to stdout/stderr, always through the Redactor.
type Printer struct {
	Stdout   io.Writer
	Stderr   io.Writer
	JSON     bool
	Redactor *Redactor
}

// Envelope prints a result in JSON or human form.
func (p *Printer) Envelope(env *Envelope, kind string) {
	if p.JSON {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(env)
		p.write(p.Stdout, buf.String())
		return
	}
	if !env.OK {
		p.write(p.Stderr, fmt.Sprintf("error: %s: %s\n", env.Error.Code, env.Error.Message))
		return
	}
	p.write(p.Stdout, renderHuman(env.Data, kind))
}

// Logf writes a verbose/diagnostic line to stderr.
func (p *Printer) Logf(format string, a ...any) {
	p.write(p.Stderr, fmt.Sprintf(format, a...)+"\n")
}

// Raw writes preformatted text to stdout (help, version).
func (p *Printer) Raw(s string) { p.write(p.Stdout, s) }

func (p *Printer) write(w io.Writer, s string) {
	if p.Redactor != nil {
		s = p.Redactor.Redact(s)
	}
	_, _ = io.WriteString(w, s)
}

func renderHuman(data json.RawMessage, kind string) string {
	switch kind {
	case "context":
		var pack ContextPack
		if err := json.Unmarshal(data, &pack); err == nil {
			return renderPack(pack)
		}
	case "search":
		var res struct {
			Items    []ContextItem `json:"items"`
			Warnings []Warning     `json:"warnings"`
		}
		if err := json.Unmarshal(data, &res); err == nil {
			return renderSearch(res.Items, res.Warnings)
		}
	case "tools":
		var res struct {
			Count int `json:"count"`
			Tools []struct {
				Command, Access, Summary string
			} `json:"tools"`
		}
		if err := json.Unmarshal(data, &res); err == nil {
			var b strings.Builder
			for _, t := range res.Tools {
				fmt.Fprintf(&b, "%-11s %-52s %s\n", t.Access, t.Command, oneLine(t.Summary, 90))
			}
			fmt.Fprintf(&b, "\n%d tool(s). `fairmind <namespace> <command> --help` for parameters.\n", res.Count)
			return b.String()
		}
	case "work":
		var res struct {
			Kind  string `json:"kind"`
			Items []struct {
				ID, Status, Title string
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &res); err == nil {
			var b strings.Builder
			if len(res.Items) == 0 {
				fmt.Fprintf(&b, "No %s items.\n", res.Kind)
			}
			for _, it := range res.Items {
				fmt.Fprintf(&b, "%-16s %-12s %s\n", it.ID, it.Status, oneLine(it.Title, 100))
			}
			return b.String()
		}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data) + "\n"
	}
	return buf.String() + "\n"
}

func renderPack(p ContextPack) string {
	var b strings.Builder
	if p.Project != nil && p.Project.Name != "" {
		fmt.Fprintf(&b, "Project: %s (%s)\n", p.Project.Name, p.Project.ID)
	}
	if p.Summary != "" {
		fmt.Fprintf(&b, "\n%s\n", p.Summary)
	}
	if len(p.Items) == 0 {
		b.WriteString("\nNo FairMind context items returned.\n")
	}
	for _, it := range p.Items {
		fmt.Fprintf(&b, "\n[%s/%s] %s  %s\n", it.Source, it.Kind, it.ID, it.Title)
		if tags := stateTags(it); tags != "" {
			fmt.Fprintf(&b, "  state: %s\n", tags)
		}
		if it.WhyRelevant != "" {
			fmt.Fprintf(&b, "  why:   %s\n", it.WhyRelevant)
		}
		if it.Excerpt != "" {
			fmt.Fprintf(&b, "  %s\n", oneLine(it.Excerpt, 240))
		}
	}
	renderWarnings(&b, p.Warnings)
	if len(p.FollowUp) > 0 {
		b.WriteString("\nFollow-up:\n")
		for _, f := range p.FollowUp {
			fmt.Fprintf(&b, "  %s  # %s\n", f.Command, f.Reason)
		}
	}
	if p.Budget != nil {
		fmt.Fprintf(&b, "\nBudget: ~%d/%d tokens, %d item(s) omitted\n",
			p.Budget.EstimatedTokens, p.Budget.RequestedTokens, p.Budget.OmittedItems)
	}
	return b.String()
}

func renderSearch(items []ContextItem, warnings []Warning) string {
	var b strings.Builder
	if len(items) == 0 {
		b.WriteString("No results.\n")
	}
	for _, it := range items {
		fmt.Fprintf(&b, "%.2f  [%s/%s] %s  %s\n", it.Score, it.Source, it.Kind, it.ID, it.Title)
		if it.Excerpt != "" {
			fmt.Fprintf(&b, "      %s\n", oneLine(it.Excerpt, 200))
		}
	}
	renderWarnings(&b, warnings)
	return b.String()
}

func renderWarnings(b *strings.Builder, ws []Warning) {
	if len(ws) == 0 {
		return
	}
	b.WriteString("\nWarnings:\n")
	for _, w := range ws {
		if w.Code != "" {
			fmt.Fprintf(b, "  - %s: %s\n", w.Code, w.Message)
		} else {
			fmt.Fprintf(b, "  - %s\n", w.Message)
		}
	}
}

func stateTags(it ContextItem) string {
	var parts []string
	if it.Status != "" {
		parts = append(parts, it.Status)
	}
	if it.ReviewState != "" {
		parts = append(parts, it.ReviewState)
	}
	return strings.Join(parts, ", ")
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
