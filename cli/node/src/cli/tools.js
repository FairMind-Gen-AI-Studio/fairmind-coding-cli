// tools list|describe|call|docs, work list, and dynamic `<namespace> <command>`.
// Mirrors internal/commands/tools.go.
import { readFileSync, writeFileSync } from 'node:fs';
import { register, Run } from './app.js';
import { Kind, parseArgs, prescanGlobals } from './args.js';
import { catalog, findTool, hasNamespace, toolParams, convertValue } from './catalog.js';
import { newError, success, validation, Code, Exit } from './output.js';
import { buildToolArgs, guard, invokeTool } from './toolrun.js';
import { namespaceUsage, summaryLine, toolUsage, toolUsageLine, toolsMarkdown } from './toolhelp.js';
import * as usage from './usage.js';

// ---- tools list / describe / docs / call ----

async function toolsList(run) {
  await run.loadContext(false);
  const tools = await catalog(run, run.args.bools.refresh);
  const ns = run.args.str('namespace').toLowerCase(), access = run.args.str('access').toLowerCase();
  const rows = []; const counts = {};
  for (const t of tools) {
    if ((ns && t.namespace !== ns) || (access && t.access !== access)) continue;
    counts[t.access] = (counts[t.access] ?? 0) + 1;
    rows.push({ name: t.name, command: 'fairmind ' + t.command, access: t.access, summary: summaryLine(t.description, 160) });
  }
  return { env: success({ count: rows.length, counts, tools: rows }), kind: 'tools' };
}

async function toolsDescribe(run) {
  if (!run.args.pos.length) throw validation('expected a tool name (e.g. Studio_get_task) or command (e.g. studio get-task)');
  await run.loadContext(false);
  const t = findTool(await catalog(run, false), run.args.pos.join(' '));
  if (!t) throw newError('TOOL_NOT_FOUND', 'unknown FairMind tool: ' + run.args.pos.join(' '));
  return { env: success({ name: t.name, command: 'fairmind ' + t.command, access: t.access, description: t.description, parameters: toolParams(t), usage: toolUsageLine(t) }), kind: '' };
}

