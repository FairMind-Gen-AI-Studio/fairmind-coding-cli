// Implements the proposed /v1 Agent API by orchestrating MCP tools.
// Development-only prototype; mirrors internal/devbridge/server.go + tools.go.
import { createServer } from 'node:http';
import { MCPClient } from './mcp.js';
import {
  ApiError, Pack, asList, brainItem, classify, commandFor, fail, firstOf, limit,
  mapError, matchTerms, OID, str, studioItem, terms, truncate,
} from './helpers.js';

const SESSION_TTL_MS = 10 * 60 * 1000;
const WORK_TOOLS = { task: 'Studio_list_tasks_by_project', story: 'Studio_list_user_stories_by_project', epic: 'Studio_list_needs_by_project', need: 'Studio_list_needs_by_project' };
const SENSITIVE_ACCESS = new Set(['destructive', 'sensitive']);

export class DevBridge {
  constructor(mcpURL, logger = console) {
    this.mcp = new MCPClient(mcpURL);
    this.log = logger;
    this._catalog = new Map(); // authKey -> {tools, fetched}
  }

  handler() {
    return async (req, res) => {
      const start = Date.now();
      const url = new URL(req.url, 'http://127.0.0.1');
      let status = 200;
      try {
        status = await this.route(req, res, url);
      } catch (e) {
        const ae = mapError(e); status = ae.status;
        writeErr(res, ae);
      } finally {
        this.log.info?.(`${req.method} ${url.pathname} -> ${status} (${Date.now() - start}ms) request_id=${req.headers['x-request-id'] ?? ''}`);
      }
    };
  }

  // dispatch runs one /v1 request in-process and returns { data, page } or
  // throws an ApiError. Used by both the HTTP server and the CLI's direct-MCP
  // mode, so the two paths share identical behavior.
  async dispatch(ctx) {
    const p = ctx.pathname;
    if (!ctx.auth || !ctx.auth.startsWith('Bearer ') || ctx.auth.length < 16) throw fail(401, 'TOKEN_MISSING', 'Authorization: Bearer <token> is required');
    const c = new Call(this, ctx);
    const exact = {
      'GET /v1/projects': () => c.projects(),
      'GET /v1/search': () => c.search(),
      'POST /v1/context:get': () => c.contextGet(),
      'POST /v1/context:for-task': () => c.contextForTask(),
      'POST /v1/context:for-code-change': () => c.contextForCodeChange(),
      'GET /v1/tools': () => c.listTools(),
      'GET /v1/work': () => c.listWork(),
    };
    for (const key of Object.keys(exact)) {
      const [m, path] = key.split(' ');
      if (p === path) {
        if (ctx.method !== m) throw fail(405, 'METHOD_NOT_ALLOWED', `use ${m}`);
        return await exact[key]();
      }
    }
    if (p.startsWith('/v1/tools/')) return await c.toolsRoute();
    if (p.startsWith('/v1/brain/nodes/')) return await c.brainGet();
    throw fail(404, 'RESOURCE_NOT_FOUND', 'unknown endpoint');
  }

  async route(req, res, url) {
    if (url.pathname === '/healthz') { writeOK(res, { status: 'ok' }, null); return 200; }
    let bodyObj = {};
    if (req.method === 'POST') {
      const chunks = []; for await (const ch of req) chunks.push(ch);
      const text = Buffer.concat(chunks).toString('utf8');
      try { bodyObj = text ? JSON.parse(text) : {}; } catch { writeErr(res, fail(400, 'VALIDATION_ERROR', 'invalid JSON body')); return 400; }
    }
    const ctx = { auth: req.headers['authorization'], method: req.method, pathname: url.pathname, searchParams: url.searchParams, headers: req.headers, body: bodyObj };
    const { data, page } = await this.dispatch(ctx);
    writeOK(res, data, page ?? null);
    return 200;
  }
}

class Call {
  // ctx: { auth, method, pathname, searchParams, headers, body }
  constructor(bridge, ctx) {
    this.b = bridge;
    this.auth = ctx.auth;
    this.method = ctx.method;
    this.pathname = ctx.pathname;
    this.searchParams = ctx.searchParams;
    this.headers = ctx.headers;
    this._bodyObj = ctx.body ?? {};
  }

