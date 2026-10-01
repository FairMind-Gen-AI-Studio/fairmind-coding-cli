// Normalized JSON envelope, error codes, exit codes, redaction and human
// rendering. Mirrors internal/output of the Go edition.

export const Exit = {
  OK: 0, GENERIC: 1, AUTH: 2, NOT_FOUND: 3, FORBIDDEN: 4,
  VALIDATION: 5, CONFLICT: 6, UNAVAILABLE: 7, TIMEOUT: 8,
};

export const Code = {
  TOKEN_MISSING: 'TOKEN_MISSING',
  VALIDATION: 'VALIDATION_ERROR',
  UNKNOWN_COMMAND: 'UNKNOWN_COMMAND',
  CONFIG_INVALID: 'CONFIG_INVALID',
  CONFIG_SECRET: 'CONFIG_CONTAINS_SECRET',
  API_URL_MISSING: 'API_URL_MISSING',
  API_URL_INSECURE: 'API_URL_INSECURE',
  GIT: 'GIT_ERROR',
  NOT_GIT_REPO: 'NOT_A_GIT_REPOSITORY',
  KEYCHAIN: 'KEYCHAIN_ERROR',
  TIMEOUT: 'TIMEOUT',
  SERVICE_UNAVAILABLE: 'SERVICE_UNAVAILABLE',
  RATE_LIMITED: 'RATE_LIMITED',
  INVALID_RESPONSE: 'INVALID_RESPONSE',
  RESPONSE_TOO_LARGE: 'RESPONSE_TOO_LARGE',
  UNEXPECTED_REDIRECT: 'UNEXPECTED_REDIRECT',
  INTERNAL: 'INTERNAL_ERROR',
};

const codeExits = {
  TOKEN_MISSING: Exit.AUTH, TOKEN_EXPIRED: Exit.AUTH, TOKEN_INVALID: Exit.AUTH,
  SCOPE_REQUIRED: Exit.FORBIDDEN, ROLE_REQUIRED: Exit.FORBIDDEN, SESSION_PRIVATE: Exit.FORBIDDEN,
  RESOURCE_FORBIDDEN: Exit.FORBIDDEN, DESTRUCTIVE_NOT_ALLOWED: Exit.FORBIDDEN,
  INVALID_STATE_TRANSITION: Exit.CONFLICT, IDEMPOTENCY_CONFLICT: Exit.CONFLICT,
  VALIDATION_ERROR: Exit.VALIDATION, UNKNOWN_COMMAND: Exit.VALIDATION, CONFIG_INVALID: Exit.VALIDATION,
  CONFIG_CONTAINS_SECRET: Exit.VALIDATION, API_URL_MISSING: Exit.VALIDATION, API_URL_INSECURE: Exit.VALIDATION,
  NOT_A_GIT_REPOSITORY: Exit.VALIDATION, REPOSITORY_AMBIGUOUS: Exit.VALIDATION, PROJECT_AMBIGUOUS: Exit.VALIDATION,
  PROJECT_REQUIRED: Exit.VALIDATION, WRITE_NOT_CONFIRMED: Exit.VALIDATION,
  TIMEOUT: Exit.TIMEOUT, SERVICE_UNAVAILABLE: Exit.UNAVAILABLE, RATE_LIMITED: Exit.UNAVAILABLE,
};

export function exitForCode(code) {
  const c = String(code).toUpperCase();
  if (c in codeExits) return codeExits[c];
  if (c.endsWith('_NOT_FOUND')) return Exit.NOT_FOUND;
  return undefined;
}

export function exitForStatus(status) {
  if (status === 401) return Exit.AUTH;
  if (status === 403) return Exit.FORBIDDEN;
  if (status === 404) return Exit.NOT_FOUND;
  if (status === 400 || status === 422) return Exit.VALIDATION;
  if (status === 409) return Exit.CONFLICT;
  if (status === 408 || status === 504) return Exit.TIMEOUT;
  if (status === 429 || status >= 500) return Exit.UNAVAILABLE;
  return Exit.GENERIC;
}

export const isRetryableExit = (exit) => exit === Exit.UNAVAILABLE || exit === Exit.TIMEOUT;

/** A normalized failure carrying the process exit code. */
export class CLIError extends Error {
  constructor(err, exit) {
    super(`${err.code}: ${err.message}`);
    this.err = err;
    this.exit = exit;
  }
}

export function newError(code, message, details) {
  const exit = exitForCode(code) ?? Exit.GENERIC;
  return new CLIError({ code, message, retryable: isRetryableExit(exit), details: details ?? {} }, exit);
}

export const validation = (message) => newError(Code.VALIDATION, message);

