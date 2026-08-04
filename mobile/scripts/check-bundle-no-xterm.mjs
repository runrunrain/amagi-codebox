#!/usr/bin/env node
/**
 * scripts/check-bundle-no-xterm.mjs — M2-D 构建产物断言
 * ---------------------------------------------------------------------------
 * 权威依据：P5 v1.2 §9（配对前不加载诊断视图重资源——xterm/addon 动态导入，
 * 仅在进入诊断视图时加载）；Task Contract M2-D「build 断言主 bundle 不含 xterm」。
 * 断言（构建后运行，读取 mobile/dist）：
 *   1. 入口 chunk 剥离 deps-map 字面量后不含 'xterm' 标识——
 *      说明：Vite __vite__mapDeps 会以 "./xterm-*.js" 字符串登记动态导入的
 *      预加载映射（仅在动态 import 触发时使用，非静态引用）；真正被打进主
 *      bundle 的情形是引擎代码内联（含 xterm 标识符），剥离映射字面量后
 *      断言剩余文本无 'xterm' 即可区分两者；
 *   2. 产物中存在独立的 xterm chunk（动态 import 代码分割确已生效）；
 *   3. index.html 不 preload/modulepreload 任何 xterm 资源（首屏不加载）。
 * 退出码：0 全过；1 任一失败。
 * ---------------------------------------------------------------------------
 */
import { readdirSync, readFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const distDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist');
const failures = [];

if (!existsSync(distDir)) {
  console.error(`[xterm-bundle] dist 不存在：${distDir}（请先 npm run build）`);
  process.exit(1);
}

const html = readFileSync(join(distDir, 'index.html'), 'utf8');
const assetsDir = join(distDir, 'assets');
const assets = readdirSync(assetsDir);
const jsChunks = assets.filter((f) => f.endsWith('.js'));
const xtermChunks = jsChunks.filter((f) => /^xterm-/.test(f));

// 断言 2：独立 xterm chunk 存在（代码分割证据）。
if (xtermChunks.length === 0) {
  failures.push('未发现独立 xterm chunk（dist/assets/xterm-*.js）——动态导入未生效或引擎未接入');
}

/** 剥离 Vite deps-map / 动态导入引用的资源字面量（"./<asset>"）。 */
function stripAssetLiterals(content) {
  return content.replace(/["']\.\/[^"']+\.(js|css)["']/g, '""');
}

// 入口 chunk：index.html 内联 script src。
const entryMatches = [...html.matchAll(/<script[^>]+src="([^"]+\.js)"/g)].map((m) => m[1]);
const entryFiles = entryMatches.map((src) => src.split('/').pop());

for (const entry of entryFiles) {
  const content = readFileSync(join(assetsDir, entry), 'utf8');
  // 断言 1：剥离资源字面量后，入口不得含 'xterm'（引擎代码/包名不得内联）。
  const stripped = stripAssetLiterals(content);
  if (/xterm/i.test(stripped)) {
    const idx = stripped.search(/xterm/i);
    failures.push(`入口 chunk ${entry} 内联了 xterm 内容（剥离动态导入映射后仍含标识）：…${stripped.slice(Math.max(0, idx - 40), idx + 40)}…`);
  }
}

// 断言 4：index.html 不预加载 xterm 资源。
for (const asset of assets) {
  if (!/^xterm-/.test(asset)) continue;
  if (html.includes(asset)) {
    failures.push(`index.html 引用/preload 了 xterm 资源 ${asset}——首屏不应加载诊断引擎`);
  }
}

if (failures.length > 0) {
  console.error('[xterm-bundle] FAIL');
  for (const f of failures) console.error(`  ✗ ${f}`);
  process.exit(1);
}
console.log('[xterm-bundle] PASS');
console.log(`  ✓ 入口 chunk（${entryFiles.join(', ') || '无'}）剥离动态导入映射后无 xterm 内容`);
console.log(`  ✓ 独立 xterm chunk：${xtermChunks.join(', ')}`);
console.log('  ✓ index.html 无 xterm 预加载（首屏不加载诊断引擎）');
