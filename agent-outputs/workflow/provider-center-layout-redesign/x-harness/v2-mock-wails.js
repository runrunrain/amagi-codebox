/*
 * X 系列视觉验证用 Wails 绑定 mock（init script，在页面脚本执行前注入）。
 *
 * 为什么需要：frontend 在纯 vite 下没有 Wails 注入的 window.go / window.runtime，
 * 所有数据接口都会失败。这里按 e2e/pi-amagi-config.html 的思路提供隔离夹具：
 * 数据只存在于浏览器内存，保存类调用只写 window.__saved，从不触碰真实 ~/.pi 等目录。
 *
 * 场景由 URL 查询串 ?s=flag1,flag2 选择（init script 每次导航都会重跑）：
 *   full           有数据（默认）
 *   empty          各视图空态
 *   error          各配置读取调用 reject → 编辑器 ErrorState
 *   loading        各配置读取调用永不 resolve → LoadingState
 *   catalog-error  模型目录 reject → catalog warning 横幅
 *   bad-json       Pi/注册表/认证 JSON、OMP YAML 内容非法 → 解析错误横幅 + 源码模式
 *   long           超长路径 / 超长预设名与模型 / 超长描述（溢出场景）
 *   skin           开启皮肤（真实 App.vue 路径：skin store → html[data-skin=on]）
 */
