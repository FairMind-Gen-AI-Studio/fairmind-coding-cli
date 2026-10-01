// Local tool catalog cache and JSON-schema parameter parsing.
// Mirrors internal/commands/catalog.go.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { Code, newError } from './output.js';

const CATALOG_TTL_MS = 24 * 60 * 60 * 1000;
export const Access = { READ: 'read', WRITE: 'write', DESTRUCTIVE: 'destructive', SENSITIVE: 'sensitive' };

function cachePath(run) {
  const env = run.app.env;
  let base = env.XDG_CACHE_HOME;
  if (!base) {
    if (env.HOME) base = join(env.HOME, '.cache');
    else if (process.platform === 'win32' && env.LOCALAPPDATA) base = env.LOCALAPPDATA;
    else base = join(homedir(), '.cache');
  }
  return join(base, 'fairmind', `tools-${run.settings.profile}.json`);
}

export async function catalog(run, refresh) {
  const path = cachePath(run);
  if (!refresh) {
    try {
      const cf = JSON.parse(readFileSync(path, 'utf8'));
      if (cf.api_url === run.endpoint() && cf.tools?.length &&
          (run.app.now().getTime() - new Date(cf.fetched_at).getTime()) < CATALOG_TTL_MS) {
        return cf.tools;
      }
    } catch { /* miss */ }
  }
  const env = await run.call('GET', '/v1/tools', refresh ? { refresh: 'true' } : null, null);
  const tools = env.data?.tools;
  if (!Array.isArray(tools) || !tools.length) throw newError(Code.INVALID_RESPONSE, 'the FairMind API returned an empty or invalid tool catalog');
  tools.sort((a, b) => a.name.localeCompare(b.name));
  try {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, JSON.stringify({ fetched_at: run.app.now().toISOString(), api_url: run.endpoint(), tools }), { mode: 0o600 });
  } catch { /* cache is best-effort */ }
  return tools;
}

export function findTool(tools, nameOrCommand) {
  return tools.find((t) => t.name === nameOrCommand || t.command === nameOrCommand)
    ?? tools.find((t) => t.name.toLowerCase() === nameOrCommand.toLowerCase());
}

export function hasNamespace(tools, ns) { return tools.some((t) => t.namespace === ns); }

// ---- JSON-schema -> parameters ----

function resolveNode(n) {
  const cands = [n, ...(n.anyOf ?? []), ...(n.oneOf ?? [])];
  let typ = '', nullable = false, item = '', enumv = [];
  const typesOf = (c) => Array.isArray(c.type) ? c.type : c.type ? [c.type] : [];
  for (const c of cands) {
    for (const t of typesOf(c)) {
      if (t === 'null') { nullable = true; continue; }
      if (!typ) { typ = t; if (t === 'array' && c.items) item = resolveNode(c.items).typ; }
    }
    for (const e of c.enum ?? []) if (typeof e === 'string') enumv.push(e);
  }
  // "string or array" -> array, so comma-separated values keep meaning.
  for (const c of cands) for (const t of typesOf(c)) if (t === 'array' && typ !== 'array') { typ = 'array'; if (c.items) item = resolveNode(c.items).typ; }
  if (!typ) typ = 'any';
  return { typ, nullable, item, enumv };
}

export function toolParams(tool) {
  let schema = {};
  try { schema = typeof tool.input_schema === 'string' ? JSON.parse(tool.input_schema) : tool.input_schema ?? {}; } catch { /* none */ }
  const props = schema.properties ?? {};
  const required = new Set(schema.required ?? []);
  const build = (name) => {
    const { typ, nullable, item, enumv } = resolveNode(props[name] ?? {});
    const p = { name, flag: name.replace(/_/g, '-'), type: typ, required: required.has(name) };
    if (item) p.item_type = item;
    if (nullable) p.nullable = true;
    if (enumv.length) p.enum = enumv;
    const def = props[name]?.default;
    if (def !== undefined) p.default = def;
    const desc = (props[name]?.description ?? '').trim();
    if (desc) p.description = desc;
    return p;
  };
  const out = [];
  for (const r of schema.required ?? []) if (r in props) out.push(build(r));
  for (const name of Object.keys(props).filter((n) => !required.has(n)).sort()) out.push(build(name));
  return out;
}

export function convertValue(p, raw) {
  const one = raw[raw.length - 1];
  if (p.nullable && one === 'null') return null;
  const bad = () => newError(Code.VALIDATION, `--${p.flag} expects ${p.type} (got "${one}")`);
  switch (p.type) {
    case 'integer': { if (!/^-?\d+$/.test(one)) throw bad(); return parseInt(one, 10); }
    case 'number': { const f = Number(one); if (!Number.isFinite(f)) throw bad(); return f; }
    case 'boolean': { if (!/^(true|false|1|0)$/i.test(one)) throw bad(); return /^(true|1)$/i.test(one); }
    case 'array': {
      if (raw.length === 1 && one.trim().startsWith('[')) { try { return JSON.parse(one); } catch { throw bad(); } }
      return raw.map((v) => convertValue({ flag: p.flag, type: p.item_type || 'string' }, [v]));
    }
    case 'object': { try { return JSON.parse(one); } catch { throw newError(Code.VALIDATION, `--${p.flag} expects a JSON object`); } }
    case 'any': { try { return JSON.parse(one); } catch { return one; } }
    default:
      if (p.enum?.length && !p.enum.includes(one)) throw newError(Code.VALIDATION, `--${p.flag} must be one of ${p.enum.join(', ')}`);
      return one;
  }
}
