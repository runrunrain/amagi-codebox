// First start the isolated TestAIProxyBrowserBridge, then pass its printed URL as AI_PROXY_BRIDGE.
import { chromium, expect } from '@playwright/test';
import assert from 'node:assert/strict';

const bridge = process.env.AI_PROXY_BRIDGE;
if (!bridge?.startsWith('http://127.0.0.1:')) throw new Error('AI_PROXY_BRIDGE must identify the isolated Go test bridge');
const rpc = async (method, args = []) => {
  const response = await fetch(`${bridge}/rpc`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ method, args }) });
  if (!response.ok) throw new Error(await response.text());
  return response.json();
};
const state = await rpc('GetAIProxyState');
assert.equal(state.groups[0]?.name, 'v2rayN 测试订阅', 'This script requires the synthetic Go fixture');
const browser = await chromium.launch({ headless: true, channel: process.env.PLAYWRIGHT_CHANNEL || 'chrome' });
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' });
  const page = await context.newPage();
  const errors = []; page.on('pageerror', e => errors.push(String(e)));
  await page.goto(`http://127.0.0.1:5188/e2e/ai-proxy.html?bridge=${encodeURIComponent(bridge)}`);
  await page.getByLabel('Claude 出口节点', { exact: true }).selectOption(state.groups[0].nodes[0].id);
  await page.getByLabel('OpenAI 出口节点', { exact: true }).selectOption(state.groups[0].nodes[1].id);
  await page.getByRole('button', { name: '编辑规则 Claude', exact: true }).click();
  await page.locator('#claude-provider').selectOption('Claude 团队');
  await expect(page.locator('#claude-domains')).toHaveValue('claude.gateway.example');
  await page.getByRole('button', { name: '编辑规则 OpenAI', exact: true }).click();
  await page.locator('#openai-provider').selectOption('OpenAI 团队');
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  await expect(page.getByRole('button', { name: '启用分流', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: '启用分流', exact: true }).click();
  await expect(page.getByRole('button', { name: '停止分流', exact: true })).toBeVisible();
  assert.equal((await rpc('GetAIProxyState')).status.running, true);
  console.log('PASS real Go save, provider linking, and sing-box start');

  await page.getByLabel('规则 2 名称', { exact: true }).fill('OpenAI 草稿');
  await fetch(`${bridge}/provider-update`, { method: 'POST' });
  await expect(page.locator('#openai-domains')).toHaveValue('updated.gateway.example', { timeout: 10000 });
  await expect(page.getByLabel('规则 2 名称', { exact: true })).toHaveValue('OpenAI 草稿');
  await expect(page.getByLabel('OpenAI 草稿 出口节点', { exact: true })).toHaveValue(state.groups[0].nodes[1].id);
  await page.getByRole('tab', { name: '节点与设置', exact: true }).click();
  await page.getByLabel('出口方式', { exact: true }).selectOption('system');
  await page.getByRole('button', { name: '应用更改', exact: true }).click();
  await expect(page.locator('.pending-banner')).toHaveCount(0);
  const saved = (await rpc('GetAIProxyState')).settings;
  assert.equal(saved.routing.egress_mode, 'system');
  assert.equal(saved.routing.rules[1].name, 'OpenAI 草稿');
  assert.deepEqual(saved.routing.rules[1].domains, ['updated.gateway.example']);
  console.log('PASS live provider updates retain edits; network changes apply from settings');

  await page.getByRole('tab', { name: '连接日志', exact: true }).click();
  await expect(page.getByText('等待第一条连接', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '停止分流', exact: true }).click();
  await expect(page.getByRole('button', { name: '启用分流', exact: true })).toBeVisible();
  assert.equal((await rpc('GetAIProxyState')).status.running, false);
  assert.deepEqual(errors, []);
  console.log('PASS real traffic empty state, stop, and no runtime errors');
} finally { await browser.close(); await fetch(`${bridge}/finish`, { method: 'POST' }); }
