// 像素级细节取证：2× 设备像素比，按元素裁切。用法：node x-harness/zoom-shots.mjs
import { chromium } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const HERE = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(HERE, '..', 'screenshots');
const browser = await chromium.launch();
async function open(scenario, view, w = 1280, h = 800) {
  const ctx = await browser.newContext({ viewport: { width: w, height: h }, deviceScaleFactor: 2 });
  await ctx.addInitScript({ path: path.join(HERE, 'mock-wails.js') });
  await ctx.addInitScript({ path: path.join(HERE, 'helpers.js') });
  const page = await ctx.newPage();
  await page.goto(`http://127.0.0.1:5199/?s=${scenario}&n=${Date.now()}#/provider`);
  await page.evaluate((v) => window.__ab.seq(v), view);
  await page.waitForTimeout(900);
  return { ctx, page };
}
const clipOf = async (page, sel, pad = 8) => { const b = await page.locator(sel).first().boundingBox(); return { x: Math.max(0, b.x - pad), y: Math.max(0, b.y - pad), width: b.width + pad * 2, height: b.height + pad * 2 }; };
{
  const { ctx, page } = await open('full', 'presets-anthropic');
  // subnav + 工具栏区域（含通栏分隔线、选中下划线、右侧说明）
  const sub = await clipOf(page, '.pc-subnav'); const tb = await clipOf(page, '.pc-toolbar');
  await page.screenshot({ path: path.join(OUT, 'presets-anthropic-subnav-zoom.png'), clip: { x: sub.x, y: sub.y - 8, width: sub.width, height: tb.y + tb.height - sub.y + 16 } });
  // 行 hover：徽章在 hover 底色上仍可辨
  const row = page.locator('.preset-row').nth(0);
  await row.hover({ position: { x: 400, y: 20 } });
  await page.waitForTimeout(250);
  const rb = await row.boundingBox();
  await page.screenshot({ path: path.join(OUT, 'presets-anthropic-row-hover.png'), clip: { x: rb.x - 6, y: rb.y - 6, width: rb.width + 12, height: rb.height * 2 + 12 } });
  const hv = await page.evaluate(() => { const r = document.querySelector('.preset-row'); const bd = document.querySelector('.preset-row .param.tier'); return { rowBg: getComputedStyle(r).backgroundColor, badgeBg: getComputedStyle(bd).backgroundColor }; });
  console.log('hover colors', JSON.stringify(hv), hv.rowBg !== hv.badgeBg ? '→ 徽章与行底色不同，可辨' : '→ 同色（徽章会消失）');
  await ctx.close();
}
{
  const { ctx, page } = await open('full', 'pi-agents');
  const sub = await clipOf(page, '.pc-subnav'); const tb = await clipOf(page, '.editor-toolbar');
  await page.screenshot({ path: path.join(OUT, 'pi-agents-toolbar-zoom.png'), clip: { x: sub.x, y: sub.y - 8, width: sub.width, height: tb.y + tb.height - sub.y + 16 } });
  // 卡片头（标题 + code + 计数 ····· meta 标签 + ⓘ + 箭头）：滚到可见再裁切
  const head = page.locator('.config-cat-card', { hasText: '上下文压缩' }).locator('.ccc-head').first();
  await head.scrollIntoViewIfNeeded();
  await page.waitForTimeout(200);
  const hb = await head.boundingBox();
  await page.screenshot({ path: path.join(OUT, 'pi-agents-card-header-zoom.png'), clip: { x: hb.x - 4, y: hb.y - 4, width: hb.width + 8, height: hb.height + 8 } });
  await ctx.close();
}
await browser.close();
console.log('zoom shots done');
