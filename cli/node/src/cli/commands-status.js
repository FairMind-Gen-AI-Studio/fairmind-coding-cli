// version, status, auth status|login|logout. Mirrors internal/commands/status.go.
import { createInterface } from 'node:readline';
import { register } from './app.js';
import { Kind } from './args.js';
import { Code, newError, success, validation } from './output.js';
import { peekClaims, ENV_VAR } from './auth.js';
import { validateApiUrl } from './config.js';
import { validateMcpUrl, DEFAULT_MCP_URL } from './directmcp.js';
import { CLIError } from './output.js';
import { VERSION, API_CONTRACT } from '../version.js';
import * as usage from './usage.js';

async function versionCmd() {
  return { env: success({ version: VERSION, commit: 'nodejs', api_contract: API_CONTRACT }), kind: '' };
}

async function checkServer(run) {
  const start = Date.now();
  const env = await run.call('GET', '/v1/projects', null, null);
  const sc = { checked: true, ok: true, latency_ms: Date.now() - start };
  let list = Array.isArray(env.data) ? env.data : env.data?.items;
  sc.projects_visible = Array.isArray(list) ? list.length : 0;
  return sc;
}

function tokenInfo(run) {
  const ti = { present: !run.token.empty(), source: run.token.source || undefined, claims_verified: false };
  if (ti.present) {
    const c = peekClaims(run.token, run.app.now());
    if (c) ti.claims = c;
    else ti.note = 'token is not JWT-shaped; the server will decide whether it is valid';
  }
  return ti;
}

async function authStatus(run) {
  await run.loadContext(false);
  if (run.token.empty()) throw newError(Code.TOKEN_MISSING, `no FairMind token found; run \`fairmind auth login\` or set ${ENV_VAR}`);
  const data = { profile: run.settings.profile, token: tokenInfo(run) };
  data.server = run.args.bools.offline ? { checked: false, ok: false } : await checkServer(run);
  return { env: success(data), kind: '' };
}

async function statusCmd(run) {
  await run.loadContext(false);
  const data = { version: VERSION, settings: run.settings, token: tokenInfo(run) };
  data.repository = run.repo ? { remote: run.repo.remote ?? '', branch: run.repo.branch ?? '', head_commit: run.repo.head_commit ?? '' } : null;
  // Endpoint: a server-side Agent API (api_url) if configured, else the
  // FairMind MCP server called directly (no devbridge).
  const api = run.settings.api_url
    ? { mode: 'agent-api', url: run.settings.api_url }
    : { mode: 'direct-mcp', url: run.settings.mcp_url || DEFAULT_MCP_URL };
  try {
    if (api.mode === 'agent-api') validateApiUrl(api.url); else validateMcpUrl(api.url);
    api.valid = true;
    if (!run.args.bools.offline && !run.token.empty()) {
      try { api.check = await checkServer(run); }
      catch (e) { if (e instanceof CLIError) api.check = { checked: true, ok: false, error: e.err }; else throw e; }
    }
  } catch (e) {
    if (e instanceof CLIError) { api.valid = false; api.error = e.err; } else throw e;
  }
  data.api = api;
  return { env: success(data), kind: '' };
}

async function readSecret(run) {
  if (run.app.stdinIsTTY) {
    run.app.stderr.write('Paste your FairMind token (input hidden) and press Enter: ');
    return await readHidden(run.app.stdin, run.app.stderr);
  }
  const rl = createInterface({ input: run.app.stdin });
  const line = await new Promise((res) => { rl.once('line', res); rl.once('close', () => res('')); });
  rl.close();
  return String(line).trim();
}

function readHidden(input, output) {
  return new Promise((resolve) => {
    let buf = '';
    const onData = (ch) => {
      const s = ch.toString('utf8');
      if (s === '\n' || s === '\r' || s === '\u0004') { cleanup(); output.write('\n'); resolve(buf.trim()); return; }
      if (s === '\u0003') { cleanup(); process.exit(130); }
      if (s === '\u007f' || s === '\b') { buf = buf.slice(0, -1); return; }
      buf += s;
    };
    const wasRaw = input.isRaw;
    if (input.setRawMode) input.setRawMode(true);
    input.resume();
    input.on('data', onData);
    function cleanup() { input.off('data', onData); if (input.setRawMode) input.setRawMode(!!wasRaw); input.pause(); }
  });
}

async function authLogin(run) {
  await run.loadContext(false);
  const token = await readSecret(run);
  run.redactor.addSecret(token);
  if (!token || /[\s]/.test(token) || token.length > 16384) throw validation('the token read from stdin is empty or malformed');
  try { await run.app.keychain.set(run.settings.profile, token); }
  catch (e) { throw newError(Code.KEYCHAIN, e.message); }
  const data = { stored: true, profile: run.settings.profile, source: 'keychain' };
  if (run.app.env[ENV_VAR]) data.warning = `${ENV_VAR} is set and takes precedence over the stored credential`;
  return { env: success(data), kind: '' };
}

async function authLogout(run) {
  await run.loadContext(false);
  let deleted;
  try { deleted = await run.app.keychain.delete(run.settings.profile); }
  catch (e) { throw newError(Code.KEYCHAIN, e.message); }
  return { env: success({ deleted: deleted === true, profile: run.settings.profile }), kind: '' };
}

export function registerStatusCommands() {
  register({ path: 'version', handler: versionCmd, usage: usage.usageVersion, spec: {} });
  register({ path: 'status', handler: statusCmd, usage: usage.usageStatus, spec: { offline: Kind.BOOL } });
  register({ path: 'auth status', handler: authStatus, usage: usage.usageAuthStatus, spec: { offline: Kind.BOOL } });
  register({ path: 'auth login', handler: authLogin, usage: usage.usageAuthLogin, spec: {} });
  register({ path: 'auth logout', handler: authLogout, usage: usage.usageAuthLogout, spec: {} });
}

