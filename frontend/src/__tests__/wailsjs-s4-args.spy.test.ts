import { describe, expect, it, vi, beforeEach } from 'vitest'

// S4 diting F1 防再犯：对 wailsjs 生成层（App.js 函数体）做实参透传 spy——
// vue-tsc 只看 App.d.ts 类型、vitest api 层测试只 mock 绑定模块，均不会执行
// App.js 的函数体；一旦生成层把参数写成 ('arg1') 字符串字面量，只有本层
// 能捕获。真实调用生成函数，断言 window['go']['main']['App'][方法] 收到的
// 是调用方传入的变量值本身。

import * as appBindings from '../../wailsjs/go/main/App'

type AnyFn = (...args: unknown[]) => unknown

const spies: Record<string, ReturnType<typeof vi.fn>> = {}

const S4_METHODS = [
  'ApplyWSLCliConfigSupply',
  'ApplyWSLCliResourcesSupply',
  'CheckWSLCliResourcesSupply',
  'GetWSLCliSupplyStatus',
  'GetWSLOpenCodeMCPPreview',
  'ImportWSLCliConfigKeys',
  'KeepWSLCliConfigUnitLocal',
  'KeepWSLCliResourceLocal',
  'PreviewWSLCliConfigSupply',
  'PreviewWSLCliResourcesSupply',
  'SetWSLOpenCodeMCPSelection',
  'TakeOverWSLCliConfigUnit',
  'TakeOverWSLCliResource',
]

beforeEach(() => {
  const app: Record<string, AnyFn> = {}
  for (const name of S4_METHODS) {
    const fn = vi.fn(() => undefined)
    spies[name] = fn
    app[name] = fn as AnyFn
  }
  // App.js 函数体在调用时读 window['go']['main']['App'][name]；node 环境
  // 下显式安装到 globalThis。
  ;(globalThis as Record<string, unknown>).window = { go: { main: { App: app } } }
})

describe('wailsjs S4 绑定实参透传（F1 spy）', () => {
  it.each<[string, unknown[]]>([
    ['ApplyWSLCliConfigSupply', ['codex']],
    ['ApplyWSLCliResourcesSupply', ['claude', ['CLAUDE.md', 'commands/deploy']]],
    ['CheckWSLCliResourcesSupply', ['codex']],
    ['GetWSLCliSupplyStatus', ['claude']],
    ['GetWSLOpenCodeMCPPreview', []],
    ['ImportWSLCliConfigKeys', ['claude', 'claude/mcp', ['web', 'fetch']]],
    ['KeepWSLCliConfigUnitLocal', ['codex', 'codex/config']],
    ['KeepWSLCliResourceLocal', ['claude', 'CLAUDE.md']],
    ['PreviewWSLCliConfigSupply', ['codex']],
    ['PreviewWSLCliResourcesSupply', ['claude']],
    ['SetWSLOpenCodeMCPSelection', [['fetch', 'web']]],
    ['TakeOverWSLCliConfigUnit', ['codex', 'codex/config', 'tok-abc']],
    ['TakeOverWSLCliResource', ['claude', 'CLAUDE.md', 'tok-def']],
  ])('%s(%j) 将变量实参原样透传给绑定（非字面量）', async (name, args) => {
    const binding = (appBindings as unknown as Record<string, AnyFn>)[name]
    if (typeof binding !== 'function') {
      throw new Error(`wailsjs binding ${name} missing`)
    }
    await binding(...args)
    expect(spies[name]).toHaveBeenCalledTimes(1)
    // 精确实参匹配：若生成层写成 ('arg1') 字面量或漏传/错位，此处必失败。
    expect(spies[name]).toHaveBeenCalledWith(...args)
  })
})
