// MCP Streamable-HTTP client. Forwards the caller's Authorization header and
// stores no credentials. Mirrors internal/devbridge/mcp.go.
import { createHash } from 'node:crypto';
import { request } from '../http.js';

const PROTOCOL_VERSION = '2025-06-18';
const SESSION_TTL_MS = 10 * 60 * 1000;
const MAX_MCP_RESPONSE = 16 << 20;

export class UpstreamError extends Error { constructor(status, body) { super(`MCP server HTTP ${status}`); this.status = status; this.body = body; } }
export class ToolError extends Error { constructor(tool, message) { super(`${tool}: ${message}`); this.tool = tool; this.toolMessage = message; } }

const authKey = (auth) => createHash('sha256').update(auth).digest('hex');

export class MCPClient {
  constructor(url) { this.url = url; this.sessions = new Map(); this.nextId = 0; }

  id() { return ++this.nextId; }

  async callTool(auth, tool, args) {
    for (let attempt = 0; attempt < 2; attempt++) {
      const s = await this.session(auth, attempt > 0);
      let res;
      try {
        res = await this.rpc(auth, s, 'tools/call', { name: tool, arguments: args });
      } catch (e) {
        if (e instanceof UpstreamError && attempt === 0 &&
            (e.status === 404 || (e.status === 400 && /session/i.test(e.body || '')))) continue;
        throw e;
      }
      return decodeToolResult(tool, res);
    }
    throw new Error('MCP session could not be established');
  }

  async listTools(auth) {
    const out = [];
    let cursor = '';
    for (let page = 0; page < 20; page++) {
      const s = await this.session(auth, false);
      const params = cursor ? { cursor } : {};
      const res = await this.rpc(auth, s, 'tools/list', params);
      for (const t of res.tools ?? []) out.push(t);
      if (!res.nextCursor) break;
      cursor = res.nextCursor;
    }
    return out;
  }

  async session(auth, fresh) {
    const key = authKey(auth);
    const cur = this.sessions.get(key);
    if (cur && !fresh && Date.now() - cur.created < SESSION_TTL_MS) return cur;
    this.sessions.delete(key);
    const s = { id: '', version: PROTOCOL_VERSION, created: Date.now() };
    const { result, sid } = await this.post(auth, s, { jsonrpc: '2.0', id: this.id(), method: 'initialize', params: {
      protocolVersion: PROTOCOL_VERSION, capabilities: {}, clientInfo: { name: 'fairmind-devbridge', version: '0.1.0' },
    } });
    if (result?.protocolVersion) s.version = result.protocolVersion;
    s.id = sid;
    await this.post(auth, s, { jsonrpc: '2.0', method: 'notifications/initialized' });
    this.sessions.set(key, s);
    return s;
  }

  async rpc(auth, s, method, params) {
    const { result } = await this.post(auth, s, { jsonrpc: '2.0', id: this.id(), method, params });
    return result;
  }

  async post(auth, s, msg) {
    const headers = {
      Authorization: auth, 'Content-Type': 'application/json',
      Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': s.version,
      'User-Agent': 'fairmind-devbridge/0.1',
    };
    if (s.id) headers['Mcp-Session-Id'] = s.id;
    const res = await request({ url: this.url, method: 'POST', headers, body: JSON.stringify(msg), timeoutMs: 60000, maxBytes: MAX_MCP_RESPONSE, stream: true });
    const sid = res.headers['mcp-session-id'] ?? '';
    if (res.status >= 300) {
      const body = await readBody(res.res, 4096);
      throw new UpstreamError(res.status, body);
    }
    if (msg.id === undefined) { res.res.resume(); return { result: null, sid }; } // notification
    const ct = res.headers['content-type'] ?? '';
    const candidates = ct.startsWith('text/event-stream') ? await readSSE(res.res, msg.id) : [await readBody(res.res, MAX_MCP_RESPONSE)];
    for (const cand of candidates) {
      let r; try { r = JSON.parse(cand); } catch { continue; }
      if (r.id !== msg.id) continue;
      if (r.error) throw new ToolError(msg.method, r.error.message);
      return { result: r.result, sid };
    }
    throw new Error(`no JSON-RPC response for ${msg.method}`);
  }
}

function decodeToolResult(tool, res) {
  if (!res || typeof res !== 'object') throw new Error(`${tool}: undecodable tool result`);
  const text = (res.content ?? []).filter((c) => c.type === 'text').map((c) => c.text).join('');
  if (res.isError) throw new ToolError(tool, text.trim());
  if (res.structuredContent != null) {
    const sc = res.structuredContent;
    if (sc && typeof sc === 'object' && !Array.isArray(sc) && Object.keys(sc).length === 1 && 'result' in sc) return sc.result;
    return sc;
  }
  try { return JSON.parse(text); } catch { return text; }
}

async function readBody(res, max) {
  const chunks = []; let size = 0;
  for await (const c of res) { size += c.length; if (size > max) { res.destroy(); break; } chunks.push(c); }
  return Buffer.concat(chunks).toString('utf8');
}

/** Collects SSE `data:` payloads until the one carrying wantId arrives. */
async function readSSE(res, wantId) {
  let buf = '';
  for await (const chunk of res) { buf += chunk.toString('utf8'); if (buf.length > MAX_MCP_RESPONSE) break; }
  const out = [];
  for (const block of buf.split(/\n\n/)) {
    const data = block.split('\n').filter((l) => l.startsWith('data:')).map((l) => l.slice(5).replace(/^ /, '')).join('\n');
    if (data) out.push(data);
  }
  return out;
}
