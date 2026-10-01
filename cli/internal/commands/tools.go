package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/api"
	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

// AllowDestructiveEnv must be "1" to run destructive or credential-exposing tools.
const AllowDestructiveEnv = "FAIRMIND_ALLOW_DESTRUCTIVE"

func init() {
	register(command{path: "tools list", handler: toolsList, usage: usageToolsList,
		spec: flagSpec{"namespace": kindString, "access": kindString, "refresh": kindBool}})
	register(command{path: "tools describe", handler: toolsDescribe, usage: usageToolsDescribe, spec: flagSpec{}})
	register(command{path: "tools call", handler: toolsCall, usage: usageToolsCall,
		spec: flagSpec{"args-json": kindString, "args-file": kindString, "arg": kindString, "yes": kindBool}})
	register(command{path: "tools docs", handler: toolsDocs, usage: usageToolsDocs, spec: flagSpec{"output": kindString}})
	register(command{path: "work list", handler: workList, usage: usageWorkList,
		spec: flagSpec{"kind": kindString, "status": kindString, "parent": kindString, "limit": kindString, "cursor": kindString, "all": kindBool}})
}

// ---- tools list / describe / docs ----

func toolsList(r *run) (*output.Envelope, string, error) {
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	tools, err := r.catalog(r.args.bools["refresh"])
	if err != nil {
		return nil, "", err
	}
	ns, access := strings.ToLower(r.args.str("namespace")), strings.ToLower(r.args.str("access"))
	type row struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Access  string `json:"access"`
		Summary string `json:"summary"`
	}
	rows := []row{}
	counts := map[string]int{}
	for _, t := range tools {
		if (ns != "" && t.Namespace != ns) || (access != "" && t.Access != access) {
			continue
		}
		counts[t.Access]++
		rows = append(rows, row{t.Name, "fairmind " + t.Command, t.Access, summaryLine(t.Description, 160)})
	}
	env, err := output.Success(map[string]any{"count": len(rows), "counts": counts, "tools": rows})
	return env, "tools", err
}

func toolsDescribe(r *run) (*output.Envelope, string, error) {
	if len(r.args.pos) == 0 {
		return nil, "", output.Validationf("expected a tool name (e.g. Studio_get_task) or command (e.g. studio get-task)")
	}
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	tools, err := r.catalog(false)
	if err != nil {
		return nil, "", err
	}
	t := findTool(tools, strings.Join(r.args.pos, " "))
	if t == nil {
		return nil, "", output.NewError("TOOL_NOT_FOUND", "unknown FairMind tool: "+strings.Join(r.args.pos, " "), nil)
	}
	env, err := output.Success(map[string]any{
		"name": t.Name, "command": "fairmind " + t.Command, "access": t.Access,
		"description": t.Description, "parameters": t.params(), "usage": toolUsageLine(t),
	})
	return env, "", err
}

func toolsDocs(r *run) (*output.Envelope, string, error) {
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	tools, err := r.catalog(true)
	if err != nil {
		return nil, "", err
	}
	md := toolsMarkdown(tools, r.app.Now())
	out := r.args.str("output")
	if out == "" {
		r.printer.Raw(md)
		return nil, "", nil
	}
	if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
		return nil, "", output.Validationf("cannot write %s: %v", out, err)
	}
	env, err := output.Success(map[string]any{"written": out, "tools": len(tools)})
	return env, "", err
}

// ---- tools call and dynamic namespace commands ----

func toolsCall(r *run) (*output.Envelope, string, error) {
	if len(r.args.pos) != 1 {
		return nil, "", output.Validationf("expected exactly one tool name, e.g. `fairmind tools call Studio_get_task --arg task_id=TASK-2026-0001`")
	}
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	tools, err := r.catalog(false)
	if err != nil {
		return nil, "", err
	}
	t := findTool(tools, r.args.pos[0])
	if t == nil {
		return nil, "", output.NewError("TOOL_NOT_FOUND", "unknown FairMind tool: "+r.args.pos[0]+"; run `fairmind tools list`", nil)
	}
	if err := r.guard(t); err != nil {
		return nil, "", err
	}
	args, err := readJSONArgs(r)
	if err != nil {
		return nil, "", err
	}
	byName := map[string]param{}
	for _, p := range t.params() {
		byName[p.Name] = p
	}
	for _, kv := range r.args.list("arg") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, "", output.Validationf("--arg expects key=value (got %q)", kv)
		}
		p, known := byName[k]
		if !known {
			return nil, "", output.Validationf("%s has no parameter %q; run `fairmind tools describe %s`", t.Name, k, t.Name)
		}
		val, err := convertValue(p, []string{v})
		if err != nil {
			return nil, "", err
		}
		args[k] = val
	}
	return r.invokeTool(t, args, nil)
}

