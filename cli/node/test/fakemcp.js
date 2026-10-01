// A fake MCP Streamable-HTTP server for e2e tests (mirrors the Go devbridge test harness).
import { createServer } from 'node:http';

const TOOLS = [
  { name: 'General_list_projects', inputSchema: { type: 'object', properties: {} } },
  { name: 'Studio_get_task', inputSchema: { type: 'object', properties: { task_id: { type: 'string' } }, required: ['task_id'] } },
  { name: 'Studio_get_user_story', inputSchema: { type: 'object', properties: { user_story_id: { type: 'string' } }, required: ['user_story_id'] } },
  { name: 'Studio_get_need', inputSchema: { type: 'object', properties: { need_id: { type: 'string' } }, required: ['need_id'] } },
  { name: 'Studio_list_tests_by_userstory', inputSchema: { type: 'object', properties: { user_story_id: { type: 'string' } }, required: ['user_story_id'] } },
  { name: 'Studio_list_tasks_by_project', inputSchema: { type: 'object', properties: { project_id: { type: 'string' } }, required: ['project_id'] } },
  { name: 'Studio_list_user_stories_by_project', inputSchema: { type: 'object', properties: { project_id: { type: 'string' } }, required: ['project_id'] } },
  { name: 'Studio_list_needs_by_project', inputSchema: { type: 'object', properties: { project_id: { type: 'string' } }, required: ['project_id'] } },
  { name: 'Brain_search', inputSchema: { type: 'object', properties: { query: { type: 'string' } } } },
  { name: 'Brain_record_issue', inputSchema: { type: 'object', properties: { title: { type: 'string' }, kind: { type: 'string', enum: ['bug', 'feature'] } }, required: ['title'] } },
  { name: 'General_rag_retrieve_documents', inputSchema: { type: 'object', properties: { query: { type: 'string' } } } },
  { name: 'Insights_erase_subject', inputSchema: { type: 'object', properties: { subject: { type: 'string' } } } },
];

const err = (msg) => ({ isError: true, content: [{ type: 'text', text: msg }] });
const ok = (v) => ({ isError: false, content: [{ type: 'text', text: JSON.stringify(v) }] });

function callTool(name, a) {
  switch (name) {
    case 'General_list_projects': return ok([{ id: 'p1', name: 'Community Pulse' }]);
    case 'Studio_get_user_story':
      if (a.user_story_id === 'US-1' || a.user_story_id === 'oid-s') return ok({ id: 'oid-s', mindstreamId: 'US-1', projectId: 'p1', needId: 'oid-n', title: 'Login page', status: 'ready' });
      return err('user story not found');
    case 'Studio_get_task': return err('task not found');
    case 'Studio_get_need': return ok({ id: 'oid-n', mindstreamId: 'NEED-1', projectId: 'p1', title: 'Auth epic' });
    case 'Studio_list_tests_by_userstory': return ok({ items: [{ id: 't1', mindstreamId: 'TEST-1', title: 'rejects bad pw' }] });
    case 'Studio_list_user_stories_by_project':
      return ok({ items: [{ id: 'oid-s', mindstreamId: 'US-1', title: 'Login page', needId: 'oid-n', status: 'ready' },
                          { id: 'oid-2', mindstreamId: 'US-2', title: 'Migrate accounts from Supabase', needId: 'oid-n', status: 'todo' }], hasMore: false, totalCount: 2 });
    case 'Studio_list_tasks_by_project': return ok({ items: [{ id: 'ot', mindstreamId: 'TASK-1', title: 'Do it', status: 'todo', received: a.project_id }], hasMore: false, totalCount: 1 });
    case 'Studio_list_needs_by_project': return ok({ items: [], hasMore: false, totalCount: 0 });
    case 'Brain_search':
      if (a.project && a.project !== 'p1') return ok({ results: [] }); // names silently match nothing upstream
      return ok({ results: [{ node_id: 'n1', kind: 'decision', title: 'Use HttpOnly cookies', status: 'taken', review_state: 'confirmed', score: 0.9 }] });
    case 'General_rag_retrieve_documents': return ok([]);
    case 'Brain_record_issue': return ok({ recorded: true, title: a.title });
    case 'Brain_get': return a.knowledge_id === 'n1' ? ok({ node: { id: 'n1' }, content: 'full' }) : err('not found');
    case 'Insights_erase_subject': return ok({ erased: true });
    default: return err('Unknown tool ' + name);
  }
}

export function startFakeMCP() {
  const srv = createServer((req, res) => {
    if (req.headers['authorization'] !== 'Bearer good-token') { res.writeHead(401).end('unauthorized'); return; }
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
      const msg = JSON.parse(body || '{}');
      if (msg.method === 'notifications/initialized') { res.writeHead(202).end(); return; }
      let result;
      if (msg.method === 'initialize') result = { protocolVersion: '2025-06-18', capabilities: {} };
      else if (msg.method === 'tools/list') result = { tools: TOOLS };
      else if (msg.method === 'tools/call') result = callTool(msg.params.name, msg.params.arguments ?? {});
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Mcp-Session-Id': 'sess-1' });
      res.end(`event: message\ndata: ${JSON.stringify({ jsonrpc: '2.0', id: msg.id, result })}\n\n`);
    });
  });
  return new Promise((resolve) => srv.listen(0, '127.0.0.1', () => resolve({ srv, url: `http://127.0.0.1:${srv.address().port}` })));
}
