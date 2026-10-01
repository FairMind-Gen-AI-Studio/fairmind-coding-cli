// Shared helpers for the devbridge /v1 handlers. Mirrors the plumbing of
// internal/devbridge/server.go and tools.go. Prototype orchestration only.
import { UpstreamError, ToolError } from './mcp.js';

export class ApiError extends Error { constructor(status, code, message) { super(message); this.status = status; this.code = code; } }
export const fail = (status, code, msg) => new ApiError(status, code, msg);

export const OID = /^[0-9a-fA-F]{24}$/;

export function mapError(err) {
  if (err instanceof ApiError) return err;
  if (err instanceof UpstreamError) {
    if (err.status === 401) return fail(401, 'TOKEN_INVALID', 'FairMind rejected the token');
    if (err.status === 403) return fail(403, 'RESOURCE_FORBIDDEN', 'FairMind denied access');
    if (err.status === 429) return fail(429, 'RATE_LIMITED', 'FairMind rate limit reached');
    return fail(503, 'SERVICE_UNAVAILABLE', `FairMind MCP server answered HTTP ${err.status}`);
  }
  if (err instanceof ToolError) {
    const m = (err.toolMessage || '').toLowerCase();
    const t = truncate(err.toolMessage, 300);
    if (/not found|no such|does not exist/.test(m)) return fail(404, 'RESOURCE_NOT_FOUND', t);
    if (/permission|forbidden|access denied|scope/.test(m)) return fail(403, 'RESOURCE_FORBIDDEN', t);
    if (/expired/.test(m)) return fail(401, 'TOKEN_EXPIRED', t);
    if (/required|invalid|refused/.test(m)) return fail(422, 'VALIDATION_ERROR', t);
    return fail(502, 'UPSTREAM_TOOL_ERROR', t);
  }
  if (err?.code === 'ETIMEDOUT_FM') return fail(504, 'TIMEOUT', 'FairMind MCP server timed out');
  return fail(503, 'SERVICE_UNAVAILABLE', 'FairMind MCP server unreachable');
}

export function str(m, ...keys) {
  if (!m) return '';
  for (const k of keys) { const v = m[k]; if (typeof v === 'string' && v) return v; if (typeof v === 'number') return String(v); }
  return '';
}
export const firstOf = (m, ...keys) => { for (const k of keys) if (m?.[k] != null) return m[k]; return null; };
export const asList = (v) => Array.isArray(v) ? v : [];
export function truncate(s, n) {
  s = s ?? '';
  const b = Buffer.from(s, 'utf8');
  if (b.length <= n) return s;
  let len = n; // match Go: cut at n bytes, then back off a partial UTF-8 sequence
  while (len > 0 && (b[len - 1] & 0xc0) === 0x80) len--;
  if (len > 0 && b[len - 1] >= 0xc0) len--;
  return b.toString('utf8', 0, len) + '…';
}
export const limit = (xs, n) => xs.length > n ? xs.slice(0, n) : xs;

// ---- tool classification (spec §25, §31.10) ----
const READ_VERBS = new Set(['get', 'list', 'search', 'context', 'expand', 'timeline', 'cat', 'grep', 'tree', 'find', 'rag', 'analyze']);
const SENSITIVE = new Set(['General_get_mcp_configs_for_agent']);

function splitName(name) { const i = name.indexOf('_'); return i > 0 ? [name.slice(0, i), name.slice(i + 1)] : ['general', name]; }

export function classify(t) {
  const lower = t.name.toLowerCase();
  const ro = t.annotations?.readOnlyHint, de = t.annotations?.destructiveHint;
  if (SENSITIVE.has(t.name)) return 'sensitive';
  if ((de === true && ro !== true) || lower.includes('erase') || lower.includes('delete')) return 'destructive';
  if (ro === true) return 'read';
  const verb = splitName(t.name)[1].split('_')[0].toLowerCase();
  return READ_VERBS.has(verb) ? 'read' : 'write';
}

export function commandFor(name) {
  const [ns0, rest] = splitName(name);
  const ns = ns0.toLowerCase();
  const sub = rest.replace(/([a-z0-9])([A-Z])/g, '$1_$2').replace(/_/g, '-').toLowerCase();
  return [ns, `${ns} ${sub}`];
}

// ---- context items ----

const RANK = { requirement: 1, decision: 2, task: 3, user_story: 3, story: 3, need: 3, issue: 4, test: 5, document: 7, code: 6 };
export function rankFor(it) {
  let r = RANK[it.kind] ?? 6;
  r = it.review_state === 'confirmed' ? r * 10 : r * 10 + 5;
  if (it.status === 'superseded' || it.status === 'retired') r += 100;
  return r;
}