func readJSONArgs(r *run) (map[string]any, error) {
	args := map[string]any{}
	var raw []byte
	switch {
	case r.args.str("args-json") != "":
		raw = []byte(r.args.str("args-json"))
	case r.args.str("args-file") == "-":
		b, err := io.ReadAll(io.LimitReader(r.app.Stdin, 4<<20))
		if err != nil {
			return nil, output.Validationf("cannot read arguments from stdin")
		}
		raw = b
	case r.args.str("args-file") != "":
		b, err := os.ReadFile(r.args.str("args-file"))
		if err != nil {
			return nil, output.Validationf("cannot read %s", r.args.str("args-file"))
		}
		raw = b
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, output.Validationf("arguments must be a JSON object: %v", err)
		}
	}
	return args, nil
}

// runDynamic executes `fairmind <namespace> <command> [flags]`, generated from
// the tool catalog.
func (a *App) runDynamic(pr *output.Printer, red *output.Redactor, argv []string) int {
	pre := prescanGlobals(argv)
	if pre.bools["json"] {
		pr.JSON = true
	}
	r := &run{app: a, args: pre, printer: pr, red: red, verbose: pre.bools["verbose"]}
	var err error
	if r.timeout, err = parseTimeout(firstNonEmpty(pre.str("timeout"), a.Getenv("FAIRMIND_TIMEOUT"))); err != nil {
		return a.fail(pr, err)
	}
	if err := r.loadContext(false); err != nil {
		return a.fail(pr, err)
	}
	tools, err := r.catalog(false)
	if err != nil {
		return a.fail(pr, err)
	}
	words := leadingWords(argv)
	ns := words[0]
	if len(words) < 2 {
		if !hasNamespace(tools, ns) {
			return a.fail(pr, output.NewError(output.CodeUnknownCommand, "unknown command: "+ns+"; run `fairmind help`", nil))
		}
		pr.Raw(namespaceUsage(tools, ns))
		return output.ExitOK
	}
	t := findTool(tools, ns+" "+words[1])
	if t == nil {
		msg := fmt.Sprintf("unknown command: %s %s; run `fairmind %s --help`", ns, words[1], ns)
		if !hasNamespace(tools, ns) {
			msg = "unknown command: " + strings.Join(words, " ") + "; run `fairmind help`"
		}
		return a.fail(pr, output.NewError(output.CodeUnknownCommand, msg, nil))
	}
	spec := flagSpec{"yes": kindBool, "args-json": kindString}
	for _, p := range t.params() {
		switch p.Type {
		case "boolean":
			spec[p.Flag] = kindBool
		case "array":
			spec[p.Flag] = kindList
		default:
			spec[p.Flag] = kindString
		}
	}
	p, err := parseArgs(argv[2:], spec)
	if err != nil {
		return a.fail(pr, err)
	}
	if p.bools["help"] {
		pr.Raw(toolUsage(t))
		return output.ExitOK
	}
	r.args = p
	if err := r.guard(t); err != nil {
		return a.fail(pr, err)
	}
	args, err := r.buildToolArgs(t)
	if err != nil {
		return a.fail(pr, err)
	}
	env, kind, err := r.invokeTool(t, args, nil)
	if err != nil {
		return a.fail(pr, err)
	}
	pr.Envelope(env, kind)
	return output.ExitOK
}

func hasNamespace(tools []toolInfo, ns string) bool {
	for _, t := range tools {
		if t.Namespace == ns {
			return true
		}
	}
	return false
}

// prescanGlobals extracts global flags before the tool schema is known.
func prescanGlobals(argv []string) *parsed {
	p := &parsed{vals: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name, val, has := strings.Cut(a[2:], "=")
		kind, ok := globalFlags[name]
		if !ok || name == "project" { // --project may also be a tool parameter; resolved after parsing
			if name == "project" {
				if !has && i+1 < len(argv) {
					i++
					val = argv[i]
				}
				p.vals[name] = append(p.vals[name], val)
			}
			continue
		}
		if kind == kindBool {
			p.bools[name] = !has || val == "true"
			continue
		}
		if !has && i+1 < len(argv) {
			i++
			val = argv[i]
		}
		p.vals[name] = append(p.vals[name], val)
	}
	return p
}

