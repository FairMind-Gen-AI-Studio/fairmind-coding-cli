// Minimal HTTP(S) transport shared by the CLI and the devbridge: TLS
// verification always on, no redirects followed, explicit timeouts, response
// size cap, and HTTPS_PROXY / NO_PROXY support (CONNECT tunnel) for
// corporate networks — Node's fetch ignores proxy variables.
import http from 'node:http';
import https from 'node:https';
import tls from 'node:tls';

export class TimeoutError extends Error { constructor() { super('request timed out'); this.code = 'ETIMEDOUT_FM'; } }
export class TooLargeError extends Error { constructor(n) { super(`response exceeded ${n} bytes`); } }

const isLoopback = (h) => h === 'localhost' || h === '::1' || h === '[::1]' || /^127\./.test(h);

function proxyFor(url, env) {
  if (url.protocol !== 'https:' || isLoopback(url.hostname)) return null;
  const raw = env.HTTPS_PROXY || env.https_proxy || env.ALL_PROXY || env.all_proxy;
  if (!raw) return null;
  const noProxy = (env.NO_PROXY || env.no_proxy || '').split(',').map((s) => s.trim().toLowerCase()).filter(Boolean);
  const host = url.hostname.toLowerCase();
  if (noProxy.some((n) => n === '*' || host === n.replace(/^\./, '') || host.endsWith(n.startsWith('.') ? n : '.' + n))) return null;
  try { return new URL(raw.includes('://') ? raw : `http://${raw}`); } catch { return null; }
}

// Opens a TLS socket to target through an HTTP CONNECT proxy.
function tunnel(target, proxy, signal) {
  return new Promise((resolve, reject) => {
    const headers = { Host: `${target.hostname}:${target.port || 443}` };
    if (proxy.username) headers['Proxy-Authorization'] = 'Basic ' + Buffer.from(`${decodeURIComponent(proxy.username)}:${decodeURIComponent(proxy.password)}`).toString('base64');
    const req = (proxy.protocol === 'https:' ? https : http).request({
      host: proxy.hostname, port: proxy.port || (proxy.protocol === 'https:' ? 443 : 80),
      method: 'CONNECT', path: headers.Host, headers, signal,
    });
    req.once('connect', (res, socket) => {
      if (res.statusCode !== 200) { socket.destroy(); reject(new Error(`proxy CONNECT failed: HTTP ${res.statusCode}`)); return; }
      const s = tls.connect({ socket, servername: target.hostname });
      s.once('secureConnect', () => resolve(s));
      s.once('error', reject);
    });
    req.once('error', reject);
    req.end();
  });
}

/**
 * Sends one request. Resolves to { status, headers, body: Buffer } or, with
 * stream: true, to { status, headers, res } (caller consumes res).
 */
export async function request({ url, method = 'GET', headers = {}, body, timeoutMs = 30000, maxBytes = 8 << 20, stream = false, env = process.env }) {
  const u = url instanceof URL ? url : new URL(url);
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(new TimeoutError()), timeoutMs);
  const lib = u.protocol === 'https:' ? https : http;
  const proxy = proxyFor(u, env);
  try {
    const opts = {
      method, headers: { ...headers }, signal: ac.signal,
      hostname: u.hostname.replace(/^\[|\]$/g, ''), port: u.port || undefined, path: u.pathname + u.search,
      minVersion: 'TLSv1.2', servername: u.hostname,
    };
    if (body !== undefined) opts.headers['Content-Length'] = Buffer.byteLength(body);
    if (proxy) {
      const sock = await tunnel(u, proxy, ac.signal);
      opts.createConnection = () => sock;
      opts.agent = false;
    }
    const res = await new Promise((resolve, reject) => {
      const req = lib.request(opts, resolve);
      req.once('error', (e) => reject(ac.signal.aborted ? ac.signal.reason : e));
      if (body !== undefined) req.write(body);
      req.end();
    });
    if (stream) {
      res.once('close', () => clearTimeout(timer));
      return { status: res.statusCode, headers: res.headers, res, abort: () => ac.abort() };
    }
    const chunks = [];
    let size = 0;
    for await (const c of res) {
      size += c.length;
      if (size > maxBytes) { res.destroy(); throw new TooLargeError(maxBytes); }
      chunks.push(c);
    }
    return { status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) };
  } catch (e) {
    throw ac.signal.aborted && ac.signal.reason instanceof TimeoutError ? ac.signal.reason : e;
  } finally {
    if (!stream) clearTimeout(timer);
  }
}

export function newUUID() {
  return globalThis.crypto?.randomUUID ? globalThis.crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
