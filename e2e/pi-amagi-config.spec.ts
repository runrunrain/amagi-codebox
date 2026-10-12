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
const compaction = (page: Page) => page.locator('.editor-section[data-section="compaction"]');
async function showCompaction(page: Page) {
  await page.getByRole('navigation', { name: '配置分区', exact: true }).getByRole('button', { name: '上下文压缩', exact: true }).click();
  await expect(compaction(page)).toBeVisible();
  await expect(page.getByRole('region', { name: '上下文压缩', exact: true })).toBeVisible();
  await expect(compaction(page).getByRole('heading', { name: '上下文压缩', exact: true, level: 3 })).toBeVisible();
}
async function open(page: Page, config: unknown = fixture, query = '', options: { section?: 'compaction' | null } = {}) {
  await page.goto(url);
  await page.evaluate(config => { localStorage.setItem('pi-config', JSON.stringify(config)); localStorage.removeItem('save-count'); }, config);
  await page.goto(url + query);
  await expect(page.getByRole('button', { name: '保存配置', exact: true })).toBeVisible();
  if (options.section !== null) await showCompaction(page);
}
async function saved(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem('pi-config') || '{}'));
}
async function select(page: Page, index: number, option: string | RegExp) {
  await expect(compaction(page).locator('.mss-row > .mss-dd')).toHaveCount(3);
  if (index === 2) await expect(compaction(page).locator('.dropdown-trigger')).toHaveCount(3);
  await compaction(page).locator('.dropdown-trigger').nth(index).click();
  await page.getByRole('option', { name: option, exact: typeof option === 'string' }).click();
}
async function save(page: Page) {
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  await expect(page.locator('#test-notifications')).toContainText('配置已保存');
}

