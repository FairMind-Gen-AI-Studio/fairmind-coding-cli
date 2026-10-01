// Write guards, argument building and invocation for dynamic tool commands.
import { readFileSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { Access, convertValue, toolParams } from './catalog.js';
import { newError, success, validation, Code } from './output.js';
import { newUUID } from '../http.js';

export const ALLOW_DESTRUCTIVE_ENV = 'FAIRMIND_ALLOW_DESTRUCTIVE';

/** Enforces the write / destructive opt-ins. --dry-run is always allowed. */
export function guard(run, t) {
  if (run.args.bools['dry-run']) return;
  if (t.access === Access.DESTRUCTIVE || t.access === Access.SENSITIVE) {
    if (run.app.env[ALLOW_DESTRUCTIVE_ENV] !== '1') {
      throw newError('DESTRUCTIVE_NOT_ALLOWED', `${t.name} is ${t.access}; it runs only when a human sets ${ALLOW_DESTRUCTIVE_ENV}=1 for this command`);
    }
  }
  if (t.access !== Access.READ && !run.args.bools.yes) {
    throw newError('WRITE_NOT_CONFIRMED', `${t.name} modifies FairMind (${t.access}). Preview with --dry-run, then re-run with --yes only if the user asked for this change`);
  }
}

// @file reads a file, @- reads stdin, @@x is a literal "@x".
function expandAt(run, v) {
  if (v.startsWith('@@')) return v.slice(1);
  if (v === '@-') { try { return readFileSync(0, 'utf8'); } catch { throw validation('cannot read stdin'); } }
  if (v.startsWith('@') && v.length > 1) {
    const path = isAbsolute(v.slice(1)) ? v.slice(1) : join(run.app.cwd, v.slice(1));
    let b; try { b = readFileSync(path); } catch { throw validation(`cannot read ${v.slice(1)}`); }
    if (b.length > (4 << 20)) throw validation(`${v.slice(1)} is larger than 4 MiB`);
    return b.toString('utf8');
  }
  return v;
}

/** Builds typed tool arguments and fills project/git_remote/agent from context. */
export function buildToolArgs(run, t) {
  const args = {};
  const js = run.args.str('args-json');
  if (js) { try { Object.assign(args, JSON.parse(js)); } catch (e) { throw validation(`--args-json must be a JSON object: ${e.message}`); } }
  const params = toolParams(t);
  for (const p of params) {
    if (p.type === 'boolean') { if (p.flag in run.args.bools) args[p.name] = run.args.bools[p.flag]; continue; }
    let raw = run.args.vals[p.flag];
    if (!raw?.length) continue;
    if (p.type === 'string' || p.type === 'any' || p.type === 'object') raw = [expandAt(run, raw[raw.length - 1])];
    args[p.name] = convertValue(p, raw);
  }
  let pos = [...run.args.pos];
  for (const p of params) {
    if (!pos.length) break;
    if (p.name in args || !p.required) continue;
    args[p.name] = convertValue(p, [pos[0]]); pos = pos.slice(1);
  }
  if (pos.length) throw validation(`unexpected argument "${pos[0]}"; run \`fairmind ${t.command} --help\``);
  for (const p of params) {
    if (p.name in args) continue;
    if (['project', 'project_id', 'projectId'].includes(p.name) && run.settings.project) args[p.name] = run.settings.project;
    else if (p.name === 'git_remote' && run.repo?.remote) args[p.name] = `https://${run.repo.remote}.git`;
    else if (p.name === 'agent' && run.settings.agent) args[p.name] = run.settings.agent;
  }
  const missing = params.filter((p) => p.required && !(p.name in args)).map((p) => '--' + p.flag);
  if (missing.length) throw validation(`missing required parameter(s) ${missing.join(', ')}; run \`fairmind ${t.command} --help\``);
  return args;
}

/** Applies the guards, then POST /v1/tools/{name}:call. */
export async function invokeTool(run, t, args) {
  guard(run, t);
  if (run.args.bools['dry-run']) {
    return { env: success({ dry_run: true, tool: t.name, access: t.access, arguments: args }), kind: '' };
  }
  const headers = {};
  if (t.access !== Access.READ) { headers['X-FairMind-Write-Intent'] = 'confirmed'; headers['Idempotency-Key'] = newUUID(); }
  if (t.access === Access.DESTRUCTIVE || t.access === Access.SENSITIVE) headers['X-FairMind-Allow-Destructive'] = '1';
  const env = await run.call('POST', '/v1/tools/' + encodeURIComponent(t.name) + ':call', null, { arguments: args }, headers);
  return { env, kind: '' };
}

export { Code };