  async tool(name, args) {
    for (const k of Object.keys(args)) if (args[k] == null || args[k] === '') delete args[k];
    const raw = await this.b.mcp.callTool(this.auth, name, args);
    if (raw && typeof raw === 'object' && !Array.isArray(raw)) return raw;
    if (Array.isArray(raw)) return { result: raw };
    return { text: String(raw ?? '') };
  }

  async body() { return this._bodyObj; }

  // ---- projects ----
  async listProjects() {
    const res = await this.tool('General_list_projects', {});
    return asList(firstOf(res, 'result', 'projects', 'items')).map((p) => ({ id: str(p, 'id', '_id', 'project_id'), name: str(p, 'name') }));
  }

  async resolveProject(ref, required) {
    const ps = await this.listProjects();
    if (!ref) {
      if (ps.length === 1) return ps[0];
      if (!required) return null;
      if (!ps.length) throw fail(404, 'PROJECT_NOT_FOUND', 'no FairMind project is accessible with this token');
      throw fail(400, 'PROJECT_REQUIRED', `${ps.length} projects are accessible; pass --project or add .fairmind/config.json`);
    }
    const p = ps.find((x) => x.id === ref || x.name.toLowerCase() === ref.toLowerCase());
    if (!p) throw fail(404, 'PROJECT_NOT_FOUND', `project "${ref}" was not found or is not accessible`);
    return p;
  }

  async projects() {
    const ps = await this.listProjects();
    return { data: ps.map((p) => ({ id: p.id, name: p.name })), page: { cursor: null, limit: ps.length, total: ps.length, has_more: false } };
  }

  // ---- search ----
  async ragDocs(query, projectID, k) {
    const res = await this.tool('General_rag_retrieve_documents', { query, project_id: projectID, k });
    const out = [];
    asList(firstOf(res, 'result', 'documents', 'results', 'items')).forEach((m, i) => {
      if (!m || typeof m !== 'object') return;
      const meta = m.metadata ?? {};
      const title = str(meta, 'title', 'file_name', 'filename', 'source', 'name') || str(m, 'title', 'name', 'source');
      let id = str(meta, 'document_id', 'doc_id', 'id', 'source') || str(m, 'id', 'document_id') || `doc-${i + 1}`;
      const it = { source: 'document', kind: 'document', id, title, status: 'indexed', review_state: undefined,
        why_relevant: 'matched document search', excerpt: truncate(str(m, 'page_content', 'content', 'text', 'chunk'), 600),
        score: typeof m.score === 'number' ? m.score : 0, anchors: [], provenance: {} };
      out.push(it);
    });
    return out;
  }

  async studioSearch(projectID, query) {
    const ts = terms(query);
    if (!ts.length) return [];
    const lists = [['need', 'Studio_list_needs_by_project'], ['story', 'Studio_list_user_stories_by_project'], ['task', 'Studio_list_tasks_by_project']];
    const results = await Promise.all(lists.map(async ([kind, tool]) => {
      const items = [];
      for (let skip = 0; skip < 500; skip += 100) {
        const page = await this.tool(tool, { project_id: projectID, fields: 'summary', limit: 100, skip });
        for (const row of asList(firstOf(page, 'items', 'result'))) {
          if (!row || typeof row !== 'object') continue;
          const hit = matchTerms(ts, str(row, 'title'));
          if (!hit.length) continue;
          const it = studioItem(kind, row, 'title matches: ' + hit.join(', '));
          it.score = Math.trunc((hit.length / ts.length) * 100) / 100;
          items.push(it);
        }
        if (!page.hasMore) break;
      }
      return items;
    }));
    return results.flat();
  }

  notInBrain(studio, brain) {
    return studio.filter((s) => !brain.some((b) => b.source === 'brain' && b.title.length >= 30 && s.title.startsWith(b.title.replace(/…$/, ''))));
  }
  brainSparse(n) { return { code: 'BRAIN_INDEX_SPARSE', message: `${n} Studio item(s) matched by title only: FairMind Brain has not indexed them, so linked decisions/anchors may be missing; use \`fairmind context for-task <id>\` for details` }; }

