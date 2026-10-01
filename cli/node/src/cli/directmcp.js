// Direct-MCP client: runs the /v1 orchestration in-process and calls the
// FairMind MCP server directly, with no separate devbridge. This is the
// deployment mode for customers who expose only the MCP server. The heavy
// FairMind logic (ranking, retrieval) stays inside the MCP tools; the CLI only
// performs the light "call tool A, then B, assemble" orchestration.
import { DevBridge } from '../devbridge/server.js';
import { ApiError, mapError as mapUpstream } from '../devbridge/helpers.js';
import { CLIError, Code, Exit, exitForCode, exitForStatus, newError } from './output.js';
import { ENV_VAR } from './auth.js';

export const DEFAULT_MCP_URL = 'https://project-context.fairmind.ai/mcp/mcp';

/** https only (http for loopback), no credentials/query/fragment. */
export function validateMcpUrl(raw) {
  if (!raw) throw newError(Code.API_URL_MISSING, 'FairMind MCP URL is not configured; set FAIRMIND_MCP_URL or mcp_url in your user profile');
  let u;
  try { u = new URL(raw); } catch { throw newError('MCP_URL_INSECURE', 'FairMind MCP URL is not a valid absolute URL'); }
  const loop = u.hostname === 'localhost' || u.hostname === '::1' || /^127\./.test(u.hostname);
  if (u.username || u.password || u.search || u.hash) throw newError('MCP_URL_INSECURE', 'FairMind MCP URL must not contain credentials, query or fragment');
  if (u.protocol === 'http:') { if (!loop) throw newError('MCP_URL_INSECURE', 'FairMind MCP URL must use https (http is allowed only for localhost)'); }
  else if (u.protocol !== 'https:') throw newError('MCP_URL_INSECURE', 'FairMind MCP URL must use https');
  return u;
}

export class DirectClient {
  constructor({ mcpURL, token, agent, project, env, logf }) {
    this.bridge = new DevBridge(mcpURL, { info: logf ? (m) => logf(m) : () => {} });
    this.token = token; this.agent = agent; this.project = project; this.env = env; this.logf = logf;
    this.headers = {};
  }

  async do(method, path, query, body) {
    if (this.token.empty()) throw newError(Code.TOKEN_MISSING, `no FairMind token found; run \`fairmind auth login\` or set ${ENV_VAR}`);
    const searchParams = new URLSearchParams();
    if (query) for (const [k, vs] of Object.entries(query)) for (const v of [].concat(vs)) searchParams.append(k, v);
    const headers = { authorization: `Bearer ${this.token.value()}` };
    if (this.agent) headers['x-fairmind-agent'] = this.agent;
    if (this.project) headers['x-fairmind-project'] = this.project;
    for (const [k, v] of Object.entries(this.headers)) headers[k.toLowerCase()] = v; // write-intent, allow-destructive
    if (this.logf) this.logf(`-> ${method} ${path} (direct MCP)`);
    const ctx = { auth: headers.authorization, method, pathname: path, searchParams, headers, body: body ?? {} };
    try {
      const { data, page } = await this.bridge.dispatch(ctx);
      return { ok: true, data: data ?? null, page: page ?? null, evidence: null, error: null };
    } catch (e) {
      throw toCLIError(e);
    }
  }
}

function toCLIError(e) {
  const ae = e instanceof ApiError ? e : mapUpstream(e);
  const status = ae.status;
  const code = ae.code;
  const err = { code, message: ae.message, retryable: status >= 500 || status === 429, details: { via: 'fairmind-cli-direct', http_status: status } };
  let exit = exitForCode(code);
  if (exit === undefined) exit = exitForStatus(status);
  return new CLIError(err, exit);
}
