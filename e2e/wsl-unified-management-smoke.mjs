// Real Vue + in-memory Wails fixture only; never installs software or writes user configs.
// Start: node frontend/node_modules/vite/bin/vite.js --config e2e/wsl-cli-supply-ui.vite.config.mjs
import { execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
const session = `codebox-wsl-unified-${process.pid}`;
const base = process.env.WSL_FIXTURE_URL || 'http://127.0.0.1:5191/e2e/wsl-cli-supply-ui.html';
const results = [];
function browser(...args) {
  const output = execFileSync('agent-browser', ['--session', session, '--json', ...args], {
    encoding: 'utf8', timeout: 35_000, maxBuffer: 1024 * 1024,
  });
  const response = JSON.parse(output);
  if (!response.success) throw new Error(JSON.stringify(response.error));
  return response.data;
}
const evaluate = value => browser('eval', value).result;
const wait = value => browser('wait', '--fn', value);
const click = name => browser('find', 'role', 'button', 'click', '--name', name, '--exact');
function open(scenario = 'default') {
  browser('open', `${base}?scenario=${scenario}`);
  wait(`Array.isArray(window.__wslCalls) && Array.from(document.querySelectorAll('button')).some(b=>b.textContent.trim()==='启用统一管理')`);
}
function passed(name) { results.push(name); console.log(`PASS ${name}`); }
try {
  open();
  assert.equal(evaluate(`window.__wslCalls.some(c=>c.name==='EnableWSLUnifiedSupply')`), false);
  click('启用统一管理');
  wait(`window.__wslCalls.some(c=>c.name==='EnableWSLUnifiedSupply') && !Array.from(document.querySelectorAll('button')).some(b=>b.textContent.trim()==='启用统一管理')`);
  assert.equal(evaluate(`window.__wslCalls.filter(c=>c.name==='EnableWSLUnifiedSupply').length`), 1);
  passed('明确点击一次授权后统一管理状态刷新，无需逐项导入');

  click('停止自动纳入新项');
  wait(`window.__wslCalls.some(c=>c.name==='DisableWSLUnifiedSupply') && document.body.textContent.includes('启用统一管理')`);
  passed('停用入口可达，状态恢复且不调用逐项撤回');

  click('升级');
  wait(`window.__wslCalls.some(c=>c.name==='UpdateCLIInWSL') && document.querySelector('[role=status]')?.textContent`);
  assert.equal(evaluate(`window.__wslCalls.filter(c=>c.name==='UpdateCLIInWSL' && c.args[0]==='claude').length`), 1);
  assert.equal(evaluate(`window.__wslCalls.filter(c=>c.name==='InstallCLIToWSL').length`), 0);
  passed('已安装 CLI 升级调用 UpdateCLIInWSL，不再幂等安装短路');

  click('安装到 WSL');
  wait(`window.__wslCalls.some(c=>c.name==='InstallCLIToWSL' && c.args[0]==='omp')`);
  passed('OMP 未安装卡片可从真实按钮发起安装');

  open('enable-failure');
  click('启用统一管理');
  wait(`Array.from(document.querySelectorAll('[role=status]')).some(e=>e.textContent.includes('TEST_ONLY_CONSENT_FAILURE'))`);
  assert.equal(evaluate(`document.body.textContent.includes('启用统一管理')`), true);
  passed('部分授权失败显示原错，不假报统一管理完成');

  open('upgrade-failure');
  click('升级');
  wait(`Array.from(document.querySelectorAll('[role=status]')).some(e=>e.textContent.includes('TEST_ONLY_UPGRADE_FAILURE'))`);
  passed('WSL 升级失败从界面可见');
  console.log(JSON.stringify({ passed: results.length, results }, null, 2));
} finally {
  browser('close');
}
