// GNU-style long-flag parser with interspersed positionals (same rules as
// the Go edition): --flag value, --flag=value, list flags consume values
// until the next --flag and split on commas, `--` ends flags.
import { validation } from './output.js';

export const Kind = { BOOL: 'bool', STRING: 'string', LIST: 'list' };

export const globalFlags = {
  project: Kind.STRING, repo: Kind.STRING, json: Kind.BOOL, profile: Kind.STRING,
  timeout: Kind.STRING, verbose: Kind.BOOL, 'dry-run': Kind.BOOL, help: Kind.BOOL,
};

// Credentials are never accepted as arguments.
const forbidden = new Set(['token', 'jwt', 'api-key', 'password']);

export class Parsed {
  constructor() { this.pos = []; this.vals = {}; this.bools = {}; }
  str(name) { const v = this.vals[name]; return v?.length ? v[v.length - 1] : ''; }
  list(name) { return this.vals[name] ?? []; }
  intFlag(name, def, min, max) {
    const s = this.str(name);
    if (s === '') return def;
    const n = Number(s);
    if (!/^-?\d+$/.test(s) || n < min || n > max) throw validation(`--${name} must be an integer between ${min} and ${max}`);
    return n;
  }
}

export function parseArgs(args, spec) {
  const p = new Parsed();
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === '--') { p.pos.push(...args.slice(i + 1)); break; }
    if (a === '-h') { p.bools.help = true; continue; }
    if (!a.startsWith('--') || a === '-') { p.pos.push(a); continue; }
    const eq = a.indexOf('=');
    const name = eq >= 0 ? a.slice(2, eq) : a.slice(2);
    const hasVal = eq >= 0;
    let val = hasVal ? a.slice(eq + 1) : '';
    if (forbidden.has(name.toLowerCase())) {
      throw validation(`--${name} is not supported: credentials are never accepted as arguments; use \`fairmind auth login\` or FAIRMIND_TOKEN`);
    }
    const kind = spec[name] ?? globalFlags[name];
    if (!kind) throw validation(`unknown flag --${name}`);
    if (kind === Kind.BOOL) {
      if (hasVal && !['true', 'false', '1', '0'].includes(val.toLowerCase())) throw validation(`--${name} expects true or false`);
      p.bools[name] = !hasVal || ['true', '1'].includes(val.toLowerCase());
    } else if (kind === Kind.STRING) {
      if (!hasVal) {
        if (i + 1 >= args.length) throw validation(`--${name} requires a value`);
        val = args[++i];
      }
      (p.vals[name] ??= []).push(val);
    } else {
      const raw = hasVal ? [val] : [];
      while (!hasVal && i + 1 < args.length && !args[i + 1].startsWith('--')) raw.push(args[++i]);
      if (!raw.length) throw validation(`--${name} requires at least one value`);
      for (const r of raw) for (let v of r.split(',')) if ((v = v.trim())) (p.vals[name] ??= []).push(v);
    }
  }
  return p;
}

/** Extracts global flags before a dynamic tool schema is known. */
export function prescanGlobals(argv) {
  const p = new Parsed();
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith('--')) continue;
    const eq = a.indexOf('=');
    const name = eq >= 0 ? a.slice(2, eq) : a.slice(2);
    let val = eq >= 0 ? a.slice(eq + 1) : '';
    const kind = globalFlags[name];
    if (!kind) continue;
    if (kind === Kind.BOOL) { p.bools[name] = eq < 0 || val === 'true'; continue; }
    if (eq < 0 && i + 1 < argv.length) val = argv[++i];
    (p.vals[name] ??= []).push(val);
  }
  return p;
}
