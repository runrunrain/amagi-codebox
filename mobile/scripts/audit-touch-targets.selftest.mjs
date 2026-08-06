#!/usr/bin/env node
/**
 * audit-touch-targets.selftest.mjs — 44px 审计负向探针自检（M4-R3）
 * ---------------------------------------------------------------------------
 * 谛听 M4-R3-001：双实现曾共享 decideKind 共同漏掉 checkbox/radio（注入
 * checkbox 两实现同漏、oracle 无漂移、--gate exit 0 假绿）。R3 补全枚举后，
 * 本自检在**临时副本**（不动真实源码/oracle）注入探针控件，证明门禁不盲：
 *
 *   场景 1 baseline      ：未注入副本 --gate 必须 exit 0（副本即真实树快照）。
 *   场景 2 小 checkbox   ：注入 20×20 checkbox → 必须 exit 1
 *                          （risk：<44px 声明 + oracle 枚举漂移双重命中）。
 *   场景 3 小 radio      ：注入 20×20 radio → 必须 exit 1（同场景 2）。
 *   场景 4 达标 checkbox ：注入 ≥44px checkbox → 必须 exit 1
 *                          （纯 oracle 漂移——合法新增控件也不得静默扩张，
 *                           有意变更须重新独立验证 + --write-oracle）。
 *
 * 临时目录清理（M4-R4-001 / R5）：每场景在 finally 中 rmSync 临时副本（成功失败都清）；
 * makeCopy() 初始化失败亦自清（先登记路径再 try/catch，cpSync 抛错先 rmSync 再 re-throw）；
 * 残留门置于最外层 finally，确保 makeCopy/场景同步异常也不会跳过「无新增残留」断言。
 *
 * 用法：node mobile/scripts/audit-touch-targets.selftest.mjs
 * 退出码：0 = 四场景符合预期 且 无临时目录残留；1 = 任一场景不符合 或 有残留。
 * ---------------------------------------------------------------------------
 */
import { cpSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const HERE = fileURLToPath(new URL('.', import.meta.url))
const MOBILE = join(HERE, '..')
const AUDIT = join(HERE, 'audit-touch-targets.mjs')
const PROBE_DIR = join('src', '__selftest_probe__')
const TMP_PREFIX = 'touch-audit-selftest-'

/** 列出 tmpdir 下本自检创建的 touch-audit-selftest-* 目录（绝对路径）。 */
function listSelftestDirs() {
  return readdirSync(tmpdir(), { withFileTypes: true })
    .filter((d) => d.isDirectory() && d.name.startsWith(TMP_PREFIX))
    .map((d) => join(tmpdir(), d.name))
    .sort()
}

/** 复制真实 src + oracle 到临时 mobile 根。
 *  R5（M4-R4-001 残留）：先 mkdtempSync 登记路径，初始化（cpSync/mkdirSync）包入 try/catch——
 *  任一步抛错（src 缺失/I/O/权限）须先 rmSync 已创建 tmp 再 re-throw，杜绝「创建即泄漏」。 */
function makeCopy() {
  const tmp = mkdtempSync(join(tmpdir(), TMP_PREFIX))   // 先登记路径（无论后续是否抛错都纳入清理范围）
  try {
    cpSync(join(MOBILE, 'src'), join(tmp, 'src'), { recursive: true })
    mkdirSync(join(tmp, 'scripts'), { recursive: true })
    cpSync(join(HERE, 'audit-touch-targets.oracle.json'), join(tmp, 'scripts', 'audit-touch-targets.oracle.json'))
    return tmp
  } catch (e) {
    rmSync(tmp, { recursive: true, force: true })   // 初始化失败：清理已创建 tmp 后再抛（不泄漏）
    throw e
  }
}

/** 注入一个独立探针 SFC（新文件，不改动既有模板）。 */
function injectProbe(root, filename, styleDecl) {
  const dir = join(root, PROBE_DIR)
  mkdirSync(dir, { recursive: true })
  writeFileSync(
    join(dir, filename),
    `<template>\n  <input type="${filename.includes('radio') ? 'radio' : 'checkbox'}" class="synthetic-probe" aria-label="selftest probe" />\n</template>\n<style scoped>\n.synthetic-probe { ${styleDecl} }\n</style>\n`,
  )
}

function runGate(root) {
  const r = spawnSync(process.execPath, [AUDIT, '--root', root, '--gate'], { encoding: 'utf-8' })
  return { code: r.status ?? -1, out: `${r.stdout}\n${r.stderr}` }
}

const scenarios = [
  {
    name: 'baseline（未注入副本）',
    inject: null,
    expectCode: 0,
    expectOut: [],
  },
  {
    name: '注入 20×20 checkbox（不达标原生控件）',
    inject: ['SyntheticCheckboxProbe.vue', 'width: 20px; height: 20px;'],
    expectCode: 1,
    expectOut: ['GATE FAIL', 'risk', 'oracle'],
  },
  {
    name: '注入 20×20 radio（不达标原生控件）',
    inject: ['SyntheticRadioProbe.vue', 'width: 20px; height: 20px;'],
    expectCode: 1,
    expectOut: ['GATE FAIL', 'risk', 'oracle'],
  },
  {
    name: '注入 ≥44px checkbox（达标但未经 oracle 冻结的新增）',
    inject: ['SyntheticCheckboxOkProbe.vue', 'width: 44px; min-height: 44px;'],
    expectCode: 1,
    expectOut: ['GATE FAIL', 'oracle'],
  },
]

// M4-R4-001：运行前快照既有 touch-audit-selftest-* 目录，结束后比对新增残留
const beforeDirs = listSelftestDirs()

let failed = 0
try {
  for (const s of scenarios) {
    const root = makeCopy()
    try {
      if (s.inject) injectProbe(root, s.inject[0], s.inject[1])
      const { code, out } = runGate(root)
      const missing = s.expectOut.filter((needle) => !out.includes(needle))
      const ok = code === s.expectCode && missing.length === 0
      console.log(`${ok ? 'PASS' : 'FAIL'}  ${s.name}：exit ${code}（期望 ${s.expectCode}）`)
      if (!ok) {
        failed += 1
        if (missing.length > 0) console.log(`      输出缺少预期片段：${JSON.stringify(missing)}`)
        console.log(`      --- 输出摘录 ---\n${out.split('\n').slice(0, 30).map((l) => `      ${l}`).join('\n')}`)
      }
    } finally {
      // 成功失败都必须清理临时副本（M4-R4-001）；force:true 容忍已不存在
      rmSync(root, { recursive: true, force: true })
    }
  }
} finally {
  // M4-R4-001/R5：残留门必须在 finally——makeCopy 初始化异常或场景同步异常都不能跳过它
  const afterDirs = listSelftestDirs()
  const leaked = afterDirs.filter((d) => !beforeDirs.includes(d))
  if (leaked.length > 0) {
    console.error(`FAIL  临时目录残留 ${leaked.length} 个（M4-R4-001/R5 harness 资源泄漏）：\n${leaked.map((d) => `      ${d}`).join('\n')}`)
    failed += 1
  } else {
    console.log(`PASS  无临时目录残留（M4-R4-001/R5：${afterDirs.length} 个既有 / 0 新增）`)
  }
}

if (failed > 0) {
  console.error(`\nSELFTEST FAIL：${failed} 项不符合预期（场景盲点或临时目录泄漏）`)
  process.exit(1)
}
console.log(`\nSELFTEST PASS：${scenarios.length}/${scenarios.length} 场景符合预期 + 无临时目录残留——checkbox/radio 枚举与门禁不盲`)
