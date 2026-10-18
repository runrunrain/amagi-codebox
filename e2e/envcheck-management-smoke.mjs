// Run the isolated Vite fixture first:
// node frontend/node_modules/vite/bin/vite.js --config e2e/envcheck-management.vite.config.mjs
// Then: node e2e/envcheck-management-smoke.mjs
// Uses real Vue UI with memory-only Wails fixtures, never real install/uninstall/PATH.
import { execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';

const session = `codebox-envcheck-smoke-${process.pid}`;
const base = process.env.ENVCHECK_FIXTURE_URL || 'http://127.0.0.1:5194/e2e/envcheck-management.html';
const results = [];
function browser(...args) {
  const output = execFileSync('agent-browser', ['--session', session, '--json', ...args], {
    encoding: 'utf8', timeout: 35_000, maxBuffer: 1024 * 1024,
  });
  const response = JSON.parse(output);
  if (!response.success) throw new Error(JSON.stringify(response.error));
  return response.data;
}
const evaluate = expression => browser('eval', expression).result;
const wait = expression => browser('wait', '--fn', expression);
function open(scenario, index = 4) {
  browser('open', `${base}?scenario=${scenario}`);
  browser('wait', '.envcheck-tool-item');
  browser('click', `.envcheck-tool-item:nth-child(${index})`);
}
const click = name => browser('find', 'role', 'button', 'click', '--name', name, '--exact');
const buttons = `Array.from(document.querySelectorAll('button'))`;
function passed(name) { results.push(name); console.log(`PASS ${name}`); }

try {
  open('dependency');
  assert.equal(evaluate(`${buttons}.find(b=>b.textContent.trim()==='安装')?.disabled`), true);
  assert.equal(evaluate(`${buttons}.filter(b=>b.textContent.trim()==='安装 Node.js').length`), 1);
  click('安装 Node.js');
  assert.equal(evaluate(`${buttons}.find(b=>b.textContent.trim()==='处理中...')?.disabled`), true);
  wait(`window.__envFixture.dependencyInstalled && ${buttons}.some(b=>b.textContent.trim()==='安装' && !b.disabled)`);
  assert.equal(evaluate(`window.__envCalls.filter(c=>c.name==='RunEnvFixAction' && c.args[0]==='install_node').length`), 1);
  passed('依赖按钮可达，安装按钮遵守canInstall，依赖完成后刷新');

  open('manual');
  click('查看手动命令');
  wait(`document.body.textContent.includes('TEST_ONLY_MANUAL_GUIDANCE')`);
  assert.equal(evaluate(`window.__envCalls.some(c=>c.name==='RunEnvFixAction')`), false);
  passed('手动命令仅展示，不执行系统操作');

  open('path');
  assert.equal(evaluate(`${buttons}.filter(b=>b.textContent.trim()==='修复 PATH').length`), 1);
  evaluate('window.__envFixture.confirmAllowed=false');
  click('修复 PATH');
  assert.equal(evaluate(`window.__envCalls.some(c=>c.name==='RunEnvFixAction')`), false);
  evaluate('window.__envFixture.confirmAllowed=true');
  click('修复 PATH');
  wait(`document.querySelector('.op-result')?.textContent.includes('PATH 已修复') && document.querySelector('.info-grid')?.textContent.includes('PATH 正常')`);
  passed('PATH修复去重、取消零调用、确认后结果与检测状态刷新');

  for (const scenario of ['fast', 'snapshot-error']) {
    open(scenario);
    click('安装');
    wait(`document.querySelector('.op-result-title')?.textContent.includes('安装成功')`);
    assert.match(evaluate(`document.querySelector('.envcheck-tool-item:nth-child(4)').textContent`), /已安装/);
    passed(`${scenario}：即时完成/首次快照失败仍跟踪正确终态`);
  }

  open('failure');
  click('安装');
  wait(`document.querySelector('.op-result-title')?.textContent.includes('安装失败')`);
  assert.match(evaluate(`document.querySelector('.envcheck-tool-item:nth-child(4)').textContent`), /未安装/);
  passed('安装失败不报成功，保留未安装状态');

  open('slow-accept');
  click('安装');
  assert.equal(evaluate(`${buttons}.filter(b=>['安装','安装中...'].includes(b.textContent.trim())).every(b=>b.disabled)`), true);
  wait(`document.querySelector('.op-result-title')?.textContent.includes('安装成功')`);
  assert.equal(evaluate(`window.__envCalls.filter(c=>c.name==='StartInstallToolAsync').length`), 1);
  passed('等待受理期间锁定按钮，防止重复安装');

  for (const [tool, index] of [['opencode', 2], ['codex', 3], ['pi', 4], ['omp', 5]]) {
    open('installed', index);
    click('卸载');
    wait(`document.querySelector('.op-result-title')?.textContent.includes('卸载成功')`);
    assert.equal(evaluate(`window.__envCalls.filter(c=>c.name==='RunEnvFixAction' && c.args[0]==='uninstall_tool' && c.args[1]==='${tool}').length`), 1);
    assert.match(evaluate(`document.querySelector('.envcheck-tool-item:nth-child(${index})').textContent`), /未安装/);
    passed(`${tool} 卸载确认→异步动作→终态→卡片刷新`);
  }
  for (const scenario of ['uninstall-snapshot-error', 'uninstall-fast']) {
    open(scenario);
    click('卸载');
    wait(`document.querySelector('.op-result-title')?.textContent.includes('卸载成功')`);
    assert.match(evaluate(`document.querySelector('.envcheck-tool-item:nth-child(4)').textContent`), /未安装/);
    passed(`${scenario}：卸载受理无operation id时仍追踪终态`);
  }
  for (const scenario of ['uninstall-stale-timeout', 'uninstall-replaced-timeout']) {
    open(scenario);
    click('卸载');
    evaluate('window.__envFixture.advanceTime(46_000)');
    wait(`window.__envCalls.some(c=>c.name==='CheckTool')`);
    // Let the completion watcher and any follow-up snapshot settle, not just
    // the first transient unknown banner (R-2).
    browser('wait', '2200');
    assert.match(evaluate(`document.querySelector('.op-result-title')?.textContent || ''`), /状态未知/);
    assert.equal(evaluate(`document.querySelector('.op-result-title')?.textContent.includes('成功')`), false);
    passed(`${scenario}：超时后旧槽/其他工具终态不能覆盖状态未知`);
  }
  console.log(JSON.stringify({ passed: results.length, results }, null, 2));
} finally {
  browser('close');
}
