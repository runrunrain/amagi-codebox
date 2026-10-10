import { expect, test, type Page } from '@playwright/test';

const url = 'http://127.0.0.1:5188/e2e/pi-amagi-config.html';
const fixture = {
  profile: 'tiered',
  agents: { luban: { model: 'amagi-glm/glm-5.3:max' } },
  compaction: { model: 'ollama/qwen:32b:high' },
  mcp: { leader: [], default: ['search'], agents: { luban: ['lsp'] } },
  concurrency: { default: 4 },
  permission: { deny: ['private'] },
  future: { enabled: true },
};
const card = (page: Page) => page.locator('.config-cat-card').filter({ has: page.getByRole('button', { name: /上下文压缩/ }) });
async function open(page: Page, config: unknown = fixture, query = '') {
  await page.goto(url);
  await page.evaluate(config => { localStorage.setItem('pi-config', JSON.stringify(config)); localStorage.removeItem('save-count'); }, config);
  await page.goto(url + query);
  await expect(page.getByRole('button', { name: '保存配置', exact: true })).toBeVisible();
}
async function saved(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem('pi-config') || '{}'));
}
async function select(page: Page, index: number, option: string | RegExp) {
  await card(page).locator('.dropdown-trigger').nth(index).click();
  await page.getByRole('option', { name: option, exact: typeof option === 'string' }).click();
}
async function save(page: Page) {
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  await expect(page.locator('#test-notifications')).toContainText('配置已保存');
}

test('copies luban, persists the exact spec and preserves all unrelated fields on reload', async ({ page }) => {
  await open(page);
  await expect(card(page).locator('.mss-spec')).toHaveText('ollama/qwen:32b:high');
  await card(page).getByRole('button', { name: '复制 luban 当前模型' }).click();
  await save(page);
  expect(await saved(page)).toEqual({ ...fixture, compaction: { model: 'amagi-glm/glm-5.3:max' } });
  await page.reload();
  await expect(card(page).locator('.mss-spec')).toHaveText('amagi-glm/glm-5.3:max');
  await expect(card(page).getByRole('button', { name: '统一模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await page.screenshot({ path: test.info().outputPath('pi-compaction.png'), fullPage: true });
});

test('distinguishes null and deletion, retains an empty custom editor and blocks incomplete saves', async ({ page }) => {
  await open(page, { agents: {}, concurrency: { default: 4 } });
  await expect(card(page).getByRole('button', { name: '未指定', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await card(page).getByRole('button', { name: '统一模型', exact: true }).click();
  await expect(card(page).getByRole('button', { name: '统一模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(card(page).getByRole('alert')).toContainText('完整');
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem('save-count'))).toBeNull();
  await select(page, 0, /^amagi-glm/);
  await select(page, 2, 'max');
  await save(page);
  expect((await saved(page)).compaction.model).toBe('amagi-glm/glm-5.3:max');
  await card(page).getByRole('button', { name: 'pi 原生模型', exact: true }).click();
  await save(page);
  expect((await saved(page)).compaction).toEqual({ model: null });
  await page.reload();
  await expect(card(page).getByRole('button', { name: 'pi 原生模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await card(page).getByRole('button', { name: '未指定', exact: true }).click();
  await save(page);
  expect(await saved(page)).not.toHaveProperty('compaction');
  await page.reload();
  await expect(card(page).getByRole('button', { name: '未指定', exact: true })).toHaveAttribute('aria-pressed', 'true');
});

test('handles native colon IDs and resets an unsupported level when changing models', async ({ page }) => {
  await open(page, { ...fixture, compaction: { model: fixture.agents.luban.model } });
  await select(page, 0, /^plain/);
  await expect(card(page).locator('.mss-spec')).toHaveText('plain/summary');
  await select(page, 0, /^ollama/);
  await expect(card(page).locator('.mss-spec')).toHaveText('ollama/qwen:32b');
  await select(page, 2, 'high');
  await save(page);
  expect((await saved(page)).compaction.model).toBe('ollama/qwen:32b:high');
  await select(page, 2, '默认（不指定档位）');
  await save(page);
  expect((await saved(page)).compaction.model).toBe('ollama/qwen:32b');
});

test('JSON edits synchronize back to visual mode; invalid syntax is never overwritten', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'JSON', exact: true }).click();
  await page.locator('.pac-json-editor').fill(JSON.stringify({ ...fixture, compaction: { model: null } }));
  await page.getByRole('button', { name: '可视化', exact: true }).click();
  await expect(card(page).getByRole('button', { name: 'pi 原生模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: 'JSON', exact: true }).click();
  const broken = '{"compaction":{"model":"retain this source"';
  await page.locator('.pac-json-editor').fill(broken);
  await page.getByRole('button', { name: '可视化', exact: true }).click();
  await expect(page.locator('.pac-json-editor')).toHaveValue(broken);
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem('save-count'))).toBeNull();
  expect(await saved(page)).toEqual(fixture);
});

test('invalid root opens intact in JSON mode; malformed compaction must be repaired explicitly', async ({ page }) => {
  await open(page, ['preserve source']);
  await expect(page.locator('.pac-json-editor')).toHaveValue('["preserve source"]');
  await page.getByRole('button', { name: '可视化', exact: true }).click();
  await expect(page.locator('.pac-json-editor')).toHaveValue('["preserve source"]');
  await page.locator('.pac-json-editor').fill(JSON.stringify({ ...fixture, compaction: { model: 42 } }));
  await page.getByRole('button', { name: '可视化', exact: true }).click();
  await expect(card(page).getByRole('alert')).toContainText('compaction.model');
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem('save-count'))).toBeNull();
  await card(page).getByRole('button', { name: '复制 luban 当前模型' }).click();
  await save(page);
  expect((await saved(page)).compaction.model).toBe(fixture.agents.luban.model);
});

test('catalogue failure still allows a complete manual model spec', async ({ page }) => {
  await open(page, { agents: {}, concurrency: { default: 4 } }, '?catalog-error=1');
  await expect(page.locator('.pac-catalog-warn')).toContainText('fixture catalog unavailable');
  await card(page).getByRole('button', { name: '统一模型', exact: true }).click();
  await card(page).locator('input').fill('dynamic/vendor/model:high');
  await save(page);
  expect((await saved(page)).compaction.model).toBe('dynamic/vendor/model:high');
});