  async search() {
    const q = this.searchParams;
    const query = (q.get('q') || '').trim();
    if (!query) throw fail(422, 'VALIDATION_ERROR', 'q is required');
    let k = parseInt(q.get('k') || '', 10); if (!k || k > 50) k = 10;
    const inSet = new Set((q.get('in') || '').split(',').map((s) => s.trim()).filter(Boolean));
    const all = inSet.size === 0;
    const p = await this.resolveProject(q.get('project') || '', true);
    let items = []; const warnings = [];
    const jobs = [];
    if (all || inSet.has('brain') || inSet.has('studio')) jobs.push((async () => {
      try {
        const res = await this.tool('Brain_search', { query, k, project: p.id });
        for (const row of asList(res.results)) {
          const it = brainItem(row, 'matched search');
          const isStudio = it.provenance?.source === 'studio';
          if (all || (inSet.has('brain') && !isStudio) || (inSet.has('studio') && isStudio)) items.push(it);
        }
      } catch (e) { warnings.push({ code: 'SOURCE_UNAVAILABLE', message: 'brain: ' + mapError(e).message }); }
    })());
    if (all || inSet.has('docs')) jobs.push((async () => {
      try { items.push(...await this.ragDocs(query, p.id, k)); }
      catch (e) { warnings.push({ code: 'SOURCE_UNAVAILABLE', message: 'docs: ' + mapError(e).message }); }
    })());
    let studio = [];
    if (all || inSet.has('studio')) jobs.push((async () => {
      try { studio = await this.studioSearch(p.id, query); }
      catch (e) { warnings.push({ code: 'SOURCE_UNAVAILABLE', message: 'studio: ' + mapError(e).message }); }
    })());
    if (inSet.has('code')) jobs.push((async () => { warnings.push({ code: 'CODE_SEARCH_SKIPPED', message: 'code search needs --repo <repository name or id> in this prototype' }); })());
    await Promise.all(jobs);
    const extra = this.notInBrain(studio, items);
    if (extra.length) { items.push(...extra); warnings.push(this.brainSparse(extra.length)); }
    items.sort((a, b) => (b.score ?? 0) - (a.score ?? 0));
    const total = items.length;
    if (items.length > k) items = items.slice(0, k);
    for (const it of items) delete it.rank;
    return { data: { items, warnings }, page: { cursor: null, limit: k, total, has_more: total > k } };
  }

  // ---- context:get ----
  async contextGet() {
    const req = await this.body();
    if (!(req.intent || '').trim()) throw fail(422, 'VALIDATION_ERROR', 'intent is required');
    const p = await this.resolveProject(req.project || '', true);
    const pk = new Pack(p); pk.intent = req.intent;
    if (req.task) await this.traverse(pk, req.task);
    try {
      const res = await this.tool('Brain_search', { query: req.intent, k: 10, project: p.id, repository: repoName(req.repository), git_remote: gitRemote(req.repository) });
      for (const row of asList(res.results)) pk.add(brainItem(row, 'semantically related to the intent'));
    } catch (e) { pk.warn('SOURCE_UNAVAILABLE', 'brain search failed: ' + mapError(e).message); }
    try {
      const found = await this.studioSearch(p.id, req.intent);
      const extra = this.notInBrain(found, pk.items);
      if (extra.length) { extra.sort((a, b) => (b.score ?? 0) - (a.score ?? 0)); for (const it of limit(extra, 10)) pk.add(it); const w = this.brainSparse(extra.length); pk.warn(w.code, w.message); }
    } catch { /* studio optional */ }
    try { for (const d of await this.ragDocs(req.intent, p.id, 5)) pk.add(d); } catch { /* rag optional */ }
    for (const path of limit(req.paths ?? [], 10)) await this.fileContext(pk, p, req.repository, path, '');
    if (!pk.items.length) pk.warn('NO_RELEVANT_CONTEXT', 'FairMind returned nothing for this intent');
    pk.finish(req.budget_tokens);
    return { data: pk.toJSON() };
  }

