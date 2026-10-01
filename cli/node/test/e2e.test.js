// End-to-end: real command tree + real devbridge + fake MCP.
import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { Writable } from 'node:stream';
import { createDevBridge } from '../src/devbridge/server.js';
import { App } from '../src/cli/app.js';
import { registerAll } from '../src/cli/index.js';
import { NOT_FOUND } from '../src/cli/auth.js';
import { execGit } from '../src/cli/git.js';
import { startFakeMCP } from './fakemcp.js';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

registerAll();

let mcp, bridge, bridgeURL;

before(async () => {
  mcp = await startFakeMCP();
  bridge = createDevBridge(mcp.url, { info() {} });
  await new Promise((r) => bridge.listen(0, '127.0.0.1', r));
  bridgeURL = `http://127.0.0.1:${bridge.address().port}`;
});
after(() => { bridge.close(); mcp.srv.close(); });

class MemKeychain {
  constructor() { this.items = {}; }
  async get(p) { return p in this.items ? this.items[p] : NOT_FOUND; }
  async set(p, v) { this.items[p] = v; }
  async delete(p) { if (!(p in this.items)) return NOT_FOUND; delete this.items[p]; return true; }
}

function capture() {
  const w = new Writable({ write(c, e, cb) { w._buf += c; cb(); } });
  w._buf = '';
  Object.defineProperty(w, 'text', { get() { return this._buf; } });
  return w;
}

async function run(args, env = {}) {
  const out = capture(); const err = capture();
  const app = new App({
    stdin: process.stdin, stdout: out, stderr: err,
    env: { HOME: mkdtempSync(join(tmpdir(), 'fmh-')), FAIRMIND_API_URL: bridgeURL, FAIRMIND_TOKEN: 'good-token', ...env },
    cwd: process.cwd(), keychain: new MemKeychain(), git: execGit, now: () => new Date(),
    stdoutIsTTY: false, stdinIsTTY: false,
  });
  const exit = await app.run([...args, '--json']);
  const token = env.FAIRMIND_TOKEN ?? 'good-token';
  assert.ok(!(out.text + err.text).includes('eyJ'), 'no JWT should leak');
  let json = null; try { json = JSON.parse(out.text); } catch { /* raw */ }
  return { exit, out: out.text, err: err.text, json };
}

test('context for-task traverses story -> need -> tests + brain', async () => {
  const r = await run(['context', 'for-task', 'US-1']);
  assert.equal(r.exit, 0);
  const ids = r.json.data.items.map((i) => i.id);
  for (const want of ['US-1', 'NEED-1', 'TEST-1', 'n1']) assert.ok(ids.includes(want), want + ' in ' + ids);
});

test('unknown task -> 404 TASK_NOT_FOUND', async () => {
  const r = await run(['context', 'for-task', 'TASK-999']);
  assert.equal(r.exit, 3);
  assert.equal(r.json.error.code, 'TASK_NOT_FOUND');
});

test('search finds studio items missing from brain, flags BRAIN_INDEX_SPARSE', async () => {
  const r = await run(['search', 'migrate supabase', '--project', 'Community Pulse']);
  assert.equal(r.exit, 0);
  assert.ok(r.json.data.items.some((i) => i.id === 'US-2'));
  assert.ok(r.json.data.warnings.some((w) => w.code === 'BRAIN_INDEX_SPARSE'));
});

test('tools list classifies access', async () => {
  const r = await run(['tools', 'list']);
  assert.equal(r.exit, 0);
  const by = Object.fromEntries(r.json.data.tools.map((t) => [t.name, t.access]));
  assert.equal(by['Studio_get_task'], 'read');
  assert.equal(by['Brain_record_issue'], 'write');
  assert.equal(by['Insights_erase_subject'], 'destructive');
  assert.equal(by['General_get_mcp_configs_for_agent'], undefined); // not in fake catalog
});

test('write tool needs --yes; --dry-run sends nothing', async () => {
  const w = { FAIRMIND_TOKEN: 'good-token' };
  const noYes = await run(['brain', 'record-issue', '--title', 'x', '--kind', 'bug', '--project', 'Community Pulse'], w);
  assert.equal(noYes.exit, 5);
  assert.equal(noYes.json.error.code, 'WRITE_NOT_CONFIRMED');
  const dry = await run(['brain', 'record-issue', '--title', 'x', '--kind', 'bug', '--project', 'Community Pulse', '--dry-run'], w);
  assert.equal(dry.exit, 0);
  assert.equal(dry.json.data.dry_run, true);
  const yes = await run(['brain', 'record-issue', '--title', 'x', '--kind', 'bug', '--project', 'Community Pulse', '--yes'], w);
  assert.equal(yes.exit, 0);
  assert.equal(yes.json.data.recorded, true);
});

