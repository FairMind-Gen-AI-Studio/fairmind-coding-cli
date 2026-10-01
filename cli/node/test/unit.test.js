import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Redactor, exitForCode, exitForStatus, Exit } from '../src/cli/output.js';
import { parseArgs, Kind } from '../src/cli/args.js';
import { normalizeRemote, extractSymbol } from '../src/cli/git.js';
import { validateApiUrl, loadSettings } from '../src/cli/config.js';
import { toolParams, convertValue } from '../src/cli/catalog.js';
import { peekClaims, Token } from '../src/cli/auth.js';
import { mkdtempSync, writeFileSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

test('redactor removes secrets and keeps valid JSON', () => {
  const r = new Redactor(); r.addSecret('opaque-api-token-value');
  const out = r.redact('{"a":"Bearer abc.def","jwt":"eyJhbGciOiJub25lIn0.eyJzdWIiOiJ4In0.sig","raw":"opaque-api-token-value","password":"hunter2"}');
  for (const leak of ['abc.def', 'eyJhbGciOiJub25lIn0', 'opaque-api-token-value', 'hunter2']) assert.ok(!out.includes(leak), leak);
  assert.doesNotThrow(() => JSON.parse(out));
});

test('redactor leaves ordinary text unchanged', () => {
  const r = new Redactor();
  const s = '{"requested_tokens": 8000, "title": "Password reset tokens expire"}';
  assert.equal(r.redact(s), s);
});

test('exit code mapping', () => {
  assert.equal(exitForCode('TOKEN_EXPIRED'), Exit.AUTH);
  assert.equal(exitForCode('task_not_found'), Exit.NOT_FOUND);
  assert.equal(exitForCode('WRITE_NOT_CONFIRMED'), Exit.VALIDATION);
  assert.equal(exitForCode('DESTRUCTIVE_NOT_ALLOWED'), Exit.FORBIDDEN);
  assert.equal(exitForStatus(503), Exit.UNAVAILABLE);
  assert.equal(exitForStatus(409), Exit.CONFLICT);
});

test('args: flags, lists, forbidden token, positionals', () => {
  const p = parseArgs(['code', '--intent', 'x y', '--in', 'brain,docs', '--json'], { intent: Kind.STRING, in: Kind.LIST });
  assert.equal(p.str('intent'), 'x y');
  assert.deepEqual(p.list('in'), ['brain', 'docs']);
  assert.deepEqual(p.pos, ['code']);
  assert.ok(p.bools.json);
  assert.throws(() => parseArgs(['--token', 'x'], {}), /credentials are never accepted/);
  assert.throws(() => parseArgs(['--nope'], {}), /unknown flag/);
});

test('git normalizeRemote strips credentials and .git', () => {
  assert.equal(normalizeRemote('https://user:ghp_x@GitHub.com/acme/backend-api.git'), 'github.com/acme/backend-api');
  assert.equal(normalizeRemote('git@github.com:acme/backend-api.git'), 'github.com/acme/backend-api');
  assert.equal(normalizeRemote('/local/path/repo.git'), '');
});

test('git extractSymbol', () => {
  assert.equal(extractSymbol('export function confirmReset(token) {'), 'confirmReset');
  assert.equal(extractSymbol('def reset_password(user):'), 'reset_password');
  assert.equal(extractSymbol('if (x) {'), '');
});

test('validateApiUrl accepts https and loopback http only', () => {
  for (const u of ['https://api.fairmind.example', 'http://127.0.0.1:8788', 'http://localhost:1/base/']) assert.doesNotThrow(() => validateApiUrl(u), u);
  for (const u of ['', 'http://api.fairmind.example', 'https://u:p@api.example.com', 'https://api.example.com?x=1', 'ftp://x']) assert.throws(() => validateApiUrl(u), u);
});

test('config precedence and secret rejection', () => {
  const home = mkdtempSync(join(tmpdir(), 'fmh-')); const repo = mkdtempSync(join(tmpdir(), 'fmr-'));
  mkdirSync(join(home, '.config/fairmind'), { recursive: true });
  writeFileSync(join(home, '.config/fairmind/config.json'), '{"profiles":{"default":{"api_url":"https://api.example.com","project":"from-profile"}}}');
  mkdirSync(join(repo, '.fairmind'), { recursive: true });
  writeFileSync(join(repo, '.fairmind/config.json'), '{"project":"from-file","repository":"backend-api"}');
  const s = loadSettings({}, { HOME: home }, repo);
  assert.equal(s.project, 'from-file');
  assert.equal(s.sources.project, 'project_file');
  assert.equal(s.api_url, 'https://api.example.com');
  const s2 = loadSettings({ project: 'from-flag' }, { HOME: home, FAIRMIND_PROJECT: 'from-env' }, repo);
  assert.equal(s2.project, 'from-flag');
  writeFileSync(join(repo, '.fairmind/config.json'), '{"token":"eyJ..."}');
  assert.throws(() => loadSettings({}, { HOME: home }, repo), /CONFIG_CONTAINS_SECRET|credential-like/);
});

test('catalog params: required first, typed conversion', () => {
  const tool = { input_schema: { type: 'object', properties: { project_id: { type: 'string' }, limit: { type: 'integer', default: 20 }, kinds: { anyOf: [{ type: 'string' }, { type: 'array', items: { type: 'string' } }] } }, required: ['project_id'] } };
  const ps = toolParams(tool);
  assert.equal(ps[0].name, 'project_id');
  assert.equal(ps[0].required, true);
  const kinds = ps.find((p) => p.name === 'kinds');
  assert.equal(kinds.type, 'array');
  assert.equal(convertValue({ flag: 'limit', type: 'integer' }, ['5']), 5);
  assert.throws(() => convertValue({ flag: 'limit', type: 'integer' }, ['five']), /expects integer/);
  assert.deepEqual(convertValue({ flag: 'kinds', type: 'array', item_type: 'string' }, ['a', 'b']), ['a', 'b']);
});

test('token never prints; peekClaims decodes without verifying', () => {
  const b64 = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
  const jwt = `${b64({ alg: 'none' })}.${b64({ scope: 'read write', exp: 4102444800 })}.sig`;
  const t = new Token(jwt, 'env');
  assert.equal(`${t}`, '[REDACTED]');
  assert.equal(JSON.stringify({ t }), '{"t":"[REDACTED]"}');
  const c = peekClaims(t);
  assert.deepEqual(c.scope, ['read', 'write']);
  assert.equal(c.expired, false);
});

import { diffMetadata } from '../src/cli/git.js';
test('git: option-like base refs are rejected (arg injection)', () => {
  const fakeRun = (dir, ...a) => { if (a[0] === 'rev-parse') return 'ok'; return ''; };
  for (const bad of ['--output=/tmp/pwn', '-ext-diff', '--upload-pack=x']) {
    assert.throws(() => diffMetadata(fakeRun, '/repo', { base: bad }), /invalid base ref/);
  }
});
