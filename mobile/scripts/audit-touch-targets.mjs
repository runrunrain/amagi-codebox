#!/usr/bin/env node
/**
 * audit-touch-targets.mjs — M4-A 44px 触控目标全量静态审计（可重复）
 * ---------------------------------------------------------------------------
 * 职责：枚举 mobile/src 全部 .vue 模板中的可交互元素
 * （button / a[href] / input（含 checkbox/radio，排除 hidden）/ select /
 * textarea / summary / [role=button|menuitem|tab|
 * link|switch|checkbox|option] / 带 @click 的非交互标签），并交叉引用
 * <style> 中对应 class 的 min-height/height/padding 声明，给出三级判定：
 *   · pass    — 声明 min-height ≥44px（或 height ≥44px）
 *   · risk    — 声明了固定 height/min-height <44px，或无高度声明（需运行时复核）
 *   · exempt  — 命中 EXEMPTIONS 注册表（须附豁免理由；运行时同样豁免）
 *
 * R1 修复（谛听 M4-002 回流）：
 *   · 移除 EX-01 按文件整页豁免；EXEMPTIONS 仅保留机制，逐元素精确到 class。
 *   · 解析器三处缺陷修复（注释误报 / quoted '>' 截断 attrs / line-height 误判）。
 *
 * R2 修复（谛听 M4-002 Round2 回流：嵌套 template 截断）：
 *   · 弃用正则提取 SFC 外层 <template>——Vue 合法嵌套 <template v-if/v-for>
 *     会让非贪婪正则在首个内层 </template> 处截断，R2 实测漏枚举 27 项
 *     （96 vs 独立 Vue AST 123，7 文件：ConnectPage 9 / ComposerBar 5 /
 *     SessionsPage 5 / ControlBar 3 / QrScannerPanel 2 / ContinuityBanner 2 /
 *     TimelineView 1）。
 *   · 改为双实现交叉验证：
 *     ① 主枚举：@vue/compiler-sfc 解析 SFC 块 + @vue/compiler-dom baseParse
 *        模板 AST 递归遍历（嵌套 template 由编译器原生处理，无截断语义）；
 *     ② 独立枚举：compiler-sfc 提取完整 template 内容后，栈式 tokenizer
 *        扫描（与主枚举不同代码路径，作为交叉 oracle）；
 *     两实现枚举 identity 不一致 → gate FAIL（解析器回归不得静默通过）。
 *   · oracle 回归：audit-touch-targets.oracle.json 冻结 R2 时点的 123 项
 *     identity（由谛听独立 AST 枚举对齐生成）；模板有意变更后须重新独立
 *     验证并 --write-oracle 更新，否则 gate FAIL 提示枚举漂移。
 *
 * R3 修复（谛听 M4-R3-001 回流：双实现共享 decideKind 策略盲点）：
 *   · checkbox/radio 是原生可交互触控目标，不再与 hidden 同等排除——
 *     枚举为 kind=input:checkbox / input:radio；<summary>（details 原生
 *     披露控件）同步纳入。hidden input 仍排除。
 *   · 补全后真实页面核查：SettingsPage Auto Start 自定义开关的原生
 *     checkbox（视觉隐藏、旧实现 0×0）修复为覆盖整个 44×44 触控面的
 *     真实 hit area（见该文件 .toggle/.toggle-input 样式）。
 *   · 负向探针门：audit-touch-targets.selftest.mjs 在临时副本注入
 *     <44px checkbox/radio 与 ≥44px checkbox，断言 --gate 均 exit 1
 *     （risk+oracle 漂移 / 纯 oracle 漂移），证明枚举与门禁不盲。
 *   · 新增 --root <dir>：指定被审 mobile 根（自检临时副本用；默认脚本
 *     上级目录，行为不变）。
 *
 * 定位：静态盘点 + 整改跟踪清单（入库可重复，CI 可门禁 --gate）。
 * 权威判定以运行时测量为准（e2e/a11y-m4.spec.ts 在真实 Chromium 中逐元素
 * 量 bounding box）；本脚本不替代运行时证据，负责让枚举无遗漏、整改可跟踪。
 *
 * 用法：
 *   node mobile/scripts/audit-touch-targets.mjs [--json <path>] [--gate]
 *                                              [--write-oracle] [--root <dir>]
 *   --gate：risk（非豁免）项 >0、双实现枚举不一致、或与 oracle 漂移时退出码 1。
 *   --write-oracle：以当前主枚举重写 oracle（仅限模板有意变更后使用）。
 * ---------------------------------------------------------------------------
 */
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse as parseSfc } from '@vue/compiler-sfc'
import { baseParse } from '@vue/compiler-dom'