test('destructive tool needs FAIRMIND_ALLOW_DESTRUCTIVE=1', async () => {
  const blocked = await run(['insights', 'erase-subject', '--subject', 'u1', '--yes']);
  assert.equal(blocked.exit, 4);
  assert.equal(blocked.json.error.code, 'DESTRUCTIVE_NOT_ALLOWED');
  const allowed = await run(['insights', 'erase-subject', '--subject', 'u1', '--yes'], { FAIRMIND_ALLOW_DESTRUCTIVE: '1' });
  assert.equal(allowed.exit, 0);
});

test('work list --parent filters children (project name accepted)', async () => {
  const r = await run(['work', 'list', '--kind', 'story', '--parent', 'NEED-1', '--project', 'Community Pulse']);
  assert.equal(r.exit, 0);
  assert.equal(r.json.data.items.length, 2);
});

test('dynamic command: positional + project injection, typed flags', async () => {
  const r = await run(['studio', 'list-tasks-by-project', '--project', 'Community Pulse']);
  assert.equal(r.exit, 0);
  assert.equal(r.json.data.items[0].received, 'p1'); // project name normalized to id by the bridge
  const bad = await run(['studio', 'list-tasks-by-project', '--project', 'Community Pulse', '--limit', 'notnum']);
  assert.equal(bad.exit, 5);
});

test('missing token fails locally without a request', async () => {
  const r = await run(['tools', 'list'], { FAIRMIND_TOKEN: '' });
  assert.equal(r.exit, 2);
  assert.equal(r.json.error.code, 'TOKEN_MISSING');
});

test('unknown command', async () => {
  const r = await run(['frobnicate']);
  assert.equal(r.exit, 5);
  assert.equal(r.json.error.code, 'UNKNOWN_COMMAND');
});

// --- Direct-MCP mode: CLI -> MCP, no devbridge (customer deployment) ---
async function runDirect(args, extra = {}) {
  const out = capture(); const err = capture();
  const app = new App({
    stdin: process.stdin, stdout: out, stderr: err,
    env: { HOME: mkdtempSync(join(tmpdir(), 'fmd-')), FAIRMIND_MCP_URL: mcp.url, FAIRMIND_TOKEN: 'good-token', ...extra },
    cwd: process.cwd(), keychain: new MemKeychain(), git: execGit, now: () => new Date(),
    stdoutIsTTY: false, stdinIsTTY: false,
  });
  const exit = await app.run([...args, '--json']);
  assert.ok(!(out.text + err.text).includes('eyJ'), 'no JWT should leak');
  let json = null; try { json = JSON.parse(out.text); } catch { /* raw */ }
  return { exit, json };
}

test('direct: general list-projects reaches the MCP with no bridge', async () => {
  const r = await runDirect(['general', 'list-projects']);
  assert.equal(r.exit, 0);
  assert.ok(r.json.data.some((p) => p.name === 'Community Pulse'));
});

test('direct: context for-task traverses via MCP', async () => {
  const r = await runDirect(['context', 'for-task', 'US-1']);
  assert.equal(r.exit, 0);
  const ids = r.json.data.items.map((i) => i.id);
  assert.ok(ids.includes('NEED-1') && ids.includes('TEST-1'));
});

test('direct: status reports mode direct-mcp', async () => {
  const r = await runDirect(['status']);
  assert.equal(r.exit, 0);
  assert.equal(r.json.data.api.mode, 'direct-mcp');
  assert.equal(r.json.data.api.url, mcp.url);
});

test('direct: missing token fails without a request', async () => {
  const r = await runDirect(['general', 'list-projects'], { FAIRMIND_TOKEN: '' });
  assert.equal(r.exit, 2);
  assert.equal(r.json.error.code, 'TOKEN_MISSING');
});

test('direct: write guard still applies', async () => {
  const noYes = await runDirect(['brain', 'record-issue', '--title', 'x', '--kind', 'bug', '--project', 'Community Pulse']);
  assert.equal(noYes.exit, 5);
  assert.equal(noYes.json.error.code, 'WRITE_NOT_CONFIRMED');
  const yes = await runDirect(['brain', 'record-issue', '--title', 'x', '--kind', 'bug', '--project', 'Community Pulse', '--yes']);
  assert.equal(yes.exit, 0);
});