test('copies luban, persists the exact spec and preserves all unrelated fields on reload', async ({ page }) => {
  await open(page);
  await expect(compaction(page).locator('.mss-spec')).toHaveText('ollama/qwen:32b:high');
  await compaction(page).getByRole('button', { name: '复制 luban 当前模型' }).click();
  await save(page);
  expect(await saved(page)).toEqual({ ...fixture, compaction: { model: 'amagi-glm/glm-5.3:max' } });
  await page.reload();
  await showCompaction(page);
  await expect(compaction(page).locator('.mss-spec')).toHaveText('amagi-glm/glm-5.3:max');
  await expect(compaction(page).getByRole('button', { name: '统一模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await page.screenshot({ path: test.info().outputPath('pi-compaction.png'), fullPage: true });
});

test('distinguishes null and deletion, retains an empty custom editor and blocks incomplete saves', async ({ page }) => {
  await open(page, { agents: {}, concurrency: { default: 4 } });
  await expect(compaction(page).getByRole('button', { name: '未指定', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await compaction(page).getByRole('button', { name: '统一模型', exact: true }).click();
  await expect(compaction(page).getByRole('button', { name: '统一模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(compaction(page).getByRole('alert')).toContainText('完整');
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem('save-count'))).toBeNull();
  await select(page, 0, /^amagi-glm/);
  await select(page, 2, 'max');
  await save(page);
  expect((await saved(page)).compaction.model).toBe('amagi-glm/glm-5.3:max');
  await compaction(page).getByRole('button', { name: 'pi 原生模型', exact: true }).click();
  await save(page);
  expect((await saved(page)).compaction).toEqual({ model: null });
  await page.reload();
  await showCompaction(page);
  await expect(compaction(page).getByRole('button', { name: 'pi 原生模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await compaction(page).getByRole('button', { name: '未指定', exact: true }).click();
  await save(page);
  expect(await saved(page)).not.toHaveProperty('compaction');
  await page.reload();
  await showCompaction(page);
  await expect(compaction(page).getByRole('button', { name: '未指定', exact: true })).toHaveAttribute('aria-pressed', 'true');
});

test('handles native colon IDs and resets an unsupported level when changing models', async ({ page }) => {
  await open(page, { ...fixture, compaction: { model: fixture.agents.luban.model } });
  await select(page, 0, /^plain/);
  await expect(compaction(page).locator('.mss-spec')).toHaveText('plain/summary');
  await select(page, 0, /^ollama/);
  await expect(compaction(page).locator('.mss-spec')).toHaveText('ollama/qwen:32b');
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
  await expect(compaction(page)).toBeVisible();
  await expect(compaction(page).getByRole('button', { name: 'pi 原生模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
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
  await open(page, ['preserve source'], '', { section: null });
  await expect(page.locator('.pac-json-editor')).toHaveValue('["preserve source"]');
  await page.getByRole('button', { name: '可视化', exact: true }).click();
  await expect(page.locator('.pac-json-editor')).toHaveValue('["preserve source"]');
  await page.locator('.pac-json-editor').fill(JSON.stringify({ ...fixture, compaction: { model: 42 } }));
  await page.getByRole('button', { name: '可视化', exact: true }).click();
  await showCompaction(page);
  await expect(compaction(page).getByRole('alert')).toContainText('compaction.model');
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem('save-count'))).toBeNull();
  await compaction(page).getByRole('button', { name: '复制 luban 当前模型' }).click();
  await save(page);
  expect((await saved(page)).compaction.model).toBe(fixture.agents.luban.model);
});

test('catalogue failure still allows a complete manual model spec', async ({ page }) => {
  await open(page, { agents: {}, concurrency: { default: 4 } }, '?catalog-error=1');
  await expect(page.locator('.pac-catalog-warn')).toContainText('fixture catalog unavailable');
  await compaction(page).getByRole('button', { name: '统一模型', exact: true }).click();
  await expect(compaction(page).locator('input')).toHaveCount(1);
  await compaction(page).locator('input').fill('dynamic/vendor/model:high');
  await save(page);
  expect((await saved(page)).compaction.model).toBe('dynamic/vendor/model:high');
});

test('single section preserves drafts, hidden errors and roving keyboard navigation', async ({ page }) => {
  await open(page, { agents: {}, concurrency: { default: 4 } }, '', { section: null });
  const nav = page.getByRole('navigation', { name: '配置分区', exact: true });
  await expect(page.getByRole('region', { name: '角色模型', exact: true })).toBeVisible();
  await showCompaction(page);
  await compaction(page).getByRole('button', { name: '统一模型', exact: true }).click();
  await nav.getByRole('button', { name: '并发限制', exact: true }).click();
  const concurrencyInput = page.locator('.editor-section[data-section="concurrency"] input[type="number"]').first();
  await expect(concurrencyInput).toBeVisible();
  await expect(page.getByRole('button', { name: '上下文压缩待修正', exact: true })).toBeVisible();
  await showCompaction(page);
  await expect(compaction(page).getByRole('button', { name: '统一模型', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(compaction(page).getByRole('alert')).toContainText('完整');
  await page.getByRole('button', { name: '保存配置', exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem('save-count'))).toBeNull();
  await expect(page.locator('.editor-section:visible')).toHaveCount(1);
  // Hidden visited inputs cannot enter the tab sequence (input type=number → spinbutton, not textbox).
  await expect(concurrencyInput).toBeHidden();
  await expect(page.getByRole('spinbutton', { name: '留空 = 内置默认 4' })).toHaveCount(0);
  const current = nav.getByRole('button', { name: '上下文压缩', exact: true });
  await current.focus();
  await current.press('ArrowDown');
  await expect(nav.getByRole('button', { name: 'MCP 路由', exact: true })).toBeFocused();
  await expect(compaction(page)).toBeVisible(); // Arrow keys do not activate.
  await page.keyboard.press('End');
  await expect(nav.getByRole('button', { name: '并发限制', exact: true })).toBeFocused();
  await page.keyboard.press('Home');
  await expect(nav.getByRole('button', { name: '角色模型', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowUp');
  await expect(nav.getByRole('button', { name: '角色模型', exact: true })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('region', { name: '角色模型', exact: true })).toBeVisible();
  await expect(nav.locator('button[tabindex="0"]')).toHaveCount(1);
  await page.keyboard.press('End');
  await page.keyboard.press('Space');
  await expect(page.getByRole('region', { name: '并发限制', exact: true })).toBeVisible();
  await page.keyboard.press('Tab');
  expect(await page.evaluate(() => !!document.activeElement?.closest('.editor-detail'))).toBe(true);
});
