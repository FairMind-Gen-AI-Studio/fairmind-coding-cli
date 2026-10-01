// Non-sensitive configuration. Credentials are never read from or written to
// config files (spec §23). Precedence: flags > env > project file > profile.
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { Code, newError } from './output.js';

export const PROJECT_FILE = join('.fairmind', 'config.json');
const secretKey = /token|secret|password|passwd|jwt|authorization|bearer|api[_-]?key|credential|cookie/i;

export function userConfigPath(env) {
  if (env.FAIRMIND_CONFIG) return env.FAIRMIND_CONFIG;
  if (env.XDG_CONFIG_HOME) return join(env.XDG_CONFIG_HOME, 'fairmind', 'config.json');
  if (env.HOME) return join(env.HOME, '.config', 'fairmind', 'config.json');
  if (process.platform === 'win32' && env.APPDATA) return join(env.APPDATA, 'fairmind', 'config.json');
  return join(homedir(), '.config', 'fairmind', 'config.json');
}

function findSecretKey(v) {
  if (Array.isArray(v)) { for (const c of v) { const k = findSecretKey(c); if (k) return k; } return ''; }
  if (v && typeof v === 'object') {
    for (const [k, c] of Object.entries(v)) {
      if (secretKey.test(k)) return k;
      const f = findSecretKey(c); if (f) return f;
    }
  }
  return '';
}

function readJSON(path) {
  let text;
  try { text = readFileSync(path, 'utf8'); } catch (e) { if (e.code === 'ENOENT' || e.code === 'ENOTDIR') return null; throw e; }
  let obj;
  try { obj = JSON.parse(text); } catch { throw newError(Code.CONFIG_INVALID, `${path} is not valid JSON`); }
  const key = findSecretKey(obj);
  if (key) throw newError(Code.CONFIG_SECRET, `${path} contains credential-like key "${key}"; credentials must not be stored in config files`);
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) throw newError(Code.CONFIG_INVALID, `${path} has an invalid structure`);
  return obj;
}

export function loadSettings(o, env, repoRoot) {
  const s = { profile: '', api_url: '', sources: {} };
  const uf = readJSON(userConfigPath(env)) ?? {};
  s.profile = o.profile || env.FAIRMIND_PROFILE || uf.default_profile || 'default';
  if (o.profile && uf.profiles && !(o.profile in uf.profiles) && o.profile !== 'default') {
    throw newError(Code.CONFIG_INVALID, `profile "${o.profile}" not found in ${userConfigPath(env)}`);
  }
  const prof = uf.profiles?.[s.profile] ?? {};
  let pf = {};
  if (repoRoot) {
    pf = readJSON(join(repoRoot, PROJECT_FILE)) ?? {};
    for (const bad of ['api_url', 'mcp_url']) {
      if (pf[bad]) {
        // A committed file must not be able to redirect the token elsewhere.
        throw newError(Code.CONFIG_INVALID, `${PROJECT_FILE.replace(/\\/g, '/')} must not set ${bad}; configure it per user (profile or FAIRMIND_${bad === 'api_url' ? 'API' : 'MCP'}_URL)`);
      }
    }
  }
  const set = (field, ...cands) => {
    for (const [v, src] of cands) if (v) { s.sources[field] = src; return v; }
    return '';
  };
  s.api_url = set('api_url', [env.FAIRMIND_API_URL, 'env'], [prof.api_url, 'profile']);
  const mcp_url = set('mcp_url', [env.FAIRMIND_MCP_URL, 'env'], [prof.mcp_url, 'profile']);
  const project = set('project', [o.project, 'flag'], [env.FAIRMIND_PROJECT, 'env'], [pf.project, 'project_file'], [prof.project, 'profile']);
  const repository = set('repository', [o.repository, 'flag'], [env.FAIRMIND_REPOSITORY, 'env'], [pf.repository, 'project_file'], [prof.repository, 'profile']);
  const agent = set('agent', [env.FAIRMIND_AGENT, 'env'], [pf.agent, 'project_file'], [prof.agent, 'profile'], ['cli', 'default']);
  // Same field order and omitempty behaviour as the Go edition.
  const out = { profile: s.profile, api_url: s.api_url };
  if (mcp_url) out.mcp_url = mcp_url;
  if (project) out.project = project;
  if (repository) out.repository = repository;
  if (agent) out.agent = agent;
  out.sources = s.sources;
  return out;
}

const isLoopback = (host) => host === 'localhost' || host === '::1' || host === '[::1]' || /^127\./.test(host);

/** https only (http allowed for loopback); no credentials, query or fragment. */
export function validateApiUrl(raw) {
  if (!raw) throw newError(Code.API_URL_MISSING, 'FairMind API URL is not configured; set FAIRMIND_API_URL or api_url in your user profile');
  let u;
  try { u = new URL(raw); } catch { throw newError(Code.API_URL_INSECURE, 'FairMind API URL is not a valid absolute URL'); }
  if (!u.host) throw newError(Code.API_URL_INSECURE, 'FairMind API URL is not a valid absolute URL');
  if (u.username || u.password || u.search || u.hash) throw newError(Code.API_URL_INSECURE, 'FairMind API URL must not contain credentials, query or fragment');
  if (u.protocol === 'http:') {
    if (!isLoopback(u.hostname)) throw newError(Code.API_URL_INSECURE, 'FairMind API URL must use https (http is allowed only for localhost)');
  } else if (u.protocol !== 'https:') {
    throw newError(Code.API_URL_INSECURE, 'FairMind API URL must use https');
  }
  u.pathname = u.pathname.replace(/\/+$/, '');
  return u;
}
