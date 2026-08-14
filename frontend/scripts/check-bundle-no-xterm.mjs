#!/usr/bin/env node
/**
 * scripts/check-bundle-no-xterm.mjs — 前端 bundle 瘦身构建断言（S3）
 * ---------------------------------------------------------------------------
 * 移植自 mobile/scripts/check-bundle-no-xterm.mjs，按桌面端产物结构调整。
 * 断言（npm run build 后运行，读取 frontend/dist）：
 *   1. 入口 chunk（index-*.js）剥离 deps-map 资源字面量后不含 'xterm' /
 *      'WebglAddon' / 'element-plus' 标识——终端栈与设置树不得进首屏；
 *   2. 存在独立的 TerminalPageView chunk（路由级懒加载，xterm 栈在其中）、
 *      独立的 addon-webgl chunk（动态 import，仅非 macOS 按需加载）、
 *      独立的 SettingsView chunk（defineAsyncComponent 异步设置页）；
 *   3. index.html 不 preload/modulepreload 上述懒加载 chunk（首屏不加载）。
 * 退出码：0 全过；1 任一失败。
 * ---------------------------------------------------------------------------
 */
import { readdirSync, readFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const distDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist');
const failures = [];

if (!existsSync(distDir)) {
  console.error(`[bundle-check] dist 不存在：${distDir}（请先 npm run build）`);
  process.exit(1);
}

const html = readFileSync(join(distDir, 'index.html'), 'utf8');
const assetsDir = join(distDir, 'assets');
const assets = readdirSync(assetsDir);
const jsChunks = assets.filter((f) => f.endsWith('.js'));

const terminalChunks = jsChunks.filter((f) => /^TerminalPageView-/.test(f));
const webglChunks = jsChunks.filter((f) => /^addon-webgl-/.test(f));
const settingsChunks = jsChunks.filter((f) => /^SettingsView-/.test(f));

// 断言 2：独立懒加载 chunk 存在（代码分割确已生效）。
if (terminalChunks.length === 0) {
  failures.push('未发现独立 TerminalPageView chunk——终端路由懒加载未生效');
}
if (webglChunks.length === 0) {
  failures.push('未发现独立 addon-webgl chunk——WebGL addon 动态 import 未生效');
}
if (settingsChunks.length === 0) {
  failures.push('未发现独立 SettingsView chunk——设置页异步加载未生效');
}

/** 剥离 Vite deps-map / 动态导入引用的资源字面量（"./<asset>" 或 "/assets/<asset>"）。 */
function stripAssetLiterals(content) {
  return content.replace(/["'](?:\.\/|\/assets\/)[^"']+\.(js|css)["']/g, '""');
}

// 入口 chunk：index.html 内联 script src。
const entryMatches = [...html.matchAll(/<script[^>]+src="([^"]+\.js)"/g)].map((m) => m[1]);
const entryFiles = entryMatches.map((src) => src.split('/').pop());

for (const entry of entryFiles) {
  const content = readFileSync(join(assetsDir, entry), 'utf8');
  const stripped = stripAssetLiterals(content);
  // 断言 1：剥离资源字面量后，入口不得内联终端栈 / WebGL / element-plus。
  for (const marker of ['xterm', 'WebglAddon', 'element-plus']) {
    const idx = stripped.indexOf(marker);
    if (idx !== -1) {
      failures.push(
        `入口 chunk ${entry} 内联了 ${marker}（剥离动态导入映射后仍含标识）：…${stripped.slice(Math.max(0, idx - 40), idx + 40)}…`
      );
    }
  }
}

// 断言 3：index.html 不预加载懒加载 chunk。
for (const asset of assets) {
  if (!/^(TerminalPageView-|addon-webgl-|SettingsView-)/.test(asset)) continue;
  if (html.includes(asset)) {
    failures.push(`index.html 引用/preload 了懒加载资源 ${asset}——首屏不应加载`);
  }
}

if (failures.length > 0) {
  console.error('[bundle-check] FAIL');
  for (const f of failures) console.error(`  ✗ ${f}`);
  process.exit(1);
}
console.log('[bundle-check] PASS');
console.log(`  ✓ 入口 chunk（${entryFiles.join(', ') || '无'}）剥离动态导入映射后无 xterm/WebglAddon/element-plus`);
console.log(`  ✓ 独立 chunk：${[...terminalChunks, ...webglChunks, ...settingsChunks].join(', ')}`);
console.log('  ✓ index.html 无懒加载 chunk 预加载（首屏不加载终端栈/设置树）');