// buildToolArgs converts flags/positionals to typed tool arguments and fills
// project / git_remote from the resolved context when the tool accepts them.
func (r *run) buildToolArgs(t *toolInfo) (map[string]any, error) {
	args := map[string]any{}
	if js := r.args.str("args-json"); js != "" {
		if err := json.Unmarshal([]byte(js), &args); err != nil {
			return nil, output.Validationf("--args-json must be a JSON object: %v", err)
		}
	}
	params := t.params()
	for _, p := range params {
		if p.Type == "boolean" {
			if v, ok := r.args.bools[p.Flag]; ok {
				args[p.Name] = v
			}
			continue
		}
		raw := r.args.vals[p.Flag]
		if len(raw) == 0 {
			continue
		}
		if p.Type == "string" || p.Type == "any" || p.Type == "object" {
			expanded, err := r.expandAt(raw[len(raw)-1])
			if err != nil {
				return nil, err
			}
			raw = []string{expanded}
		}
		v, err := convertValue(p, raw)
		if err != nil {
			return nil, err
		}
		args[p.Name] = v
	}
	// Positionals fill required parameters in order: `fairmind studio get-task TASK-1`.
	pos := r.args.pos
	for _, p := range params {
		if len(pos) == 0 {
			break
		}
		if _, set := args[p.Name]; set || !p.Required {
			continue
		}
		v, err := convertValue(p, pos[:1])
		if err != nil {
			return nil, err
		}
		args[p.Name] = v
		pos = pos[1:]
	}
	if len(pos) > 0 {
		return nil, output.Validationf("unexpected argument %q; run `fairmind %s --help`", pos[0], t.Command)
	}
	for _, p := range params {
		if _, set := args[p.Name]; set {
			continue
		}
		switch p.Name {
		case "project", "project_id", "projectId":
			if r.settings.Project != "" {
				args[p.Name] = r.settings.Project
			}
		case "agent":
			if r.settings.Agent != "" {
				args[p.Name] = r.settings.Agent
			}
		case "git_remote":
			if r.repo != nil && r.repo.Remote != "" {
				args[p.Name] = "https://" + r.repo.Remote + ".git"
			}
		}
	}
	var missing []string
	for _, p := range params {
		if _, set := args[p.Name]; p.Required && !set {
			missing = append(missing, "--"+p.Flag)
		}
	}
	if len(missing) > 0 {
		return nil, output.Validationf("missing required parameter(s) %s; run `fairmind %s --help`", strings.Join(missing, ", "), t.Command)
	}
	return args, nil
}

// expandAt reads "@path" from a file and "@-" from stdin; "@@x" is a literal "@x".
func (r *run) expandAt(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, "@@"):
		return v[1:], nil
	case v == "@-":
		b, err := io.ReadAll(io.LimitReader(r.app.Stdin, 4<<20))
		if err != nil {
			return "", output.Validationf("cannot read stdin")
		}
		return string(b), nil
	case strings.HasPrefix(v, "@") && len(v) > 1:
		path := v[1:]
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.app.Dir, path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", output.Validationf("cannot read %s", v[1:])
		}
		if len(b) > 4<<20 {
			return "", output.Validationf("%s is larger than 4 MiB", v[1:])
		}
		return string(b), nil
	}
	return v, nil
}

