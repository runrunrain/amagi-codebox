// v3 L2 browser checks: production App/components + isolated Wails transport (v2-mock-wails.js). No disk config IO.
// v3 IA：二三级在顶侧 .pc-subnav（v1 形态）；页面里唯一的 SectionNav 是编辑器内部 local 分区导航。
// 编辑器 local 路径在 v2 页面验收里从未执行过（v2 全走 external），所以每一批都必须在此重跑，不得沿用 v2 结果。
// 用法：node v3-runtime.mjs <drafts|dynamic|lifecycle|narrow|states|coverage|interactions> [viewFilter(dynamic only)]
import { chromium, expect } from '@playwright/test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const HERE = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(HERE, '..', 'screenshots');
const PORT = process.env.V3_PORT || '5199';
const BASE = `http://127.0.0.1:${PORT}`;
const LOCAL = 'nav.section-nav[aria-label="配置分区"]';
const LOCAL_BTN = `${LOCAL} button[data-nav-id]`;
const TOOLBAR = '.pc-detail-content .editor-toolbar, .pc-detail-content .pc-toolbar';
const browser = await chromium.launch();
// 滚动条探针专用：Playwright 默认带 --hide-scrollbars，所以其余所有批次的宽度都是「无滚动条」口径；真实应用的 base.css 给页面设了 8px 占布局的自定义滚动条。
const browserSb = await chromium.launch({ ignoreDefaultArgs:['--hide-scrollbars'] });
let activeBrowser = browser;
const errors = [];
const findings = [];
let checks = 0;
const pass = name => { checks++; console.log('PASS ' + name); };
const note = (k, v) => console.log(`NOTE ${k} = ${typeof v === 'string' ? v : JSON.stringify(v)}`);
// FINDING：不是通过项，也不吞掉——作为显式发现打印并计数，写入报告由 Leader 裁决。
const finding = (id, text) => { findings.push(`${id}: ${text}`); console.log(`FINDING ${id}: ${text}`); };
async function open(view, scenario = 'full', w = 1280, h = 800, extra = '', setup) {
  const ctx = await activeBrowser.newContext({ viewport:{ width:w, height:h } });
  await ctx.addInitScript({ path:path.join(HERE, 'v2-mock-wails.js') });
  await ctx.addInitScript({ path:path.join(HERE, 'v3-helpers.js') });
  if (setup) await setup(ctx);
  const page = await ctx.newPage();
  page.setDefaultTimeout(8000);
  page.on('pageerror', e => { errors.push(`${view}/${scenario}: ${e.stack || String(e)}`); console.log('PAGE ERROR', view, scenario, e.stack || String(e)); });
  await page.goto(`${BASE}/?s=${scenario}${extra}#/provider`);
  await page.evaluate(v => window.__ab.seq(v), view);
  return { ctx, page };
}
// section(p,id)：点编辑器内部 local 分区导航的局部 id，并断言唯一可见分区
const section = async (p, id) => { await p.evaluate(id => window.__ab.section(id), id); await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', id); };
const nav = (p, id) => p.locator(`${LOCAL_BTN}[data-nav-id="${id}"]`);
const shot = (p, name) => p.screenshot({ path:path.join(OUT, `v3-${name}.png`) });
const calls = p => p.evaluate(() => window.__calls.filter(n => /GetAmagi|GetPi|GetOmp|GetOpenCode/.test(n)));
const metrics = p => p.evaluate(() => window.__ab.metrics());
const tabLabels = p => p.evaluate(() => window.__ab.tabLabels());
const ready = p => expect(p.getByRole('button', { name:'保存配置', exact:true })).toBeVisible();
const tab = (p, name) => p.locator('.pc-subnav-tabs').getByRole('button', { name, exact:true });
const pill = (p, name) => p.locator('.pc-subnav-modes').getByRole('button', { name, exact:true });
// 测试预言机：v1 页签文案（来自 v1 之前提交 1637046^ 的 FORMAT_TABS/CLI_TABS/PI_MODES/OMP_MODES/OPENCODE_MODES 与 formatConsumers），
// 与生产常量独立持有，才能发现 configNavigation.ts 的文案漂移。
const V1 = {
  main: ['服务提供商', '预设', 'CLI 独立配置'], formats: ['Anthropic 格式', 'OpenAI 格式'], clis: ['Pi', 'OMP', 'OpenCode'],
  modes: { Pi: ['Agent 配置', '模型提供商', '认证登录'], OMP: ['Agent 配置', '模型提供商'], OpenCode: ['预设管理', '全局配置'] },
  caption: { 'presets-anthropic': '用于 Claude Code', 'presets-openai': '用于 Codex · Pi · OMP' },
};
const CLI_OF = view => ({ pi:'Pi', omp:'OMP', opencode:'OpenCode' })[view.split('-')[0]];
const NINE = ['presets-anthropic', 'presets-openai', 'pi-agents', 'pi-registry', 'pi-auth', 'omp-agents', 'omp-registry', 'opencode-presets', 'opencode-global'];
const WITH_SECTIONS = new Set(['pi-agents', 'pi-registry', 'pi-auth', 'omp-agents', 'omp-registry', 'opencode-global']);
const BANDS = ['pc-head', 'pc-subnav', 'toolbar'];
const batch = process.argv[2] || 'drafts';
try {
if (batch === 'drafts') {
  const { page:p, ctx } = await open('pi-agents', 'full,empty-compaction', 800, 600);
  await ready(p);
  const initialCalls = await calls(p);
  await section(p, 'compaction');
  const c = p.locator('.editor-section[data-section="compaction"]');
  await c.getByRole('button', { name:'统一模型', exact:true }).click();
  await c.getByRole('button', { name:'手动输入模型', exact:true }).click();
  await section(p, 'mcp');
  const mcp = p.locator('.editor-section[data-section="mcp"]');
  await mcp.getByRole('button', { name:'白名单', exact:true }).first().click();
  await section(p, 'concurrency');
  await p.locator('.pac-limit-default input').fill('');
  await section(p, 'compaction');
  await expect(c.getByRole('button', { name:'统一模型', exact:true })).toHaveAttribute('aria-pressed', 'true');
  await expect(c.locator('input')).toHaveValue('');
  await expect(c.getByRole('alert')).toContainText('完整');
  pass('V4 Pi 空 custom + ModelSpecSelector 手动态来回保留（local 路径）');
  await section(p, 'mcp');
  await expect(mcp.getByRole('button', { name:'白名单', exact:true }).first()).toHaveAttribute('aria-pressed', 'true');
  await expect(mcp).toContainText('名单为空，保存后等同「不加载」');
  await section(p, 'concurrency');
  await expect(p.locator('.pac-limit-default input')).toHaveValue('');
  assert.deepEqual(await calls(p), initialCalls);
  pass('V4 Pi MCP 空白名单／并发空值保留；V5 同文件切区零新增 API');
  await expect(p.getByRole('button', { name:'上下文压缩待修正' })).toBeVisible();
  await p.getByRole('button', { name:'上下文压缩待修正' }).click();
  await expect(c).toBeVisible();
  await shot(p, 'pi-hidden-error-800x600');
  pass('X10 隐藏压缩错误定位');
  // Programmatic section selection deliberately avoids pointerdown/blur as the close mechanism.
  await c.getByRole('button', { name:'说明：上下文压缩', exact:true }).focus();
  await p.waitForTimeout(100);
  await p.keyboard.press('Enter');
  await expect(p.locator('.infotip-pop:visible')).toHaveCount(1);
  await shot(p, 'pi-tip-800x600');
  const tipBox = await p.locator('.infotip-pop:visible').boundingBox();
  assert(tipBox.x >= 7 && tipBox.x + tipBox.width <= 793 && tipBox.y >= 7 && tipBox.y + tipBox.height <= 593);
  await p.keyboard.press('Escape');
  await expect(c.getByRole('button', { name:'说明：上下文压缩', exact:true })).toBeFocused();
  await p.keyboard.press('Space');
  await expect(p.locator('.infotip-pop:visible')).toHaveCount(1);
  await section(p, 'agents');
  await expect(p.locator('.infotip-pop:visible')).toHaveCount(0);
  await p.locator('.pac-profile-dd .dropdown-trigger').click();
  await expect(p.locator('.dropdown-menu:visible')).toHaveCount(1);
  await section(p, 'compaction');
  await expect(p.locator('.dropdown-menu:visible')).toHaveCount(0);
  pass('X4/X5 Enter/Space/Escape、回焦、800px 完整浮层；无 pointerdown 切区亦收 InfoTip/Dropdown');
  assert.deepEqual((await metrics(p)).overflow, []);
  await ctx.close();
  const oc = await open('opencode-global');
  await ready(oc.page);
  const ocCalls = await calls(oc.page);
  await section(oc.page, 'unknown');
  const raw = oc.page.locator('.rje-area');
  await raw.fill('{"retain invalid draft"');
  await section(oc.page, 'provider');
  await section(oc.page, 'unknown');
  await expect(raw).toHaveValue('{"retain invalid draft"');
  await expect(oc.page.locator('.rje-error')).toBeVisible();
  assert.deepEqual(await calls(oc.page), ocCalls);
  await shot(oc.page, 'opencode-invalid-local-draft');
  pass('V4 OpenCode RawJsonEditor 非法局部文本保留；切区不读取、不清洗数据');
  await oc.page.getByRole('button', { name:'JSON', exact:true }).click();
  await expect(nav(oc.page, 'unknown')).toBeDisabled();   // v3：局部 unknown 按钮，源码态禁用
  assert((await oc.page.locator('.ocg-json-editor').inputValue()).includes('"provider"')); // Entire-file source, not section source.
  await oc.page.getByRole('button', { name:'可视化', exact:true }).click();
  await expect(oc.page.locator('.editor-section:visible')).toHaveAttribute('data-section', 'unknown');
  pass('X10 源码为整个文件（local 分区按钮源码态禁用），返回保留有效选择');
  await oc.ctx.close();
}
if (batch === 'dynamic') {
  // 本批在 external 模式下的 v2 版本从未覆盖 local 路径；N=0/1/20 全部重跑。可选第 3 参过滤视图以便拆批。
  const only = process.argv[3];
  for (const view of ['pi-registry', 'omp-registry', 'pi-auth'].filter(v => !only || v === only)) {
    for (const n of [0, 1, 20]) {
      const { page:p, ctx } = await open(view, 'full', 800, 600, `&N=${n}`);
      await ready(p);
      const children = () => p.locator(LOCAL_BTN);
      await expect(children()).toHaveCount(n + 1);
      await expect(p.locator('.editor-section:visible')).toHaveCount(1);
      assert.deepEqual((await metrics(p)).overflow, []);
      if (n > 0) {
        // 限高：local .section-nav 的 clientHeight ≤ max(240, 100vh-280)；N=20 时可滚动，End 键后 add 进入可视区
        const box = await p.locator(LOCAL).evaluate(e => ({ h:e.clientHeight, sh:e.scrollHeight, max:getComputedStyle(e).maxHeight, limit:Math.max(240, window.innerHeight - 280) }));
        assert(box.h <= box.limit + 1, `${view} N=${n}: nav height ${box.h} > limit ${box.limit}`);
        assert(n !== 20 || box.sh > box.h, `${view} N=20 should scroll (sh=${box.sh} h=${box.h})`);
        note(`${view} N=${n} local nav`, box);
        assert((await p.locator('.editor-section:visible h3').innerText()).includes('long-fixture'));
        if (n === 20) {
          const firstId = await children().first().getAttribute('data-nav-id');
          const lastId = await children().last().getAttribute('data-nav-id');
          assert.equal(lastId, 'add');
          await nav(p, firstId).focus();
          await p.keyboard.press('End');
          await expect(nav(p, lastId)).toBeFocused();
          const vis = await nav(p, lastId).evaluate(e => { const r = e.getBoundingClientRect(), b = e.closest('nav').getBoundingClientRect(); return { top:r.top, bottom:r.bottom, navTop:b.top, navBottom:b.bottom, vh:window.innerHeight }; });
          assert(vis.top >= vis.navTop - 1 && vis.bottom <= vis.navBottom + 1 && vis.bottom <= vis.vh + 1, `add not in view after End: ${JSON.stringify(vis)}`);
          note(`${view} N=20 End→add`, vis);
        }
      }
      await shot(p, `${view}-N${n}-800x600`);
      await section(p, 'add');
      if (view.endsWith('registry')) {
        await p.locator('.pre-add-input input').fill('new-fixture');
        await p.getByRole('button', { name:'+ 添加提供商', exact:true }).click();
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider:new-fixture');
        await expect(p.locator('.editor-section:visible h3')).toBeFocused();
        await section(p, 'add');
        await p.locator('.pre-add-input input').fill('new-fixture');
        await p.getByRole('button', { name:'+ 添加提供商', exact:true }).click();
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'add');
        await section(p, 'provider:new-fixture');
        p.once('dialog', d => d.dismiss());
        await p.getByRole('button', { name:'删除提供商 new-fixture', exact:true }).click();
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider:new-fixture');
        p.once('dialog', d => d.accept());
        await p.getByRole('button', { name:'删除提供商 new-fixture', exact:true }).click();
        // 删除后继：直接取局部 data-nav-id，不再剥页面级前缀，也不再替换 %3A
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', n ? await children().nth(n - 1).getAttribute('data-nav-id') : 'add');
      } else {
        await p.locator('.ace-add-row .ace-name input').fill('new-fixture');
        await p.locator('.ace-key-input input').fill('isolated-placeholder');
        await p.getByRole('button', { name:'+ 添加', exact:true }).click();
        const added = await p.locator('.editor-section:visible').getAttribute('data-section');
        await expect(p.locator('.editor-section:visible h3')).toBeFocused();
        const input = p.locator('.editor-section:visible .ace-name input');
        await input.focus();
        await p.evaluate(() => { window.__originalInput = document.activeElement; });
        await input.press('End');
        await input.pressSequentially('-renamed');
        assert(await p.evaluate(() => document.activeElement === window.__originalInput));
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', added);
        await expect(p.locator('.editor-section:visible h3')).toHaveText('new-fixture-renamed');
        await p.getByRole('button', { name:'保存配置', exact:true }).click();
        const saved = await p.evaluate(() => JSON.parse(window.__saved.piAuth));
        assert(saved['new-fixture-renamed'].key === 'isolated-placeholder');
        assert(!JSON.stringify(saved).includes('uiId'));
        await section(p, 'add');
        await p.locator('.ace-add-row .ace-name input').fill('new-fixture-renamed');
        await p.locator('.ace-key-input input').fill('duplicate-placeholder');
        await p.getByRole('button', { name:'+ 添加', exact:true }).click();
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'add');
        await section(p, added);
        p.once('dialog', d => d.dismiss());
        await p.getByRole('button', { name:'删除条目', exact:true }).click();
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', added);
        p.once('dialog', d => d.accept());
        await p.getByRole('button', { name:'删除条目', exact:true }).click();
        await expect(p.locator('.editor-section:visible')).not.toHaveAttribute('data-section', added);
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', n ? await children().nth(n - 1).getAttribute('data-nav-id') : 'add');
      }
      // Middle deletion must select the NEXT item; last deletion above selected previous/add.
      if (n === 20) {
        const middleId = await children().nth(10).getAttribute('data-nav-id');
        const nextId = await children().nth(11).getAttribute('data-nav-id');
        await nav(p, middleId).click();
        p.once('dialog', d => d.accept());
        await p.locator('.editor-section:visible').getByRole('button', { name:view.endsWith('registry') ? /删除提供商/ : '删除条目', exact:!view.endsWith('registry') }).click();
        await expect(nav(p, nextId)).toHaveAttribute('aria-current', 'page');
        await expect(nav(p, nextId)).toBeFocused();
      }
      pass(`X8 ${view} N=${n} local 动态限高滚动/长名/新增/重名停留/取消/删除后继${view === 'pi-auth' ? '/重命名不丢焦点' : ''}`);
      await ctx.close();
    }
  }
  if (!only || only === 'pi-registry') {
    const { page:p, ctx } = await open('pi-registry');
    await ready(p); await section(p, 'root');
    await p.getByRole('button', { name:'删除键', exact:true }).click();
    await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider:amagi-glm');
    pass('X8 最后 rootUnknownKey 删除回退真实默认提供商');
    await ctx.close();
  }
}
if (batch === 'lifecycle') {
  const { page:p, ctx } = await open('pi-agents');
  await ready(p);
  const initial = await calls(p);
  await section(p, 'compaction'); await section(p, 'concurrency'); await section(p, 'agents');
  assert.deepEqual(await calls(p), initial);
  await p.locator('.ap-qs-trigger').click();
  await p.locator('.ap-qs-apply-btn').click();
  await expect(p.locator('.pac-profile-dd .dropdown-trigger')).toContainText('inherit');
  const after = await calls(p);
  assert.equal(after.filter(n => n === 'main.App.GetAmagiConfig').length, 2);
  pass('V5 配置档应用真实 applied 事件重挂载，GetAmagiConfig 1→2，读取新 fixture profile');
  await p.evaluate(() => window.__ab.mode('Pi', '模型提供商')); await ready(p);
  await expect(p.locator('.et-path')).toContainText('models.json');
  await p.evaluate(() => window.__ab.mode('OMP', '模型提供商')); await ready(p);
  await expect(p.locator('.et-path')).toContainText('models.yml');
  await p.getByRole('button', { name:'YAML', exact:true }).click();
  await expect(p.locator('.pre-source-editor')).toHaveValue(/^providers:/);
  pass('V5 Pi→OMP 注册表不复用旧实例：路径 JSON→YAML、数据格式正确（经顶侧 subnav 切换）');
  await ctx.close();
  // Editor data-load error retry is genuine UI retry, not state manipulation.
  const retry = await open('pi-agents', 'full', 1280, 800, '', async ctx => ctx.addInitScript(() => {
    const original = window.go.main.App.GetAmagiConfig; let fail = true;
    // Proxy supplies new functions on get; wrap only the selected read and preserve every other namespace.
    const go = window.go;
    const app = go.main.App;
    const wrappedApp = new Proxy(app, { get:(target, name) => name === 'GetAmagiConfig' ? () => { if (fail) { fail = false; return Promise.reject(new Error('isolated first read failed')); } return original(); } : target[name] });
    window.go = new Proxy(go, { get:(target, pkg) => pkg === 'main' ? new Proxy(go.main, { get:(main, cls) => cls === 'App' ? wrappedApp : main[cls] }) : target[pkg] });
  }));
  await expect(retry.page.getByText('isolated first read failed', { exact:false })).toBeVisible();
  await retry.page.getByRole('button', { name:'重试', exact:true }).click();
  await ready(retry.page);
  pass('V5 编辑器读取错误→真实重试→ready');
  await retry.ctx.close();
  // Chunk delay：三级留在容器里，所以异步编辑器未到位时 subnav 照常可用（X12）。
  let release;
  const blocked = new Promise(r => { release = r; });
  let delayedAttempts = 0;
  const delayed = await open('pi-agents', 'full', 1280, 800, '', ctx => ctx.route(/\/components\/provider\/PiAmagiConfig\.vue(?:\?|$)/, async route => { delayedAttempts++; await blocked; await route.continue(); }));
  await expect.poll(() => delayedAttempts).toBeGreaterThan(0);
  await expect(delayed.page.getByRole('button', { name:'保存配置', exact:true })).toHaveCount(0);
  await expect(delayed.page.locator('.pc-subnav-tabs button:enabled')).toHaveCount(3);
  await expect(delayed.page.locator('.pc-subnav-modes button:enabled')).toHaveCount(3);
  // defineAsyncComponent 的 loadingComponent 在挂载后 delay:120ms 才出现：先等它出现再截图（否则会截到空白帧，属时序假象）
  await expect(delayed.page.locator('.pc-detail-content .loading-state')).toBeVisible();
  await shot(delayed.page, 'chunk-delayed-subnav');
  await delayed.page.evaluate(() => window.__ab.mode('OMP', 'Agent 配置'));
  await ready(delayed.page);
  await expect(delayed.page.locator('.ogc-root')).toBeVisible();
  await expect(delayed.page.locator('.et-path')).toContainText('config.yml');
  release();
  await delayed.page.waitForTimeout(500);   // 放行迟到的 Pi chunk，它不得把视图换回 Pi
  await expect(delayed.page.locator('.pc-subnav-tabs button.active')).toHaveText('OMP');
  await expect(delayed.page.locator('.pac-root')).toHaveCount(0);
  await expect(delayed.page.locator('.ogc-root')).toBeVisible();
  await expect(delayed.page.locator('.et-path')).toContainText('config.yml');
  pass('X12 Pi chunk 挂起期间 subnav 三个 CLI 页签＋当前 pill 可点；切 OMP · Agent 配置能出 OMP 编辑器；放行 Pi chunk 后视图仍是 OMP');
  await delayed.ctx.close();
  let failures = 0;
  let failChunk = true;
  const failed = await open('pi-agents', 'full', 1280, 800, '', ctx => ctx.route(/\/components\/provider\/PiAmagiConfig\.vue(?:\?|$)/, route => { failures++; if (failChunk) return route.abort('failed'); return route.continue(); }));
  console.log('chunk attempts before manual retry:', failures);
  await expect(failed.page.getByText('配置页加载失败', { exact:true })).toBeVisible();
  await expect(failed.page.locator('.pc-subnav')).toBeVisible();
  await expect(failed.page.locator('.pc-subnav-tabs button:enabled')).toHaveCount(3);
  await expect(failed.page.locator('.pc-subnav-modes button:enabled')).toHaveCount(3);
  await shot(failed.page, 'chunk-error-subnav');
  pass('X12 chunk 失败：错误态下 subnav 在场，三个 CLI 页签＋三个 pill 全部可用');
  failChunk = false;
  await failed.page.evaluate(() => { window.__preReloadMarker = 'alive'; });
  await failed.page.getByRole('button', { name:'重试', exact:true }).click();
  // HEAD 行为（保留）：Chromium 负缓存失败模块 URL，重试改为整页重载恢复（module map 随文档重建）。
  await failed.page.waitForFunction(() => window.__preReloadMarker === undefined, undefined, { timeout: 15000 });
  await failed.page.getByRole('button', { name:'CLI 独立配置', exact:true }).click();
  await expect(failed.page.getByRole('button', { name:'保存配置', exact:true })).toBeVisible({ timeout: 15000 });
  await expect(failed.page.getByText('配置页加载失败', { exact:true })).toHaveCount(0);
  pass('V5 chunk 失败→重试整页重载→URL 已释放后恢复加载（HEAD 整页重载恢复保留）');
  await failed.page.evaluate(() => window.__ab.mode('OMP', 'Agent 配置')); await ready(failed.page);
  pass('V5 chunk 失败后经 subnav 可达其他模式（OMP · Agent 配置）');
  await failed.ctx.close();
  const late = await open('pi-registry', 'full,late-pi');
  await expect(late.page.locator('.loading-state')).toBeVisible();   // Pi 注册表编辑器自己的 LoadingState（原「加载分区中…」随页面级导航删除）
  await late.page.evaluate(() => window.__ab.mode('OMP', '模型提供商')); await ready(late.page);
  await late.page.evaluate(() => window.__resolveLatePi());
  await expect(late.page.locator('.et-path')).toContainText('models.yml');
  await expect(late.page.locator(LOCAL_BTN)).toHaveCount(2);
  pass('V5 旧 Pi 注册表迟到请求不污染 OMP：local 导航按钮数 2、路径 models.yml');
  await late.ctx.close();
}
if (batch === 'narrow') {
  for (const [view, sections] of [
    ['pi-agents', ['agents', 'compaction', 'mcp', 'concurrency']],
    ['omp-agents', ['modelRoles', 'overrides']],
    ['opencode-global', ['model', 'provider', 'agent', 'mcp', 'permission', 'instructions', 'plugin', 'experimental', 'unknown']],
  ]) {
    const { page:p, ctx } = await open(view, 'full,long', 800, 600); await ready(p);
    for (const id of sections) {
      await section(p, id);
      await p.locator('.editor-section:visible').evaluate(e => e.querySelectorAll('.pe-thumb-head[aria-expanded="false"], .ae-thumb-head[aria-expanded="false"], .me-thumb-head[aria-expanded="false"]').forEach(b => b.click()));
      const mm = await metrics(p);
      assert.deepEqual(mm.overflow, [], `${view}/${id} overflow: ${JSON.stringify(mm.overflow)}`);
      await shot(p, `${view}-${id}-800x600`);
      pass(`X2 800×600 ${view}/${id} 单显/展开记录无横溢`);
    }
    await ctx.close();
  }
  // X1/X2 @800×600 九视图结构：条带数、subnav 行数、工具栏行数、grid、整宽、无横溢（含 .pc-subnav）
  for (const view of NINE) {
    const { page:p, ctx } = await open(view, 'full', 800, 600);
    await expect(p.locator(TOOLBAR)).toBeVisible();
    await p.waitForTimeout(300);
    const m = await metrics(p);
    assert.equal(m.side, 264);
    assert.deepEqual(m.overflow, [], `${view} overflow ${JSON.stringify(m.overflow)}`);
    assert.equal(m.pageNav, 0);
    assert.equal(m.subnavCount, 1);
    assert.deepEqual(m.bands, BANDS, `${view} bands ${JSON.stringify(m.bands)}`);
    if (WITH_SECTIONS.has(view)) { assert.equal(m.grid, '112px 356px', `${view} grid ${m.grid}`); assert.equal(m.navs, 1); assert.equal(m.navsInEditor, 1); }
    else { assert.equal(m.grid, null); assert.equal(m.navs, 0); assert(m.detailW >= 464 && m.toolbarW >= 464, `${view} not full width: detail=${m.detailW} toolbar=${m.toolbarW}`); }
    assert(m.subnavRows <= 2, `${view} subnavRows=${m.subnavRows}`);
    if (m.subnavRows === 2) finding('X1', `${view}@800 subnav 折成 2 行（规格兜底允许，已记录）`);
    if (view === 'opencode-presets') {
      // 规格 X1：toolbarRows ≤ 2。这个视图实测 3 行：成因是 HEAD 既有的两条规则叠加——EditorToolbar 在 <640px 容器里 lead/trail 各占整行，
      // OpenCode 搜索在窄工具栏下 flex-basis:100%（v2 §4.2 的既定设计）。v3 把宽度从 v2 的 356 恢复到整宽 480，但断点仍在 640。
      // 该文件/基件在 v3 边界之外（零改动），所以不改代码、不放宽阈值，而是硬断言成因并作为 FINDING 上报。
      const cause = await p.evaluate(() => { const tb = document.querySelector('.pc-detail-content .editor-toolbar'), s = tb.querySelector('.pc-search'); return { searchW:Math.round(s.getBoundingClientRect().width), toolbarW:Math.round(tb.getBoundingClientRect().width) }; });
      assert.equal(cause.searchW, cause.toolbarW, '搜索框应独占整行（既有 flex-basis:100%）');
      assert.equal(m.toolbarRows, 3, `opencode-presets 工具栏行数变化：${m.toolbarRows}`);
      finding('X1-toolbar', `opencode-presets@800×600 toolbarRows=3 > 规格 ${'≤'}2（工具栏 ${m.toolbarH}px：搜索 / 路径 / 模式＋添加 各一行）。成因：EditorToolbar <640px 容器 lead/trail 各占整行＋搜索 flex-basis:100%（HEAD 既有，v2 §4.2 定稿）；宽度已从 v2 的 356 恢复为 480，但断点在 640，故行数不变。基件在 v3 零改动边界之外。`);
    } else assert(m.toolbarRows <= 2, `${view} toolbarRows=${m.toolbarRows}`);
    note(`X1@800 ${view}`, { subnavRows:m.subnavRows, toolbarRows:m.toolbarRows, firstContentTop:m.firstContentTop, pageHeadH:m.pageHeadH, pcHeadH:m.pcHeadH, pcHeadRows:m.pcHeadRows, subnavH:m.subnavH, toolbarH:m.toolbarH, subnavW:m.subnavW, tabsW:m.tabsW, modesW:m.modesW });
    if (view === 'pi-agents') {
      // 规格上限：firstContentTop ≤ 340（§4.3 估算 302 + 38 容差）。先硬断言"意外折行"这一规格本意，再如实报告数值上限。
      assert.equal(m.subnavRows, 1, 'Pi Agent subnav 不得折行');
      assert.equal(m.toolbarRows, 2, 'Pi Agent 工具栏在 356px 容器里按设计为 lead/trail 两行');
      if (m.firstContentTop > 340) finding('X1-cap', `pi-agents@800×600 firstContentTop=${m.firstContentTop} > 规格上限 340。分层：PageHead ${m.pageHeadH}px（估 46）、.pc-head ${m.pcHeadH}px（估 36；pcHeadRows=${m.pcHeadRows}，动作区折到第二行，HEAD 既有）、subnav ${m.subnavH}px、工具栏 ${m.toolbarH}px（估 76）。subnav 与工具栏均未意外折行；超出来自估算里 PageHead/.pc-head 两项常量偏低（均为 HEAD 既有、v3 明令不动）。`);
      else pass('X1 pi-agents@800×600 firstContentTop ≤ 340');
    }
    pass(`X1/X2 800×600 ${view}：3 条带、subnavRows=${m.subnavRows}、toolbarRows=${m.toolbarRows}、grid=${m.grid}、无横溢（含 .pc-subnav）`);
    await ctx.close();
  }
  // subnav 单行余量与折行兜底（design §4.2 的「约 60px 余量」在真实渲染下复核）：逐级放大 .pc-subnav 内文字（CSS zoom 仅作用于该元素内部，容器物理宽度不变），找第一次折行的倍率
  {
    const { page:p, ctx } = await open('pi-agents', 'full', 800, 600); await ready(p);
    let firstWrap = null;
    for (const z of [1, 1.1, 1.2, 1.3, 1.4, 1.5]) {
      await p.evaluate(z => { document.querySelector('.pc-subnav').style.zoom = String(z); }, z);
      await p.waitForTimeout(80);
      const m = await metrics(p);
      note(`subnav 文字放大 ${z}× @800`, { subnavRows:m.subnavRows, subnavH:m.subnavH, overflow:m.overflow.length });
      assert.deepEqual(m.overflow, [], `zoom ${z}: 折行兜底不得横溢`);
      // 折行兜底态下当前 CLI 页签的下划线仍在（计算样式断言；1× 截图里亚像素下划线肉眼很淡，不能靠读图判断）
      const ul = await p.evaluate(() => { const b = [...document.querySelectorAll('.pc-subnav-tabs button')].find(x => x.classList.contains('active')); const cs = getComputedStyle(b); return { w:parseFloat(cs.borderBottomWidth), style:cs.borderBottomStyle, color:cs.borderBottomColor }; });
      assert(ul.w >= 1.2 && ul.style === 'solid' && ul.color !== 'rgba(0, 0, 0, 0)', `zoom ${z}: 当前页签下划线消失 ${JSON.stringify(ul)}`);
      if (m.subnavRows === 2 && firstWrap === null) { firstWrap = z; await shot(p, 'subnav-wrap-fallback-800x600'); }
    }
    assert(firstWrap === null || firstWrap >= 1.2, `subnav 在文字放大 ${firstWrap}× 就折行，余量不足 10%`);
    note('subnav 单行余量', { firstWrapZoom:firstWrap, 说明:'文字放大到该倍率才第一次折成 2 行；折行后并无横溢（见截图）' });
    pass(`X1 subnav @800 文字放大至 ${firstWrap === null ? '>1.5' : '≥1.2 且第一次折行在 ' + firstWrap}× 才折行；折行兜底无横溢`);
    await ctx.close();
  }
  // 滚动条预算（设计 §4.1 预留了 Windows 16px 经典滚动条的 340px）。实际上 base.css:49 全局设置 ::-webkit-scrollbar{width:8px}，WKWebView/WebView2 都会占 8px 布局宽度。
  // 两档仿真（用不带 --hide-scrollbars 的专用浏览器，仍是 Chromium，不是真实 WebView）：应用真实 8px；保守上界 16px（注入覆盖样式）。
  activeBrowser = browserSb;
  try {
    for (const [label, px] of [['应用真实 8px', 8], ['保守上界 16px', 16]]) {
      for (const view of NINE) {
        const setup = px === 16 ? (c => c.addInitScript(() => { document.addEventListener('DOMContentLoaded', () => { const s = document.createElement('style'); s.textContent = '.main::-webkit-scrollbar{width:16px;height:16px}'; document.head.appendChild(s); }); })) : undefined;
        const { page:p, ctx } = await open(view, 'full', 800, 600, '', setup);
        await expect(p.locator(TOOLBAR)).toBeVisible();
        await p.waitForTimeout(300);
        const m = await metrics(p);
        const sb = await p.evaluate(() => { const e = document.querySelector('.main'); return { sbW:e.offsetWidth - e.clientWidth, scrolls:e.scrollHeight > e.clientHeight }; });
        // 页面不溢出（如只有 2 条的 OpenAI 预设列表）就没有滚动条，预算不被占用：按 0px 校验，同样是有效证据
        const used = sb.scrolls ? px : 0;
        assert.equal(sb.sbW, used, `${label}: 滚动条实际占宽 ${sb.sbW}px（页面${sb.scrolls ? '溢出' : '未溢出'}）`);
        assert.deepEqual(m.overflow, [], `${view} ${label} 横溢 ${JSON.stringify(m.overflow)}`);
        assert.deepEqual(m.bands, BANDS);
        assert(m.subnavRows <= 2, `${view} ${label}: subnavRows=${m.subnavRows}`);
        if (WITH_SECTIONS.has(view)) assert.equal(m.grid, `112px ${356 - used}px`, `${view} ${label} grid ${m.grid}`);
        else assert.equal(m.detailW, 480 - used, `${view} ${label} detailW ${m.detailW}`);
        note(`滚动条仿真 ${label} ${view}`, { pageOverflows:sb.scrolls, sbW:sb.sbW, grid:m.grid, detailW:m.detailW, subnavRows:m.subnavRows, spare:m.detailW - m.tabsW - (m.modesW || 0) - 16, toolbarRows:m.toolbarRows });
        pass(`X2 800×600 + ${label}滚动条 ${view}：${WITH_SECTIONS.has(view) ? 'grid 112px ' + (356 - used) + 'px' : '整宽 ' + (480 - used) + 'px'}（${sb.scrolls ? '页面溢出，滚动条占 ' + used + 'px' : '页面未溢出，无滚动条'}）、subnavRows=${m.subnavRows}、无横溢`);
        await ctx.close();
      }
    }
  } finally { activeBrowser = browser; }
  for (const view of ['pi-registry', 'opencode-presets']) {
    const { page:p, ctx } = await open(view, 'full,long', 1024, 768); await expect(p.locator(TOOLBAR)).toBeVisible();
    await p.waitForTimeout(300);
    const m = await metrics(p);
    assert.deepEqual(m.overflow, []);
    assert.deepEqual(m.bands, BANDS);
    if (view === 'pi-registry') { assert.equal(m.grid, '144px 524px'); assert.equal(m.navs, 1); assert.equal(m.navsInEditor, 1); }
    else { assert.equal(m.grid, null); assert.equal(m.navs, 0); assert(m.toolbarW >= 660, `${view} toolbar width ${m.toolbarW} < 660`); }
    note(`1024 ${view}`, { grid:m.grid, toolbarCls:m.toolbarCls, toolbarW:m.toolbarW, detailW:m.detailW, subnavRows:m.subnavRows, toolbarRows:m.toolbarRows, firstContentTop:m.firstContentTop });
    await shot(p, `${view}-1024x768`);
    pass(`X2 1024 档 ${view}：${view === 'pi-registry' ? 'grid 144px 524px' : 'grid null、工具栏整宽 ' + m.toolbarW + 'px（≥660）'}、无横溢`); await ctx.close();
  }
  for (const scenario of ['full,no-model,no-unknown', 'empty']) {
    const { page:p, ctx } = await open('opencode-global', scenario); await ready(p);
    await expect(p.locator(LOCAL_BTN)).toHaveCount(7);
    await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider');
    await shot(p, `opencode-${scenario.startsWith('empty') ? 'empty' : 'seven-sections'}`);
    pass('X9 OpenCode ' + scenario + ' 7 常驻分区（local 导航按钮）/provider 默认'); await ctx.close();
  }
  const { page:p, ctx } = await open('opencode-global'); await ready(p);
  await expect(p.locator(LOCAL_BTN)).toHaveCount(9);
  await section(p, 'unknown');
  await p.getByRole('button', { name:'JSON', exact:true }).click();
  const source = JSON.parse(await p.locator('.ocg-json-editor').inputValue()); delete source.preset; delete source.model;
  await p.locator('.ocg-json-editor').fill(JSON.stringify(source));
  await p.getByRole('button', { name:'可视化', exact:true }).click();
  await expect(p.locator(LOCAL_BTN)).toHaveCount(7);
  await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider');
  pass('X9 model/unknown 条件消失回退 provider，不生成不存在的键（9→7）'); await ctx.close();
}
if (batch === 'states') {
  const views = ['pi-agents','pi-registry','pi-auth','omp-agents','omp-registry','opencode-presets','opencode-global'];
  for (const scenario of ['loading', 'error']) for (const view of views) {
    const { page:p, ctx } = await open(view, scenario, 800, 600);
    await expect(p.locator(scenario === 'loading' ? '.loading-state' : '.error-state')).toBeVisible();
    // X12：subnav 在场，3 个 CLI 页签 + 当前 CLI 的全部 pill 可用（不依赖编辑器是否加载成功）
    await expect(p.locator('.pc-subnav')).toBeVisible();
    await expect(p.locator('.pc-subnav-tabs button:enabled')).toHaveCount(3);
    await expect(p.locator('.pc-subnav-modes button:enabled')).toHaveCount(V1.modes[CLI_OF(view)].length);
    assert.deepEqual((await metrics(p)).overflow, []);
    await shot(p, `${view}-${scenario}-800x600`);
    pass(`X12 全状态 ${view}/${scenario}：subnav 可见，3 CLI 页签＋${V1.modes[CLI_OF(view)].length} 个 pill 均可用，无横溢`); await ctx.close();
  }
  // 抽一例：pi-registry 在 loading / error 时点 OMP 页签仍能进入 OMP 编辑器
  for (const scenario of ['loading', 'error']) {
    const { page:p, ctx } = await open('pi-registry', scenario, 800, 600);
    await expect(p.locator('.pre-root')).toBeVisible();
    await tab(p, 'OMP').click();
    await expect(p.locator('.ogc-root')).toBeVisible();
    await expect(p.locator('.pre-root')).toHaveCount(0);
    await expect(p.locator('.pc-subnav-tabs button.active')).toHaveText('OMP');
    await expect(p.locator(scenario === 'loading' ? '.loading-state' : '.error-state')).toBeVisible();
    await shot(p, `pi-registry-${scenario}-to-omp-800x600`);
    pass(`X12 pi-registry/${scenario} → 点 OMP 页签后进入 OMP 编辑器（其自身 ${scenario} 态）`); await ctx.close();
  }
  for (const view of ['presets-anthropic','presets-openai','pi-registry','pi-auth','omp-registry','opencode-presets']) {
    const { page:p, ctx } = await open(view, 'empty', 800, 600);
    await expect(p.locator('.empty-state')).toBeVisible();
    assert.deepEqual((await metrics(p)).overflow, []);
    await shot(p, `${view}-empty-800x600`);
    pass(`全状态 ${view}/empty 保留新增动作`); await ctx.close();
  }
  for (const scenario of ['full', 'full,save-error']) {
    const { page:p, ctx } = await open('pi-agents', scenario); await ready(p);
    await p.getByRole('button', { name:'保存配置', exact:true }).click();
    await expect(p.locator('.toast-container')).toContainText(scenario.includes('save-error') ? 'isolated save rejected' : '配置已保存');
    await expect(p.getByRole('button', { name:'保存配置', exact:true })).toBeEnabled();
    await expect.poll(() => p.locator('.toast-item').first().evaluate(e => getComputedStyle(e).opacity)).toBe('1');
    await shot(p, scenario.includes('save-error') ? 'pi-save-error' : 'pi-save-success');
    pass('保存反馈 ' + scenario); await ctx.close();
  }
}
if (batch === 'coverage') {
  // 1280×800 九视图结构 + S13 文案预言机 + X1 计量
  for (const view of NINE) {
    const { page:p, ctx } = await open(view); await expect(p.locator(TOOLBAR)).toBeVisible();
    await p.waitForTimeout(300);
    const m = await metrics(p);
    assert.deepEqual(m.overflow, [], `${view} overflow ${JSON.stringify(m.overflow)}`);
    assert.equal(m.side, 264);
    assert.equal(m.pageNav, 0, `${view}: 页面级 .pc-workspace 必须为 0`);
    assert.equal(m.subnavCount, 1, `${view}: .pc-subnav 必须恰好 1 个`);
    if (WITH_SECTIONS.has(view)) { assert.equal(m.navs, 1); assert.equal(m.navsInEditor, 1); assert.equal(m.grid, '184px 712px', `${view} grid ${m.grid}`); }
    else { assert.equal(m.navs, 0); assert.equal(m.navsInEditor, 0); assert.equal(m.grid, null); }
    await expect(p.locator('.pc-detail-content h2, .config-cat-card')).toHaveCount(0);
    // X1@1280：3 条带，subnavRows=1，toolbarRows=1
    assert.deepEqual(m.bands, BANDS, `${view} bands ${JSON.stringify(m.bands)}`);
    assert.equal(m.subnavRows, 1, `${view} subnavRows`);
    assert.equal(m.toolbarRows, 1, `${view} toolbarRows=${m.toolbarRows} (toolbarW=${m.toolbarW})`);
    assert(m.underlineDelta !== null && Math.abs(m.underlineDelta) <= 0.5, `${view} underline 未压在分隔线上 delta=${m.underlineDelta}`);
    // S13：渲染出的页签文案与 v1 逐一相等
    const t = await tabLabels(p);
    assert.deepEqual(t.main, V1.main);
    if (view.startsWith('presets-')) { assert.deepEqual(t.tabs, V1.formats); assert.deepEqual(t.modes, []); assert.equal(t.caption, V1.caption[view]); }
    else { assert.deepEqual(t.tabs, V1.clis); assert.deepEqual(t.modes, V1.modes[CLI_OF(view)]); assert.equal(t.caption, null); }
    note(`X1@1280 ${view}`, { bands:m.bands.length, subnavRows:m.subnavRows, toolbarRows:m.toolbarRows, firstContentTop:m.firstContentTop, subnavW:m.subnavW, tabsW:m.tabsW, modesW:m.modesW, toolbarW:m.toolbarW, underlineDelta:m.underlineDelta });
    pass(`X1/S3/S13 ${view} @1280：pageNav=0、subnav=1、${WITH_SECTIONS.has(view) ? 'navs=1=navsInEditor、grid 184px 712px' : 'navs=0、grid null'}、3 条带、subnavRows=1、toolbarRows=1、页签文案=v1`); await ctx.close();
  }
  // X4：subnav 是原生按钮，逐个 Tab 可达（顺序 = DOM 顺序）
  {
    const { page:p, ctx } = await open('pi-agents'); await ready(p);
    const names = (await p.locator('.pc-subnav button').allInnerTexts()).map(s => s.trim());
    assert.deepEqual(names, ['Pi', 'OMP', 'OpenCode', 'Agent 配置', '模型提供商', '认证登录']);
    await p.locator('.pc-subnav button').first().focus();
    const seen = [await p.evaluate(() => document.activeElement.textContent.trim())];
    for (let i = 1; i < names.length; i++) { await p.keyboard.press('Tab'); seen.push(await p.evaluate(() => document.activeElement.textContent.trim())); }
    assert.deepEqual(seen, names);
    assert.deepEqual(await p.locator('.pc-subnav button').evaluateAll(bs => bs.map(b => b.tabIndex)), [0, 0, 0, 0, 0, 0]);
    pass('X4 subnav 6 个原生按钮逐个 Tab 可达，顺序与 DOM 一致');
    // 键盘焦点可见性：Segmented 自带无 :focus-visible 样式，靠 UA focus ring。真实 Tab 到 OMP 页签与「模型提供商」pill，断言全局样式没抹掉 outline，并存截图供读图。
    const focusInfo = () => p.evaluate(() => { const e = document.activeElement, cs = getComputedStyle(e); return { text:e.textContent.trim(), focusVisible:e.matches(':focus-visible'), outlineStyle:cs.outlineStyle }; });
    const subBox = await p.locator('.pc-subnav').boundingBox();
    const clip = { x:Math.max(subBox.x - 8, 0), y:subBox.y - 4, width:subBox.width + 16, height:subBox.height + 8 };
    await p.locator('.pc-subnav-tabs button').first().focus(); await p.keyboard.press('Tab');
    const f1 = await focusInfo();
    assert(f1.text === 'OMP' && f1.focusVisible && f1.outlineStyle !== 'none', 'underline 页签焦点环不可见 ' + JSON.stringify(f1));
    await p.screenshot({ path:path.join(OUT, 'v3-subnav-focus-tab-1280x800.png'), clip });
    for (let i = 0; i < 3; i++) await p.keyboard.press('Tab');   // OMP -> OpenCode -> Agent 配置 -> 模型提供商（再按 3 次）
    const f2 = await focusInfo();
    assert(f2.text === '模型提供商' && f2.focusVisible && f2.outlineStyle !== 'none', 'pill 焦点环不可见 ' + JSON.stringify(f2));
    await p.screenshot({ path:path.join(OUT, 'v3-subnav-focus-pill-1280x800.png'), clip });
    pass('X4 subnav 键盘焦点环可见：underline 页签与 pill 均 :focus-visible 且 outline-style=' + f1.outlineStyle + '（全局样式未抹掉）');
    // local 导航单 Tab 入口：subnav 之外、编辑器内只有一个 tabindex=0 的分区按钮
    assert.equal(await p.locator(LOCAL_BTN).evaluateAll(bs => bs.filter(b => b.tabIndex === 0).length), 1);
    pass('X4 local 分区导航单 Tab 入口（roving tabindex）');
    await ctx.close();
  }
  // X11：subnav 行为
  {
    const { page:p, ctx } = await open('pi-registry'); await ready(p);
    let t = await tabLabels(p);
    assert.equal(t.activeTab, 'Pi'); assert.equal(t.activeMode, '模型提供商'); assert.deepEqual(t.modes, V1.modes.Pi);
    await tab(p, 'OMP').click(); await ready(p);
    t = await tabLabels(p);
    assert.deepEqual(t.modes, V1.modes.OMP, '切到 OMP 后 pill 组应随之切换');
    assert.equal(t.activeMode, 'Agent 配置');
    await expect(p.locator('.ogc-root')).toBeVisible();
    await tab(p, 'Pi').click(); await ready(p);
    t = await tabLabels(p);
    assert.equal(t.activeTab, 'Pi'); assert.equal(t.activeMode, '模型提供商', 'Pi 选「模型提供商」→ 切 OMP → 回 Pi 应仍停在「模型提供商」');
    await expect(p.locator('.pre-root')).toBeVisible();
    await expect(p.locator('.et-path')).toContainText('models.json');
    await tab(p, 'OpenCode').click(); await ready(p);
    t = await tabLabels(p);
    assert.deepEqual(t.modes, V1.modes.OpenCode); assert.equal(t.activeMode, '全局配置');
    await pill(p, '预设管理').click();
    await expect(p.locator('.oc-group')).toBeVisible();
    assert.equal((await metrics(p)).grid, null);
    await tab(p, 'Pi').click();
    t = await tabLabels(p); assert.equal(t.activeMode, '模型提供商');
    pass('X11 切 CLI 时 pill 组随之切换；Pi「模型提供商」→OMP→回 Pi 仍停在「模型提供商」；OpenCode 默认「全局配置」，可切「预设管理」');
    // 预设页 caption 随格式切换
    await p.getByRole('button', { name:'预设', exact:true }).click();
    t = await tabLabels(p); assert.equal(t.caption, '用于 Claude Code'); assert.deepEqual(t.modes, []);
    await tab(p, 'OpenAI 格式').click();
    t = await tabLabels(p); assert.equal(t.caption, '用于 Codex · Pi · OMP');
    await tab(p, 'Anthropic 格式').click();
    t = await tabLabels(p); assert.equal(t.caption, '用于 Claude Code');
    pass('X11 预设页 caption 随格式在「用于 Claude Code」与「用于 Codex · Pi · OMP」之间切换');
    // 相对 v2 的「可观察行为变化」（design §6.1 已声明）：sectionMemory 已删，切模式再回来分区回到默认（与 v1、fixture 一致）。实测，不靠推断。
    await p.getByRole('button', { name:'CLI 独立配置', exact:true }).click();
    await tab(p, 'Pi').click();
    await pill(p, 'Agent 配置').click(); await ready(p);
    await section(p, 'concurrency');
    await pill(p, '模型提供商').click(); await ready(p);
    await pill(p, 'Agent 配置').click(); await ready(p);
    await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'agents');
    pass('X11 行为变化实测：Pi · Agent 配置选并发限制→切「模型提供商」再回来，分区回到默认「角色模型」（v2 的分区记忆已删，设计 §6.1 已声明）');
    const m = await metrics(p);
    assert(Math.abs(m.underlineDelta) <= 0.5, `预设页 underline delta=${m.underlineDelta}`);
    await ctx.close();
  }
  // X8（替换）：真实点击计数。起点固定 Pi · 认证登录；同一 CLI 换模式再到叶子 ≤2；跨 CLI 到叶子 ≤3（目标 CLI 上次就停在目标模式则 ≤2）
  {
    const { page:p, ctx } = await open('pi-auth'); await ready(p);
    const toStart = async () => { await p.evaluate(() => window.__ab.mode('Pi', '认证登录')); await ready(p); };
    const targets = [
      ['Pi', 'Agent 配置', ['agents', 'compaction', 'mcp', 'concurrency'], 2],
      ['OMP', 'Agent 配置', ['modelRoles', 'overrides'], 3],
      ['OpenCode', '全局配置', ['model', 'provider', 'agent', 'mcp', 'permission', 'instructions', 'plugin', 'experimental', 'unknown'], 3],
    ];
    const worst = [];
    for (const [cli, modeLabel, ids, bound] of targets) for (const id of ids) {
      await toStart();
      let clicks = 0;
      const t0 = tab(p, cli);
      if (!(await t0.evaluate(b => b.classList.contains('active')))) { await t0.click(); clicks++; }
      const p0 = pill(p, modeLabel);
      if ((await p0.getAttribute('aria-pressed')) !== 'true') { await p0.click(); clicks++; }
      await ready(p);
      await nav(p, id).click(); clicks++;
      await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', id);
      assert(clicks <= bound, `${cli}/${id}: ${clicks} clicks > ${bound}`);
      worst.push(clicks);
      pass(`X8 Pi·认证登录 → ${cli}·${modeLabel}·${id}：${clicks} 次真实点击（上限 ${bound}）`);
    }
    // 最坏路径：目标 CLI 上次停在另一模式（OMP 先停「模型提供商」），回到起点后到 OMP · 子代理覆盖 = CLI 页签＋pill＋叶子 = 3
    await p.evaluate(() => window.__ab.mode('OMP', '模型提供商')); await ready(p);
    await toStart();
    let clicks = 0;
    await tab(p, 'OMP').click(); clicks++;
    assert.equal(await pill(p, '模型提供商').getAttribute('aria-pressed'), 'true', 'OMP 应记得上次停在「模型提供商」');
    await pill(p, 'Agent 配置').click(); clicks++; await ready(p);
    await nav(p, 'overrides').click(); clicks++;
    await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'overrides');
    assert.equal(clicks, 3);
    note('X8 点击深度', { allLeafClicks:worst.length, max:Math.max(...worst), worstPathClicks:clicks });
    pass('X8 最坏路径（目标 CLI 上次停在另一模式）= CLI 页签＋pill＋叶子 = 3 次，未超过上限');
    await ctx.close();
  }
  // local 路径首次在页面级嵌入：sticky 分区导航随 .main 真实滚动后停在 top:12px 且不被裁掉（design §4.3 的纸面核算在真实滚动下复核）
  // 期望 top = max(12, 自然位置 - scrollTop)：页面足够长时粘在 12px，否则跟随页面。选足够长的视图：800 档 pi-agents，1280 档 pi-registry（pi-agents@1280 只能滚 25px）。
  for (const [view, w, h] of [['pi-agents', 800, 600], ['pi-registry', 1280, 800]]) {
    const { page:p, ctx } = await open(view, 'full', w, h); await ready(p);
    const before = await p.locator(LOCAL).evaluate(e => e.getBoundingClientRect().top);
    await p.evaluate(() => { document.querySelector('.main').scrollTop = 300; });
    await p.waitForTimeout(200);
    const after = await p.locator(LOCAL).evaluate(e => { const r = e.getBoundingClientRect(); return { top:r.top, bottom:r.bottom, scrollTop:document.querySelector('.main').scrollTop, vh:window.innerHeight }; });
    assert(after.scrollTop > 0, `${w}x${h}: .main 未滚动（页面太短，无法验证 sticky）`);
    const expectTop = Math.max(12, before - after.scrollTop);
    assert(Math.abs(after.top - expectTop) <= 1 && after.bottom <= after.vh, `${view}@${w}x${h}: sticky 分区导航位置 ${JSON.stringify(after)} 期望 top=${expectTop}`);
    assert(Math.abs(after.top - 12) <= 1, `${view}@${w}x${h}: 滚动 ${after.scrollTop}px 后未粘在 12px（top=${after.top}），未验证到 sticky`);
    note(`sticky ${view}@${w}x${h}`, { topBeforeScroll:Math.round(before), ...after });
    pass(`local 分区导航在 ${view} ${w}×${h} 页面真实滚动后 sticky 停在 top:12px（滚动 ${after.scrollTop}px），底边 ${Math.round(after.bottom)} ≤ 视口 ${after.vh}`);
    if (w === 800) note('a11y 顶侧 underline 页签/pill 选中态语义（设计 §6.1 已登记的既存缺口，只取证）', await p.locator('.pc-subnav button').evaluateAll(bs => bs.map(b => ({ text:b.textContent.trim(), ariaPressed:b.getAttribute('aria-pressed'), ariaCurrent:b.getAttribute('aria-current'), active:b.classList.contains('active') }))));
    await ctx.close();
  }
  const bottom = await open('pi-agents', 'full', 800, 600); await ready(bottom.page); await section(bottom.page, 'mcp');
  const tip = bottom.page.getByRole('button', { name:'说明：角色附加服务器', exact:true });
  await tip.evaluate(e => { const main = document.querySelector('.main'); const r = e.getBoundingClientRect(); main.scrollTop += r.top - 560; });
  // InfoTip 对任何 scroll（window 捕获阶段）都会 close()，而程序化 scrollTop 的 scroll 事件在下一帧才派发：必须等它（以及 focus 触发的滚动）派发完再按 Enter，
  // 否则会被迟到的 scroll 事件关掉（这是整扫里一次间歇失败的根因：harness 竞态，不是产品缺陷）。
  const settle = () => bottom.page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(r, 50)))));
  await settle();
  await tip.focus(); await settle();
  await bottom.page.keyboard.press('Enter');
  await expect(bottom.page.locator('.infotip-pop:visible')).toHaveCount(1);
  const pop = await bottom.page.locator('.infotip-pop:visible').boundingBox(), anchor = await tip.boundingBox();
  assert(pop.y < anchor.y && pop.y >= 8 && pop.y + pop.height <= 592 && pop.x >= 8 && pop.x + pop.width <= 792);
  await shot(bottom.page, 'pi-bottom-field-tip-800x600');
  await bottom.page.keyboard.press('Escape'); await expect(tip).toBeFocused();
  pass('X5 窄栏视口底部字段 InfoTip 自动向上完整显示/Escape 回焦');
  await section(bottom.page, 'compaction');
  await bottom.page.locator('.pac-actions-inline').scrollIntoViewIfNeeded();
  await expect(bottom.page.locator('.pac-caption.is-consequence')).toHaveText('窗口不足或调用失败时取消本次压缩，历史保留');
  await shot(bottom.page, 'pi-compaction-consequence-800x600');
  pass('X3 800px 下后果句完整自然换行，没有单行裁切');
  await bottom.ctx.close();
}
if (batch === 'interactions') {
  for (const view of ['presets-anthropic', 'presets-openai', 'opencode-presets']) {
    const { page:p, ctx } = await open(view);
    const row = p.locator(view.startsWith('presets-') ? '.preset-open' : '.oc-open').first();
    await row.focus(); await p.keyboard.press('Enter');
    await expect(p.locator('.dialog-overlay')).toBeVisible();
    await expect.poll(() => p.locator('.dialog-overlay').evaluate(e => getComputedStyle(e).opacity)).toBe('1');
    await expect.poll(() => p.locator('.dialog-container').evaluate(e => getComputedStyle(e).opacity)).toBe('1');
    await shot(p, `${view}-keyboard-dialog`);
    await p.locator('.dialog-close').click();
    await expect(p.locator('.dialog-overlay')).toHaveCount(0);
    if (view.startsWith('presets-')) {
      await row.focus(); await p.keyboard.press('Tab');
      assert(await p.evaluate(() => document.activeElement.classList.contains('preset-delete')));
      await p.keyboard.press('Enter');
      await expect(p.locator('.dialog-title')).toHaveText('删除预设');
      await p.getByRole('button', { name:'取消', exact:true }).click();
    } else {
      await p.locator('.oc-search-input').fill('does-not-exist');
      await expect(p.getByText('无匹配预设', { exact:true })).toBeVisible();
      await p.getByRole('button', { name:'JSON', exact:true }).click();
      await expect(p.locator('pre.oc-json')).toBeVisible();
      await expect(p.locator('.oc-view textarea')).toHaveCount(0);
    }
    pass('X6 ' + view + ' 键盘打开真实弹窗/独立删除焦点或只读 JSON'); await ctx.close();
  }
  const { page:p, ctx } = await open('pi-agents', 'full,skin', 1280, 800, '', ctx => ctx.route('**/skins/demo.svg', r => r.fulfill({ path:path.join(HERE, 'skin-demo.svg'), contentType:'image/svg+xml' })));
  await ready(p); await section(p, 'compaction');
  await expect(p.locator('html')).toHaveAttribute('data-skin', 'on');
  const tip = p.getByRole('button', { name:'说明：上下文压缩', exact:true });
  await tip.focus(); await p.waitForTimeout(150); await p.keyboard.press('Enter');
  await expect(p.locator('.infotip-pop:visible')).toHaveCount(1);
  assert.equal(await p.locator('.infotip-pop:visible').evaluate(e => getComputedStyle(e).backgroundColor), 'rgb(255, 255, 255)');
  await shot(p, 'skin-tip-1280x800');
  pass('X7 真实 skin store 路径打开；InfoTip 不透明'); await ctx.close();
}
console.log(`BATCH ${batch}: ${checks} checks passed; findings: ${findings.length}; page errors: ${errors.length}`);
if (findings.length) console.log('FINDINGS:\n' + findings.map(f => ' - ' + f).join('\n'));
if (errors.length) { console.log([...new Set(errors)].join('\n')); process.exitCode = 1; }
} finally { await browser.close(); await browserSb.close(); }
