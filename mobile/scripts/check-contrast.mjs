#!/usr/bin/env node
/**
 * check-contrast.mjs — M0-04 VT 令牌对比度门禁（CI: Check contrast ratios）
 *
 * 权威来源：P4 视觉风格 v2.2 §5.1 / §6.3 / §8.1（正文 ≥4.5:1 不可削弱）。
 *
 * 行为：
 *  0. REQUIRED_TOKENS schema（独立于 pair 表）：P4 冻结的 29 个 token
 *     （18 light semantic + 9 ANSI spec + 2 legacy bridge）必须先验证——
 *     缺失 / 意外声明 / 值偏离冻结值一律 FAIL，与该 token 是否进入 pair 无关。
 *  1. 从 mobile/src/styles/tokens.css 解析 --VT-* 声明；
 *     重复声明一律 FAIL；冻结 token 只接受不透明 6 位 #RRGGBB，
 *     明确拒绝 3/4/8 位 hex、命名色、transparent/var()/color-mix() 等
 *     （alpha 不静默丢弃、不按不透明色计算）。
 *  2. WCAG 相对亮度（sRGB 线性化）逐对实算对比度，按下表分类与阈值判定。
 *  3. color-mix() 动态表达式不假装解析：扫描 mobile/src 实际出现处，
 *     输出 EXCLUDED 数量/文件清单，归 M4 computed-style 浏览器复核，不计入失败。
 *
 * 退出码：0 = 0 失败；1 = 任一 pair 不达标或令牌表非法。
 *
 * 无第三方依赖（纯 Node ESM）。从 mobile/ 目录运行：
 *   node scripts/check-contrast.mjs
 */

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const MOBILE_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const TOKENS_PATH = join(MOBILE_ROOT, 'src/styles/tokens.css')
const SRC_ROOT = join(MOBILE_ROOT, 'src')

/* ------------------------------------------------------------------ *
 * 1. tokens.css 解析：--name: #hex; 声明表
 *    非法情形一律 FAIL：重复声明、非法 hex 值
 * ------------------------------------------------------------------ */

const OPAQUE_HEX_RE = /^#[0-9a-fA-F]{6}$/
const DECL_RE = /--([A-Za-z0-9-]+)\s*:\s*([^;]+);/g

/* ------------------------------------------------------------------ *
 * REQUIRED_TOKENS：P4 v2.2 冻结令牌 schema（独立于下方 pair 表）
 * 覆盖当前 tokens.css 全部 29 个冻结声明（与首审逐值通过事实一致）：
 *   18 个 §5.1 light semantic + 9 个 §6.3 ANSI spec + 2 个 legacy bridge。
 * 每个条目含冻结值：缺失 / 意外声明 / 值偏离均 FAIL，不依赖 pair 引用。
 * ------------------------------------------------------------------ */

const REQUIRED_TOKENS = [
  /* ---- §5.1 light semantic（18） ---- */
  ['--VT-canvas', '#FAF9F5'],
  ['--VT-surface', '#F4F3EE'],
  ['--VT-surface-raised', '#EFE9DE'],
  ['--VT-surface-dark', '#1F1E1B'],
  ['--VT-border', '#E6DFD8'],
  ['--VT-border-strong', '#8E8B82'],
  ['--VT-text', '#252523'],
  ['--VT-text-secondary', '#6C6A64'],
  ['--VT-text-disabled', '#8E8B82'],
  ['--VT-accent', '#C15F3C'],
  ['--VT-accent-strong', '#A9583E'],
  ['--VT-success', '#2F7D46'],
  ['--VT-warning', '#8A5A12'],
  ['--VT-danger', '#BC3F3F'],
  ['--VT-control', '#33607D'],
  ['--VT-secondary', '#6C6A64'],
  ['--VT-gap', '#6C6A64'],
  ['--VT-on-dark', '#FAF9F5'],
  /* ---- §6.3 ANSI spec（9，bright 系具体值后置 M2-D） ---- */
  ['--VT-ansi-foreground', '#FAF9F5'],
  ['--VT-ansi-bright-foreground', '#FFFFFF'],
  ['--VT-ansi-black', '#5A564F'],
  ['--VT-ansi-red', '#E07A5F'],
  ['--VT-ansi-green', '#7FBF8E'],
  ['--VT-ansi-yellow', '#D9A441'],
  ['--VT-ansi-blue', '#7FA8D9'],
  ['--VT-ansi-magenta', '#C48BC0'],
  ['--VT-ansi-cyan', '#6FBDB3'],
  /* ---- legacy on-dark bridge（2，M2 视觉迁移后退役） ---- */
  ['--VT-legacy-on-dark-muted', '#8b949e'],
  ['--VT-legacy-on-dark-border', '#6e7681'],
]

