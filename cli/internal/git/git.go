// Package git inspects the local checkout: repository identity and diff
// metadata. It never reads or transmits file contents (spec §33).
package git

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// emptyTree is git's well-known empty tree object, used before the first commit.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// ErrNotRepo is returned when dir is not inside a git work tree.
var ErrNotRepo = errors.New("not a git repository")

// Repo describes the local repository identity sent to FairMind.
type Repo struct {
	Root       string `json:"-"`
	Remote     string `json:"remote,omitempty"` // normalized host/owner/name
	Branch     string `json:"branch,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
}

// Hunk is a changed line range in the new version of a file.
type Hunk struct {
	Start int `json:"start"`
	Lines int `json:"lines"`
}

// FileChange is diff metadata for one file — never its content.
type FileChange struct {
	Path      string   `json:"path"`
	OldPath   string   `json:"old_path,omitempty"`
	Status    string   `json:"status"` // added|modified|deleted|renamed|copied|typechange|untracked|unspecified
	Additions *int     `json:"additions,omitempty"`
	Deletions *int     `json:"deletions,omitempty"`
	Binary    bool     `json:"binary,omitempty"`
	Hunks     []Hunk   `json:"hunks,omitempty"`
	Symbols   []string `json:"symbols,omitempty"`
}

// Runner executes git; tests may substitute it.
type Runner func(dir string, args ...string) (string, error)

// Exec runs the real git binary.
func Exec(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.quotePath=false"}, args...)...)
	// Stable, untranslated messages; never prompt for credentials.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C", "GIT_TERMINAL_PROMPT=0")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "not a git repository") {
			return "", ErrNotRepo
		}
		return "", fmt.Errorf("git %s: %s", args[0], firstLine(msg))
	}
	return out.String(), nil
}

// Detect returns repository identity for dir, or ErrNotRepo.
func Detect(run Runner, dir string) (*Repo, error) {
	root, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: strings.TrimSpace(root)}
	if raw, err := run(dir, "remote", "get-url", "origin"); err == nil {
		r.Remote = NormalizeRemote(strings.TrimSpace(raw))
	}
	if b, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		r.Branch = strings.TrimSpace(b)
	}
	if h, err := run(dir, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		r.HeadCommit = strings.TrimSpace(h)
	}
	return r, nil
}

var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

// NormalizeRemote converts any git remote URL to "host/owner/name", lowercased
// host, without scheme, port, credentials or ".git". Credentials embedded in
// the remote (https://user:token@host/...) are always dropped.
func NormalizeRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		host, path = u.Hostname(), u.Path
	} else if m := scpLike.FindStringSubmatch(raw); m != nil {
		host, path = m[1], m[2]
	} else {
		// Local path remotes carry no shareable identity.
		return ""
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host) + "/" + path
}

// DiffOptions controls DiffMetadata.
type DiffOptions struct {
	Base    string // ref to diff the working tree against; default HEAD
	Symbols bool   // extract enclosing symbol names from hunk headers
}

// DiffMetadata lists changes between Base and the working tree (staged and
// unstaged), plus untracked files. Only metadata is returned.
func DiffMetadata(run Runner, root string, opt DiffOptions) ([]FileChange, error) {
	base := opt.Base
	if base == "" {
		base = "HEAD"
	}
	// Reject option-like refs: git parses options anywhere before "--", so a
	// base such as "--output=/path" would be an argument-injection vector.
	if strings.HasPrefix(base, "-") {
		return nil, fmt.Errorf("invalid base ref %q", base)
	}
	if _, err := run(root, "rev-parse", "--verify", "-q", base+"^{commit}"); err != nil {
		if opt.Base != "" {
			return nil, fmt.Errorf("unknown base ref %q", opt.Base)
		}
		base = emptyTree // repository without commits
	}

	ns, err := run(root, "diff", "--name-status", "-z", "-M", base)
	if err != nil {
		return nil, err
	}
	changes, index := parseNameStatus(ns)

	num, err := run(root, "diff", "--numstat", "-z", "-M", base)
	if err != nil {
		return nil, err
	}
	applyNumstat(num, changes, index)

	patch, err := run(root, "diff", "-U0", "-M", "--no-color", "--no-ext-diff", base)
	if err != nil {
		return nil, err
	}
	applyHunks(patch, changes, index, opt.Symbols)

	untracked, err := run(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(untracked, "\x00") {
		if p != "" {
			if _, seen := index[p]; !seen {
				index[p] = len(changes)
				changes = append(changes, FileChange{Path: p, Status: "untracked"})
			}
		}
	}
	return changes, nil
}

var statusNames = map[byte]string{
	'A': "added", 'M': "modified", 'D': "deleted", 'R': "renamed",
	'C': "copied", 'T': "typechange", 'U': "unmerged",
}

func parseNameStatus(out string) ([]FileChange, map[string]int) {
	var changes []FileChange
	index := map[string]int{}
	f := strings.Split(out, "\x00")
	for i := 0; i < len(f); i++ {
		code := f[i]
		if code == "" {
			continue
		}
		st := statusNames[code[0]]
		if st == "" {
			st = "modified"
		}
		fc := FileChange{Status: st}
		if (code[0] == 'R' || code[0] == 'C') && i+2 < len(f) {
			fc.OldPath, fc.Path = f[i+1], f[i+2]
			i += 2
		} else if i+1 < len(f) {
			fc.Path = f[i+1]
			i++
		}
		index[fc.Path] = len(changes)
		changes = append(changes, fc)
	}
	return changes, index
}

func applyNumstat(out string, changes []FileChange, index map[string]int) {
	f := strings.Split(out, "\x00")
	for i := 0; i < len(f); i++ {
		cols := strings.SplitN(f[i], "\t", 3)
		if len(cols) != 3 {
			continue
		}
		path := cols[2]
		if path == "" && i+2 < len(f) { // rename: "a\td\t\0old\0new"
			path = f[i+2]
			i += 2
		}
		idx, ok := index[path]
		if !ok {
			continue
		}
		if cols[0] == "-" {
			changes[idx].Binary = true
			continue
		}
		a, _ := strconv.Atoi(cols[0])
		d, _ := strconv.Atoi(cols[1])
		changes[idx].Additions, changes[idx].Deletions = &a, &d
	}
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

func applyHunks(patch string, changes []FileChange, index map[string]int, withSymbols bool) {
	cur := -1
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = -1
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				continue
			}
			p = strings.TrimPrefix(strings.Trim(p, "\""), "b/")
			if idx, ok := index[p]; ok {
				cur = idx
			}
		case strings.HasPrefix(line, "rename to "), strings.HasPrefix(line, "copy to "):
			p := line[strings.Index(line, " to ")+4:]
			if idx, ok := index[p]; ok {
				cur = idx
			}
		case strings.HasPrefix(line, "@@ ") && cur >= 0:
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			changes[cur].Hunks = append(changes[cur].Hunks, Hunk{Start: start, Lines: n})
			if withSymbols {
				if sym := ExtractSymbol(m[3]); sym != "" && !contains(changes[cur].Symbols, sym) {
					changes[cur].Symbols = append(changes[cur].Symbols, sym)
				}
			}
		}
	}
}

var symbolPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bfunc\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`),
	regexp.MustCompile(`\b(?:function|def|class|interface|struct|enum|trait|impl|fn|type|module|record)\s+([A-Za-z_$][\w$]*)`),
	regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\(|function)`),
	regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*(?::[^{]*)?\{?\s*$`),
}

// ExtractSymbol reduces a git hunk-header context line to just the enclosing
// symbol identifier, so no source text leaves the machine.
func ExtractSymbol(ctx string) string {
	ctx = strings.TrimSpace(ctx)
	if ctx == "" {
		return ""
	}
	for _, re := range symbolPatterns {
		if m := re.FindStringSubmatch(ctx); m != nil {
			switch m[1] {
			case "if", "for", "while", "switch", "catch", "return":
				continue
			}
			return m[1]
		}
	}
	return ""
}

// RelToRoot converts a user-supplied path to a slash-separated path relative
// to the repository root, rejecting paths outside the repository.
func RelToRoot(root, cwd, p string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, p)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if d, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(d, filepath.Base(abs))
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the repository", p)
	}
	return filepath.ToSlash(rel), nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