async function toolsDocs(run) {
  await run.loadContext(false);
  const md = toolsMarkdown(await catalog(run, true), run.app.now());
  const out = run.args.str('output');
  if (!out) { run.printer.raw(md); return { env: null, kind: '' }; }
  try { writeFileSync(out, md); } catch (e) { throw validation(`cannot write ${out}: ${e.message}`); }
  return { env: success({ written: out, tools: md.match(/^### /gm)?.length ?? 0 }), kind: '' };
}

async function toolsCall(run) {
  if (run.args.pos.length !== 1) throw validation('expected exactly one tool name, e.g. `fairmind tools call Studio_get_task --arg task_id=TASK-2026-0001`');
  await run.loadContext(false);
  const t = findTool(await catalog(run, false), run.args.pos[0]);
  if (!t) throw newError('TOOL_NOT_FOUND', 'unknown FairMind tool: ' + run.args.pos[0] + '; run `fairmind tools list`');
  guard(run, t);
  const args = readJSONArgs(run);
  const byName = new Map(toolParams(t).map((p) => [p.name, p]));
  for (const kv of run.args.list('arg')) {
    const eq = kv.indexOf('=');
    if (eq <= 0) throw validation(`--arg expects key=value (got "${kv}")`);
    const k = kv.slice(0, eq), v = kv.slice(eq + 1);
    const p = byName.get(k);
    if (!p) throw validation(`${t.name} has no parameter "${k}"; run \`fairmind tools describe ${t.name}\``);
    args[k] = convertValue(p, [v]);
  }
  return invokeTool(run, t, args);
}

function readJSONArgs(run) {
  const args = {};
  let raw = '';
  if (run.args.str('args-json')) raw = run.args.str('args-json');
  else if (run.args.str('args-file') === '-') { try { raw = readFileSync(0, 'utf8'); } catch { throw validation('cannot read arguments from stdin'); } }
  else if (run.args.str('args-file')) { try { raw = readFileSync(run.args.str('args-file'), 'utf8'); } catch { throw validation(`cannot read ${run.args.str('args-file')}`); } }
  if (raw) { try { Object.assign(args, JSON.parse(raw)); } catch (e) { throw validation(`arguments must be a JSON object: ${e.message}`); } }
  return args;
}

// ---- work list ----

async function workList(run) {
  const kind = (run.args.str('kind') || 'task').toLowerCase();
  if (!['task', 'story', 'epic', 'need'].includes(kind)) throw validation('--kind must be task, story or epic');
  const limit = run.args.intFlag('limit', 50, 1, 100);
  await run.loadContext(false);
  const q = { kind, limit: String(limit) };
  if (run.settings.project) q.project = run.settings.project;
  if (run.args.str('status')) q.status = run.args.str('status');
  if (run.args.str('parent')) q.parent = run.args.str('parent');
  if (run.args.str('cursor')) q.cursor = run.args.str('cursor');
  if (run.args.bools.all) q.all = 'true';
  const env = await run.call('GET', '/v1/work', q, null);
  return { env, kind: 'work' };
}

// ---- dynamic `<namespace> <command>` ----

export async function runDynamic(app, printer, redactor, argv) {
  const pre = prescanGlobals(argv);
  if (pre.bools.json) printer.json = true;
  const run = new Run(app, pre, printer, redactor);
  run.verbose = !!pre.bools.verbose;
  try {
    run.timeoutMs = (await import('./app.js')).parseTimeout(pre.str('timeout') || app.env.FAIRMIND_TIMEOUT || '');
    await run.loadContext(false);
    const tools = await catalog(run, false);
    const words = []; for (const a of argv) { if (a.startsWith('-')) break; words.push(a); }
    const ns = words[0];
    if (words.length < 2) {
      if (!hasNamespace(tools, ns)) return app.fail(printer, newError(Code.UNKNOWN_COMMAND, 'unknown command: ' + ns + '; run `fairmind help`'));
      printer.raw(namespaceUsage(tools, ns));
      return Exit.OK;
    }
    const t = findTool(tools, ns + ' ' + words[1]);
    if (!t) {
      const msg = hasNamespace(tools, ns)
        ? `unknown command: ${ns} ${words[1]}; run \`fairmind ${ns} --help\``
        : 'unknown command: ' + words.join(' ') + '; run `fairmind help`';
      return app.fail(printer, newError(Code.UNKNOWN_COMMAND, msg));
    }
    const spec = { yes: Kind.BOOL, 'args-json': Kind.STRING };
    for (const p of toolParams(t)) spec[p.flag] = p.type === 'boolean' ? Kind.BOOL : p.type === 'array' ? Kind.LIST : Kind.STRING;
    const parsed = parseArgs(argv.slice(2), spec);
    if (parsed.bools.help) { printer.raw(toolUsage(t)); return Exit.OK; }
    run.args = parsed;
    guard(run, t);
    const args = buildToolArgs(run, t);
    const { env, kind } = await invokeTool(run, t, args);
    if (env) printer.envelope(env, kind);
    return Exit.OK;
  } catch (e) {
    return app.fail(printer, e);
  }
}

export function registerToolCommands() {
  register({ path: 'tools list', handler: toolsList, usage: usage.usageToolsList,
    spec: { namespace: Kind.STRING, access: Kind.STRING, refresh: Kind.BOOL } });
  register({ path: 'tools describe', handler: toolsDescribe, usage: usage.usageToolsDescribe, spec: {} });
  register({ path: 'tools call', handler: toolsCall, usage: usage.usageToolsCall,
    spec: { 'args-json': Kind.STRING, 'args-file': Kind.STRING, arg: Kind.LIST, yes: Kind.BOOL } });
  register({ path: 'tools docs', handler: toolsDocs, usage: usage.usageToolsDocs, spec: { output: Kind.STRING } });
  register({ path: 'work list', handler: workList, usage: usage.usageWorkList,
    spec: { kind: Kind.STRING, status: Kind.STRING, parent: Kind.STRING, limit: Kind.STRING, cursor: Kind.STRING, all: Kind.BOOL } });
}
