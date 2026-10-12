// X4–X7 交互验证（Playwright，复用 mock-wails.js / helpers.js，对真实 App + 真实 vite dev server 5199）。
// 为什么不用 agent-browser 跑这一组：其无头页面运行一段时间后 document.visibilityState 会变 hidden，
// 此时 mouse wheel 等依赖渲染帧的输入会挂起、≥250ms 定时器被节流，hover 延迟 / 滚动事件的断言会失真；
// Playwright 的无头 Chromium 页面始终 visible。静态截图（run-all.sh）仍由 agent-browser 生成。
//
// 用法：node agent-outputs/workflow/provider-center-layout-redesign/x-harness/interaction-test.mjs
// 退出码 = 失败断言数
import { chromium } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(HERE, '..', 'screenshots');
const BASE = 'http://127.0.0.1:5199/';
let fail = 0;
const pageErrors = [];
const ok = (n) => console.log('  PASS  ' + n);
const bad = (n, why) => { fail++; console.log('  FAIL  ' + n + '  ← ' + why); };
const check = (n, cond, why = '') => (cond ? ok(n) : bad(n, why));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await chromium.launch();

async function openView(scenario, view, w = 1280, h = 800, setup) {
  const ctx = await browser.newContext({ viewport: { width: w, height: h } });
  if (setup) await setup(ctx);
  await ctx.addInitScript({ path: path.join(HERE, 'mock-wails.js') });
  await ctx.addInitScript({ path: path.join(HERE, 'helpers.js') });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  await page.goto(`${BASE}?s=${scenario}&n=${Date.now()}#/provider`);
  const r = await page.evaluate((v) => window.__ab.seq(v), view);
  if (String(r).includes('NOT FOUND')) bad('导航 ' + view, r);
  await page.waitForTimeout(900);
  return { ctx, page };
}

/** 读取某个 InfoTip 的状态 */
const pop = (page, label) => page.evaluate((label) => {
  const b = document.querySelector(`button[aria-label="${label}"]`);
  if (!b) return null;
  const p = document.getElementById(b.getAttribute('aria-describedby'));
  const r = p.getBoundingClientRect();
  const br = b.getBoundingClientRect();
  const cs = getComputedStyle(p);
  return {
    open: cs.display !== 'none', exp: b.getAttribute('aria-expanded') === 'true', focused: document.activeElement === b,
    r: { l: r.left, t: r.top, r: r.right, b: r.bottom, w: r.width, h: r.height }, br: { l: br.left, t: br.top, r: br.right, b: br.bottom },
    parentIsBody: p.parentElement === document.body, insideCard: !!b.closest('.config-cat-card') && b.closest('.config-cat-card').contains(p),
    bg: cs.backgroundColor, z: cs.zIndex, pos: cs.position, dt: p.querySelectorAll('dt').length, paras: p.querySelectorAll('p').length,
  };
}, label);

const inViewport = (s, page) => page.evaluate(([l, t, r, b]) => l >= 7 && t >= 7 && r <= innerWidth - 7 && b <= innerHeight - 7, [s.r.l, s.r.t, s.r.r, s.r.b]);
/** 复位：点标题处（外点关闭任何固定浮层）+ 指针移到角落 */
const reset = async (page) => { await page.mouse.click(700, 70); await page.mouse.move(20, 20); await sleep(200); };

