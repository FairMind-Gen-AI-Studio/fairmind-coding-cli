// Local checkout inspection: repository identity and diff metadata. File
// contents are never read or transmitted (spec §33).
import { spawnSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, relative, sep } from 'node:path';

export const EMPTY_TREE = '4b825dc642cb6eb9a060e54bf8d69288fbee4904';
export class NotRepoError extends Error {}

/** Runs git with a stable locale and no credential prompts. */
export function execGit(dir, ...args) {
  const r = spawnSync('git', ['-C', dir, '-c', 'core.quotePath=false', ...args], {
    encoding: 'utf8', maxBuffer: 64 << 20, windowsHide: true,
    env: { ...process.env, LC_ALL: 'C', LANGUAGE: 'C', GIT_TERMINAL_PROMPT: '0' },
  });
  if (r.error) throw new Error(`git ${args[0]}: ${r.error.message}`);
  if (r.status !== 0) {
    const msg = (r.stderr ?? '').trim();
    if (msg.includes('not a git repository')) throw new NotRepoError('not a git repository');
    throw new Error(`git ${args[0]}: ${msg.split('\n')[0]}`);
  }
  return r.stdout;
}

export function detectRepo(run, dir) {
  const repo = { root: run(dir, 'rev-parse', '--show-toplevel').trim() };
  try { repo.remote = normalizeRemote(run(dir, 'remote', 'get-url', 'origin').trim()); } catch { /* no origin */ }
  try { repo.branch = run(dir, 'rev-parse', '--abbrev-ref', 'HEAD').trim(); } catch { /* unborn */ }
  try { repo.head_commit = run(dir, 'rev-parse', '--verify', '-q', 'HEAD').trim(); } catch { /* no commits */ }
  return repo;
}

const scpLike = /^(?:[^@/]+@)?([^:/]+):(.+)$/;

/** Any remote URL -> "host/owner/name"; embedded credentials are dropped. */
export function normalizeRemote(raw) {
  raw = (raw ?? '').trim();
  if (!raw) return '';
  let host, path;
  if (raw.includes('://')) {
    try { const u = new URL(raw); host = u.hostname; path = decodeURIComponent(u.pathname); } catch { return ''; }
  } else {
    const m = scpLike.exec(raw);
    if (!m) return '';
    [, host, path] = m;
  }
  path = path.replace(/^\/+|\/+$/g, '').replace(/\.git$/, '');
  if (!host || !path) return '';
  return `${host.toLowerCase()}/${path}`;
}

const statusNames = { A: 'added', M: 'modified', D: 'deleted', R: 'renamed', C: 'copied', T: 'typechange', U: 'unmerged' };

export function diffMetadata(run, root, { base = '', symbols = false } = {}) {
  // Reject option-like refs: git parses options anywhere before "--", so a
  // base such as "--output=/path" would be an argument-injection vector.
  if (base.startsWith('-')) throw new Error(`invalid base ref "${base}"`);
  let ref = base || 'HEAD';
  try { run(root, 'rev-parse', '--verify', '-q', `${ref}^{commit}`); } catch {
    if (base) throw new Error(`unknown base ref "${base}"`);
    ref = EMPTY_TREE; // repository without commits
  }
  const changes = [];
  const index = new Map();

  const ns = run(root, 'diff', '--name-status', '-z', '-M', ref).split('\0');
  for (let i = 0; i < ns.length; i++) {
    const code = ns[i];
    if (!code) continue;
    const fc = { path: '', status: statusNames[code[0]] ?? 'modified' };
    if ((code[0] === 'R' || code[0] === 'C') && i + 2 < ns.length) {
      fc.old_path = ns[i + 1]; fc.path = ns[i + 2]; i += 2;
    } else if (i + 1 < ns.length) {
      fc.path = ns[i + 1]; i++;
    }
    index.set(fc.path, changes.length);
    changes.push(fc);
  }

  const num = run(root, 'diff', '--numstat', '-z', '-M', ref).split('\0');
  for (let i = 0; i < num.length; i++) {
    const cols = num[i].split('\t');
    if (cols.length < 3) continue;
    let path = cols.slice(2).join('\t');
    if (path === '' && i + 2 < num.length) { path = num[i + 2]; i += 2; }
    const idx = index.get(path);
    if (idx === undefined) continue;
    if (cols[0] === '-') { changes[idx].binary = true; continue; }
    changes[idx].additions = Number(cols[0]);
    changes[idx].deletions = Number(cols[1]);
  }

  const patch = run(root, 'diff', '-U0', '-M', '--no-color', '--no-ext-diff', ref);
  let cur = -1;
  for (const line of patch.split('\n')) {
    if (line.startsWith('diff --git ')) { cur = -1; continue; }
    if (line.startsWith('+++ ')) {
      let p = line.slice(4);
      if (p === '/dev/null') continue;
      p = p.replace(/^"|"$/g, '').replace(/^b\//, '');
      if (index.has(p)) cur = index.get(p);
      continue;
    }
    const to = /^(?:rename|copy) to (.*)$/.exec(line);
    if (to) { if (index.has(to[1])) cur = index.get(to[1]); continue; }
    if (line.startsWith('@@ ') && cur >= 0) {
      const m = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@ ?(.*)$/.exec(line);
      if (!m) continue;
      (changes[cur].hunks ??= []).push({ start: Number(m[1]), lines: m[2] !== undefined ? Number(m[2]) : 1 });
      if (symbols) {
        const sym = extractSymbol(m[3]);
        if (sym && !(changes[cur].symbols ?? []).includes(sym)) (changes[cur].symbols ??= []).push(sym);
      }
    }
  }

  for (const p of run(root, 'ls-files', '--others', '--exclude-standard', '-z').split('\0')) {
    if (p && !index.has(p)) { index.set(p, changes.length); changes.push({ path: p, status: 'untracked' }); }
  }
  // Field order as in the Go edition (path, old_path, status, additions, deletions, binary, hunks, symbols).
  return changes.map((c) => {
    const o = { path: c.path };
    if (c.old_path) o.old_path = c.old_path;
    o.status = c.status;
    if (c.additions !== undefined) { o.additions = c.additions; o.deletions = c.deletions; }
    if (c.binary) o.binary = true;
    if (c.hunks) o.hunks = c.hunks;
    if (c.symbols) o.symbols = c.symbols;
    return o;
  });
}

const symbolPatterns = [
  /\bfunc\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)/,
  /\b(?:function|def|class|interface|struct|enum|trait|impl|fn|type|module|record)\s+([A-Za-z_$][\w$]*)/,
  /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\(|function)/,
  /([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*(?::[^{]*)?\{?\s*$/,
];
const notSymbols = new Set(['if', 'for', 'while', 'switch', 'catch', 'return']);

/** Reduces a hunk-header context line to the enclosing identifier only. */
export function extractSymbol(ctx) {
  ctx = (ctx ?? '').trim();
  if (!ctx) return '';
  for (const re of symbolPatterns) {
    const m = re.exec(ctx);
    if (m && !notSymbols.has(m[1])) return m[1];
  }
  return '';
}

/** Repository-relative, slash-separated path; rejects paths outside the repo. */
export function relToRoot(root, cwd, p) {
  let abs = isAbsolute(p) ? p : join(cwd, p);
  try { root = realpathSync(root); } catch { /* keep */ }
  try { abs = join(realpathSync(dirname(abs)), basename(abs)); } catch { /* keep */ }
  const rel = relative(root, abs);
  if (rel === '..' || rel.startsWith('..' + sep) || isAbsolute(rel)) throw new Error(`path "${p}" is outside the repository`);
  return rel.split(sep).join('/');
}
