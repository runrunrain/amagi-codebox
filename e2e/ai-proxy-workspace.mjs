// Run against: node frontend/node_modules/vite/bin/vite.js --config e2e/ai-proxy.vite.config.mjs
import { chromium, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { installFixture, fixtureURL } from './ai-proxy-fixture.mjs';

const output = process.env.AI_PROXY_SCREENSHOTS || '/tmp/amagi-proxy-redesign';
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: process.env.PLAYWRIGHT_CHANNEL || 'chrome' });
let checks = 0;
async function check(name, run) { await run(); checks++; console.log(`PASS ${name}`); }
try {
  const context = await browser.newContext({ viewport: { width: 1360, height: 1000 }, reducedMotion: 'reduce' });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  const mock = await installFixture(page);
  const tab = name => page.getByRole('tab', { name, exact: true });
  const calls = method => mock.calls.filter(call => call.method === method);
  await page.goto(fixtureURL);
  await page.getByRole('button', { name: '编辑规则 Claude', exact: true }).waitFor();

  await check('clean state, compact rules, and keyboard tab navigation', async () => {
    await expect(page.getByRole('button', { name: '保存配置', exact: true })).toBeDisabled();
    await expect(page.locator('.rule-card')).toHaveCount(4);
    await expect(page.locator('.rule-editor:visible')).toHaveCount(0);
    await page.screenshot({ path: `${output}/rules.png` });
    await tab('分流规则').focus(); await page.keyboard.press('ArrowRight');
    await expect(tab('连接日志')).toBeFocused();
    await page.keyboard.press('End'); await expect(tab('节点与设置')).toBeFocused();
    await page.keyboard.press('Home'); await expect(tab('分流规则')).toBeFocused();
  });
  await check('editing preserves drafts across tabs and KeepAlive navigation', async () => {
    await page.getByRole('button', { name: '编辑规则 Claude', exact: true }).click();
    await page.getByLabel('规则 1 名称', { exact: true }).fill('Claude 工作');
    await page.getByLabel('Claude 工作 出口节点', { exact: true }).selectOption('sub:sg');
    await tab('节点与设置').click();
    await page.getByLabel('独立 DNS', { exact: false }).fill('1.1.1.1');
    await page.getByRole('link', { name: '其他页面', exact: true }).click();
    await page.getByRole('link', { name: 'AI 代理分流', exact: true }).click();
    await expect(page.getByLabel('独立 DNS', { exact: false })).toHaveValue('1.1.1.1');
    await tab('分流规则').click();
    await expect(page.getByLabel('规则 1 名称', { exact: true })).toHaveValue('Claude 工作');
    await expect(page.getByLabel('Claude 工作 出口节点', { exact: true })).toHaveValue('sub:sg');
    await page.screenshot({ path: `${output}/rule-edit.png` });
    await page.getByRole('button', { name: '保存配置', exact: true }).click();
    await expect(page.getByText('配置已保存，等待应用', { exact: true })).toBeVisible();
    assert.equal(mock.state.settings.routing.dns_server, '1.1.1.1');
    await page.getByRole('button', { name: '应用更改', exact: true }).click();
    await expect(page.locator('.pending-banner')).toHaveCount(0);
    await page.getByRole('button', { name: '完成编辑', exact: true }).click();
    await expect(page.getByRole('button', { name: '编辑规则 Claude 工作', exact: true })).toBeFocused();
  });
  await check('add manual rule, validate missing values, and apply changes', async () => {
    await page.getByRole('button', { name: '添加规则', exact: true }).click();
    await page.getByRole('button', { name: '手动添加域名', exact: true }).click();
    await expect(page.getByLabel('规则 5 名称', { exact: true })).toBeFocused();
    await expect(page.getByRole('button', { name: '应用更改', exact: true })).toBeDisabled();
    await page.getByLabel('规则 5 名称', { exact: true }).fill('测试服务');
    await page.locator('.rule-editor textarea').fill('API.TEST.EXAMPLE\napi.test.example\nsecond.test.example');
    await page.getByLabel('测试服务 出口节点', { exact: true }).selectOption('@direct');
    await page.getByRole('button', { name: '应用更改', exact: true }).click();
    await expect(page.locator('.pending-banner')).toHaveCount(0);
    assert.deepEqual(mock.state.settings.routing.rules.at(-1).domains, ['api.test.example', 'second.test.example']);
    await page.getByRole('button', { name: '移除规则 5', exact: true }).click();
  });
  await check('provider linking, readonly domains, and keyboard ordering', async () => {
    await page.getByRole('button', { name: '添加规则', exact: true }).click();
    await page.getByLabel('添加已配置的服务提供商', { exact: true }).selectOption('team');
    await page.getByRole('button', { name: '关联并添加', exact: true }).click();
    await expect(page.locator('.rule-editor textarea')).toHaveAttribute('readonly', '');
    await expect(page.locator('.rule-editor textarea')).toHaveValue('api.team.example');
    await page.getByLabel('团队中转 出口节点', { exact: true }).selectOption('sub:jp');
    const firstID = await page.locator('.rule-card').first().getAttribute('data-rule-id');
    await page.getByRole('button', { name: '调整规则 1 顺序', exact: true }).focus();
    await page.keyboard.press('ArrowDown');
    assert.equal(await page.locator('.rule-card').nth(1).getAttribute('data-rule-id'), firstID);
    await page.getByRole('button', { name: '移除规则 2', exact: true }).click();
  });
  await check('pointer sorting retains the selected rule identity', async () => {
    const original = await page.locator('.rule-card').first().getAttribute('data-rule-id');
    const handle = await page.getByRole('button', { name: '调整规则 1 顺序', exact: true }).boundingBox();
    const target = await page.locator('.rule-card').nth(1).boundingBox();
    await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
    await page.mouse.down();
    await page.mouse.move(target.x + 100, target.y + target.height / 2, { steps: 10 });
    await page.mouse.up();
    assert.equal(await page.locator('.rule-card').nth(1).getAttribute('data-rule-id'), original);
  });
  await check('settings, import submission, and start/stop actions', async () => {
    await tab('节点与设置').click();
    await page.getByLabel('出口方式', { exact: true }).selectOption('interface');
    await page.getByLabel('物理出口网卡', { exact: true }).selectOption('en0');
    await page.locator('summary').filter({ hasText: '手动导入订阅或节点' }).click();
    await page.getByLabel('名称', { exact: true }).fill('备用节点');
    await page.getByLabel('或粘贴节点内容', { exact: true }).fill('socks://127.0.0.1:1080');
    const before = calls('ImportAIProxySubscription').length;
    await page.getByRole('button', { name: '导入节点', exact: true }).click();
    await expect(page.getByText('备用节点', { exact: true })).toBeVisible();
    assert.equal(calls('ImportAIProxySubscription').length, before + 1);
    await page.getByRole('button', { name: '应用更改', exact: true }).click();
    await expect(page.locator('.pending-banner')).toHaveCount(0);
    assert.equal(mock.state.settings.routing.bind_interface, 'en0');
    await page.getByRole('button', { name: '停止分流', exact: true }).click();
    await expect(page.getByRole('button', { name: '启用分流', exact: true })).toBeEnabled();
    await page.getByRole('button', { name: '启用分流', exact: true }).click();
    await expect(page.getByRole('button', { name: '停止分流', exact: true })).toBeVisible();
  });
  await check('traffic grouping, anomaly highlight, and detail pagination', async () => {
    await tab('连接日志').click();
    await expect(page.locator('.results-count strong')).toHaveText('105');
    await expect(page.locator('tr.mismatch')).toHaveCount(1);
    await expect(page.locator('tbody tr').first()).toHaveClass('mismatch');
    await page.screenshot({ path: `${output}/traffic.png` });
    await page.getByRole('button', { name: '连接明细', exact: true }).click();
    await expect(page.locator('tbody tr')).toHaveCount(100);
    await page.getByRole('button', { name: '下一页', exact: true }).click();
    await expect(page.locator('.pagination')).toContainText('101–105 / 105');
    await expect(page.locator('tbody tr')).toHaveCount(5);
    await page.getByRole('button', { name: '上一页', exact: true }).click();
    await page.getByRole('button', { name: '域名汇总', exact: true }).click();
  });
  await check('quick verdict, group drilldown, filter chips, and empty results', async () => {
    await page.getByRole('button', { name: '出口不符', exact: true }).click();
    await expect(page.locator('.results-count strong')).toHaveText('1');
    await page.locator('tr.mismatch .exit-link').click();
    await expect(page.getByRole('button', { name: '移除筛选 实际出口', exact: true })).toBeVisible();
    await page.getByRole('button', { name: '清空', exact: true }).click();
    await expect(page.locator('.results-count strong')).toHaveText('105');
    await page.getByLabel('搜索连接', { exact: true }).fill('nonexistent');
    await page.getByLabel('搜索连接', { exact: true }).press('Enter');
    await expect(page.getByText('没有符合条件的连接', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: '移除筛选 关键词', exact: true }).click();
    await expect(page.locator('.results-count strong')).toHaveText('105');
  });
  await check('date validation, applied filters, and retained filters on tab switch', async () => {
    await page.getByRole('button', { name: '更多筛选', exact: true }).click();
    await page.getByLabel('开始时间（本地）', { exact: true }).fill('2026-11-12T00:00');
    await page.getByLabel('结束时间（本地）', { exact: true }).fill('2026-11-10T00:00');
    await page.getByRole('button', { name: '查询', exact: true }).click();
    await expect(page.getByText('开始时间不能晚于结束时间。', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: '清空筛选', exact: true }).click();
    await page.getByLabel('提供商', { exact: true }).fill('OpenAI');
    await page.getByRole('button', { name: '查询', exact: true }).click();
    await expect(page.locator('.results-count strong')).toHaveText('35');
    await tab('分流规则').click(); await tab('连接日志').click();
    await expect(page.getByLabel('提供商', { exact: true })).toHaveValue('OpenAI');
    await expect(page.locator('.results-count strong')).toHaveText('35');
    await page.getByRole('button', { name: '清空筛选', exact: true }).click();
    await page.getByRole('button', { name: '更多筛选', exact: true }).click();
  });
  await check('late traffic replies cannot overwrite a newer filter', async () => {
    await expect(page.getByRole('button', { name: '刷新', exact: true })).toBeEnabled();
    mock.trafficDelay = 500;
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    await page.getByRole('button', { name: '出口不符', exact: true }).click();
    await page.getByRole('button', { name: '无法确认', exact: true }).click();
    await expect(page.locator('.results-count strong')).toHaveText('1');
    await expect(page.locator('tbody tr')).toHaveClass('unknown');
    await page.getByRole('button', { name: '全部连接', exact: true }).click();
  });
  await check('hidden log panel pauses polling', async () => {
    await expect(page.getByRole('button', { name: '刷新', exact: true })).toBeEnabled();
    await tab('分流规则').click();
    const before = calls('GetAIProxyTraffic').length;
    await page.waitForTimeout(3300);
    assert.equal(calls('GetAIProxyTraffic').length, before);
  });
  await check('incomplete log evidence stays visible as a warning', async () => {
    mock.trafficError = true;
    await tab('连接日志').click();
    await expect(page.getByRole('alert')).toContainText('记录可能不完整');
    await expect(page.locator('tr.mismatch')).toHaveCount(1);
    mock.trafficError = false;
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    await expect(page.getByRole('alert')).toHaveCount(0);
  });
  await check('long exit identifiers remain available without dominating the summary', async () => {
    const fullExit = `香港 [v2rayn-${'abcdef'.repeat(4)}:${'123456'.repeat(4)}]`;
    mock.records[0].actual = fullExit;
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    await expect(page.locator('tr.mismatch .exit-link')).toHaveText('香港');
    await expect(page.locator('tr.mismatch .exit-link')).toHaveAttribute('title', fullExit);
    await page.locator('tr.mismatch .exit-link').click();
    await expect(page.locator('.results-count strong')).toHaveText('1');
    assert.equal(calls('GetAIProxyTraffic').at(-1).args[0].exit, fullExit);
    await page.getByRole('button', { name: '连接明细', exact: true }).click();
    await expect(page.locator('.actual-node')).toContainText(fullExit);
    await page.getByRole('button', { name: '清空', exact: true }).click();
    await page.getByRole('button', { name: '域名汇总', exact: true }).click();
  });
  for (const width of [1360, 1024, 720, 390]) {
    await page.setViewportSize({ width, height: width < 600 ? 844 : 1000 });
    for (const [name, file] of [['分流规则', 'rules'], ['连接日志', 'traffic'], ['节点与设置', 'settings']]) {
      await check(`${width}px ${name}: layout and accessibility`, async () => {
        await tab(name).click();
        await page.locator('.proxy-content').evaluate(el => { el.scrollTop = 0; });
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth || document.querySelector('.proxy-content').scrollWidth > document.querySelector('.proxy-content').clientWidth);
        assert.equal(overflow, false, `${name} overflows at ${width}px`);
        if (width === 1360 || width === 390) {
          const audit = await new AxeBuilder({ page }).include('.ai-proxy-page').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
          assert.deepEqual(audit.violations.map(v => ({ id: v.id, nodes: v.nodes.map(n => n.target) })), []);
        }
        await page.screenshot({ path: `${output}/${file}-${width}.png` });
      });
    }
  }
  await check('no browser runtime errors', async () => assert.deepEqual(errors, []));
  console.log(`${checks} checks passed. Screenshots: ${output}`);
} finally { await browser.close(); }
