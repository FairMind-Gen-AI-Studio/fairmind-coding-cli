// Token resolution from safe sources only: FAIRMIND_TOKEN or the OS
// credential store. There is deliberately no command-line flag (spec §11.1).
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

export const ENV_VAR = 'FAIRMIND_TOKEN';
export const SERVICE = 'fairmind-cli';
export const NOT_FOUND = Symbol('credential not found');

const SECRET = Symbol('token');

/** Wraps the raw JWT so accidental printing never shows it. */
export class Token {
  constructor(value = '', source = '') {
    this[SECRET] = String(value).trim();
    this.source = source;
  }
  value() { return this[SECRET]; }
  empty() { return this[SECRET] === ''; }
  toString() { return '[REDACTED]'; }
  toJSON() { return '[REDACTED]'; }
  [Symbol.for('nodejs.util.inspect.custom')]() { return 'Token([REDACTED])'; }
}

export async function resolveToken(profile, env, keychain) {
  const v = (env[ENV_VAR] ?? '').trim();
  if (v) return new Token(v, 'env');
  if (!keychain) return new Token();
  const stored = await keychain.get(profile);
  if (stored === NOT_FOUND || !stored) return new Token();
  return new Token(stored, 'keychain');
}

/** Decodes the JWT payload WITHOUT verification, for display only (spec §13). */
export function peekClaims(token, now = new Date()) {
  const parts = token.value().split('.');
  if (parts.length !== 3) return null;
  let raw;
  try { raw = JSON.parse(Buffer.from(parts[1], 'base64url').toString('utf8')); } catch { return null; }
  if (!raw || typeof raw !== 'object') return null;
  const list = (v) => typeof v === 'string' ? v.split(/\s+/).filter(Boolean) : Array.isArray(v) ? v.filter((x) => typeof x === 'string') : undefined;
  const c = {};
  if (typeof raw.iss === 'string') c.iss = raw.iss;
  const aud = list(raw.aud); if (aud?.length) c.aud = aud;
  const scope = list(raw.scope)?.length ? list(raw.scope) : list(raw.scp); if (scope?.length) c.scope = scope;
  if (typeof raw.exp === 'number') {
    const at = new Date(raw.exp * 1000);
    c.expires_at = at.toISOString().replace(/\.\d{3}Z$/, 'Z');
    c.expired = now > at;
  } else {
    c.expired = false;
  }
  return c;
}

// ---- OS credential stores (token always passed on stdin, never in argv) ----

function run(cmd, args, input) {
  const r = spawnSync(cmd, args, { input, encoding: 'utf8', windowsHide: true });
  return { ok: r.status === 0, out: (r.stdout ?? '').trim(), err: r.error };
}

function hasCmd(cmd) {
  const r = spawnSync(process.platform === 'win32' ? 'where' : 'which', [cmd], { stdio: 'ignore' });
  return r.status === 0;
}

// Windows: the token is encrypted with DPAPI (current user) via PowerShell's
// ConvertFrom-SecureString and stored in %LOCALAPPDATA%\fairmind\token-<profile>.dpapi.
function dpapiPath(profile) {
  const base = process.env.LOCALAPPDATA || join(process.env.USERPROFILE || '.', 'AppData', 'Local');
  return join(base, 'fairmind', `token-${profile.replace(/[^A-Za-z0-9_-]/g, '_')}.dpapi`);
}
const PS = ['-NoProfile', '-NonInteractive', '-Command'];

export class SystemKeychain {
  async get(profile) {
    if (process.platform === 'darwin') {
      const r = run('security', ['find-generic-password', '-s', SERVICE, '-a', profile, '-w']);
      return r.ok && r.out ? r.out : NOT_FOUND;
    }
    if (process.platform === 'linux') {
      if (!hasCmd('secret-tool')) return NOT_FOUND;
      const r = run('secret-tool', ['lookup', 'service', SERVICE, 'account', profile]);
      return r.ok && r.out ? r.out : NOT_FOUND;
    }
    if (process.platform === 'win32') {
      const f = dpapiPath(profile);
      if (!existsSync(f)) return NOT_FOUND;
      const script = '$e=[Console]::In.ReadToEnd().Trim(); $s=ConvertTo-SecureString $e; ' +
        '[Runtime.InteropServices.Marshal]::PtrToStringBSTR([Runtime.InteropServices.Marshal]::SecureStringToBSTR($s))';
      const r = run('powershell.exe', [...PS, script], readFileSync(f, 'utf8'));
      if (!r.ok) throw new Error('could not decrypt the stored token (DPAPI)');
      return r.out || NOT_FOUND;
    }
    return NOT_FOUND;
  }

  async set(profile, token) {
    if (/["'\\\s]/.test(token)) throw new Error('token contains unexpected characters');
    if (process.platform === 'darwin') {
      const label = `FairMind CLI (${profile})`;
      const r = run('security', ['-i'], `add-generic-password -U -s ${SERVICE} -a ${JSON.stringify(profile)} -l ${JSON.stringify(label)} -w "${token}"\n`);
      if (!r.ok) throw new Error('credential store write failed');
    } else if (process.platform === 'linux') {
      if (!hasCmd('secret-tool')) throw new Error(`no supported OS credential store on this platform; use ${ENV_VAR}`);
      const r = run('secret-tool', ['store', `--label=FairMind CLI (${profile})`, 'service', SERVICE, 'account', profile], token);
      if (!r.ok) throw new Error('credential store write failed');
    } else if (process.platform === 'win32') {
      const script = '$t=[Console]::In.ReadToEnd().Trim(); ConvertTo-SecureString $t -AsPlainText -Force | ConvertFrom-SecureString';
      const r = run('powershell.exe', [...PS, script], token);
      if (!r.ok || !r.out) throw new Error('credential store write failed (DPAPI)');
      const f = dpapiPath(profile);
      mkdirSync(dirname(f), { recursive: true });
      writeFileSync(f, r.out, { mode: 0o600 });
    } else {
      throw new Error(`no supported OS credential store on this platform; use ${ENV_VAR}`);
    }
    if ((await this.get(profile)) !== token) throw new Error('credential store write could not be verified');
  }

  async delete(profile) {
    if (process.platform === 'darwin') {
      return run('security', ['delete-generic-password', '-s', SERVICE, '-a', profile]).ok ? true : NOT_FOUND;
    }
    if (process.platform === 'linux') {
      return hasCmd('secret-tool') && run('secret-tool', ['clear', 'service', SERVICE, 'account', profile]).ok ? true : NOT_FOUND;
    }
    if (process.platform === 'win32') {
      const f = dpapiPath(profile);
      if (!existsSync(f)) return NOT_FOUND;
      rmSync(f);
      return true;
    }
    return NOT_FOUND;
  }
}

