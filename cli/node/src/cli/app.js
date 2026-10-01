// Command dispatch and per-invocation context. Mirrors internal/commands/app.go.
import { existsSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { parseArgs } from './args.js';
import { Client } from './api.js';
import { DirectClient, validateMcpUrl, DEFAULT_MCP_URL } from './directmcp.js';
import { resolveToken, ENV_VAR } from './auth.js';
import { loadSettings, validateApiUrl, PROJECT_FILE } from './config.js';
import { detectRepo, NotRepoError } from './git.js';
import { CLIError, Code, Exit, failure, newError, Printer, Redactor, validation } from './output.js';
import { rootUsage } from './usage.js';

export const DEFAULT_TIMEOUT_MS = 30000;
export const TOOL_NAMESPACES = new Set(['brain', 'code', 'general', 'insights', 'studio']);

const table = [];
export function register(cmd) { table.push(cmd); }

function leadingWords(argv) {
  const w = [];
  for (const a of argv) { if (a.startsWith('-')) break; w.push(a); }
  return w;
}

function lookup(argv) {
  const words = leadingWords(argv);
  let best = null;
  for (const c of table) {
    const parts = c.path.split(' ');
    if (parts.length <= words.length && parts.length > (best?.parts.length ?? 0) && parts.every((p, i) => p === words[i])) {
      best = { cmd: c, parts };
    }
  }
  return best ? { cmd: best.cmd, rest: argv.slice(best.parts.length) } : null;
}

export function parseTimeout(s) {
  if (!s) return DEFAULT_TIMEOUT_MS;
  const f = Number(s);
  if (!Number.isFinite(f) || f <= 0 || f > 600) throw validation('--timeout must be a number of seconds between 0 and 600');
  return Math.round(f * 1000);
}

function findConfigDir(dir) {
  for (let d = dir; d; ) {
    const f = join(d, PROJECT_FILE);
    if (existsSync(f) && statSync(f).isFile()) return d;
    const parent = dirname(d);
    if (parent === d) break;
    d = parent;
  }
  return '';
}

/** Per-invocation state shared by command handlers. */
export class Run {
  constructor(app, args, printer, redactor) {
    Object.assign(this, { app, args, printer, redactor, verbose: !!args.bools.verbose, timeoutMs: DEFAULT_TIMEOUT_MS });
    this.settings = null; this.repo = null; this.token = null;
  }

  logf(msg) { if (this.verbose) this.printer.log('[fairmind] ' + msg); }

  async loadContext(needRepo) {
    try {
      this.repo = detectRepo(this.app.git, this.app.cwd);
    } catch (e) {
      if (needRepo) {
        if (e instanceof NotRepoError) throw newError(Code.NOT_GIT_REPO, 'this command must run inside a git repository');
        throw newError(Code.GIT, e.message);
      }
      if (!(e instanceof NotRepoError)) this.logf(`git detection skipped: ${e.message}`);
    }
    const root = this.repo ? this.repo.root : findConfigDir(this.app.cwd);
    this.settings = loadSettings({ profile: this.args.str('profile'), project: this.args.str('project'), repository: this.args.str('repo') }, this.app.env, root);
    try {
      this.token = await resolveToken(this.settings.profile, this.app.env, this.app.keychain);
    } catch (e) {
      throw newError(Code.KEYCHAIN, `could not read the OS credential store: ${e.message}`);
    }
    this.redactor.addSecret(this.token.value());
  }

  // endpoint identity used for the tool-catalog cache key.
  endpoint() { return this.settings.api_url || this.settings.mcp_url || 'direct:' + DEFAULT_MCP_URL; }

  client() {
    const logf = this.verbose ? (m) => this.logf(m) : null;
    // A real server-side Agent API is used only when api_url is explicitly set.
    // Otherwise the CLI talks to the FairMind MCP server directly (no devbridge).
    if (this.settings.api_url) {
      const base = validateApiUrl(this.settings.api_url);
      if (this.token.empty()) throw newError(Code.TOKEN_MISSING, `no FairMind token found; run \`fairmind auth login\` or set ${ENV_VAR}`);
      return new Client({ baseURL: base, token: this.token, project: this.settings.project, agent: this.settings.agent, timeoutMs: this.timeoutMs, env: this.app.env, logf });
    }
    const mcp = validateMcpUrl(this.settings.mcp_url || DEFAULT_MCP_URL);
    if (this.token.empty()) throw newError(Code.TOKEN_MISSING, `no FairMind token found; run \`fairmind auth login\` or set ${ENV_VAR}`);
    return new DirectClient({ mcpURL: mcp.href, token: this.token, project: this.settings.project, agent: this.settings.agent, env: this.app.env, logf });
  }

  async call(method, path, query, body, headers) {
    const c = this.client();
    if (headers) c.headers = headers;
    return c.do(method, path, query, body);
  }

  /** Server-side binding resolution input: explicit -> remote -> configured (spec §22). */
  repositoryRef() {
    const ref = {};
    const src = this.settings.sources.repository;
    if (this.settings.repository) {
      if (src === 'flag' || src === 'env') ref.explicit = this.settings.repository;
      else ref.configured = this.settings.repository;
    }
    if (this.repo) {
      if (this.repo.remote) ref.remote = this.repo.remote;
      if (this.repo.branch) ref.branch = this.repo.branch;
      if (this.repo.head_commit) ref.head_commit = this.repo.head_commit;
    }
    const out = {};
    for (const k of ['explicit', 'remote', 'branch', 'head_commit', 'configured']) if (ref[k]) out[k] = ref[k];
    return Object.keys(out).length ? out : null;
  }
}

export class App {
  constructor(deps) { Object.assign(this, deps); }

  fail(printer, err) {
    const ce = err instanceof CLIError ? err : newError(Code.INTERNAL, err?.message ?? String(err));
    printer.envelope(failure(ce.err), '');
    return ce.exit;
  }

  async run(argv) {
    const redactor = new Redactor();
    const printer = new Printer({
      stdout: this.stdout, stderr: this.stderr, redactor,
      json: !this.stdoutIsTTY || String(this.env.FAIRMIND_OUTPUT ?? '').toLowerCase() === 'json',
    });
    const hasFlag = (f) => argv.some((a) => a === f || a.startsWith(f + '='));
    const found = lookup(argv);
    if (!found) {
      if (!argv.length || ['help', '--help', '-h'].includes(argv[0])) { printer.raw(rootUsage); return Exit.OK; }
      if (TOOL_NAMESPACES.has(argv[0])) {
        const mod = await import('./tools.js');
        return mod.runDynamic(this, printer, redactor, argv);
      }
      if (hasFlag('--json')) printer.json = true;
      return this.fail(printer, newError(Code.UNKNOWN_COMMAND, 'unknown command: ' + leadingWords(argv).join(' ') + '; run `fairmind help`'));
    }

    let args;
    try {
      args = parseArgs(found.rest, found.cmd.spec);
    } catch (e) {
      if (hasFlag('--json')) printer.json = true;
      return this.fail(printer, e);
    }
    if (args.bools.json) printer.json = true;
    if (args.bools.help) { printer.raw(found.cmd.usage); return Exit.OK; }

    const run = new Run(this, args, printer, redactor);
    try {
      run.timeoutMs = parseTimeout(args.str('timeout') || this.env.FAIRMIND_TIMEOUT || '');
    } catch (e) {
      return this.fail(printer, e);
    }
    if (args.bools['dry-run']) run.logf('--dry-run has no effect on read-only commands');

    try {
      const { env, kind } = await found.cmd.handler(run);
      if (env) printer.envelope(env, kind);
      return Exit.OK;
    } catch (e) {
      return this.fail(printer, e);
    }
  }
}

export function firstNonEmpty(...vals) { return vals.find(Boolean) ?? ''; }