  // ---- work items resolution ----
  async fetchWork(id) {
    const up = id.toUpperCase();
    let tries;
    if (up.startsWith('TASK-')) tries = [['task', 'Studio_get_task', 'task_id']];
    else if (up.startsWith('US-')) tries = [['story', 'Studio_get_user_story', 'user_story_id']];
    else if (up.startsWith('EPIC-') || up.startsWith('NEED-')) tries = [['need', 'Studio_get_need', 'need_id']];
    else if (OID.test(id)) tries = [['task', 'Studio_get_task', 'task_id'], ['story', 'Studio_get_user_story', 'user_story_id'], ['need', 'Studio_get_need', 'need_id']];
    else throw fail(422, 'VALIDATION_ERROR', `unrecognized id "${id}": expected TASK-, US-, EPIC-/NEED- or an ObjectId`);
    for (const [kind, tool, arg] of tries) {
      try {
        const res = await this.tool(tool, { [arg]: id });
        if (str(res, 'id', '_id') || str(res, 'mindstreamId')) return { kind, ent: res };
      } catch (e) {
        const st = mapError(e).status;
        if (st !== 404 && st !== 422 && st !== 502) throw e;
      }
    }
    throw fail(404, 'TASK_NOT_FOUND', `no task, user story or need ${id} is accessible`);
  }

  async traverse(pk, id) {
    const { kind, ent } = await this.fetchWork(id);
    pk.evidence.resolved_id = { input: id, id: str(ent, 'mindstreamId'), object_id: str(ent, 'id', '_id'), kind };
    pk.add(studioItem(kind, ent, 'requested item'));
    let storyID = '', needID = '';
    if (kind === 'task') { storyID = str(ent, 'userStoryId', 'user_story_id', 'storyId'); needID = str(ent, 'needId', 'need_id'); }
    else if (kind === 'story') { storyID = str(ent, 'id', '_id'); needID = str(ent, 'needId', 'need_id'); }
    if (kind === 'task' && storyID) {
      try { const st = await this.tool('Studio_get_user_story', { user_story_id: storyID }); pk.add(studioItem('story', st, 'parent user story of ' + str(ent, 'mindstreamId'))); if (!needID) needID = str(st, 'needId', 'need_id'); } catch { /* optional */ }
    }
    if (needID) { try { const nd = await this.tool('Studio_get_need', { need_id: needID }); pk.add(studioItem('need', nd, 'parent need/epic')); } catch { /* optional */ } }
    if (storyID) {
      try { const tests = await this.tool('Studio_list_tests_by_userstory', { user_story_id: storyID, fields: 'full', limit: 20 }); for (const t of asList(firstOf(tests, 'items', 'result'))) if (t && typeof t === 'object') pk.add(studioItem('test', t, 'test case of the user story')); } catch { /* optional */ }
    }
    const title = str(ent, 'title');
    if (title) {
      try { const res = await this.tool('Brain_search', { query: title, k: 8, project: str(ent, 'projectId'), kinds: ['decision', 'requirement', 'issue'] }); for (const row of asList(res.results)) pk.add(brainItem(row, 'related knowledge for ' + str(ent, 'mindstreamId'))); }
      catch (e) { pk.warn('SOURCE_UNAVAILABLE', 'brain search failed: ' + mapError(e).message); }
    }
  }

  async contextForTask() {
    const req = await this.body();
    const pk = new Pack(null);
    await this.traverse(pk, (req.id || '').trim());
    if (pk.items.length) {
      const pid = pk.items[0].provenance?.project;
      try { const ps = await this.listProjects(); const p = ps.find((x) => x.id === pid); if (p) pk.project = { id: p.id, name: p.name }; } catch { /* optional */ }
    }
    pk.finish(req.budget_tokens);
    return { data: pk.toJSON() };
  }

  // ---- context:for-code-change ----
  async fileContext(pk, p, ref, path, symbol) {
    const args = { file_path: path, repository: repoName(ref), git_remote: gitRemote(ref), max_items: 10, detail_level: 'standard', symbol_name: symbol };
    if (p) args.project = p.id;
    let res;
    try { res = await this.tool('Brain_context', args); }
    catch (e) { const m = mapError(e); if (/repositor/i.test(m.message)) pk.warn('REPOSITORY_NOT_BOUND', 'FairMind could not resolve this repository: ' + m.message); else pk.warn('SOURCE_UNAVAILABLE', `context for ${path}: ${m.message}`); return; }
    for (const row of asList(res.items)) pk.add(brainItem(row, symbol ? `anchored to ${path}#${symbol}` : `anchored to ${path}`));
    const repo = res.staleness?.repo;
    if (repo) {
      pk.evidence.repository_sync = repo;
      const state = str(repo, 'sync_state');
      if (state === 'stale' || state === 'outdated') pk.warn('STALE_REPOSITORY_DATA', "FairMind's copy of this repository is outdated; trust the local checkout for current code");
      if (!str(repo, 'catalog_id') && state === 'unknown') pk.warn('REPOSITORY_NOT_BOUND', 'repository is not bound/ingested in FairMind; results rely on knowledge anchors only');
    }
  }

