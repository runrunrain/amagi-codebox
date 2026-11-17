/**
 * dist-base.test.ts — 构建产物 base 检查（design §7.2-1/§7.2 测试行、R14
 * 双 base 回归锚）：断言 dist/index.html 无 href="/ 或 src="/ 根绝对资源引用。
 *
 * dist 位置：vite outDir=../internal/claudewebui/dist（design §7.3）。
 * CI 与本地约定 build 先于 test（dist 存在）；dist 缺失时 skip 并提示先构建
 * （占位 .gitkeep 态不构成失败）。
 */
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const distIndexUrl = new URL('../../../internal/claudewebui/dist/index.html', import.meta.url)
const distIndexPath = fileURLToPath(distIndexUrl)
const hasDist = existsSync(distIndexPath)

if (!hasDist) {
  // eslint-disable-next-line no-console
  console.warn(
    `[dist-base] ${distIndexPath} 不存在——请先构建：npm --prefix webui-claude ci && npm --prefix webui-claude run build`,
  )
}

(describe.skipIf(!hasDist)('构建产物 base 检查（dist/index.html）', () => {
  const html = hasDist ? readFileSync(distIndexPath, 'utf8') : ''

  it('产物存在（base ./ 构建）', () => {
    expect(html).not.toBe('')
    expect(html).toContain('<div id="app">')
  })

  it('无根绝对资源引用（href="/ 或 src="/）——远程子路径形态白屏根因锚', () => {
    // vite base:'./' 产物应全部是 ./assets/... 相对引用；出现根绝对引用
    // 说明 base 配置回退（R14 双 base 回归）。
    const rootAbsolute = /(?:href|src)=['"]\//.exec(html)
    expect(rootAbsolute, `发现根绝对引用: ${rootAbsolute?.[0] ?? ''}`).toBeNull()
  })

  it('资源引用为相对形态（./assets/）', () => {
    const refs = [...html.matchAll(/(?:href|src)=['"]([^'"]+)['"]/g)].map((m) => m[1]).filter((r) => r.endsWith('.js') || r.endsWith('.css'))
    expect(refs.length).toBeGreaterThan(0)
    for (const ref of refs) {
      expect(ref.startsWith('./')).toBe(true)
    }
  })
}))