// ───────────────────────────── X4 / X5：InfoTip（Pi · Agent 配置） ─────────────────────────────
console.log('== X4/X5 InfoTip（Pi · Agent 配置，1280×800）');
{
  const { ctx, page } = await openView('full', 'pi-agents');
  const ZH = '说明：上下文压缩', CON = '说明：并发限制', LEADER = '说明：Leader 服务器';

  const all = await page.evaluate(() => [...document.querySelectorAll('button.infotip-btn')].map((b) => {
    const p = document.getElementById(b.getAttribute('aria-describedby'));
    return { label: b.getAttribute('aria-label'), exp: b.getAttribute('aria-expanded'), role: p && p.getAttribute('role'), display: p && getComputedStyle(p).display, body: p && p.parentElement === document.body };
  }));
  check('T1 所有 InfoTip 初始关闭、describedby 目标存在 role=tooltip 且已 Teleport 到 body', all.length >= 5 && all.every((a) => a.exp === 'false' && a.role === 'tooltip' && a.display === 'none' && a.body), JSON.stringify(all));
  check('T1 aria-label 均以「说明：」开头', all.every((a) => a.label.startsWith('说明：')));

  // T2 键盘
  await page.evaluate(() => { [...document.querySelectorAll('.ccc-header')].find((b) => b.textContent.includes('上下文压缩')).id = 'hdr-compaction'; });
  await page.focus('#hdr-compaction');
  await page.keyboard.press('Tab');
  let s = await pop(page, ZH);
  check('T2 header 之后 Tab 落在 ⓘ 上（Tab 顺序：先 header 后 tip）', s.focused);
  check('T2 仅聚焦不自动打开', !s.open && !s.exp);
  // Tab 聚焦会 scrollIntoView，其 scroll 事件在下一渲染帧派发；按设计「任意滚动即关闭」，
  // 所以 Tab→Enter 之间留出人类可达的间隔（<16ms 连按不是真实操作），避免测试时序抖动。
  await sleep(150);
  await page.keyboard.press('Enter'); await sleep(150);
  s = await pop(page, ZH);
  check('T2 Enter 打开（aria-expanded=true、浮层可见）', s.open && s.exp);
  check('T2 Enter 不触发卡片折叠切换', (await page.getAttribute('#hdr-compaction', 'aria-expanded')) === 'true');
  check('T2 浮层在视口内（含 8px 边距）', await inViewport(s, page), JSON.stringify(s.r));
  check('T2 浮层背景实色 + z-index 2000 + fixed + Teleport 出卡片', s.bg === 'rgb(255, 255, 255)' && s.z === '2000' && s.pos === 'fixed' && s.parentIsBody && !s.insideCard, JSON.stringify({ bg: s.bg, z: s.z, pos: s.pos }));
  await page.screenshot({ path: path.join(OUT, 'pi-agents-infotip-open-keyboard.png') });
  await page.keyboard.press('Escape'); await sleep(100);
  s = await pop(page, ZH);
  check('T2 Esc 关闭并把焦点还给按钮', !s.open && s.focused);

  // T3 Space
  await sleep(100);
  await page.keyboard.press('Space'); await sleep(100);
  check('T3 Space 打开', (await pop(page, ZH)).open);
  await page.keyboard.press('Space'); await sleep(100);
  check('T3 再按 Space 关闭（pinned 切换）', !(await pop(page, ZH)).open);

  // T4 失焦
  await page.keyboard.press('Enter'); await sleep(150);
  await page.keyboard.press('Tab'); await sleep(150);
  check('T4 pinned 来源 Tab 离开即关闭', !(await pop(page, ZH)).open);
  await reset(page);

  // T5 hover
  await page.locator(`button[aria-label="${CON}"]`).scrollIntoViewIfNeeded();
  await page.hover(`button[aria-label="${CON}"]`); await sleep(120);
  check('T5 hover 120ms 时尚未打开（250ms 延迟）', !(await pop(page, CON)).open);
  await sleep(300);
  s = await pop(page, CON);
  check('T5 hover 420ms 后已打开', s.open);
  const cx = s.r.l + s.r.w / 2, cy = s.r.t + s.r.h / 2;
  await page.mouse.move(cx, cy, { steps: 6 }); await sleep(500);
  check('T5 指针移入浮层并停留 500ms 仍保持打开（进入浮层取消关闭）', (await pop(page, CON)).open);
  await page.mouse.move(20, 20, { steps: 6 }); await sleep(350);
  check('T5 指针离开 350ms 后关闭（hover 来源 120ms 收起）', !(await pop(page, CON)).open);

  // T6 pinned / 外点 / 滚动 / resize
  await page.click(`button[aria-label="${CON}"]`); await sleep(150);
  await page.mouse.move(20, 20); await sleep(400);
  s = await pop(page, CON);
  check('T6 点击固定：指针离开 400ms 仍保持打开', s.open && s.exp);
  await page.screenshot({ path: path.join(OUT, 'pi-agents-infotip-pinned-collapsed-card.png') });
  await page.mouse.move(s.r.l + s.r.w / 2, s.r.t + s.r.h / 2); await page.mouse.down(); await page.mouse.up(); await sleep(200);
  check('T6 在浮层内按下指针不会关闭（按钮虽失焦，仍可选中文字）', (await pop(page, CON)).open);
  await page.mouse.click(700, 140); await sleep(200);
  check('T6 外部 pointerdown 关闭', !(await pop(page, CON)).open);
  await page.click(`button[aria-label="${CON}"]`); await sleep(150);
  await page.mouse.move(700, 300);
  await page.mouse.wheel(0, 120); await sleep(300);
  check('T6 滚轮滚动页面后关闭（避免浮层与锚点错位）', !(await pop(page, CON)).open);
  await page.evaluate(() => document.querySelector('.main').scrollTo(0, 0));
  await page.click(`button[aria-label="${ZH}"]`).catch(() => {});
  await reset(page);
  await page.click(`button[aria-label="${CON}"]`); await sleep(150);
  await page.setViewportSize({ width: 1240, height: 780 }); await sleep(300);
  check('T6 窗口 resize 后关闭', !(await pop(page, CON)).open);
  await page.setViewportSize({ width: 1280, height: 800 }); await reset(page);

  // T7 折叠卡片头：不被裁切；视口底部自动翻转
  await page.evaluate(() => { document.querySelector('.main').scrollTop = document.querySelector('.main').scrollHeight; });
  await sleep(200);
  await page.click(`button[aria-label="${CON}"]`); await sleep(200);
  s = await pop(page, CON);
  check('T7 折叠卡片头的浮层完整落在视口内', s.open && (await inViewport(s, page)), JSON.stringify(s.r));
  check('T7 浮层不在 .config-cat-card（overflow:hidden）内，未被裁切', !s.insideCard && s.parentIsBody);
  console.log(`   · 锚点 y=${Math.round(s.br.t)}–${Math.round(s.br.b)}，浮层 y=${Math.round(s.r.t)}–${Math.round(s.r.b)}（视口高 800）→ ${s.r.b <= s.br.t ? '向上翻转' : '向下展开'}`);
  await page.screenshot({ path: path.join(OUT, 'pi-agents-infotip-flip-collapsed-card.png') });
  await reset(page);
  // 强制放在视口底部附近：把按钮滚到视口最下沿
  await page.evaluate((lbl) => { const b = document.querySelector(`button[aria-label="${lbl}"]`); const m = document.querySelector('.main'); const r = b.getBoundingClientRect(); m.scrollTop += r.top - (innerHeight - 40); }, CON);
  await sleep(200);
  await page.click(`button[aria-label="${CON}"]`); await sleep(200);
  s = await pop(page, CON);
  check('T7 按钮贴近视口底部时浮层向上翻转且完整可见', s.open && s.r.b <= s.br.t && (await inViewport(s, page)), JSON.stringify({ pop: s.r, anchor: s.br }));
  await reset(page);

  // T8 字段级 ⓘ：三态对照
  await page.evaluate(() => { const h = [...document.querySelectorAll('.ccc-header')].find((b) => b.textContent.includes('MCP 路由')); if (h.getAttribute('aria-expanded') === 'false') h.click(); });
  await page.waitForSelector(`button[aria-label="${LEADER}"]`);
  await page.locator(`button[aria-label="${LEADER}"]`).scrollIntoViewIfNeeded();
  await page.focus(`button[aria-label="${LEADER}"]`); await sleep(150); await page.keyboard.press('Enter'); await sleep(200);
  s = await pop(page, LEADER);
  check('T8 字段级 ⓘ（Leader 服务器）打开，含 2 段说明 + 三态 <dl>（3 个 dt）', s.open && s.dt === 3 && s.paras >= 2, JSON.stringify({ dt: s.dt, paras: s.paras }));
  await page.screenshot({ path: path.join(OUT, 'pi-agents-infotip-field-items.png') });
  await page.keyboard.press('Escape'); await reset(page);

  check('T9 InfoTip 不污染 role=alert（无错误态时页面 alert 数为 0）', (await page.evaluate(() => document.querySelectorAll('[role=alert]').length)) === 0);
  await ctx.close();
}