const HERE = fileURLToPath(new URL('.', import.meta.url))
const cliArgs = process.argv.slice(2)
// --root <dir>：被审 mobile 根（自检/临时副本审计用）；默认脚本上级目录。
const rootIdx = cliArgs.indexOf('--root')
const ROOT = rootIdx !== -1 && cliArgs[rootIdx + 1] ? resolve(process.cwd(), cliArgs[rootIdx + 1]) : join(HERE, '..')
const SRC = join(ROOT, 'src')
const ORACLE_PATH = join(ROOT, 'scripts', 'audit-touch-targets.oracle.json')
const MIN_TARGET = 44

/**
 * 豁免注册表（豁免 ≠ 不管：每项须有理由；运行时测量同表豁免）。
 * selector 形如 '文件相对路径#class片段'，子串匹配。
 * R1：整页豁免已移除（谛听 M4-002：可达页面按文件名豁免全部元素不成立）。
 * 后续新增条目必须逐元素精确到 class 片段，并在理由中给出可机器复核的
 * 运行时证据关联；禁止 '文件#' 形式的整页条目。
 * 类别：
 *   inline-text  — 文本内联元素（WCAG 2.5.5 target-size inline 例外）
 *   native-equiv — 原生等效控件（系统级滚动条/终端网格等非自定控件）
 */
const EXEMPTIONS = [
  // 当前无豁免项：全部可交互元素静态声明 ≥44px（pass）或整改闭环。
]

const INTERACTIVE_ROLES = new Set(['button', 'menuitem', 'tab', 'link', 'switch', 'checkbox', 'option', 'radio'])

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (name.endsWith('.vue')) out.push(p)
  }
  return out
}

function lineOf(content, index) {
  return content.slice(0, index).split('\n').length
}

/** 元素是否纳入枚举（button/a[href]/input/select/textarea/summary/交互 role/@click）。
 *  R3（谛听 M4-R3-001）：checkbox/radio 是原生可交互触控目标，不得与
 *  hidden 同等排除——枚举为 input:checkbox / input:radio；仅 hidden 排除。 */
function decideKind(tag, { role, hasClick, hasHref, inputType }) {
  const lower = tag.toLowerCase()
  const isNative = ['button', 'a', 'input', 'select', 'textarea', 'summary'].includes(lower)
  let kind = null
  if (lower === 'input' && ['checkbox', 'radio'].includes(inputType)) kind = `input:${inputType}`
  else if (isNative) kind = lower
  else if (role && INTERACTIVE_ROLES.has(role)) kind = `role:${role}`
  else if (hasClick) kind = '@click'
  if (!kind) return null
  if (lower === 'a' && !hasHref && !hasClick) return null
  if (lower === 'input' && inputType === 'hidden') return null
  return kind
}

