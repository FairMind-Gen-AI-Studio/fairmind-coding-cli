// HTTPS client for the FairMind Agent API. It only transports requests: all
// orchestration, ranking and authorization happen server-side.
import { request, newUUID, TimeoutError, TooLargeError } from '../http.js';
import { CLIError, Code, Exit, exitForCode, exitForStatus, isRetryableExit, newError } from './output.js';
import { ENV_VAR } from './auth.js';
import { VERSION } from '../version.js';

export const MAX_RESPONSE_BYTES = 8 << 20;
export const CLIENT_ID = `fairmind-cli/${VERSION}`;

const tlsCodes = new Set(['UNABLE_TO_VERIFY_LEAF_SIGNATURE', 'CERT_HAS_EXPIRED', 'DEPTH_ZERO_SELF_SIGNED_CERT',
  'SELF_SIGNED_CERT_IN_CHAIN', 'ERR_TLS_CERT_ALTNAME_INVALID', 'UNABLE_TO_GET_ISSUER_CERT_LOCALLY', 'CERT_UNTRUSTED']);

export class Client {
  constructor({ baseURL, token, project, agent, timeoutMs, logf, env }) {
    Object.assign(this, { baseURL, token, project, agent, timeoutMs, logf, env, headers: {} });
  }

  log(msg) { if (this.logf) this.logf(msg); }

  async do(method, path, query, body) {
    if (this.token.empty()) throw newError(Code.TOKEN_MISSING, `no FairMind token found; run \`fairmind auth login\` or set ${ENV_VAR}`);
    const prefix = this.baseURL.origin + this.baseURL.pathname.replace(/\/+$/, '');
    const u = new URL(prefix + path);
    if (query) for (const [k, vs] of Object.entries(query)) for (const v of [].concat(vs)) u.searchParams.append(k, v);
    const reqID = newUUID();
    const headers = {
      Authorization: `Bearer ${this.token.value()}`, Accept: 'application/json',
      'User-Agent': CLIENT_ID, 'X-FairMind-Client': CLIENT_ID, 'X-Request-Id': reqID,
    };
    if (this.agent) headers['X-FairMind-Agent'] = this.agent;
    if (this.project) headers['X-FairMind-Project'] = this.project;
    let payload;
    if (body !== undefined && body !== null) { payload = JSON.stringify(body); headers['Content-Type'] = 'application/json'; }
    Object.assign(headers, this.headers);

    const shown = new URL(u.href); shown.username = ''; shown.password = '';
    this.log(`-> ${method} ${shown.href} request_id=${reqID}`);
    for (const [k, v] of Object.entries(headers)) {
      this.log(`   ${k}: ${/^(authorization|cookie|x-api-key)$/i.test(k) ? '[REDACTED]' : v}`);
    }
    const start = Date.now();
    let res;
    try {
      res = await request({ url: u, method, headers, body: payload, timeoutMs: this.timeoutMs, maxBytes: MAX_RESPONSE_BYTES, env: this.env });
    } catch (e) {
      throw transportError(e, reqID);
    }
    this.log(`<- ${res.status} in ${Date.now() - start}ms request_id=${reqID}`);
    return decode(res, reqID);
  }
}

function transportError(e, reqID) {
  if (e instanceof TimeoutError) return newError(Code.TIMEOUT, 'request to the FairMind API timed out');
  if (e instanceof TooLargeError) return newError(Code.RESPONSE_TOO_LARGE, e.message, { request_id: reqID });
  if (tlsCodes.has(e.code) || /certificate|x509/i.test(e.message)) {
    return new CLIError({ code: 'TLS_VERIFICATION_FAILED', message: 'TLS certificate verification failed for the FairMind API', retryable: false, details: {} }, Exit.UNAVAILABLE);
  }
  return newError(Code.SERVICE_UNAVAILABLE, 'the FairMind API is unreachable', { cause: e.code || e.message });
}

function statusCode(status) {
  if (status === 401) return 'TOKEN_INVALID';
  if (status === 403) return 'RESOURCE_FORBIDDEN';
  if (status === 404) return 'RESOURCE_NOT_FOUND';
  if (status === 429) return Code.RATE_LIMITED;
  if (status === 408 || status === 504) return Code.TIMEOUT;
  if (status >= 500) return Code.SERVICE_UNAVAILABLE;
  if (status === 400 || status === 422) return Code.VALIDATION;
  return `HTTP_${status}`;
}

function decode(res, reqID) {
  const { status } = res;
  if (status >= 300 && status < 400) {
    throw newError(Code.UNEXPECTED_REDIRECT, 'the FairMind API answered with a redirect; check the configured API URL', { status });
  }
  let env = null;
  try { env = JSON.parse(res.body.toString('utf8')); } catch { /* not JSON */ }
  const valid = env && typeof env === 'object' && (env.ok === true || (env.error && typeof env.error === 'object'));
  if (status >= 200 && status < 300) {
    if (!valid) throw newError(Code.INVALID_RESPONSE, 'the FairMind API returned a response that is not a valid envelope', { status, request_id: reqID });
    if (env.ok) return { ok: true, data: env.data ?? null, page: env.page ?? null, evidence: env.evidence ?? null, error: null };
  }
  let e;
  if (valid && env.error) {
    e = { code: String(env.error.code ?? '').toUpperCase(), message: env.error.message ?? '', retryable: !!env.error.retryable, details: env.error.details ?? {} };
  } else {
    e = { code: statusCode(status), message: `FairMind API returned HTTP ${status}`, retryable: false, details: {} };
  }
  e.details.http_status = status;
  e.details.request_id = reqID;
  let exit = exitForCode(e.code);
  if (exit === undefined) exit = status < 400 ? Exit.GENERIC : exitForStatus(status);
  if (!valid) e.retryable = isRetryableExit(exit);
  if ((status === 429 || status >= 500) && res.headers['retry-after']) e.details.retry_after = res.headers['retry-after'];
  throw new CLIError(e, exit);
}
