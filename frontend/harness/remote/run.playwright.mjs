/**
 * TEST-ONLY Playwright 场景脚本（M1-C PG-05 浏览器证据 · C-009 headless, video off）。
 * 用法（仓库根目录）：
 *   node frontend/harness/remote/run.playwright.mjs [--base-url http://localhost:5199]
 * 默认自动启动 `vite dev`（端口 5199），跑完关闭。
 * 截图写入 artifact screenshots/，断言与 console 记录写入 logs/playwright.log。
 * 任一断言失败或出现 console error → 进程非零退出。
 */
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { chromium } from 'playwright';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FRONTEND_DIR = path.resolve(__dirname, '../..');
const ARTIFACT_DIR =
  process.env.REMOTE_ARTIFACT_DIR ||
  '/Users/maorun/maorun-workpace/projects-memory/projects/amagi-codebox/agent-outputs/luoshen/20260802-m1-c-desktop-control-center';
const SHOT_DIR = path.join(ARTIFACT_DIR, 'screenshots');
const LOG_DIR = path.join(ARTIFACT_DIR, 'logs');
mkdirSync(SHOT_DIR, { recursive: true });
mkdirSync(LOG_DIR, { recursive: true });

const argv = process.argv.slice(2);
const baseUrlIdx = argv.indexOf('--base-url');
const BASE_URL = baseUrlIdx >= 0 ? argv[baseUrlIdx + 1] : 'http://localhost:5199';
const HARNESS_URL = `${BASE_URL}/harness/remote/`;

const logLines = [];
function log(msg) {
  const line = `[${new Date().toISOString()}] ${msg}`;
  logLines.push(line);
  console.log(line);
}

const failures = [];
function assert(cond, msg) {
  if (cond) {
    log(`ASSERT PASS: ${msg}`);
  } else {
    log(`ASSERT FAIL: ${msg}`);
    failures.push(msg);
  }
}

async function waitForServer(url, timeoutMs = 30_000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(url);
      if (res.ok) return true;
    } catch {
      /* retry */
    }
    await new Promise((r) => setTimeout(r, 400));
  }
  return false;
}

