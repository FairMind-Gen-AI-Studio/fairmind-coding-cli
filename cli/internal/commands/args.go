package commands

import (
	"strconv"
	"strings"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/output"
)

type flagKind int

const (
	kindBool flagKind = iota
	kindString
	kindList // consumes following values until the next --flag; also splits on ','
)

type flagSpec map[string]flagKind

// globalFlags are accepted by every command, anywhere on the line (spec §37).
var globalFlags = flagSpec{
	"project": kindString, "repo": kindString, "json": kindBool, "profile": kindString,
	"timeout": kindString, "verbose": kindBool, "dry-run": kindBool, "help": kindBool,
}

// forbiddenFlags must never exist: tokens are not accepted as arguments.
var forbiddenFlags = map[string]bool{"token": true, "jwt": true, "api-key": true, "password": true}

type parsed struct {
	pos   []string
	vals  map[string][]string
	bools map[string]bool
}

func (p *parsed) str(name string) string {
	if v := p.vals[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

func (p *parsed) list(name string) []string { return p.vals[name] }
func (p *parsed) has(name string) bool      { return p.bools[name] || len(p.vals[name]) > 0 }

// parseArgs parses GNU-style long flags interspersed with positionals.
func parseArgs(args []string, spec flagSpec) (*parsed, error) {
	p := &parsed{vals: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.pos = append(p.pos, args[i+1:]...)
			break
		}
		if a == "-h" {
			p.bools["help"] = true
			continue
		}
		if !strings.HasPrefix(a, "--") || a == "-" {
			p.pos = append(p.pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(a[2:], "=")
		if forbiddenFlags[strings.ToLower(name)] {
			return nil, output.Validationf("--%s is not supported: credentials are never accepted as arguments; use `fairmind auth login` or FAIRMIND_TOKEN", name)
		}
		kind, ok := spec[name]
		if !ok {
			kind, ok = globalFlags[name]
		}
		if !ok {
			return nil, output.Validationf("unknown flag --%s", name)
		}
		switch kind {
		case kindBool:
			b := true
			if hasVal {
				var err error
				if b, err = strconv.ParseBool(val); err != nil {
					return nil, output.Validationf("--%s expects true or false", name)
				}
			}
			p.bools[name] = b
		case kindString:
			if !hasVal {
				if i+1 >= len(args) {
					return nil, output.Validationf("--%s requires a value", name)
				}
				i++
				val = args[i]
			}
			p.vals[name] = append(p.vals[name], val)
		case kindList:
			var raw []string
			if hasVal {
				raw = append(raw, val)
			}
			for !hasVal && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				raw = append(raw, args[i])
			}
			if len(raw) == 0 {
				return nil, output.Validationf("--%s requires at least one value", name)
			}
			for _, r := range raw {
				for _, v := range strings.Split(r, ",") {
					if v = strings.TrimSpace(v); v != "" {
						p.vals[name] = append(p.vals[name], v)
					}
				}
			}
		}
	}
	return p, nil
}

func (p *parsed) intFlag(name string, def, min, max int) (int, error) {
	s := p.str(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > max {
		return 0, output.Validationf("--%s must be an integer between %d and %d", name, min, max)
	}
	return n, nil
}