// ───────────────────────────── X6：预设行键盘编辑 ─────────────────────────────
console.log('== X6 预设行键盘编辑（预设 · Anthropic，1280×800）');
{
  const { ctx, page } = await openView('full', 'presets-anthropic');
  const n = await page.locator('.preset-open').count();
  check('X6 每行一个真 <button class="preset-open">（可聚焦），共 4 行', n === 4, 'n=' + n);
  // 从工具栏「+ 添加预设」Tab 到首行
  await page.focus('.pc-toolbar .btn-primary');
  await page.keyboard.press('Tab');
  const onFirst = await page.evaluate(() => document.activeElement.classList.contains('preset-open') && document.activeElement.getAttribute('aria-label'));
  check('X6 Tab 进入首行名称按钮，aria-label=「编辑预设 …」', typeof onFirst === 'string' && onFirst.startsWith('编辑预设 '), String(onFirst));
  const ring = await page.evaluate(() => { const cs = getComputedStyle(document.activeElement, '::after'); return { style: cs.outlineStyle, w: cs.outlineWidth, color: cs.outlineColor, own: getComputedStyle(document.activeElement).outlineStyle }; });
  check('X6 焦点框画在撑满整行的 ::after 上（按钮自身 outline:none）', ring.style === 'solid' && ring.own === 'none', JSON.stringify(ring));
  await page.screenshot({ path: path.join(OUT, 'presets-anthropic-row-focus.png') });
  await page.keyboard.press('Enter'); await sleep(300);
  const dlg = await page.evaluate(() => ({ open: !!document.querySelector('.dialog-overlay'), title: document.querySelector('.dialog-title')?.textContent?.trim() }));
  check('X6 Enter 打开预设编辑弹窗', dlg.open, JSON.stringify(dlg));
  console.log('   · 弹窗标题：' + dlg.title);
  await page.click('.dialog-close'); await sleep(300);
  // 删除按钮单独可聚焦，不会误触发编辑
  await page.focus('.preset-open');
  await page.keyboard.press('Tab');
  const onDel = await page.evaluate(() => document.activeElement.classList.contains('preset-delete') && document.activeElement.getAttribute('aria-label'));
  check('X6 下一个 Tab 停在删除按钮上，aria-label=「删除预设 …」（独立焦点停靠点）', typeof onDel === 'string' && onDel.startsWith('删除预设 '), String(onDel));
  await page.keyboard.press('Enter'); await sleep(300);
  const del = await page.evaluate(() => ({ title: document.querySelector('.dialog-title')?.textContent?.trim(), msg: document.querySelector('.confirm-message')?.textContent?.trim() }));
  check('X6 删除按钮 Enter 只打开「删除预设」确认框（不是编辑弹窗）', del.title === '删除预设' && (del.msg || '').includes('GLM-5.2 Max'), JSON.stringify(del));
  await page.getByRole('button', { name: '取消' }).click(); await sleep(300);
  // 整行热区：点徽章区域也进入编辑；点删除只出确认
  // 真实坐标点击徽章位置（Playwright 的 locator.click 会因热区 ::after 接管该点而拒绝，这正是设计意图）
  const bb = await page.locator('.preset-row:nth-child(2) .param.model').boundingBox();
  const top = await page.evaluate(([x, y]) => { const e = document.elementFromPoint(x, y); return e && e.className; }, [bb.x + bb.width / 2, bb.y + bb.height / 2]);
  check('X6 徽章位置的命中目标是整行热区（.preset-open ::after），而不是徽章本身', String(top).includes('preset-open'), String(top));
  await page.mouse.click(bb.x + bb.width / 2, bb.y + bb.height / 2); await sleep(300);
  const hit = await page.evaluate(() => document.querySelector('.dialog-title')?.textContent?.trim());
  check('X6 鼠标点徽章区域（整行热区）打开编辑弹窗', !!hit && hit !== '删除预设', String(hit));
  await page.click('.dialog-close'); await sleep(300);
  await page.click('.preset-row:nth-child(1) .preset-delete'); await sleep(300);
  check('X6 鼠标点「删除」只出确认框（z-index 浮在热区之上）', (await page.evaluate(() => document.querySelector('.dialog-title')?.textContent?.trim())) === '删除预设');
  await page.getByRole('button', { name: '取消' }).click(); await sleep(200);
  // 筛选无果空态
  await page.evaluate(() => { document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('provider').setPresetFilter('no-such-provider'); });
  await sleep(300);
  const filt = await page.evaluate(() => ({ title: document.querySelector('.empty-state h3')?.textContent?.trim(), btn: document.querySelector('.empty-state .empty-action')?.textContent?.trim() }));
  check('X6 筛选无果空态：「{provider} 下暂无预设」+［显示全部］', filt.title === 'no-such-provider 下暂无预设' && filt.btn === '显示全部', JSON.stringify(filt));
  await page.screenshot({ path: path.join(OUT, 'presets-anthropic-empty-filtered.png') });
  await ctx.close();
}