let viteProc = null;
async function startVite() {
  log(`starting vite dev on ${BASE_URL} (cwd=${FRONTEND_DIR})`);
  viteProc = spawn('npx', ['vite', '--port', '5199', '--strictPort'], {
    cwd: FRONTEND_DIR,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  viteProc.stdout.on('data', (d) => logLines.push(`[vite:out] ${d.toString().trim()}`));
  viteProc.stderr.on('data', (d) => logLines.push(`[vite:err] ${d.toString().trim()}`));
  const ok = await waitForServer(HARNESS_URL);
  if (!ok) throw new Error('vite dev server did not become ready');
  log('vite dev server ready');
}

async function main() {
  if (baseUrlIdx < 0) await startVite();

  const browser = await chromium.launch({ headless: true }); // C-009: headless, video off
  const context = await browser.newContext({ viewport: { width: 1280, height: 960 } });
  const page = await context.newPage();

  const consoleErrors = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      consoleErrors.push(msg.text());
      log(`CONSOLE ERROR: ${msg.text()}`);
    }
  });
  page.on('pageerror', (err) => {
    consoleErrors.push(String(err));
    log(`PAGE ERROR: ${err}`);
  });

  const shot = async (name) => {
    await page.screenshot({ path: path.join(SHOT_DIR, `${name}.png`), fullPage: true });
    log(`screenshot: screenshots/${name}.png`);
  };

  const switchBtn = page.locator('.rc-switch[role="switch"]');

  // ---------- S1 服务关态 ----------
  await page.goto(HARNESS_URL, { waitUntil: 'networkidle' });
  await page.waitForSelector('text=已停止', { timeout: 10_000 });
  assert(await page.locator('text=已停止').isVisible(), 'S1 初始为服务关态（已停止）');
  // Major-06：关态下设备卡/事件卡保持可见（根权威语义），仅提示网络动作受限。
  assert(
    await page.locator('[data-testid="devices-service-off"]').isVisible(),
    'S1 服务关态下设备卡呈现网络受限提示',
  );
  await page.waitForSelector('.device-row', { timeout: 5000 });
  assert(
    (await page.locator('.device-row').count()) === 2,
    'S1 服务关态下设备列表仍可见（2 台已配对设备）',
  );
  assert(
    await page.locator('[data-testid="events-service-off"]').isVisible(),
    'S1 服务关态下事件卡呈现停止前记录提示',
  );
  assert(
    await page.locator('text=暂无安全事件记录').isVisible(),
    'S1 服务关态下事件卡可见（初始为空态，诚实呈现）',
  );
  assert(
    await page.locator('[data-testid="start-pairing-btn"]').isDisabled(),
    'S1 服务关态下配对按钮禁用（仅配对窗口受 running 限制）',
  );
  // 硬规则：不展示 Token / 不提供凭据复制入口
  const bodyText1 = await page.textContent('body');
  assert(!bodyText1.includes('STUB-TOKEN'), '硬规则: 页面不展示 Token 值');
  assert(
    !(await page.locator('button:has-text("复制")').count()),
    '硬规则: 页面无任何"复制"按钮（凭据无方便复制入口）',
  );
  await shot('01-initial-service-off');

  // ---------- S2 未确认 LAN 直接开启 → 阻止分支 ----------
  await switchBtn.click();
  await page.waitForSelector('text=LAN 暴露风险确认', { timeout: 5000 });
  assert(
    await page.locator('text=开启前需先在下方完成 LAN 暴露风险确认').isVisible(),
    'S2 未勾选 LAN 确认时开启被阻止并给出引导',
  );
  assert(await page.locator('text=已停止').isVisible(), 'S2 服务仍保持关态');
  await shot('02-enable-blocked-no-lan-confirm');

  // ---------- S3 勾选 LAN 确认 → 开启分支 ----------
  const lanCheckbox = page.locator('[data-testid="lan-confirm-checkbox"]');
  assert(!(await lanCheckbox.isChecked()), 'S3 LAN 确认复选框不预勾选（P-02）');
  await lanCheckbox.check();
  await page.waitForSelector('text=已于', { timeout: 5000 });
  assert(await page.locator('text=记录保存在本机可查').isVisible(), 'S3 确认后展示本机确认记录');
  await switchBtn.click();
  await page.waitForSelector('text=运行中', { timeout: 5000 });
  assert(await page.locator('text=运行中 · 监听').isVisible(), 'S3 服务开启成功（运行中）');
  await shot('03-service-on-lan-confirmed');

  // ---------- S4 配对：未勾选 terminal-exposure → 阻止 ----------
  await page.locator('[data-testid="start-pairing-btn"]').click();
  await page.waitForSelector('text=终端输出暴露确认', { timeout: 5000 });
  assert(
    await page.locator('text=请先勾选上方终端输出暴露确认').isVisible(),
    'S4 未勾选 terminal-exposure 时发起配对被阻止',
  );
  await shot('04-pairing-blocked-no-exposure');

  // ---------- S5 勾选后发起配对 → QR + 等宽倒计时 ----------
  const exposureCheckbox = page.locator('[data-testid="terminal-exposure-checkbox"]');
  assert(!(await exposureCheckbox.isChecked()), 'S5 terminal-exposure 复选框不预勾选');
  await exposureCheckbox.check();
  await page.locator('[data-testid="start-pairing-btn"]').click();
  await page.waitForSelector('[data-testid="pairing-qr"]', { timeout: 5000 });
  assert(await page.locator('[data-testid="pairing-qr"]').isVisible(), 'S5 QR 画布已渲染');
  const code = await page.textContent('[data-testid="pairing-code"]');
  assert(code && code.includes('ABCD-'), `S5 配对码已展示（${code}）`);
  const cd1 = await page.textContent('[data-testid="pairing-countdown"]');
  await shot('05-pairing-window-qr');

  // ---------- S6 倒计时推进证据（定时器真实运行） ----------
  await page.waitForTimeout(2600);
  const cd2 = await page.textContent('[data-testid="pairing-countdown"]');
  assert(cd1 !== cd2, `S6 倒计时随时间推进（${cd1} → ${cd2}）`);
  const attempts = await page.locator('.pair-attempts').textContent().catch(() => null);
  assert(attempts && attempts.includes('剩余尝试次数'), 'S6 轮询返回剩余尝试次数并已展示');
  await shot('06-pairing-countdown-progress');

  // ---------- S7 取消配对窗口 ----------
  await page.locator('[data-testid="cancel-pairing-btn"]').click();
  await page.waitForSelector('text=配对窗口已取消', { timeout: 5000 });
  assert(true, 'S7 取消窗口成功并给出回执');
  await shot('07-pairing-cancelled');

  // ---------- S8 短过期窗口：轮询检测自动关闭（定时器/轮询证据） ----------
  await page.evaluate(() => window.__remoteHarness.setNextWindowTtl(4000));
  await exposureCheckbox.check();
  await page.locator('[data-testid="start-pairing-btn"]').click();
  await page.waitForSelector('[data-testid="pairing-qr"]', { timeout: 5000 });
  await page.waitForSelector('text=配对窗口已结束', { timeout: 15_000 });
  assert(true, 'S8 过期窗口经轮询自动关闭并刷新（定时器/轮询证据）');
  await shot('08-pairing-expired-auto-close');

  // ---------- S9 设备列表 + 撤销 Dialog（Esc 取消分支） ----------
  await page.waitForSelector('.device-row', { timeout: 5000 });
  const rowCount1 = await page.locator('.device-row').count();
  assert(rowCount1 === 2, `S9 设备列表渲染 2 行（实际 ${rowCount1}）`);
  await shot('09-devices-list');

  await page.locator('[data-testid^="revoke-"]').first().click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  assert(await page.locator('.pg06-dialog .pg06-icon').isVisible(), 'S9 Dialog 含危险图标');
  assert(
    await page.locator('.pg06-consequence').textContent().then((t) => t.includes('立即失去访问')),
    'S9 Dialog 含一句话后果',
  );
  assert(
    await page.locator('.pg06-irreversible').textContent().then((t) => t.includes('不可撤销')),
    'S9 Dialog 含不可逆性说明',
  );
  const dangerBtn = page.locator('.pg06-danger');
  assert((await dangerBtn.textContent()).includes('撤销设备'), 'S9 危险按钮动词化（撤销设备）');
  await shot('09b-revoke-dialog-open');

  // 焦点圈闭：连续 Tab 不得离开对话框
  for (let i = 0; i < 4; i++) await page.keyboard.press('Tab');
  const focusInside = await page.evaluate(() => {
    const dlg = document.querySelector('.pg06-dialog');
    return dlg && dlg.contains(document.activeElement);
  });
  assert(focusInside, 'S9 焦点圈闭：多次 Tab 后焦点仍在对话框内');

  await page.keyboard.press('Escape');
  await page.waitForSelector('.pg06-dialog', { state: 'detached', timeout: 5000 });
  assert((await page.locator('.device-row').count()) === 2, 'S9 Esc 取消撤销，设备仍在列表');
  await shot('10-revoke-dialog-esc-cancel');

  // ---------- S10 撤销确认分支（confirm=true） ----------
  const firstDeviceId = await page.locator('.device-row').first().getAttribute('data-device-id');
  await page.locator(`[data-testid="revoke-${firstDeviceId}"]`).click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  await page.locator('.pg06-danger').click();
  await page.waitForSelector('.pg06-dialog', { state: 'detached', timeout: 5000 });
  await page.waitForFunction(() => document.querySelectorAll('.device-row').length === 1, null, {
    timeout: 5000,
  });
  const revokeCalls = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'RevokeRemoteDevice'),
  );
  assert(
    revokeCalls.length === 1 &&
      revokeCalls[0].args[0] === firstDeviceId &&
      revokeCalls[0].args[1] === true,
    `S10 RevokeRemoteDevice 以 (deviceID, confirm=true) 调用（实际 ${JSON.stringify(revokeCalls)}）`,
  );
  assert(
    await page.locator('text=设备被撤销').first().isVisible().catch(() => false),
    'S10 撤销已写入本地可见记录',
  );
  await shot('11-device-revoked');

  // ---------- S11 全部撤销 → 空态 ----------
  await page.locator('[data-testid^="revoke-"]').first().click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  await page.locator('.pg06-danger').click();
  await page.waitForSelector('text=暂无已配对设备', { timeout: 5000 });
  assert(true, 'S11 全部撤销后呈现空态（图标+说明）');
  await shot('12-devices-empty');

  // ---------- S12 事件记录卡 ----------
  const eventsText = await page.locator('[data-testid="events-list"]').textContent();
  for (const kw of ['开启远程服务', '配对窗口开启', '配对窗口取消', '配对窗口过期', '设备被撤销']) {
    assert(eventsText.includes(kw), `S12 事件记录卡包含「${kw}」`);
  }
  await shot('13-events-log');

  // ---------- S13 健康问题 + Acknowledge（种子场景重载） ----------
  await page.goto(`${HARNESS_URL}?seedHealth=1`, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="health-issues"]', { timeout: 10_000 });
  assert(await page.locator('text=记录存储降级').isVisible(), 'S13 健康问题有界展示');
  await shot('14-health-issue');
  await page.locator('[data-testid="ack-durable_sink_degraded"]').click();
  await page.waitForSelector('text=已确认', { timeout: 5000 });
  assert(true, 'S13 Acknowledge 后 issue 标记已确认');
  await shot('15-health-acknowledged');

  // ---------- S14 按钮 ≥44px 抽检 ----------
  await page.goto(HARNESS_URL, { waitUntil: 'networkidle' });
  await page.waitForSelector('text=已停止', { timeout: 10_000 });
  const sizes = await page.evaluate(() => {
    const pick = (sel) => {
      const el = document.querySelector(sel);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { w: r.width, h: r.height };
    };
    return {
      startPairing: pick('[data-testid="start-pairing-btn"]'),
      switchBtn: pick('.rc-switch'),
    };
  });
  assert(sizes.startPairing && sizes.startPairing.h >= 44, `S14 配对按钮高度 ≥44px（实际 ${sizes.startPairing?.h}）`);
  assert(sizes.switchBtn && sizes.switchBtn.h >= 44, `S14 服务开关命中区 ≥44px（实际 ${sizes.switchBtn?.h}）`);
  await shot('16-final-state');

  // ---------- S15 关闭服务走 PG-06 确认（Major-05） ----------
  await page.goto(HARNESS_URL, { waitUntil: 'networkidle' });
  await page.waitForSelector('text=已停止', { timeout: 10_000 });
  // LAN 确认记录已在 localStorage（S3 写入），可直接开启。
  await switchBtn.click();
  await page.waitForSelector('text=运行中', { timeout: 5000 });
  // 运行中点击开关 → 必须先弹 PG-06 确认对话，不得直接停止。
  await switchBtn.click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  assert(
    await page.locator('.pg06-dialog .pg06-title').textContent().then((t) => t.includes('关闭远程服务')),
    'S15 关闭服务弹出 PG-06 确认对话（标题：关闭远程服务）',
  );
  assert(
    await page.locator('.pg06-consequence').textContent().then((t) => t.includes('无法连接')),
    'S15 对话如实说明后果（设备断开/无法连接）',
  );
  assert(
    await page.locator('.pg06-irreversible').textContent().then((t) => t.includes('可逆')),
    'S15 不可逆性如实呈现（本动作可逆，明确说明）',
  );
  assert(
    await page.locator('.pg06-danger').textContent().then((t) => t.includes('关闭服务')),
    'S15 危险按钮动词化（关闭服务）',
  );
  // 确认前后端零调用：stub 断言 ToggleRemoteServer 尚未以 false 调用。
  const toggleCallsBefore = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'ToggleRemoteServer' && c.args[0] === false),
  );
  assert(toggleCallsBefore.length === 0, 'S15 确认前 ToggleRemoteServer(false) 未被调用（stub 断言）');
  assert(await page.locator('text=运行中 · 监听').isVisible(), 'S15 对话打开期间服务仍在运行');
  await shot('17-stop-confirm-dialog');

  // Esc 取消分支：对话关闭，服务保持运行，仍无停止调用。
  await page.keyboard.press('Escape');
  await page.waitForSelector('.pg06-dialog', { state: 'detached', timeout: 5000 });
  assert(await page.locator('text=运行中 · 监听').isVisible(), 'S15 Esc 取消后服务保持运行');
  const toggleCallsAfterEsc = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'ToggleRemoteServer' && c.args[0] === false),
  );
  assert(toggleCallsAfterEsc.length === 0, 'S15 Esc 取消后 ToggleRemoteServer(false) 仍未被调用');

  // 确认分支：对话确认 → 真实触发停止。
  await switchBtn.click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  await page.locator('.pg06-danger').click();
  await page.waitForSelector('text=已停止', { timeout: 5000 });
  const toggleCallsConfirmed = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'ToggleRemoteServer' && c.args[0] === false),
  );
  assert(
    toggleCallsConfirmed.length === 1,
    `S15 PG-06 确认后 ToggleRemoteServer(false) 真实触发一次（stub 断言，实际 ${toggleCallsConfirmed.length}）`,
  );
  await shot('18-service-stopped-via-confirm');

  // ---------- S16 关闭态设备治理与记录可见（Major-06 浏览器证据） ----------
  await page.waitForSelector('.device-row', { timeout: 5000 });
  assert(
    (await page.locator('.device-row').count()) === 2,
    'S16 服务关闭后设备列表仍可见（2 台设备）',
  );
  // 关闭态完成撤销：PG-06 → RevokeRemoteDevice 真实生效。
  const stoppedFirstId = await page.locator('.device-row').first().getAttribute('data-device-id');
  await page.locator(`[data-testid="revoke-${stoppedFirstId}"]`).click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  await page.locator('.pg06-danger').click();
  await page.waitForSelector('.pg06-dialog', { state: 'detached', timeout: 5000 });
  await page.waitForFunction(() => document.querySelectorAll('.device-row').length === 1, null, {
    timeout: 5000,
  });
  const stoppedRevokeCalls = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'RevokeRemoteDevice'),
  );
  assert(
    stoppedRevokeCalls.length === 1 &&
      stoppedRevokeCalls[0].args[0] === stoppedFirstId &&
      stoppedRevokeCalls[0].args[1] === true,
    `S16 服务关闭态下撤销真实生效（stub 断言 RevokeRemoteDevice(${stoppedFirstId}, true)）`,
  );
  await shot('19-stopped-devices-manageable');
  // 关闭态事件记录可见且包含本次停止与撤销事件。
  const stoppedEventsText = await page.locator('[data-testid="events-list"]').textContent();
  for (const kw of ['停止远程服务', '设备被撤销']) {
    assert(stoppedEventsText.includes(kw), `S16 关闭态事件卡可见且包含「${kw}」`);
  }
  assert(
    await page
      .locator('[data-testid="events-service-off"]')
      .textContent()
      .then(
        (t) =>
          t.includes('停止前及停止期间写入') && t.includes('不依赖监听器在线'),
      ),
    'S16 关闭态事件卡提示如实覆盖停止前/停止期间写入（R2-N02）',
  );
  await page
    .locator('.rc-card', { has: page.locator('[data-testid="events-service-off"]') })
    .screenshot({ path: path.join(SHOT_DIR, '21-stopped-events-card-hint.png') });
  log('screenshot: screenshots/21-stopped-events-card-hint.png');
  assert(
    await page.locator('[data-testid="start-pairing-btn"]').isDisabled(),
    'S16 关闭态下配对按钮仍禁用（仅配对受 running 限制）',
  );
  await shot('20-stopped-revoke-and-events');

  // ---------- S17 stopped API parity（R2-N01，对照 internal/remote/server_security.go:307-320） ----------
  // 生产语义：GetPairingWindow/CancelPairingWindow 只查 requireSecurity，不检查 running；
  // stopped 且无活跃窗口时分别返回 Active:false / false。仅 CreateWindow 有 accepting 门。
  const parity = await page.evaluate(async () => {
    const app = window.go.main.App;
    const out = { get: null, cancel: null, createThrew: false, getThrew: false, cancelThrew: false };
    try {
      out.get = await app.GetRemotePairingWindow();
    } catch {
      out.getThrew = true;
    }
    try {
      out.cancel = await app.CancelRemotePairingWindow(1);
    } catch {
      out.cancelThrew = true;
    }
    try {
      await app.CreateRemotePairingWindow(true);
    } catch {
      out.createThrew = true;
    }
    return out;
  });
  assert(
    !parity.getThrew && parity.get && parity.get.active === false,
    `S17 stopped 下 GetRemotePairingWindow 返回 inactive 不抛错（与生产对齐，实际 ${JSON.stringify(parity.get)}）`,
  );
  assert(
    !parity.cancelThrew && parity.cancel === false,
    `S17 stopped 下 CancelRemotePairingWindow 返回 false 不抛错（与生产对齐，实际 ${JSON.stringify(parity.cancel)}）`,
  );
  assert(
    parity.createThrew === true,
    'S17 stopped 下 CreateRemotePairingWindow 仍被拒绝（仅 Create 保留 accepting 门）',
  );

  // ==================== M2-INT R12：外部进程清理恢复 UX（S18–S23） ====================

  // ---------- S18 Startup warning 横幅消费（可关闭、当次启动内持久提醒） ----------
  await page.goto(`${HARNESS_URL}?seedStartupWarning=1`, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="startup-warning-banner"]', { timeout: 10_000 });
  assert(
    await page.locator('[data-testid="startup-warning-banner"]').textContent().then((t) => t.includes('未完成的外部进程清理')),
    'S18 Startup warning 被当前 App shell 消费为可见提示条',
  );
  await shot('r1-startup-warning-banner');
  // 前往处理入口可达（harness 中仅翻转 uiStore，不应报错）；横幅不随导航自动消失（持久提醒）。
  await page.locator('[data-testid="startup-warning-goto"]').click();
  assert(
    await page.locator('[data-testid="startup-warning-banner"]').isVisible(),
    'S18 点击「前往处理」后横幅仍可见（不自动关闭，持久提醒）',
  );
  // 手动关闭：当次启动内不再显示。
  await page.locator('[data-testid="startup-warning-dismiss"]').click();
  await page.waitForSelector('[data-testid="startup-warning-banner"]', { state: 'detached', timeout: 5000 });
  assert(true, 'S18 横幅可手动关闭');
  await shot('r2-startup-warning-dismissed');
  // 重新加载（等同下次启动）：警告仍在则再次提醒（关闭不落 localStorage，不当次外持久）。
  await page.goto(`${HARNESS_URL}?seedStartupWarning=1`, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="startup-warning-banner"]', { timeout: 10_000 });
  assert(true, 'S18 重新启动后警告再次提醒（关闭仅当次启动生效）');
  await page.locator('[data-testid="startup-warning-dismiss"]').click();
  await page.waitForSelector('[data-testid="startup-warning-banner"]', { state: 'detached', timeout: 5000 });

  // ---------- S19 恢复卡 running 态：隐私安全状态展示 + 重新核验 ----------
  await page.goto(`${HARNESS_URL}?seedRecovery=running`, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="recovery-blocked"]', { timeout: 10_000 });
  assert(
    await page.locator('[data-testid="recovery-blocked"]').textContent().then((t) => t.includes('检测到 1 项未完成的外部进程清理')),
    'S19 恢复卡展示待恢复计数（1 项）',
  );
  assert(
    await page.locator('[data-testid="recovery-state-running"]').isVisible(),
    'S19 running 态如实展示「旧终端仍在运行」',
  );
  assert(
    await page.locator('text=请先在系统中关闭对应的旧外部终端').isVisible(),
    'S19 running 态给出可执行指导（关闭旧终端后重新核验）',
  );
  assert(
    (await page.locator('[data-testid="recovery-confirm-open"]').count()) === 0,
    'S19 running 态不出现确认入口（canConfirm=false，防误确认）',
  );
  // 隐私门禁：sessionId/PID/路径一律不上屏（对齐 R11-002 privacy status 语义）。
  const recoveryCardText = await page.locator('.rec-card').textContent();
  assert(!recoveryCardText.includes('SES-LEGACY'), 'S19 隐私：恢复卡不渲染 sessionId');
  assert(!recoveryCardText.includes('PID') && !recoveryCardText.includes('/Users/'), 'S19 隐私：恢复卡不渲染 PID/路径');
  assert(
    await page.locator('.rec-card').textContent().then((t) => t.includes('Claude Headroom 进程') && t.includes('旧版本遗留进程')),
    'S19 仅展示类型与原因文案（计数/类型语义）',
  );
  await shot('r3-recovery-running');
  // 重新核验：stub 进程仍存活 → 保持 running 态并给出提示。
  const statusCallsBefore = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'GetExternalCleanupRecoveryStatus').length,
  );
  await page.locator('[data-testid="recovery-recheck"]').click();
  await page.waitForFunction(
    (n) => window.__remoteHarness.calls.filter((c) => c.method === 'GetExternalCleanupRecoveryStatus').length > n,
    statusCallsBefore,
    { timeout: 5000 },
  );
  assert(true, 'S19 「重新核验」真实触发后端活性复检（status 调用递增）');
  await page.waitForSelector('.toast-item', { timeout: 5000 });
  assert(
    await page.locator('.toast-item').first().textContent().then((t) => t.includes('仍在运行')),
    'S19 核验后仍运行 → 明确提示未通过',
  );
  assert(
    await page.locator('[data-testid="recovery-state-running"]').isVisible(),
    'S19 核验未通过后保持 running 态（不伪装可确认）',
  );
  await shot('r4-recovery-recheck-still-running');

  // ---------- S20 awaiting 态 → PG-06 显式确认对话 ----------
  await page.goto(`${HARNESS_URL}?seedRecovery=awaiting`, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="recovery-state-awaiting"]', { timeout: 10_000 });
  assert(true, 'S20 旧进程已退出 → awaiting_confirmation 态如实展示');
  await page.locator('[data-testid="recovery-confirm-open"]').click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  assert(
    await page.locator('.pg06-dialog .pg06-title').textContent().then((t) => t.includes('完成外部进程清理恢复')),
    'S20 PG-06 对话标题（完成外部进程清理恢复）',
  );
  assert(
    await page.locator('.pg06-consequence').textContent().then((t) => t.includes('再次核验') && t.includes('仍在运行将被拒绝')),
    'S20 对话如实说明后果（确认前再次核验，仍在运行将被拒绝）',
  );
  assert(
    await page.locator('.pg06-irreversible').textContent().then((t) => t.includes('不可撤销') && t.includes('强制清除')),
    'S20 不可逆性说明 + 明确无强制清除入口（no force-clear）',
  );
  assert(
    await page.locator('.pg06-danger').textContent().then((t) => t.includes('确认已完成清理')),
    'S20 危险按钮动词化（确认已完成清理）',
  );
  const focusOnCancel = await page.evaluate(() =>
    document.activeElement?.classList.contains('pg06-cancel'),
  );
  assert(focusOnCancel, 'S20 安全默认：初始焦点在「取消」，危险按钮需主动移焦');
  await shot('r5-recovery-confirm-dialog');
  // Esc 取消分支：对话关闭且零后端调用。
  await page.keyboard.press('Escape');
  await page.waitForSelector('.pg06-dialog', { state: 'detached', timeout: 5000 });
  const confirmCallsAfterEsc = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'ConfirmExternalCleanupRecovery'),
  );
  assert(confirmCallsAfterEsc.length === 0, 'S20 Esc 取消后 confirm 未被调用');

  // ---------- S21 显式确认成功：状态清除 + 提示 + 本机记录 ----------
  await page.locator('[data-testid="recovery-confirm-open"]').click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  await page.locator('.pg06-danger').click();
  await page.waitForSelector('.toast-item.toast-success', { timeout: 5000 });
  assert(
    await page.locator('.toast-item.toast-success').first().textContent().then((t) => t.includes('Headroom 安全锁定已解除')),
    'S21 成功提示如实呈现 fenceReleased（Headroom 锁定已解除）',
  );
  const confirmCalls = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'ConfirmExternalCleanupRecovery'),
  );
  assert(
    confirmCalls.length === 1 && confirmCalls[0].args[0] === 'SES-LEGACY-0001' && confirmCalls[0].args[1] === true,
    `S21 ConfirmExternalCleanupRecovery 以 (sessionID, confirmed=true) 调用一次（实际 ${JSON.stringify(confirmCalls)}）`,
  );
  await page.waitForSelector('[data-testid="recovery-healthy"]', { timeout: 5000 });
  assert(true, 'S21 恢复成功后状态清除（健康态）');
  await page.waitForSelector('[data-testid="recovery-local-log"]', { timeout: 5000 });
  assert(
    await page.locator('[data-testid="recovery-local-log"]').textContent().then((t) => t.includes('已完成恢复')),
    'S21 本机恢复确认记录可见（已完成恢复）',
  );
  await shot('r6-recovery-cleared');

  // ---------- S22 live 拒绝态：确认瞬间进程仍存活 → 拒绝原因展示 ----------
  await page.goto(`${HARNESS_URL}?seedRecovery=awaiting`, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="recovery-state-awaiting"]', { timeout: 10_000 });
  await page.locator('[data-testid="recovery-confirm-open"]').click();
  await page.waitForSelector('.pg06-dialog', { timeout: 5000 });
  // 对话打开后、确认前进程被拉回存活（stub 模拟 OS 真相）→ confirm 被 live 拒绝。
  await page.evaluate(() => window.__remoteHarness.setRecoveryProcessAlive('SES-LEGACY-0001', true));
  await page.locator('.pg06-danger').click();
  await page.waitForSelector('.toast-item.toast-error', { timeout: 5000 });
  assert(
    await page.locator('.toast-item.toast-error').first().textContent().then((t) => t.includes('恢复被拒绝') && t.includes('仍在运行')),
    'S22 live 拒绝时展示拒绝原因（旧进程仍在运行）',
  );
  await page.waitForSelector('[data-testid="recovery-state-running"]', { timeout: 5000 });
  assert(true, 'S22 拒绝后状态如实回到 running 态（未清除、未伪装成功）');
  const rejectedConfirmCalls = await page.evaluate(() =>
    window.__remoteHarness.calls.filter((c) => c.method === 'ConfirmExternalCleanupRecovery'),
  );
  assert(
    rejectedConfirmCalls.length === 1 && rejectedConfirmCalls[0].args[1] === true,
    'S22 显式确认语义真实到达后端（confirmed=true），由后端 live 拒绝',
  );
  assert(
    await page.locator('[data-testid="recovery-local-log"]').textContent().then((t) => t.includes('被拒绝：旧进程仍在运行')),
    'S22 拒绝结果写入本机恢复确认记录',
  );
  await shot('r7-recovery-rejected');

  // ---------- S23 健康态（无种子）：持久可发现 + 无横向溢出 ----------
  await page.goto(HARNESS_URL, { waitUntil: 'networkidle' });
  await page.waitForSelector('[data-testid="recovery-healthy"]', { timeout: 10_000 });
  assert(
    await page.locator('[data-testid="recovery-healthy"]').textContent().then((t) => t.includes('未发现待恢复的外部进程清理项')),
    'S23 无待恢复项时健康态如实呈现（卡片持久可发现）',
  );
  const recOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert(recOverflow <= 0, `S23 桌面视口无横向溢出（实际 ${recOverflow}px）`);
  await shot('r8-recovery-healthy');

  // ---------- console 无 error 断言 ----------
  assert(consoleErrors.length === 0, `全程 console 无 error（实际 ${consoleErrors.length} 条）`);

  await browser.close();

  log(failures.length === 0 ? 'ALL SCENARIOS PASSED' : `FAILURES: ${failures.length}`);
  writeFileSync(path.join(LOG_DIR, 'playwright.log'), logLines.join('\n') + '\n');
  process.exit(failures.length === 0 ? 0 : 1);
}

main()
  .catch((err) => {
    log(`FATAL: ${err?.stack || err}`);
    writeFileSync(path.join(LOG_DIR, 'playwright.log'), logLines.join('\n') + '\n');
    process.exit(2);
  })
  .finally(() => {
    if (viteProc) viteProc.kill('SIGTERM');
  });