function parseTokens(path) {
  const text = readFileSync(path, 'utf8')
  // 去注释，避免注释内的示例值被误解析为声明
  const stripped = text.replace(/\/\*[\s\S]*?\*\//g, '')
  const tokens = new Map()
  const errors = []
  let m
  while ((m = DECL_RE.exec(stripped)) !== null) {
    const name = `--${m[1]}`
    const value = m[2].trim()
    if (tokens.has(name)) {
      errors.push(`duplicate declaration: ${name}`)
      continue
    }
    if (!OPAQUE_HEX_RE.test(value)) {
      errors.push(
        `INVALID-OPAQUE-COLOR: illegal value for ${name}: "${value}"` +
          ` (frozen tokens must be opaque 6-digit #RRGGBB; 3/4/8-digit hex, named colors, transparent/var()/color-mix() rejected)`
      )
      continue
    }
    tokens.set(name, value)
  }
  return { tokens, errors }
}

/* ------------------------------------------------------------------ *
 * 2. WCAG 对比度算法（sRGB 线性化 + 相对亮度）
 * ------------------------------------------------------------------ */

function hexToRgb(hex) {
  // 仅 6 位不透明 hex 可达此处（OPAQUE_HEX_RE 已在解析与 pair 字面量处把关）
  const n = parseInt(hex.slice(1), 16)
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff]
}