// ---------------------------------------------------------------------------
// 主枚举：Vue 编译器 AST（嵌套 template 由编译器原生处理）
// ---------------------------------------------------------------------------
function enumerateAst(templateContent, templateStartLine, relFile) {
  const items = []
  const root = baseParse(templateContent)
  const visit = (node) => {
    if (node.type === 1 /* NodeTypes.ELEMENT */) {
      const props = node.props ?? []
      let classes = ''
      let role = ''
      let inputType = ''
      let hasClick = false
      let hasHref = false
      let disabled = false
      for (const p of props) {
        if (p.type === 6 /* NodeTypes.ATTRIBUTE */) {
          if (p.name === 'class') classes = p.value?.content ?? ''
          else if (p.name === 'role') role = p.value?.content ?? ''
          else if (p.name === 'type') inputType = p.value?.content ?? ''
          else if (p.name === 'href') hasHref = true
          else if (p.name === 'disabled') disabled = true
        } else if (p.type === 7 /* NodeTypes.DIRECTIVE */) {
          if (p.name === 'on' && p.arg?.type === 4 && p.arg.content === 'click') hasClick = true
          else if (p.name === 'bind' && p.arg?.type === 4 && p.arg.content === 'href') hasHref = true
          else if (p.name === 'bind' && p.arg?.type === 4 && p.arg.content === 'disabled') {
            if (p.exp?.type === 4 && p.exp.content === 'true') disabled = true
          }
        }
      }
      const tag = node.tag.toLowerCase()
      const kind = decideKind(tag, { role, hasClick, hasHref, inputType })
      if (kind) {
        const label =
          props.find((p) => p.type === 6 && p.name === 'aria-label')?.value?.content ??
          props.find((p) => p.type === 6 && p.name === 'placeholder')?.value?.content ??
          ''
        items.push({
          file: relFile,
          line: templateStartLine + node.loc.start.line - 1,
          tag,
          kind,
          classes,
          label,
          disabled,
          role,
          click: hasClick,
        })
      }
      for (const child of node.children ?? []) visit(child)
    } else if (node.type === 11 /* NodeTypes.FOR */ || node.type === 9 /* NodeTypes.IF */) {
      // v-if/v-for 表达式节点：枚举其静态分支下的全部元素（条件分支同样须 44px）。
      if (node.type === 9) for (const branch of node.branches) for (const c of branch.children) visit(c)
      else for (const c of node.children) visit(c)
    } else if (node.type === 0 /* ROOT */) {
      for (const c of node.children) visit(c)
    }
  }
  visit(root)
  return items
}

// ---------------------------------------------------------------------------
// 独立枚举：栈式 tokenizer（不同代码路径，作为交叉 oracle）。
// 模板内容取自 compiler-sfc（含全部嵌套 template，无截断）；
// 注释先剥离，防止注释文本中的标签字面量误报（SessionCard 整改注释教训）。
// ---------------------------------------------------------------------------
function enumerateTokenizer(templateContent, templateStartLine, relFile) {
  const items = []
  const source = templateContent.replace(/<!--[\s\S]*?-->/g, '')
  // 属性串允许 quoted 值内含 '>'（如 v-if="list.length > 0"）：quoted 段优先匹配。
  const tagRe = /<(button|a|input|select|textarea|[a-z][a-z0-9-]*)((?:[^<>"']|"[^"]*"|'[^']*')*)(\/?)>/gis
  let m
  while ((m = tagRe.exec(source)) !== null) {
    const [, tag, attrs = ''] = m
    const lower = tag.toLowerCase()
    const role = attrs.match(/role="([^"]+)"/)?.[1] ?? ''
    const hasClick = /@click|v-on:click/.test(attrs)
    const hasHref = /:?\bhref\b/.test(attrs)
    const inputType = attrs.match(/type="([^"]+)"/)?.[1] ?? ''
    const disabled = /\sdisabled[\s=>]|\s:disabled="true"/.test(attrs)
    const kind = decideKind(lower, { role, hasClick, hasHref, inputType })
    if (!kind) continue
    const classes = attrs.match(/class="([^"]+)"/)?.[1] ?? ''
    const label =
      attrs.match(/aria-label="([^"]+)"/)?.[1] ??
      attrs.match(/placeholder="([^"]+)"/)?.[1] ??
      ''
    items.push({
      file: relFile,
      line: templateStartLine + lineOf(source, m.index) - 1,
      tag: lower,
      kind,
      classes,
      label,
      disabled,
      role,
      click: hasClick,
    })
  }
  return items
}

/** 枚举 identity（多重集 key）：文件+标签+class+role+click；行号不参与
 *  （注释剥离/实现差异会漂移行号，identity 语义不含行号）。 */
function identityOf(item) {
  return `${item.file}|${item.tag}|${item.classes}|role=${item.role}|click=${item.click}`
}

function toMultiset(items) {
  const map = new Map()
  for (const i of items) map.set(identityOf(i), (map.get(identityOf(i)) ?? 0) + 1)
  return map
}

function diffMultisets(a, b) {
  const missing = [] // 在 b 不在 a（或数量不足）
  const extra = [] // 在 a 不在 b（或数量超出）
  for (const [k, n] of b) if ((a.get(k) ?? 0) < n) missing.push(`${k} ×${n - (a.get(k) ?? 0)}`)
  for (const [k, n] of a) if ((b.get(k) ?? 0) < n) extra.push(`${k} ×${n - (b.get(k) ?? 0)}`)
  return { missing, extra }
}

