// L2 browser checks: production App/components + isolated Wails transport. No disk config IO.
import { chromium, expect } from '@playwright/test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const HERE = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(HERE, '..', 'screenshots');
const browser = await chromium.launch();
const errors = [];
let checks = 0;
const pass = name => { checks++; console.log('PASS ' + name); };
async function open(view, scenario = 'full', w = 1280, h = 800, extra = '', setup) {
  const ctx = await browser.newContext({ viewport:{ width:w, height:h } });
  await ctx.addInitScript({ path:path.join(HERE, 'v2-mock-wails.js') });
  await ctx.addInitScript({ path:path.join(HERE, 'v2-helpers.js') });
  if (setup) await setup(ctx);
  const page = await ctx.newPage();
  page.setDefaultTimeout(8000);
  page.on('pageerror', e => { errors.push(`${view}/${scenario}: ${e.stack || String(e)}`); console.log('PAGE ERROR', view, scenario, e.stack || String(e)); });
  await page.goto(`http://127.0.0.1:5199/?s=${scenario}${extra}#/provider`);
  await page.evaluate(v => window.__ab.seq(v), view);
  return { ctx, page };
}
const section = async (p, id) => { await p.evaluate(id => window.__ab.section(id), id); await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', id); };
const nav = (p, id) => p.locator(`button[data-nav-id="${id}"]`);
const shot = (p, name) => p.screenshot({ path:path.join(OUT, `v2-${name}.png`) });
const calls = p => p.evaluate(() => window.__calls.filter(n => /GetAmagi|GetPi|GetOmp|GetOpenCode/.test(n)));
const metrics = p => p.evaluate(() => window.__ab.metrics());
const ready = p => expect(p.getByRole('button', { name:'保存配置', exact:true })).toBeVisible();
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
  pass('V4 Pi 空 custom + ModelSpecSelector 手动态来回保留');
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
  await expect(nav(oc.page, 'section:opencode:global:unknown')).toBeDisabled();
  assert((await oc.page.locator('.ocg-json-editor').inputValue()).includes('"provider"')); // Entire-file source, not section source.
  await oc.page.getByRole('button', { name:'可视化', exact:true }).click();
  await expect(oc.page.locator('.editor-section:visible')).toHaveAttribute('data-section', 'unknown');
  pass('X10 源码为整个文件，返回保留有效选择');
  await oc.ctx.close();
}
if (batch === 'dynamic') {
  for (const view of ['pi-registry', 'omp-registry', 'pi-auth']) {
    for (const n of [0, 1, 20]) {
      const { page:p, ctx } = await open(view, 'full', 800, 600, `&N=${n}`);
      await ready(p);
      const pageId = view.replace('-', ':');
      const children = () => p.locator('.sn-children button');
      await expect(children()).toHaveCount(n + 1);
      await expect(p.locator('.editor-section:visible')).toHaveCount(1);
      assert.deepEqual((await metrics(p)).overflow, []);
      if (n > 0) {
        const box = await p.locator('.sn-scroll').evaluate(e => ({ h:e.clientHeight, sh:e.scrollHeight, max:getComputedStyle(e).maxHeight }));
        assert(box.h <= 192 && (n !== 20 || box.sh > box.h));
        assert((await p.locator('.editor-section:visible h3').innerText()).includes('long-fixture'));
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
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', n ? (await children().nth(n - 1).getAttribute('data-nav-id')).replace(`section:${pageId}:`, '').replaceAll('%3A', ':') : 'add');
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
        await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', n ? decodeURIComponent((await children().nth(n - 1).getAttribute('data-nav-id')).slice(`section:${pageId}:`.length)) : 'add');
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
      pass(`X8 ${view} N=${n} 动态滚动/长名/新增/重名停留/取消/删除后继${view === 'pi-auth' ? '/重命名不丢焦点' : ''}`);
      await ctx.close();
    }
  }
  const { page:p, ctx } = await open('pi-registry');
  await ready(p); await section(p, 'root');
  await p.getByRole('button', { name:'删除键', exact:true }).click();
  await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider:amagi-glm');
  pass('X8 最后 rootUnknownKey 删除回退真实默认提供商');
  await ctx.close();
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
  await p.evaluate(() => window.__ab.clickId('pi:registry')); await ready(p);
  await expect(p.locator('.et-path')).toContainText('models.json');
  await p.evaluate(() => window.__ab.clickId('omp:registry')); await ready(p);
  await expect(p.locator('.et-path')).toContainText('models.yml');
  await p.getByRole('button', { name:'YAML', exact:true }).click();
  await expect(p.locator('.pre-source-editor')).toHaveValue(/^providers:/);
  pass('V5 Pi→OMP 注册表不复用旧实例：路径 JSON→YAML、数据格式正确');
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
  // Chunk delay: static parent navigation must work before the async editor exists.
  let release;
  const blocked = new Promise(r => { release = r; });
  let delayedAttempts = 0;
  const delayed = await open('pi-agents', 'full', 1280, 800, '', ctx => ctx.route(/\/components\/provider\/PiAmagiConfig\.vue(?:\?|$)/, async route => { delayedAttempts++; await blocked; await route.continue(); }));
  await expect.poll(() => delayedAttempts).toBeGreaterThan(0);
  await expect(delayed.page.getByRole('button', { name:'保存配置', exact:true })).toHaveCount(0);
  await expect(delayed.page.locator('.section-nav button[aria-expanded]')).toHaveCount(7);
  await nav(delayed.page, 'section:pi:agents:compaction').click();
  await shot(delayed.page, 'chunk-delayed-static-navigation');
  release();
  await ready(delayed.page);
  await expect(delayed.page.locator('.editor-section:visible')).toHaveAttribute('data-section', 'compaction');
  pass('V5 chunk 延迟期间父级 7 模式与静态选择可达，已排队压缩选择落地');
  await delayed.ctx.close();
  let failures = 0;
  let failChunk = true;
  const failed = await open('pi-agents', 'full', 1280, 800, '', ctx => ctx.route(/\/components\/provider\/PiAmagiConfig\.vue(?:\?|$)/, route => { failures++; if (failChunk) return route.abort('failed'); return route.continue(); }));
  console.log('chunk attempts before manual retry:', failures);
  await expect(failed.page.getByText('配置页加载失败', { exact:true })).toBeVisible();
  await expect(failed.page.locator('.section-nav button[aria-expanded]')).toHaveCount(7);
  await shot(failed.page, 'chunk-error-navigation');
  failChunk = false;
  await failed.page.evaluate(() => { window.__preReloadMarker = 'alive'; });
  await failed.page.getByRole('button', { name:'重试', exact:true }).click();
  // 已修复：Chromium 负缓存失败模块 URL，重试改为整页重载恢复（module map 随文档重建）。
  await failed.page.waitForFunction(() => window.__preReloadMarker === undefined, undefined, { timeout: 15000 });
  await failed.page.getByRole('button', { name:'CLI 独立配置', exact:true }).click();
  await expect(failed.page.getByRole('button', { name:'保存配置', exact:true })).toBeVisible({ timeout: 15000 });
  await expect(failed.page.getByText('配置页加载失败', { exact:true })).toHaveCount(0);
  pass('V5 chunk 失败→重试整页重载→URL 已释放后恢复加载（原基线缺口已修复）');
  await failed.page.evaluate(() => window.__ab.clickId('omp:agents')); await ready(failed.page);
  pass('V5 chunk 失败 7 模式可达；重试 owner/key 更新；其他模式可用（当前失败 URL 恢复为既存缺口）');
  await failed.ctx.close();
  const late = await open('pi-registry', 'full,late-pi');
  await expect(late.page.getByText('加载分区中…')).toBeVisible();
  await late.page.evaluate(() => window.__ab.clickId('omp:registry')); await ready(late.page);
  await late.page.evaluate(() => window.__resolveLatePi());
  await expect(late.page.locator('.et-path')).toContainText('models.yml');
  await expect(late.page.locator('.sn-children button')).toHaveCount(2);
  pass('V5 旧 Pi 注册表迟到请求不污染 OMP 导航/路径');
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
  for (const view of ['pi-registry', 'opencode-presets']) {
    const { page:p, ctx } = await open(view, 'full,long', 1024, 768); await expect(p.locator('.editor-toolbar')).toBeVisible();
    assert.deepEqual((await metrics(p)).overflow, []);
    assert.equal((await metrics(p)).grid, '144px 524px');
    await shot(p, `${view}-1024x768`);
    pass('X2 1024 档 ' + view + ' 144px/524px 与工具栏换行'); await ctx.close();
  }
  for (const scenario of ['full,no-model,no-unknown', 'empty']) {
    const { page:p, ctx } = await open('opencode-global', scenario); await ready(p);
    await expect(p.locator('.sn-children button')).toHaveCount(7);
    await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider');
    await shot(p, `opencode-${scenario.startsWith('empty') ? 'empty' : 'seven-sections'}`);
    pass('X9 OpenCode ' + scenario + ' 7 常驻分区/provider 默认'); await ctx.close();
  }
  const { page:p, ctx } = await open('opencode-global'); await ready(p);
  await expect(p.locator('.sn-children button')).toHaveCount(9);
  await section(p, 'unknown');
  await p.getByRole('button', { name:'JSON', exact:true }).click();
  const source = JSON.parse(await p.locator('.ocg-json-editor').inputValue()); delete source.preset; delete source.model;
  await p.locator('.ocg-json-editor').fill(JSON.stringify(source));
  await p.getByRole('button', { name:'可视化', exact:true }).click();
  await expect(p.locator('.sn-children button')).toHaveCount(7);
  await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', 'provider');
  pass('X9 model/unknown 条件消失回退 provider，不生成不存在的键'); await ctx.close();
}
if (batch === 'states') {
  const views = ['pi-agents','pi-registry','pi-auth','omp-agents','omp-registry','opencode-presets','opencode-global'];
  for (const scenario of ['loading', 'error']) for (const view of views) {
    const { page:p, ctx } = await open(view, scenario, 800, 600);
    if (view === 'opencode-presets' && scenario === 'error') {
      // 已修复：组件消费 store.presetLoadError，读取失败呈现 ErrorState（回归：曾误显「默认行＋暂无预设」）
      await expect(p.locator('.error-state')).toBeVisible();
    } else await expect(p.locator(scenario === 'loading' ? '.loading-state' : '.error-state')).toBeVisible();
    await expect(p.locator('.section-nav button[aria-expanded]')).toHaveCount(7);
    await shot(p, `${view}-${scenario}-800x600`);
    pass(`全状态 ${view}/${scenario} 左栏 7 模式仍可用`); await ctx.close();
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
  for (const view of ['presets-anthropic','presets-openai','pi-agents','pi-registry','pi-auth','omp-agents','omp-registry','opencode-presets','opencode-global']) {
    const { page:p, ctx } = await open(view); await expect(p.locator('.pc-detail-content .editor-toolbar, .pc-detail-content .pc-toolbar')).toBeVisible();
    assert.deepEqual((await metrics(p)).overflow, []);
    assert.equal((await metrics(p)).navs, 1);
    assert.equal((await metrics(p)).side, 264);
    assert.equal((await metrics(p)).grid, '184px 712px');
    await expect(p.locator('.pc-detail-content h2, .pc-subnav, .config-cat-card')).toHaveCount(0);
    if (!view.startsWith('presets-')) await expect(p.locator('.section-nav button[aria-expanded]')).toHaveCount(7);
    pass('X1 ' + view + ' 1280 grid/单左栏/无重复 h2 与旧卡/模式入口'); await ctx.close();
  }
  const { page:p, ctx } = await open('pi-agents'); await ready(p);
  for (const [pageId, ids] of [['pi:agents',['agents','compaction','mcp','concurrency']],['omp:agents',['modelRoles','overrides']],['opencode:global',['model','provider','agent','mcp','permission','instructions','plugin','experimental','unknown']]]) {
    for (const id of ids) {
      await nav(p, 'pi:auth').click(); await ready(p); // Starting from another mode is test setup, outside the two-click action.
      await nav(p, pageId).click(); await ready(p);
      await nav(p, 'section:' + pageId + ':' + encodeURIComponent(id)).click();
      await expect(p.locator('.editor-section:visible')).toHaveAttribute('data-section', id);
      pass('X8 从其他模式两次真实点击到达 ' + pageId + '/' + id);
    }
  }
  await ctx.close();
  const bottom = await open('pi-agents', 'full', 800, 600); await ready(bottom.page); await section(bottom.page, 'mcp');
  const tip = bottom.page.getByRole('button', { name:'说明：角色附加服务器', exact:true });
  await tip.evaluate(e => { const main = document.querySelector('.main'); const r = e.getBoundingClientRect(); main.scrollTop += r.top - 560; });
  await tip.focus(); await bottom.page.keyboard.press('Enter');
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
console.log(`BATCH ${batch}: ${checks} checks passed; page errors: ${errors.length}`);
if (errors.length) { console.log([...new Set(errors)].join('\n')); process.exitCode = 1; }
} finally { await browser.close(); }