  async contextForCodeChange() {
    const req = await this.body();
    if (!asList(req.changes).length) throw fail(422, 'VALIDATION_ERROR', 'changes must not be empty');
    if (!repoName(req.repository) && !gitRemote(req.repository)) throw fail(404, 'REPOSITORY_NOT_FOUND', 'no repository identity: run inside a git checkout or pass --repo');
    const p = await this.resolveProject(req.project || '', false);
    const pk = new Pack(p);
    const jobs = [];
    for (const ch of req.changes) {
      jobs.push([ch.path, '']);
      if (ch.old_path) jobs.push([ch.old_path, '']);
      for (const sym of limit(ch.symbols ?? [], 3)) jobs.push([ch.path, sym]);
    }
    let list = jobs;
    if (jobs.length > 30) { pk.warn('CHANGESET_TRUNCATED', `only the first 30 of ${jobs.length} file/symbol lookups were performed`); list = jobs.slice(0, 30); }
    // Sequential to keep MCP sessions simple; small changesets.
    for (const [path, sym] of list) await this.fileContext(pk, p, req.repository, path, sym);
    if (!pk.items.length) pk.warn('NO_KNOWLEDGE_FOR_FILES', 'FairMind has no decisions, requirements or issues anchored to these files');
    pk.finish(req.budget_tokens);
    return { data: pk.toJSON() };
  }

  // ---- tools ----
  async catalog(refresh) {
    const key = this.auth;
    const e = this.b._catalog.get(key);
    if (e && !refresh && Date.now() - e.fetched < SESSION_TTL_MS) return e.tools;
    const raw = await this.b.mcp.listTools(this.auth);
    const tools = raw.map((t) => {
      const [ns, cmd] = commandFor(t.name);
      let schema = t.inputSchema; if (!schema || !Object.keys(schema).length) schema = { type: 'object', properties: {} };
      return { name: t.name, namespace: ns, command: cmd, description: (t.description || '').trim(), access: classify(t), input_schema: schema };
    }).sort((a, b) => a.name.localeCompare(b.name));
    this.b._catalog.set(key, { tools, fetched: Date.now() });
    return tools;
  }

  async listTools() {
    const tools = await this.catalog(this.searchParams.get('refresh') === 'true');
    const counts = {}; for (const t of tools) counts[t.access] = (counts[t.access] ?? 0) + 1;
    return { data: { tools, counts }, page: { cursor: null, limit: tools.length, total: tools.length, has_more: false } };
  }

  async normalizeProjectArgs(args) {
    for (const key of ['project', 'project_id', 'projectId']) {
      const v = args[key];
      if (typeof v !== 'string' || !v || OID.test(v)) continue;
      const p = await this.resolveProject(v, true);
      args[key] = p.id;
    }
  }

  async toolsRoute() {
    let name = this.pathname.slice('/v1/tools/'.length);
    const isCall = name.endsWith(':call'); if (isCall) name = name.slice(0, -':call'.length);
    name = decodeURIComponent(name);
    if (!name || name.includes('/')) throw fail(404, 'TOOL_NOT_FOUND', 'unknown tool path');
    const tool = (await this.catalog(false)).find((t) => t.name === name);
    if (!tool) throw fail(404, 'TOOL_NOT_FOUND', `FairMind tool "${name}" does not exist or is not available to this token`);
    if (!isCall && this.method === 'GET') return { data: tool };
    if (!(isCall && this.method === 'POST')) throw fail(405, 'METHOD_NOT_ALLOWED', 'use GET /v1/tools/{name} or POST /v1/tools/{name}:call');
    const req = await this.body();
    const args = req.arguments ?? {};
    if (tool.access !== 'read' && this.headers['x-fairmind-write-intent'] !== 'confirmed') throw fail(400, 'WRITE_NOT_CONFIRMED', `${tool.name} modifies FairMind (${tool.access}); confirm the write intent (CLI: --yes)`);
    if (SENSITIVE_ACCESS.has(tool.access) && this.headers['x-fairmind-allow-destructive'] !== '1') throw fail(403, 'DESTRUCTIVE_NOT_ALLOWED', `${tool.name} is ${tool.access} and is disabled unless explicitly allowed (CLI: FAIRMIND_ALLOW_DESTRUCTIVE=1)`);
    await this.normalizeProjectArgs(args);
    if (req.dry_run) return { data: { dry_run: true, tool: tool.name, access: tool.access, arguments: args } };
    const raw = await this.b.mcp.callTool(this.auth, tool.name, args);
    return { data: raw };
  }