/** 解析 <style>：class → 高度相关声明（min-height/height/padding）。 */
function parseStyles(styleBlocks) {
  const css = styleBlocks.join('\n')
  const classMap = new Map()
  const ruleRe = /([^{}]+)\{([^{}]*)\}/g
  let m
  while ((m = ruleRe.exec(css)) !== null) {
    const selectors = m[1].split(',').map((s) => s.trim())
    const body = m[2]
    const decl = {}
    // 负向回顾：line-height 不得命中 height；padding-top/-bottom 优先于 padding。
    const declRe = /(?<![\w-])(min-height|height|padding(?:-top|-bottom)?)\s*:\s*([^;]+)/g
    let d
    while ((d = declRe.exec(body)) !== null) decl[d[1]] = d[2].trim()
    if (Object.keys(decl).length === 0) continue
    for (const sel of selectors) {
      const cls = sel.match(/\.([a-zA-Z0-9_-]+)/)?.[1]
      if (!cls) continue
      classMap.set(cls, { ...classMap.get(cls), ...decl })
    }
  }
  return classMap
}

function pxOf(value) {
  if (!value) return null
  const m = String(value).match(/([\d.]+)px/)
  return m ? Number(m[1]) : null
}

/** 单元素判定：取所有 class 中最有利的高度证据。 */
function classify(item, classMap) {
  const exemption = EXEMPTIONS.find((e) => `${item.file}#${item.classes}`.includes(e.match.replace(/#$/, '')))
  // 全屏遮罩/背景点击层（dismiss overlay/backdrop）：目标尺寸=整个视口，
  // 天然 ≥44px——归 pass 并注明语义，不进入 risk 清单噪声。
  if (/overlay|backdrop/.test(item.classes) && !exemption) {
    return { verdict: 'pass', evidence: { cls: item.classes, note: '全屏 dismiss 层' }, exemption: null }
  }
  let best = null
  for (const cls of item.classes.split(/\s+/).filter(Boolean)) {
    const decl = classMap.get(cls)
    if (!decl) continue
    const minH = pxOf(decl['min-height'])
    const h = pxOf(decl.height)
    const candidate = { cls, minH, h, decl }
    if (!best || (candidate.minH ?? candidate.h ?? -1) > (best.minH ?? best.h ?? -1)) best = candidate
  }
  if (best) {
    const eff = best.minH ?? best.h
    if (eff !== null && eff >= MIN_TARGET) return { verdict: exemption ? 'exempt' : 'pass', evidence: best, exemption }
    if (eff !== null && eff < MIN_TARGET) return { verdict: exemption ? 'exempt' : 'risk', evidence: best, exemption }
  }
  // 无高度声明：input/textarea 由字体+padding 撑开；button 未知 → risk（待运行时复核）
  return { verdict: exemption ? 'exempt' : 'risk', evidence: best, exemption }
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------
const files = walk(SRC)
const all = []
const crossErrors = []
for (const file of files) {
  const content = readFileSync(file, 'utf8')
  const rel = relative(ROOT, file)
  const { descriptor, errors } = parseSfc(content, { filename: rel })
  if (errors.length > 0) {
    crossErrors.push(`${rel}: compiler-sfc 解析错误 ${errors.map((e) => e.message).join('; ')}`)
    continue
  }
  if (!descriptor.template) continue
  const templateContent = descriptor.template.content
  const templateStartLine = descriptor.template.loc.start.line
  const astItems = enumerateAst(templateContent, templateStartLine, rel)
  const tokenizerItems = enumerateTokenizer(templateContent, templateStartLine, rel)
  // 双实现交叉验证：identity 多重集必须一致。
  const { missing, extra } = diffMultisets(toMultiset(astItems), toMultiset(tokenizerItems))
  if (missing.length > 0 || extra.length > 0) {
    crossErrors.push(
      `${rel}: 双实现枚举不一致 — AST 缺 ${JSON.stringify(missing)} / AST 多 ${JSON.stringify(extra)}`,
    )
  }
  const styles = (descriptor.styles ?? []).map((s) => s.content)
  const classMap = parseStyles(styles)
  for (const item of astItems) {
    all.push({ ...item, ...classify(item, classMap) })
  }
}

const pass = all.filter((i) => i.verdict === 'pass')
const risk = all.filter((i) => i.verdict === 'risk')
const exempt = all.filter((i) => i.verdict === 'exempt')

console.log(`# 44px 触控目标静态审计（M4-A，R2 双实现交叉验证）`)
console.log(`扫描 ${files.length} 个 .vue；可交互元素 ${all.length}：pass ${pass.length} / risk ${risk.length} / exempt ${exempt.length}`)
console.log('')
if (risk.length > 0) {
  console.log('## risk（声明高度 <44px 或无高度声明，需运行时复核/整改）')
  for (const i of risk) {
    console.log(
      `- ${i.file}:${i.line} <${i.tag}> [${i.kind}] .${i.classes.split(/\s+/)[0] ?? ''}` +
        `${i.label ? ` "${i.label}"` : ''}` +
        `${i.evidence ? ` — ${JSON.stringify(i.evidence.decl)}` : ' — 无高度声明'}`,
    )
  }
  console.log('')
}
if (exempt.length > 0) {
  console.log('## exempt（登记豁免）')
  const byId = new Map()
  for (const i of exempt) {
    const key = i.exemption.id
    if (!byId.has(key)) byId.set(key, { ...i.exemption, count: 0 })
    byId.get(key).count += 1
  }
  for (const e of byId.values()) console.log(`- ${e.id}（${e.category}，${e.count} 元素）：${e.reason}`)
}

const args = cliArgs
const jsonIdx = args.indexOf('--json')
if (jsonIdx !== -1 && args[jsonIdx + 1]) {
  writeFileSync(args[jsonIdx + 1], JSON.stringify({ total: all.length, pass, risk, exempt }, null, 2))
  console.log(`\nJSON 已写入 ${args[jsonIdx + 1]}`)
}

if (args.includes('--write-oracle')) {
  const oracle = {
    note: 'R2 冻结的枚举 identity oracle（谛听 M4-002：独立 Vue AST 枚举 123 项对齐生成）。模板有意变更后须重新独立验证并 --write-oracle 更新；gate 以 identity 多重集比对。',
    generatedAt: new Date().toISOString(),
    total: all.length,
    identities: [...toMultiset(all).entries()].flatMap(([k, n]) => Array(n).fill(k)).sort(),
  }
  writeFileSync(ORACLE_PATH, JSON.stringify(oracle, null, 2) + '\n')
  console.log(`\noracle 已写入 ${relative(process.cwd(), ORACLE_PATH)}（${all.length} 项）`)
}

const gateFailures = []
if (crossErrors.length > 0) {
  gateFailures.push(`双实现交叉验证失败 ${crossErrors.length} 处：\n  - ${crossErrors.join('\n  - ')}`)
}
if (args.includes('--gate') || args.includes('--write-oracle')) {
  if (existsSync(ORACLE_PATH) && !args.includes('--write-oracle')) {
    const oracle = JSON.parse(readFileSync(ORACLE_PATH, 'utf8'))
    // oracle 项为 identity 字符串，直接做字符串多重集比对。
    const curSet = toMultiset(all)
    const oracleSet = new Map()
    for (const k of oracle.identities) oracleSet.set(k, (oracleSet.get(k) ?? 0) + 1)
    const missingO = []
    const extraO = []
    for (const [k, n] of oracleSet) if ((curSet.get(k) ?? 0) < n) missingO.push(`${k} ×${n - (curSet.get(k) ?? 0)}`)
    for (const [k, n] of curSet) if ((oracleSet.get(k) ?? 0) < n) extraO.push(`${k} ×${n - (oracleSet.get(k) ?? 0)}`)
    if (missingO.length > 0 || extraO.length > 0) {
      gateFailures.push(
        `枚举与 oracle（${oracle.total} 项，${oracle.generatedAt}）漂移：\n` +
          `  oracle 有而当前缺：${missingO.length > 0 ? JSON.stringify(missingO, null, 2) : '无'}\n` +
          `  当前有而 oracle 无：${extraO.length > 0 ? JSON.stringify(extraO, null, 2) : '无'}\n` +
          `  若为模板有意变更：重新独立验证后运行 --write-oracle 更新。`,
      )
    }
  }
}
if (args.includes('--gate')) {
  if (risk.length > 0) gateFailures.push(`${risk.length} 项 risk 未整改/未豁免`)
  if (gateFailures.length > 0) {
    console.error(`\nGATE FAIL：\n${gateFailures.map((f) => `- ${f}`).join('\n')}`)
    process.exit(1)
  }
}