export function brainItem(m, why) {
  const it = { source: 'brain', kind: str(m, 'kind'), id: str(m, 'node_id', 'id'), title: str(m, 'title'),
    status: str(m, 'status'), review_state: str(m, 'review_state') || undefined, why_relevant: why,
    excerpt: truncate(str(m, 'summary', 'excerpt'), 600), anchors: [], provenance: {} };
  if (str(m, 'subtype')) it.provenance.subtype = str(m, 'subtype');
  if (m.provenance && typeof m.provenance === 'object') Object.assign(it.provenance, m.provenance);
  if (str(m, 'reason')) it.provenance.reason = str(m, 'reason');
  const sb = str(m, 'superseded_by', 'supersededBy'); if (sb) it.superseded_by = sb;
  if (typeof m.score === 'number') it.score = m.score;
  for (const a of asList(m.anchors)) {
    if (a && typeof a === 'object') { const p = str(a, 'file_path', 'path'); if (p) it.anchors.push(p); }
    else if (typeof a === 'string') it.anchors.push(a);
  }
  it.rank = rankFor(it);
  return it;
}

export function studioItem(kind, m, why) {
  const it = { source: 'studio', kind, id: str(m, 'mindstreamId', 'id'), title: str(m, 'title', 'name'),
    status: str(m, 'status'), why_relevant: why, excerpt: truncate(str(m, 'text', 'description', 'content'), 1200),
    anchors: [], provenance: { object_id: str(m, 'id', '_id'), project: str(m, 'projectId') } };
  const sb = str(m, 'supersededBy'); if (sb) { it.superseded_by = sb; it.status = 'superseded'; }
  it.rank = rankFor(it);
  return it;
}

export class Pack {
  constructor(project) {
    this.summary = ''; this.items = []; this.warnings = []; this.follow_up = [];
    this.evidence = { via: 'fairmind-devbridge (prototype over MCP)' };
    this._seen = new Set();
    if (project) this.project = { id: project.id, name: project.name };
  }
  add(it) { if (!it.id || this._seen.has(it.source + '/' + it.id)) return; this._seen.add(it.source + '/' + it.id); this.items.push(it); }
  warn(code, message) { if (!this.warnings.some((w) => w.code === code)) this.warnings.push({ code, message }); }
  bindingWarnings(b) {
    if (!b) return;
    this.evidence.repository = { id: b.repositoryID, remote: b.remote, ingested_at: b.ingestedAt };
    if (b.stale) this.warn('STALE_REPOSITORY_DATA', "FairMind's copy of this repository is outdated; trust the local checkout for current code, FairMind for rationale");
  }
  finish(budget) {
    if (!budget || budget <= 0) budget = 8000;
    this.items.sort((a, b) => a.rank - b.rank);
    let used = 0, omitted = 0; const kept = [];
    for (const it of this.items) {
      const cost = Math.floor(JSON.stringify(it).length / 4) + 1;
      if (used + cost > budget) { omitted++; continue; }
      used += cost; kept.push(it);
    }
    this.items = kept;
    for (const it of kept) {
      if (it.status === 'superseded' || it.status === 'retired') this.warn('SUPERSEDED_KNOWLEDGE', 'some items are superseded/retired: history only, follow the newer knowledge');
      else if (it.review_state === 'proposed') this.warn('PROPOSED_KNOWLEDGE', 'some items are proposed (not human-confirmed)');
      if (it.source === 'brain' && ['decision', 'requirement', 'issue'].includes(it.kind) && this.follow_up.length < 5) {
        this.follow_up.push({ command: `fairmind brain get ${it.id} --json`, reason: `full ${it.kind} content` });
      }
    }
    if (omitted > 0) this.warn('BUDGET_TRIMMED', `${omitted} item(s) omitted to fit the budget`);
    this.budget = { requested_tokens: budget, estimated_tokens: used, omitted_items: omitted };
    this.summary = kept.length ? `${kept.length} relevant item(s) from FairMind.` : 'FairMind returned no knowledge relevant to this request.';
    for (const it of this.items) delete it.rank;
  }
  toJSON() {
    const o = { summary: this.summary };
    if (this.intent) o.intent = this.intent;
    if (this.project) o.project = this.project;
    o.items = this.items; o.warnings = this.warnings; o.follow_up = this.follow_up;
    o.evidence = this.evidence; o.budget = this.budget;
    return o;
  }
}

const STOP = new Set(['the', 'and', 'for', 'with', 'from', 'that', 'this', 'into', 'gli', 'degli', 'delle', 'della', 'dello', 'dei', 'del', 'per', 'con', 'che', 'una', 'uno', 'nel', 'nella', 'sul', 'sulla']);
export function terms(s) {
  const out = []; const seen = new Set();
  for (const w of (s || '').toLowerCase().split(/[^a-z0-9\u0080-￿]+/)) {
    if (w.length >= 3 && !STOP.has(w) && !seen.has(w)) { seen.add(w); out.push(w); }
  }
  return out;
}
export function matchTerms(ts, text) { text = (text || '').toLowerCase(); return ts.filter((t) => text.includes(t)); }
