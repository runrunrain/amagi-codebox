// Synthetic data only. Never connects to the user's running CodeBox or providers.
export const fixtureURL = 'http://127.0.0.1:5188/e2e/ai-proxy.html?bridge=http://127.0.0.1:59999';
export function createState() {
  return {
    enabled: true, pending: false, error: '', issues: [],
    providers: [{ id: 'gemini', name: 'Gemini', domains: ['generativelanguage.googleapis.com'], warnings: [] }, { id: 'team', name: '团队中转', domains: ['api.team.example'], warnings: [] }],
    interfaces: [{ name: 'en0', selectable: true }, { name: 'utun0', selectable: false }],
    settings: { routing: { proxy_port: 18890, egress_mode: 'auto', bind_interface: '', dns_server: '', fallback_proxy: '', fetch_proxy: '', rules: [
      { id: 'claude', name: 'Claude', enabled: true, provider: '', domains: ['api.anthropic.com', 'claude.ai'], mode: 'proxy', node: 'sub:us', port: 18892 },
      { id: 'openai', name: 'OpenAI', enabled: true, provider: '', domains: ['api.openai.com', 'chatgpt.com', 'auth.openai.com'], mode: 'proxy', node: 'sub:jp', port: 18893 },
      { id: 'gemini', name: 'Gemini', enabled: true, provider: 'gemini', domains: ['generativelanguage.googleapis.com'], mode: 'proxy', node: 'sub:sg', port: 0 },
      { id: 'glm', name: '智谱 GLM', enabled: true, provider: '', domains: ['open.bigmodel.cn'], mode: 'direct', node: '', port: 0 },
    ] }, corePath: '', v2raynDatabase: '' },
    groups: [{ id: 'sub', name: '我的订阅', source: 'v2rayn', refreshable: true, warnings: [], nodes: [
      { id: 'sub:us', name: '美国 · 洛杉矶', type: 'vless' }, { id: 'sub:jp', name: '日本 · 东京', type: 'trojan' }, { id: 'sub:sg', name: '新加坡', type: 'shadowsocks' }, { id: 'sub:hk', name: '香港', type: 'vless' },
    ] }],
    status: { running: true, pid: 1, core: 'sing-box', revision: 7, logs: 'sing-box started', error: '' },
    source: { available: true, database: '', groups: 1, nodes: 4, skipped: 0, error: '' },
  };
}
export function createRecords() {
  return Array.from({ length: 105 }, (_, i) => {
    const provider = i % 3 === 0 ? 'Claude' : i % 3 === 1 ? 'OpenAI' : 'Gemini';
    const domain = { Claude: 'api.anthropic.com', OpenAI: 'api.openai.com', Gemini: 'generativelanguage.googleapis.com' }[provider];
    const expected = { Claude: '美国 · 洛杉矶 [sub:us]', OpenAI: '日本 · 东京 [sub:jp]', Gemini: '新加坡 [sub:sg]' }[provider];
    return { id: String(i), time: new Date(Date.UTC(2026, 10, 11, 10, 30, i)).toISOString(), revision: 7, provider, domain, destination: domain + ':443', network: 'TCP', inbound: 'auto-in', expected, actual: i === 0 ? '香港 [sub:hk]' : i === 1 ? '' : expected, verdict: i === 0 ? 'mismatch' : i === 1 ? 'unknown' : 'match' };
  });
}
export async function installFixture(page) {
  const control = { state: createState(), records: createRecords(), calls: [], trafficError: false, trafficDelay: 0 };
  await page.route('http://127.0.0.1:59999/rpc', async route => {
    const headers = { 'Access-Control-Allow-Origin': '*', 'Access-Control-Allow-Headers': 'Content-Type', 'Access-Control-Allow-Methods': 'POST' };
    if (route.request().method() === 'OPTIONS') { await route.fulfill({ status: 204, headers }); return; }
    const { method, args } = route.request().postDataJSON();
    control.calls.push({ method, args });
    let result;
    if (method === 'GetAIProxyTraffic') {
      const q = args[0];
      const records = control.records.filter(r => (!q.verdict || r.verdict === q.verdict) && (!q.provider || r.provider === q.provider) && (!q.exit || r.actual === q.exit) && (!q.search || `${r.provider} ${r.domain} ${r.actual}`.toLowerCase().includes(q.search.toLowerCase())) && (!q.since || Date.parse(r.time) >= Date.parse(q.since)) && (!q.until || Date.parse(r.time) <= Date.parse(q.until)));
      const grouped = new Map();
      for (const r of records) { const key = `${r.provider}/${r.domain}/${r.actual}`; const g = grouped.get(key) || { ...r, count: 0 }; g.count++; grouped.set(key, g); }
      const order = { mismatch: 0, unknown: 1, match: 2 };
      result = { records: records.slice(q.offset, q.offset + 100), groups: [...grouped.values()].sort((a, b) => order[a.verdict] - order[b.verdict]), total: records.length, error: control.trafficError ? '测试：记录写入失败' : '' };
      if (control.trafficDelay) { const delay = control.trafficDelay; control.trafficDelay = 0; await new Promise(resolve => setTimeout(resolve, delay)); }
    } else {
      if (method === 'SaveAIProxySettings') { control.state.settings = structuredClone(args[0]); control.state.pending = true; }
      else if (method === 'StartAIProxy') { control.state.enabled = true; control.state.status.running = true; control.state.pending = false; }
      else if (method === 'StopAIProxy') { control.state.enabled = false; control.state.status.running = false; }
      else if (method === 'ImportAIProxySubscription') { control.state.groups.push({ id: 'manual', name: args[0], source: 'manual', refreshable: false, warnings: [], nodes: [{ id: 'manual:test', name: '导入节点', type: 'socks' }] }); }
      else if (method === 'DeleteAIProxySubscription') { control.state.groups = control.state.groups.filter(g => g.id !== args[0]); }
      else if (!['GetAIProxyState', 'SyncAIProxyV2rayN', 'RefreshAIProxySubscription'].includes(method)) throw new Error(`Unexpected fixture RPC: ${method}`);
      result = structuredClone(control.state);
    }
    await route.fulfill({ json: result, headers });
  });
  return control;
}
