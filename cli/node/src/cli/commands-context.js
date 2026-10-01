// Aggregate context + search commands. Mirrors internal/commands/context.go.
import { register } from './app.js';
import { Kind } from './args.js';
import { Code, newError, success, validation } from './output.js';
import { relToRoot, diffMetadata } from './git.js';
import * as usage from './usage.js';

const MIN_BUDGET = 256, MAX_BUDGET = 200000;
const ID_PATTERN = /^[A-Za-z0-9_.:-]{1,128}$/;

function repoPaths(run, paths) {
  const out = [];
  for (const p of paths) {
    if (!run.repo) { out.push(p.replace(/\\/g, '/')); continue; }
    try { out.push(relToRoot(run.repo.root, run.app.cwd, p)); }
    catch (e) { throw validation(e.message); }
  }
  return out;
}

async function contextGet(run) {
  let intent = run.args.str('intent').trim();
  if (!intent && run.args.pos.length) intent = run.args.pos.join(' ').trim();
  if (!intent) throw validation('--intent is required');
  if (intent.length > 4000) throw validation('--intent must be at most 4000 characters');
  const budget = run.args.intFlag('budget', 0, MIN_BUDGET, MAX_BUDGET);
  await run.loadContext(false);
  const paths = repoPaths(run, run.args.list('path'));
  const body = { intent, project: run.settings.project || undefined, task: run.args.str('task') || undefined };
  if (paths.length) body.paths = paths;
  if (budget) body.budget_tokens = budget;
  const ref = run.repositoryRef(); if (ref) body.repository = ref;
  const env = await run.call('POST', '/v1/context:get', null, body);
  return { env, kind: 'context' };
}

async function contextForTask(run) {
  if (run.args.pos.length !== 1) throw validation('expected exactly one TASK/STORY/NEED id');
  const id = run.args.pos[0].trim();
  if (!ID_PATTERN.test(id)) throw validation(`invalid id "${id}": expected an ObjectId or a FairMind id such as TASK-123`);
  const budget = run.args.intFlag('budget', 0, MIN_BUDGET, MAX_BUDGET);
  await run.loadContext(false);
  // The raw id is sent as-is: ObjectId vs TASK-/US-/NEED- normalization is server-side.
  const body = { id, project: run.settings.project || undefined };
  if (budget) body.budget_tokens = budget;
  const ref = run.repositoryRef(); if (ref) body.repository = ref;
  const env = await run.call('POST', '/v1/context:for-task', null, body);
  return { env, kind: 'context' };
}

function filterChanges(changes, keep) {
  const want = new Set(keep);
  return changes.filter((c) => want.has(c.path) || (c.old_path && want.has(c.old_path)));
}

async function contextForCodeChange(run) {
  const useDiff = !!run.args.bools.diff;
  const files = run.args.list('files');
  if (!useDiff && !files.length) throw validation('specify --diff and/or --files <path>...');
  if (run.args.pos.length) throw validation(`unexpected argument "${run.args.pos[0]}" (use --files to list paths)`);
  if (run.args.str('base') && !useDiff) throw validation('--base requires --diff');
  const budget = run.args.intFlag('budget', 0, MIN_BUDGET, MAX_BUDGET);
  await run.loadContext(useDiff);
  const selected = repoPaths(run, files);

  const req = { mode: 'files', project: run.settings.project || undefined };
  const ref = run.repositoryRef(); if (ref) req.repository = ref;
  if (budget) req.budget_tokens = budget;

  if (useDiff) {
    req.mode = 'diff';
    let changes;
    try { changes = diffMetadata(run.app.git, run.repo.root, { base: run.args.str('base'), symbols: run.args.bools.symbols }); }
    catch (e) { throw newError(Code.GIT, e.message); }
    if (selected.length) changes = filterChanges(changes, selected);
    req.changes = changes;
  } else {
    if (run.args.bools.symbols) run.logf('--symbols only applies together with --diff');
    req.changes = selected.map((p) => ({ path: p, status: 'unspecified' }));
  }

  if (!req.changes.length) {
    // Nothing changed locally: answer without a network round trip.
    const env = success({
      summary: 'No local changes detected; no code-change context to retrieve.',
      items: [],
      warnings: [{ code: 'NO_LOCAL_CHANGES', message: 'the working tree has no changes relative to the base ref' }],
      follow_up: [{ command: 'fairmind context for-code-change --files <path>... --json', reason: 'request context for files you are about to modify' }],
    });
    return { env, kind: 'context' };
  }
  if (req.changes.length > 500) throw validation(`too many changed files (${req.changes.length}); narrow the request with --files`);
  const env = await run.call('POST', '/v1/context:for-code-change', null, req);
  return { env, kind: 'context' };
}

const SEARCH_SOURCES = new Set(['brain', 'docs', 'code', 'studio']);

async function search(run) {
  const q = run.args.pos.join(' ').trim();
  if (!q) throw validation('a search query is required');
  if (q.length > 1000) throw validation('query must be at most 1000 characters');
  for (const s of run.args.list('in')) if (!SEARCH_SOURCES.has(s)) throw validation(`--in accepts brain,docs,code,studio (got "${s}")`);
  const k = run.args.intFlag('k', 10, 1, 100);
  await run.loadContext(false);
  const query = { q, k: String(k) };
  const inList = run.args.list('in'); if (inList.length) query.in = inList.join(',');
  if (run.settings.project) query.project = run.settings.project;
  const ref = run.repositoryRef();
  if (ref?.explicit) query.repository = ref.explicit;
  else if (ref?.remote) query.repository_remote = ref.remote;
  const env = await run.call('GET', '/v1/search', query, null);
  return { env, kind: 'search' };
}

export function registerContextCommands() {
  register({ path: 'context get', handler: contextGet, usage: usage.usageContextGet,
    spec: { intent: Kind.STRING, task: Kind.STRING, path: Kind.LIST, budget: Kind.STRING } });
  register({ path: 'context for-task', handler: contextForTask, usage: usage.usageContextForTask, spec: { budget: Kind.STRING } });
  register({ path: 'context for-code-change', handler: contextForCodeChange, usage: usage.usageContextForCodeChange,
    spec: { diff: Kind.BOOL, files: Kind.LIST, symbols: Kind.BOOL, base: Kind.STRING, budget: Kind.STRING } });
  register({ path: 'search', handler: search, usage: usage.usageSearch, spec: { in: Kind.LIST, k: Kind.STRING } });
}