(() => {
  const flags = new Set((new URLSearchParams(location.search).get('s') || 'full').split(',').filter(Boolean));
  const has = (f) => flags.has(f);
  window.__saved = {};
  window.__calls = [];
  const count = new URLSearchParams(location.search).get('N');

  // ---------- 数据 ----------
  const longName = '极长名称的预设-用于测试溢出行为-this-is-an-extremely-long-preset-name-to-test-ellipsis-truncation-behaviour';
  const longModel = 'anthropic/claude-opus-4.1-20250805-extended-thinking-with-a-very-long-model-identifier-that-will-overflow-the-row';
  const longProv = 'openrouter-with-a-really-long-provider-name';

  const anthropicPresets = [
    { key: 'glm/max', label: 'GLM-5.2 Max', provider: 'zhipu', model: 'glm-5.2', model_haiku: 'glm-5.2-air', model_sonnet: 'glm-5.2', model_opus: 'glm-5.2-max',
      parameters: { thinking: { type: 'enabled' }, reasoning_effort: 'high', temperature: 0.7 }, source: 'terminal_preset', vision: true, video: false, harness_sync: true },
    { key: 'kimi/k2', label: 'Kimi K2', provider: 'moonshot', model: 'kimi-k2-0905-preview', parameters: { temperature: 0.6 }, source: 'terminal_preset' },
    { key: 'deepseek/v4', label: 'DeepSeek V4 Pro', provider: 'deepseek', model: 'deepseek-v4-pro', model_sonnet: 'deepseek-v4',
      parameters: { reasoning_effort: 'max' }, source: 'terminal_preset', vision: true, video: true, harness_sync: true },
    { key: 'qwen/coder', label: 'Qwen3 Coder', provider: 'zhipu', model: 'qwen3-coder-plus', parameters: {}, source: 'terminal_preset' },
  ];
  if (has('long')) {
    anthropicPresets.push({ key: 'long/1', label: longName, provider: longProv, model: longModel, model_haiku: longModel, model_sonnet: longModel,
      parameters: { thinking: { type: 'enabled' }, reasoning_effort: 'high', temperature: 0.7 }, source: 'terminal_preset', vision: true, video: true, harness_sync: true });
  }
  const openaiPresets = [
    { key: 'gpt/5', label: 'GPT-5 Codex', provider: 'openai', model: 'gpt-5-codex', parameters: { reasoning_effort: 'medium' }, source: 'terminal_preset', vision: true },
    { key: 'glm/oa', label: 'GLM-5.2 (OpenAI 兼容)', provider: 'zhipu', model: 'glm-5.2', parameters: { temperature: 0.7 }, source: 'terminal_preset' },
  ];

  const ocConfig = {
    $schema: 'https://opencode.ai/config.json',
    model: 'anthropic/claude-sonnet-4',
    provider: { anthropic: { options: { apiKey: '{env:ANTHROPIC_API_KEY}' } }, zhipu: { npm: '@ai-sdk/openai-compatible', options: { baseURL: 'https://open.bigmodel.cn/api/paas/v4' }, models: { 'glm-5.2': { name: 'GLM 5.2' } } } },
    agent: { luban: { model: 'zhipu/glm-5.2', description: '实现' }, diting: { model: 'anthropic/claude-sonnet-4' } },
    mcp: { 'web-search-prime': { type: 'remote', url: 'https://example.com/mcp', enabled: true } },
    permission: { bash: 'ask', edit: 'allow' },
    instructions: ['resources/core/common/persona.md'],
    plugin: ['github:owner/repo'],
    experimental: { batch_tool: true },
    preset: {
      work: { name: '工作', description: '日常开发使用的预设', provider: 'anthropic', model: 'claude-sonnet-4', bindings: { anthropic: { local_provider: 'anthropic' } } },
      cheap: { name: '省钱模式', description: has('long') ? '这是一段非常非常长的描述文字，用来测试单行省略号截断是否正常工作而不会撑破布局或者造成横向滚动条出现' : '低成本模型', provider: 'zhipu', model: 'glm-5.2' },
      bare: { name: 'bare', model: 'x' },
    },
  };

  const catalog = { providers: [
    { name: 'amagi-glm', api: 'openai-completions', hasAuth: true, models: [{ id: 'glm-5.3', name: 'GLM 5.3', reasoning: true, thinkingLevels: ['low', 'high', 'max'] }] },
    { name: 'plain', hasAuth: true, models: [{ id: 'summary', reasoning: false }] },
    { name: 'ollama', hasAuth: true, models: [{ id: 'qwen:32b', reasoning: true, thinkingLevels: ['low', 'high'] }] },
    { name: 'openrouter', hasAuth: false, models: [{ id: 'auto' }] },
  ] };

  const piConfig = {
    profile: 'tiered',
    agents: { luban: { model: 'amagi-glm/glm-5.3:max' }, baize: { model: 'ollama/qwen:32b:high' }, diting: { model: 'plain/summary' } },
    compaction: { model: 'ollama/qwen:32b:high' },
    mcp: { leader: [], default: ['search'], agents: { luban: ['lsp'] } },
    concurrency: { default: 4, providers: { 'amagi-glm': 3 }, models: { 'ollama/qwen:32b': 2 } },
  };

  const piModels = { providers: {
    'amagi-glm': { api: 'openai-completions', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', apiKey: 'sk-demo-1234', models: [
      { id: 'glm-5.3', name: 'GLM 5.3', reasoning: true, contextWindow: 200000, maxTokens: 131072,
        thinkingLevelMap: { high: 'high', max: 'max' }, thinking: { mode: 'effort', levels: ['low', 'high', 'max'] }, input: ['text', 'image'],
        cost: { input: 0.6, output: 2.2, cacheRead: 0.1, cacheWrite: 0 }, customFlag: true },
      { id: 'glm-5.2-air', name: '', reasoning: false },
    ] },
    ollama: { api: 'openai-completions', baseUrl: 'http://localhost:11434/v1', auth: 'none', models: [{ id: 'qwen:32b', reasoning: true, contextWindow: 32768 }] },
    'custom-relay': { api: 'weird-proto', baseUrl: 'https://relay.example.com', models: [] },
  }, $schema: 'https://example.com/models.schema.json' };

  const piAuth = {
    'openai-codex': { type: 'oauth', access: 'a', refresh: 'r', expires: 1893456000000, accountId: 'dev@example.com' },
    'amagi-glm': { type: 'api_key', key: 'sk-demo-1234' },
    'other-thing': { type: 'weird', foo: 1 },
  };

  const ompConfigYaml = [
    'modelRoles:', '  coding_worker: amagi-glm/glm-5.3:max', '  review_worker: ollama/qwen:32b:high',
    'task:', '  agentModelOverrides:', '    scout: "@coding_worker"', '    librarian: ollama/qwen:32b', '',
  ].join('\n');
  const ompModelsYaml = [
    'providers:', '  amagi-glm:', '    api: openai-completions', '    baseUrl: https://open.bigmodel.cn/api/paas/v4', '    apiKey: sk-demo-1234',
    '    models:', '      - id: glm-5.3', '        name: GLM 5.3', '        reasoning: true', '        contextWindow: 200000', '        maxTokens: 131072', '',
  ].join('\n');

  // v2: N=0/1/20 are isolated display fixtures, never real credentials.
  if (count !== null) {
    piModels.providers = Object.fromEntries(Array.from({ length: Number(count) }, (_, i) => [i === 0 ? 'amagi-' + 'long-fixture-provider-'.repeat(4) : `fixture-${i}`, { api: 'openai-completions', models: [{ id: 'fixture-model' }], custom: { nested: { value: 'fixture' } } }]));
    delete piModels.$schema;
    for (const key of Object.keys(piAuth)) delete piAuth[key];
    for (let i = 0; i < Number(count); i++) piAuth[i === 0 ? 'long-fixture-entry-'.repeat(4) : `fixture-${i}`] = i % 2 ? { type: 'api_key', key: 'isolated-placeholder' } : { type: 'oauth', access: 'isolated-placeholder', refresh: 'isolated-placeholder' };
  }
  if (has('no-model')) delete ocConfig.model;
  if (has('no-unknown')) delete ocConfig.preset;
  if (has('empty-compaction')) delete piConfig.compaction;
  const PATHS = has('long') ? {
    pi: 'C:\\Users\\someone-with-a-really-long-username\\AppData\\Roaming\\amagi-pi-agent-home\\agent\\amagi.json',
    models: 'C:\\Users\\someone-with-a-really-long-username\\AppData\\Roaming\\amagi-pi-agent-home\\agent\\models.json',
    auth: 'C:\\Users\\someone-with-a-really-long-username\\AppData\\Roaming\\amagi-pi-agent-home\\agent\\auth.json',
    omp: 'C:\\Users\\someone-with-a-really-long-username\\.omp\\agent-with-an-unusually-long-directory-name\\config.yml',
    ompModels: 'C:\\Users\\someone-with-a-really-long-username\\.omp\\agent-with-an-unusually-long-directory-name\\models.yml',
    oc: '/Users/someone-with-a-really-long-username/.config/opencode/with/a/deeply/nested/directory/structure/opencode.json',
  } : {
    pi: '/Users/demo/.pi/agent/amagi.json', models: '/Users/demo/.pi/agent/models.json', auth: '/Users/demo/.pi/agent/auth.json',
    omp: '/Users/demo/.omp/agent/config.yml', ompModels: '/Users/demo/.omp/agent/models.yml', oc: '/Users/demo/.config/opencode/opencode.json',
  };

  // ---------- 场景处理 ----------
  const J = (o) => JSON.stringify(o, null, 2);
  const never = () => new Promise(() => {});
  const fail = (m) => Promise.reject(new Error(m));
  const ok = (v) => Promise.resolve(v);

  /** 读取类配置调用：按场景包装 */
  function cfg(textFull, textEmpty, textBad) {
    if (has('loading')) return never();
    if (has('error')) return fail('读取失败：permission denied（mock）');
    if (has('bad-json') && textBad !== undefined) return ok(textBad);
    return ok(has('empty') ? textEmpty : textFull);
  }
  function cat() {
    if (has('catalog-error')) return fail('fixture catalog unavailable（mock）');
    return ok(J(has('empty') ? { providers: [] } : catalog));
  }

  const handlers = {
    // ---- 配置档：测试真实按钮触发的 presetLoadKey 重挂载，仅浏览器内存 ----
    'agentprofile.Service.ListAgentProfiles': () => ok(J({ version:1, profiles:{ fixture:{ pi:J(piConfig), omp:ompConfigYaml, updatedAt:1 } }, lastApplied:'' })),
    'agentprofile.Service.ApplyAgentProfile': () => { piConfig.profile = 'inherit'; return ok(); },
    // ---- 预设页 ----
    'main.App.GetMergedTerminalPresets': ([t]) => {
      if (has('loading')) return never();
      if (has('empty')) return ok([]);
      return ok(t === 'openai' ? openaiPresets : anthropicPresets);
    },
    'main.App.GetOpenCodeConfig': () => cfg(J(ocConfig), '', '{ "broken": '),
    'main.App.GetOpenCodeConfigPath': () => ok(PATHS.oc),
    'main.App.SaveOpenCodeConfig': ([c]) => { window.__saved.oc = c; return ok(); },
    // ---- Pi ----
    'main.App.GetAmagiConfig': () => cfg(J(piConfig), '{}', '{"compaction":{"model":"oops"'),
    'main.App.GetAmagiConfigPath': () => ok(PATHS.pi),
    'main.App.SaveAmagiConfig': ([c]) => { window.__saved.pi = c; return ok(); },
    'main.App.GetPiModelCatalog': () => cat(),
    'main.App.GetPiModelsConfig': () => has('late-pi') ? new Promise(resolve => { window.__resolveLatePi = () => resolve(J(piModels)); }) : cfg(J(piModels), '{}', '{ "providers": { "x": '),
    'main.App.GetPiModelsConfigPath': () => ok(PATHS.models),
    'main.App.SavePiModelsConfig': ([c]) => { window.__saved.piModels = c; return ok(); },
    'main.App.GetPiAuthConfig': () => cfg(J(piAuth), '{}', '{ "x": '),
    'main.App.GetPiAuthConfigPath': () => ok(PATHS.auth),
    'main.App.SavePiAuthConfig': ([c]) => { window.__saved.piAuth = c; return ok(); },
    // ---- OMP ----
    'main.App.GetOmpConfig': () => cfg(ompConfigYaml, '', 'modelRoles: [unclosed'),
    'main.App.GetOmpConfigPath': () => ok(PATHS.omp),
    'main.App.SaveOmpConfig': ([c]) => { window.__saved.omp = c; return ok(); },
    'main.App.GetOmpModelCatalog': () => cat(),
    'main.App.GetOmpModelsConfig': () => cfg(count !== null ? JSON.stringify(piModels) : ompModelsYaml, '', 'providers: [unclosed'),
    'main.App.GetOmpModelsConfigPath': () => ok(PATHS.ompModels),
    'main.App.SaveOmpModelsConfig': ([c]) => { window.__saved.ompModels = c; return ok(); },
    // ---- 服务提供商页（默认 tab，给个最小数据避免全空）----
    'config.ConfigService.GetProviders': () => ok({}),
    // ---- 皮肤 ----
    'skins.Service.GetSkinSettings': () => ok({ enabled: has('skin'), imageId: has('skin') ? 'demo' : '', dim: 35, blur: has('skin') ? 8 : 0, opacity: 70, textBoost: 0 }),   // blur>0：避开 App.vue syncSkinDom↔skinBake.requestBake 在 blur=0 时的同步递归（改造前即存在，见 impl-report 范围外发现）
    'skins.Service.ListSkins': () => ok(has('skin') ? [{ id: 'demo', name: 'demo', url: '/skins/demo.svg' }] : []),   // 与真实 AssetHandler 一致：/skins/<file>；测试里由 Playwright 路由返回 skin-demo.svg
  };

  // ---------- Proxy 装配：未登记的调用一律 resolve(null)，登记的走 handlers ----------
  const mk = (pkg, cls) => new Proxy({}, {
    get: (_t, method) => (...args) => {
      window.__calls.push(`${pkg}.${cls}.${String(method)}`);
      if (has('save-error') && String(method).startsWith('Save')) return fail('isolated save rejected');
      const h = handlers[`${pkg}.${cls}.${String(method)}`];
      return h ? h(args) : ok(null);
    },
  });
  window.go = new Proxy({}, { get: (_t, pkg) => new Proxy({}, { get: (_t2, cls) => mk(String(pkg), String(cls)) }) });
  // Wails runtime：事件订阅类全部空实现（EventsOn 需返回取消函数）
  window.runtime = new Proxy({}, { get: () => () => () => {} });
})();