export const success = (data, page = null) => ({ ok: true, data, page, evidence: null, error: null });

export function failure(err) {
  const { code, message, retryable = false, details = {} } = err;
  return { ok: false, data: null, page: null, evidence: null, error: { code, message, retryable, details: details ?? {} } };
}

// ---- redaction ----

const REDACTED = '[REDACTED]';
const jwtPattern = /eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]*/g;
const bearerPattern = /(bearer\s+)[^\s"',\\]+/gi;
const secretKVPattern = /("?(?:authorization|cookie|set-cookie|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password)"?\s*[:=]\s*"?)([^"\\\s,}]+)/gi;

/** Removes known secrets and credential-shaped strings from printed text. */
export class Redactor {
  constructor() { this.secrets = []; }
  addSecret(s) { if (s && s.length >= 8) this.secrets.push(s); }
  redact(s) {
    for (const secret of this.secrets) s = s.split(secret).join(REDACTED);
    return s.replace(jwtPattern, REDACTED).replace(bearerPattern, `$1${REDACTED}`).replace(secretKVPattern, `$1${REDACTED}`);
  }
}

// ---- printing ----

export class Printer {
  constructor({ stdout, stderr, json, redactor }) {
    Object.assign(this, { stdout, stderr, json, redactor });
  }
  write(stream, s) { stream.write(this.redactor ? this.redactor.redact(s) : s); }
  raw(s) { this.write(this.stdout, s); }
  log(s) { this.write(this.stderr, s + '\n'); }
  envelope(env, kind) {
    if (this.json) {
      this.write(this.stdout, JSON.stringify(env, null, 2) + '\n');
      return;
    }
    if (!env.ok) {
      this.write(this.stderr, `error: ${env.error.code}: ${env.error.message}\n`);
      return;
    }
    this.write(this.stdout, renderHuman(env.data, kind));
  }
}

const oneLine = (s, max) => {
  s = String(s ?? '').split(/\s+/).filter(Boolean).join(' ');
  return s.length > max ? s.slice(0, max) + '…' : s;
};

function renderWarnings(ws) {
  if (!ws?.length) return '';
  return '\nWarnings:\n' + ws.map((w) => typeof w === 'string' ? `  - ${w}\n`
    : w.code ? `  - ${w.code}: ${w.message}\n` : `  - ${w.message}\n`).join('');
}

function renderHuman(data, kind) {
  if (kind === 'context' && data && typeof data === 'object') {
    let b = '';
    if (data.project?.name) b += `Project: ${data.project.name} (${data.project.id})\n`;
    if (data.summary) b += `\n${data.summary}\n`;
    const items = data.items ?? [];
    if (!items.length) b += '\nNo FairMind context items returned.\n';
    for (const it of items) {
      b += `\n[${it.source}/${it.kind}] ${it.id}  ${it.title}\n`;
      const tags = [it.status, it.review_state].filter(Boolean).join(', ');
      if (tags) b += `  state: ${tags}\n`;
      if (it.why_relevant) b += `  why:   ${it.why_relevant}\n`;
      if (it.excerpt) b += `  ${oneLine(it.excerpt, 240)}\n`;
    }
    b += renderWarnings(data.warnings);
    if (data.follow_up?.length) b += '\nFollow-up:\n' + data.follow_up.map((f) => `  ${f.command}  # ${f.reason}\n`).join('');
    if (data.budget) b += `\nBudget: ~${data.budget.estimated_tokens}/${data.budget.requested_tokens} tokens, ${data.budget.omitted_items} item(s) omitted\n`;
    return b;
  }
  if (kind === 'search' && data?.items) {
    let b = data.items.length ? '' : 'No results.\n';
    for (const it of data.items) {
      b += `${Number(it.score ?? 0).toFixed(2)}  [${it.source}/${it.kind}] ${it.id}  ${it.title}\n`;
      if (it.excerpt) b += `      ${oneLine(it.excerpt, 200)}\n`;
    }
    return b + renderWarnings(data.warnings);
  }
  if (kind === 'tools' && data?.tools) {
    return data.tools.map((t) => `${t.access.padEnd(11)} ${t.command.padEnd(52)} ${oneLine(t.summary, 90)}\n`).join('')
      + `\n${data.count} tool(s). \`fairmind <namespace> <command> --help\` for parameters.\n`;
  }
  if (kind === 'work' && data?.items) {
    if (!data.items.length) return `No ${data.kind} items.\n`;
    return data.items.map((it) => `${String(it.id).padEnd(16)} ${String(it.status ?? '').padEnd(12)} ${oneLine(it.title, 100)}\n`).join('');
  }
  return JSON.stringify(data, null, 2) + '\n';
}