func convertValue(p param, raw []string) (any, error) {
	one := raw[len(raw)-1]
	if p.Nullable && one == "null" {
		return nil, nil
	}
	bad := func() error {
		return output.Validationf("--%s expects %s (got %q)", p.Flag, p.Type, one)
	}
	switch p.Type {
	case "integer":
		n, err := strconv.Atoi(one)
		if err != nil {
			return nil, bad()
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(one, 64)
		if err != nil {
			return nil, bad()
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(one)
		if err != nil {
			return nil, bad()
		}
		return b, nil
	case "array":
		if len(raw) == 1 && strings.HasPrefix(strings.TrimSpace(one), "[") {
			var arr []any
			if err := json.Unmarshal([]byte(one), &arr); err != nil {
				return nil, bad()
			}
			return arr, nil
		}
		out := make([]any, 0, len(raw))
		for _, v := range raw {
			item, err := convertValue(param{Flag: p.Flag, Type: firstNonEmpty(p.ItemType, "string")}, []string{v})
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case "object":
		var obj map[string]any
		if err := json.Unmarshal([]byte(one), &obj); err != nil {
			return nil, output.Validationf("--%s expects a JSON object", p.Flag)
		}
		return obj, nil
	case "any":
		var v any
		if json.Unmarshal([]byte(one), &v) == nil {
			return v, nil
		}
		return one, nil
	}
	if len(p.Enum) > 0 && !containsStr(p.Enum, one) {
		return nil, output.Validationf("--%s must be one of %s", p.Flag, strings.Join(p.Enum, ", "))
	}
	return one, nil
}

// guard enforces the write / destructive opt-ins. --dry-run is always allowed.
func (r *run) guard(t *toolInfo) error {
	if r.args.bools["dry-run"] {
		return nil
	}
	if t.Access == accessDestructive || t.Access == accessSensitive {
		if r.app.Getenv(AllowDestructiveEnv) != "1" {
			return output.NewError("DESTRUCTIVE_NOT_ALLOWED", fmt.Sprintf(
				"%s is %s; it runs only when a human sets %s=1 for this command", t.Name, t.Access, AllowDestructiveEnv), nil)
		}
	}
	if t.Access != accessRead && !r.args.bools["yes"] {
		return output.NewError("WRITE_NOT_CONFIRMED", fmt.Sprintf(
			"%s modifies FairMind (%s). Preview with --dry-run, then re-run with --yes only if the user asked for this change", t.Name, t.Access), nil)
	}
	return nil
}

// invokeTool applies the guards, then calls POST /v1/tools/{name}:call.
func (r *run) invokeTool(t *toolInfo, args map[string]any, _ map[string]string) (*output.Envelope, string, error) {
	if err := r.guard(t); err != nil {
		return nil, "", err
	}
	if r.args.bools["dry-run"] {
		env, err := output.Success(map[string]any{"dry_run": true, "tool": t.Name, "access": t.Access, "arguments": args})
		return env, "", err
	}
	headers := map[string]string{}
	if t.Access != accessRead {
		headers["X-FairMind-Write-Intent"] = "confirmed"
		headers["Idempotency-Key"] = api.NewUUID()
	}
	if t.Access == accessDestructive || t.Access == accessSensitive {
		headers["X-FairMind-Allow-Destructive"] = "1"
	}
	env, err := r.callWith(headers, "POST", "/v1/tools/"+t.Name+":call", nil, map[string]any{"arguments": args})
	return env, "", err
}

// ---- work list ----

func workList(r *run) (*output.Envelope, string, error) {
	kind := strings.ToLower(firstNonEmpty(r.args.str("kind"), "task"))
	switch kind {
	case "task", "story", "epic", "need":
	default:
		return nil, "", output.Validationf("--kind must be task, story or epic")
	}
	limit, err := r.args.intFlag("limit", 50, 1, 100)
	if err != nil {
		return nil, "", err
	}
	if err := r.loadContext(false); err != nil {
		return nil, "", err
	}
	q := map[string][]string{"kind": {kind}, "limit": {strconv.Itoa(limit)}}
	if r.settings.Project != "" {
		q["project"] = []string{r.settings.Project}
	}
	if s := r.args.str("status"); s != "" {
		q["status"] = []string{s}
	}
	if p := r.args.str("parent"); p != "" {
		q["parent"] = []string{p}
	}
	if c := r.args.str("cursor"); c != "" {
		q["cursor"] = []string{c}
	}
	if r.args.bools["all"] {
		q["all"] = []string{"true"}
	}
	env, err := r.call("GET", "/v1/work", q, nil)
	return env, "work", err
}

// ---- help and docs rendering ----

func summaryLine(desc string, max int) string {
	desc = strings.TrimSpace(desc)
	if i := strings.Index(desc, "\n\n"); i > 0 {
		desc = desc[:i]
	}
	desc = strings.Join(strings.Fields(desc), " ")
	if len(desc) > max {
		desc = desc[:max] + "…"
	}
	return desc
}

func toolUsageLine(t *toolInfo) string {
	var b strings.Builder
	b.WriteString("fairmind " + t.Command)
	for _, p := range t.params() {
		arg := " --" + p.Flag
		if p.Type != "boolean" {
			arg += " <" + typeHint(p) + ">"
		}
		if !p.Required {
			arg = " [" + strings.TrimSpace(arg) + "]"
		}
		b.WriteString(arg)
	}
	if t.Access != accessRead {
		b.WriteString(" --yes")
	}
	return b.String()
}

func typeHint(p param) string {
	switch {
	case len(p.Enum) > 0:
		return strings.Join(p.Enum, "|")
	case p.Type == "array":
		return firstNonEmpty(p.ItemType, "string") + ",..."
	case p.Type == "object":
		return "json"
	}
	return p.Type
}

func toolUsage(t *toolInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n\nFairMind tool %s — access: %s\n\n%s\n", toolUsageLine(t), t.Name, t.Access, strings.TrimSpace(t.Description))
	if ps := t.params(); len(ps) > 0 {
		b.WriteString("\nParameters:\n")
		for _, p := range ps {
			req := ""
			if p.Required {
				req = " (required)"
			}
			fmt.Fprintf(&b, "  --%s <%s>%s\n", p.Flag, typeHint(p), req)
			if p.Description != "" {
				fmt.Fprintf(&b, "      %s\n", strings.ReplaceAll(summaryLine(p.Description, 300), "\n", " "))
			}
		}
	}
	b.WriteString(`
Notes:
  - Required parameters can also be given positionally, in the order above.
  - --project / project_id, git_remote and agent are filled from the resolved context when omitted.
  - --args-json '{"key": value}' passes raw arguments (merged under explicit flags).
  - Text values: @file reads a file, @- reads stdin, @@x is a literal "@x".
  - --dry-run shows the arguments without calling FairMind.
`)
	if t.Access != accessRead {
		b.WriteString("  - This tool WRITES to FairMind: it requires --yes.\n")
	}
	if t.Access == accessDestructive || t.Access == accessSensitive {
		b.WriteString("  - " + t.Access + " tool: also requires " + AllowDestructiveEnv + "=1, set by a human.\n")
	}
	return b.String() + globalUsage
}

func namespaceUsage(tools []toolInfo, ns string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: fairmind %s <command> [flags]\n\nCommands (from the live FairMind catalog):\n", ns)
	for _, t := range tools {
		if t.Namespace != ns {
			continue
		}
		sub := strings.TrimPrefix(t.Command, ns+" ")
		tag := ""
		if t.Access != accessRead {
			tag = " [" + t.Access + "]"
		}
		fmt.Fprintf(&b, "  %-36s %s%s\n", sub, summaryLine(t.Description, 90), tag)
	}
	fmt.Fprintf(&b, "\nRun `fairmind %s <command> --help` for parameters.\n", ns)
	return b.String()
}

func toolsMarkdown(tools []toolInfo, now time.Time) string {
	var b strings.Builder
	counts := map[string]int{}
	byNS := map[string][]toolInfo{}
	var nss []string
	for _, t := range tools {
		counts[t.Access]++
		if _, ok := byNS[t.Namespace]; !ok {
			nss = append(nss, t.Namespace)
		}
		byNS[t.Namespace] = append(byNS[t.Namespace], t)
	}
	sort.Strings(nss)
	fmt.Fprintf(&b, "# FairMind tools reference\n\n")
	fmt.Fprintf(&b, "Generated by `fairmind tools docs` on %s from the live catalog: %d tools (%d read, %d write, %d destructive, %d sensitive).\n",
		now.UTC().Format("2006-01-02"), len(tools), counts[accessRead], counts[accessWrite], counts[accessDestructive], counts[accessSensitive])
	b.WriteString(`
Every tool is a CLI command: ` + "`fairmind <namespace> <command> [--flag value ...] --json`" + `.

- Required parameters can be given positionally, in the order listed.
- ` + "`project` / `project_id`" + ` default to the folder's FairMind project and accept its name; ` + "`git_remote`" + ` defaults to the local remote; ` + "`agent`" + ` defaults to the configured agent name (e.g. github-copilot).
- Text values accept ` + "`@file`" + ` (read from a file) and ` + "`@-`" + ` (stdin), e.g. ` + "`--journal-content @journal.md`" + `.
- **write** tools need ` + "`--yes`" + ` (preview with ` + "`--dry-run`" + `). Only use them when the user asked for that change.
- **destructive / sensitive** tools are for humans only: they also need ` + "`FAIRMIND_ALLOW_DESTRUCTIVE=1`" + `. Never set it yourself.
- ` + "`fairmind <namespace> <command> --help`" + ` shows the same information; ` + "`fairmind tools list --json`" + ` lists everything.
`)
	for _, ns := range nss {
		fmt.Fprintf(&b, "\n## %s\n", strings.ToUpper(ns[:1])+ns[1:])
		for _, t := range byNS[ns] {
			t := t
			fmt.Fprintf(&b, "\n### `fairmind %s` — %s\n\nTool `%s`. %s\n", t.Command, t.Access, t.Name, summaryLine(t.Description, 400))
			ps := t.params()
			if len(ps) == 0 {
				b.WriteString("\nNo parameters.\n")
				continue
			}
			b.WriteString("\n| Flag | Type | Required | Description |\n|---|---|---|---|\n")
			for _, p := range ps {
				req := ""
				if p.Required {
					req = "yes"
				}
				desc := strings.ReplaceAll(summaryLine(p.Description, 220), "|", "\\|")
				fmt.Fprintf(&b, "| `--%s` | %s | %s | %s |\n", p.Flag, strings.ReplaceAll(typeHint(p), "|", "\\|"), req, desc)
			}
		}
	}
	return b.String()
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