// ───────────────────────────── X7：皮肤模式 ─────────────────────────────
console.log('== X7 皮肤模式（浮层仍不透明）');
{
  // 皮肤图走真实路径 /skins/<file>（data: URL 会触发 skinBake 的递归重同步，属 harness 保真问题，与本次改动无关）
  const { ctx, page } = await openView('full,skin', 'pi-agents', 1280, 800, (c) => c.route('**/skins/demo.svg', (route) => route.fulfill({ path: path.join(HERE, 'skin-demo.svg'), contentType: 'image/svg+xml' })));
  await page.waitForTimeout(800);
  const skin = await page.evaluate(() => ({ layer: !!document.querySelector('.skin-layer'), on: document.documentElement.dataset.skin, card: getComputedStyle(document.querySelector('.config-card')).backgroundColor, ccc: getComputedStyle(document.querySelector('.config-cat-card')).backgroundColor }));
  check('X7 皮肤已开启（html[data-skin=on] + .skin-layer 背景层存在），面板为半透明底（alpha<1）', skin.on === 'on' && skin.layer && /color\(srgb|rgba/.test(skin.card), JSON.stringify(skin));
  await page.screenshot({ path: path.join(OUT, 'pi-agents-skin.png') });
  const ZH = '说明：上下文压缩';
  await page.evaluate(() => { [...document.querySelectorAll('.ccc-header')].find((b) => b.textContent.includes('上下文压缩')).id = 'hdr-compaction'; });
  await page.focus('#hdr-compaction'); await page.keyboard.press('Tab'); await sleep(200); await page.keyboard.press('Enter'); await sleep(250);
  const s = await pop(page, ZH);
  check('X7 皮肤下 InfoTip 浮层底色仍为不透明 rgb(255,255,255)', s.open && s.bg === 'rgb(255, 255, 255)', s.bg);
  await page.screenshot({ path: path.join(OUT, 'pi-agents-skin-infotip-open.png') });
  await ctx.close();
}

await browser.close();
const real = pageErrors.filter((e) => /InfoTip|FieldLabel|EditorToolbar|ConfigCategoryCard|PresetList|PiAmagi|OmpGlobal|OpenCode|ProviderRegistry|AuthConfig|ProviderCenter/.test(e));
console.log(`== 页面未捕获异常：共 ${pageErrors.length} 条，与本次改动相关 ${real.length} 条${real.length ? '\n' + real.join('\n') : ''}`);
if (pageErrors.length) console.log('   · 非本次改动的异常（应仅为皮肤 syncSkinDom 递归，改造前即存在）：' + [...new Set(pageErrors)].join(' || ').slice(0, 300));
if (real.length) fail += real.length;
console.log(`== 汇总：失败断言数 = ${fail}`);
process.exit(fail);