function linearize(channel) {
  const c = channel / 255
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

function relativeLuminance(hex) {
  const [r, g, b] = hexToRgb(hex).map(linearize)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrastRatio(fgHex, bgHex) {
  const l1 = relativeLuminance(fgHex)
  const l2 = relativeLuminance(bgHex)
  const [lighter, darker] = l1 >= l2 ? [l1, l2] : [l2, l1]
  return (lighter + 0.05) / (darker + 0.05)
}

/* ------------------------------------------------------------------ *
 * 3. pair 表（P4 §5.1/§6.3 权威组合；fg/bg 为令牌名或字面 hex）
 *    category: normal(≥4.5) | large(≥3.0) | ui(≥3.0) | decorative(登记不判) | ansi(≥4.5)
 * ------------------------------------------------------------------ */

const CANVAS = '--VT-canvas'
const SURFACE = '--VT-surface'
const RAISED = '--VT-surface-raised'
const SURFACE_DARK = '--VT-surface-dark'
const WHITE = '#FFFFFF'
// 当前 dark legacy surface 实测底色（style.css:10 / 卡片），bridge 令牌专用背景
const LEGACY_BG = '#0d1117'
const LEGACY_CARD = '#161b22'

const PAIRS = [
  /* ---- 正文 normal（阈值 ≥4.5:1，P4 §8.1 不可削弱） ---- */
  { fg: '--VT-text', bg: CANVAS, cat: 'normal', note: '主文本 on canvas（P4 14.58:1）' },
  { fg: '--VT-text', bg: SURFACE, cat: 'normal', note: '主文本 on surface（P4 13.82:1）' },
  { fg: '--VT-text', bg: RAISED, cat: 'normal', note: '主文本 on raised（P4 12.71:1）' },
  { fg: '--VT-text-secondary', bg: CANVAS, cat: 'normal', note: '次要文本 on canvas（P4 5.13:1）' },
  { fg: '--VT-text-secondary', bg: SURFACE, cat: 'normal', note: '次要文本 on surface（P4 4.87:1）' },
  { fg: '--VT-accent-strong', bg: CANVAS, cat: 'normal', note: '链接/小号强调 on canvas（P4 4.80:1）' },
  { fg: '--VT-accent-strong', bg: SURFACE, cat: 'normal', note: '链接/小号强调 on surface（P4 4.55:1）' },
  { fg: '--VT-success', bg: CANVAS, cat: 'normal', note: '成功语义文本 on canvas（P4 4.81:1）' },
  { fg: '--VT-success', bg: SURFACE, cat: 'normal', note: '成功语义文本 on surface（P4 4.56:1）' },
  { fg: '--VT-warning', bg: CANVAS, cat: 'normal', note: '警告语义文本 on canvas（P4 5.61:1）' },
  { fg: '--VT-warning', bg: SURFACE, cat: 'normal', note: '警告语义文本 on surface（P4 5.32:1）' },
  { fg: '--VT-warning', bg: RAISED, cat: 'normal', note: '警告语义文本 on raised（P4 4.89:1）' },
  { fg: '--VT-danger', bg: CANVAS, cat: 'normal', note: '危险语义文本 on canvas（P4 5.08:1）' },
  { fg: '--VT-danger', bg: SURFACE, cat: 'normal', note: '危险语义文本 on surface（P4 4.82:1）' },
  { fg: '--VT-control', bg: CANVAS, cat: 'normal', note: '控制权信号 on canvas（P4 6.41:1）' },
  { fg: '--VT-control', bg: SURFACE, cat: 'normal', note: '控制权信号 on surface（P4 6.08:1）' },
  { fg: '--VT-control', bg: RAISED, cat: 'normal', note: '控制权信号 on raised（P4 5.59:1）' },
  { fg: WHITE, bg: '--VT-accent-strong', cat: 'normal', note: '主按钮白字 on accent-strong（P4 5.06:1）' },
  { fg: WHITE, bg: '--VT-control', cat: 'normal', note: '控制徽章白字 on control（P4 6.75:1）' },
  { fg: '--VT-on-dark', bg: SURFACE_DARK, cat: 'normal', note: '终端正文 on surface-dark（P4 15.9:1）' },
  // legacy dark bridge（临时，M2 视觉迁移后随令牌退役）
  { fg: '--VT-legacy-on-dark-muted', bg: LEGACY_BG, cat: 'normal', note: 'bridge 正文/图标 on #0d1117（实测 6.15:1）' },
  { fg: '--VT-legacy-on-dark-muted', bg: LEGACY_CARD, cat: 'normal', note: 'bridge 正文/图标 on #161b22（实测 5.62:1）' },

  /* ---- 大字号 / 图标 / 控件边界（阈值 ≥3:1） ---- */
  { fg: '--VT-text-secondary', bg: RAISED, cat: 'large', note: '次要文本 on raised 4.48:1——仅 ≥18px/14px bold，禁小号正文' },
  { fg: '--VT-accent', bg: CANVAS, cat: 'ui', note: 'accent 图形/焦点环 on canvas（P4 4.01:1）' },
  { fg: '--VT-accent', bg: SURFACE, cat: 'ui', note: 'accent 图形/图标 on surface（P4 3.80:1）' },
  { fg: WHITE, bg: '--VT-accent', cat: 'large', note: '白字 on accent 4.23:1——仅大字号粗体（≥3:1），小号走 accent-strong' },
  { fg: '--VT-accent-strong', bg: RAISED, cat: 'large', note: 'accent-strong on raised 4.19:1——仅大字号粗体' },
  { fg: '--VT-success', bg: RAISED, cat: 'large', note: 'success on raised 4.20:1——仅图标+text 或大字号粗体' },
  { fg: '--VT-danger', bg: RAISED, cat: 'large', note: 'danger on raised 4.43:1——仅大字号粗体或图标+text' },
  { fg: '--VT-border-strong', bg: CANVAS, cat: 'ui', note: '控件识别边界 on canvas（P4 3.23:1）' },
  { fg: '--VT-border-strong', bg: SURFACE, cat: 'ui', note: '控件识别边界 on surface（P4 3.07:1）' },
  { fg: '--VT-legacy-on-dark-border', bg: LEGACY_BG, cat: 'ui', note: 'bridge 控件边界 on #0d1117（实测 4.12:1）' },

  /* ---- 装饰豁免（显式登记，不触发 FAIL） ---- */
  { fg: '--VT-border', bg: CANVAS, cat: 'decorative', note: '装饰性 hairline，不承担信息边界（P4 §5.1 仅装饰）' },
  { fg: '--VT-ansi-black', bg: SURFACE_DARK, cat: 'decorative', note: 'ANSI black 2.29:1——仅反衬底/边框，不承载正文（P4 §6.3）' },

  /* ---- ANSI 色板 on VT-surface-dark（P4 §6.3，阈值 ≥4.5:1，black 已豁免见上） ---- */
  { fg: '--VT-ansi-foreground', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI normal 前景（P4 15.82:1）' },
  { fg: '--VT-ansi-bright-foreground', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI bold/bright 前景（P4 16.67:1）' },
  { fg: '--VT-ansi-red', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI red（P4 5.65:1）' },
  { fg: '--VT-ansi-green', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI green（P4 7.74:1）' },
  { fg: '--VT-ansi-yellow', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI yellow（P4 7.41:1）' },
  { fg: '--VT-ansi-blue', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI blue（P4 6.75:1）' },
  { fg: '--VT-ansi-magenta', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI magenta（P4 6.19:1）' },
  { fg: '--VT-ansi-cyan', bg: SURFACE_DARK, cat: 'ansi', note: 'ANSI cyan（P4 7.62:1）' },
]

const THRESHOLDS = { normal: 4.5, large: 3.0, ui: 3.0, ansi: 4.5 } // decorative 无阈值，仅登记

/* ------------------------------------------------------------------ *
 * 4. color-mix() 扫描：不解析，只登记 EXCLUDED（归 M4 computed-style）
 * ------------------------------------------------------------------ */

const SCAN_EXT = new Set(['.vue', '.css', '.ts'])

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry)
    const st = statSync(p)
    if (st.isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      walk(p, out)
    } else if (SCAN_EXT.has(p.slice(p.lastIndexOf('.')))) {
      out.push(p)
    }
  }
  return out
}

function scanColorMix() {
  const files = []
  let count = 0
  for (const file of walk(SRC_ROOT)) {
    const text = readFileSync(file, 'utf8')
    const hits = text.match(/color-mix\(/g)
    if (hits) {
      count += hits.length
      files.push(file.slice(SRC_ROOT.length + 1))
    }
  }
  return { count, files }
}

/* ------------------------------------------------------------------ *
 * 5. 主流程
 * ------------------------------------------------------------------ */

const { tokens, errors: tokenErrors } = parseTokens(TOKENS_PATH)

let pass = 0
let fail = 0
let decorative = 0

console.log(`tokens: parsed ${tokens.size} declarations from src/styles/tokens.css`)
for (const e of tokenErrors) {
  console.log(`TOKEN-ERROR: ${e}: FAIL`)
  fail++
}

/* REQUIRED_TOKENS schema 验证：独立于 pair 表，缺失/意外/值偏离均 FAIL */
const requiredMap = new Map(REQUIRED_TOKENS)
for (const [reqName, reqValue] of REQUIRED_TOKENS) {
  const declared = tokens.get(reqName)
  if (declared === undefined) {
    console.log(`MISSING-TOKEN: required P4 token ${reqName} is not declared in src/styles/tokens.css: FAIL`)
    fail++
  } else if (declared.toLowerCase() !== reqValue.toLowerCase()) {
    console.log(`VALUE-MISMATCH: ${reqName} declared "${declared}" but frozen P4 value is "${reqValue}": FAIL`)
    fail++
  }
}
for (const name of tokens.keys()) {
  if (!requiredMap.has(name)) {
    console.log(`UNEXPECTED-TOKEN: ${name} is not in the frozen P4 REQUIRED_TOKENS schema (${REQUIRED_TOKENS.length} entries): FAIL`)
    fail++
  }
}
const verifiedCount = REQUIRED_TOKENS.filter(
  ([n, v]) => tokens.get(n)?.toLowerCase() === v.toLowerCase()
).length
console.log(`required tokens: ${verifiedCount}/${REQUIRED_TOKENS.length} present with frozen values`)

function resolveHex(ref, errors) {
  if (ref.startsWith('--')) {
    const v = tokens.get(ref)
    if (v === undefined) {
      errors.push(`undefined token referenced: ${ref}`)
      return null
    }
    return v
  }
  if (!OPAQUE_HEX_RE.test(ref)) {
    errors.push(`illegal literal hex in pair table: ${ref}`)
    return null
  }
  return ref
}

for (const pair of PAIRS) {
  const errs = []
  const fgHex = resolveHex(pair.fg, errs)
  const bgHex = resolveHex(pair.bg, errs)
  const label = `${pair.fg} on ${pair.bg}`
  if (errs.length > 0) {
    for (const e of errs) console.log(`${label}: FAIL (${e}) [${pair.cat}]`)
    fail++
    continue
  }
  const ratio = contrastRatio(fgHex, bgHex)
  const r = ratio.toFixed(2)
  if (pair.cat === 'decorative') {
    console.log(`${label}: ${r}:1 DECORATIVE-EXEMPT (${pair.note})`)
    decorative++
    continue
  }
  const threshold = THRESHOLDS[pair.cat]
  const ok = ratio >= threshold
  console.log(`${label}: ${r}:1 ${ok ? 'PASS' : 'FAIL'} (阈值 ${threshold}:1, 分类 ${pair.cat})${ok ? '' : ` — ${pair.note}`}`)
  if (ok) pass++
  else fail++
}

const mix = scanColorMix()
console.log(
  `EXCLUDED: ${mix.count} color-mix() expressions in ${mix.files.length} files` +
    ` — require browser computed-style verification (M4): ${mix.files.join(', ')}`
)

const total = pass + fail + decorative
console.log(`总计 ${total} 对（含登记），通过 ${pass} 对，失败 ${fail} 对，装饰豁免登记 ${decorative} 对`)

process.exit(fail > 0 ? 1 : 0)