  // ---- work ----
  async listWork() {
    const q = this.searchParams;
    let kind = (q.get('kind') || 'task').toLowerCase();
    const tool = WORK_TOOLS[kind]; if (!tool) throw fail(422, 'VALIDATION_ERROR', 'kind must be task, story or epic');
    const p = await this.resolveProject(q.get('project') || '', true);
    let lim = parseInt(q.get('limit') || '', 10); if (!lim || lim > 100) lim = 50;
    let skip = parseInt(q.get('cursor') || '0', 10) || 0;
    let all = q.get('all') === 'true';
    const status = (q.get('status') || '').toLowerCase();
    let parent = (q.get('parent') || '').trim();
    if (parent) { all = true; if (!OID.test(parent)) { const { ent } = await this.fetchWork(parent); parent = str(ent, 'id', '_id'); } }
    const items = []; let total = 0, hasMore = false;
    for (;;) {
      const page = await this.tool(tool, { project_id: p.id, fields: 'full', limit: lim, skip });
      const rows = asList(firstOf(page, 'items', 'result'));
      for (const m of rows) {
        if (!m || typeof m !== 'object') continue;
        if (status && str(m, 'status').toLowerCase() !== status) continue;
        if (parent && str(m, 'needId') !== parent && str(m, 'userStoryId') !== parent) continue;
        items.push({ id: str(m, 'mindstreamId', 'id'), object_id: str(m, 'id', '_id'), kind, title: str(m, 'title', 'name'), status: str(m, 'status'), parent: str(m, 'userStoryId', 'needId'), priority: m.priority ?? null, excerpt: truncate(str(m, 'text', 'description'), 300) });
      }
      if (typeof page.totalCount === 'number') total = page.totalCount;
      hasMore = !!page.hasMore; skip += rows.length;
      if (!all || !hasMore || !rows.length || skip >= 1000) break;
    }
    if (parent || status) { total = (!all && hasMore) ? -1 : items.length; }
    const cursor = hasMore ? String(skip) : null;
    return { data: { project: { id: p.id, name: p.name }, kind, items, warnings: [] }, page: { cursor, limit: lim, total: total < 0 ? null : total, has_more: hasMore } };
  }

  async brainGet() {
    const id = this.pathname.slice('/v1/brain/nodes/'.length);
    if (!id || id.includes('/')) throw fail(422, 'VALIDATION_ERROR', 'invalid node id');
    try { return { data: await this.tool('Brain_get', { knowledge_id: id, max_tokens: 4000 }) }; }
    catch (e) { const m = mapError(e); if (m.status === 404) throw fail(404, 'KNOWLEDGE_NOT_FOUND', `knowledge node ${id} was not found`); throw e; }
  }
}

const repoName = (r) => r ? (r.explicit || r.configured || '') : '';
const gitRemote = (r) => r?.remote ? `https://${r.remote}.git` : '';

function writeJSON(res, status, obj) { const b = JSON.stringify(obj); res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(b) }); res.end(b); }
function writeOK(res, data, page) { writeJSON(res, 200, { ok: true, data, page, evidence: null, error: null }); }
function writeErr(res, e) { writeJSON(res, e.status, { ok: false, data: null, error: { code: e.code, message: e.message, retryable: e.status >= 500 || e.status === 429, details: { via: 'fairmind-devbridge' } } }); }

export function createDevBridge(mcpURL, logger) {
  const bridge = new DevBridge(mcpURL, logger);
  return createServer(bridge.handler());
}
