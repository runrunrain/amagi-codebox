import { isLiquidTerminalCapable, resolveAppType } from '../../types/terminal'

describe('resolveAppType', () => {
  it('maps claude aliases to claudecode', () => {
    expect(resolveAppType('claude')).toBe('claudecode')
    expect(resolveAppType('claudecode')).toBe('claudecode')
  })

  it('maps opencode to opencode', () => {
    expect(resolveAppType('opencode')).toBe('opencode')
  })

  it('maps codex to codex', () => {
    expect(resolveAppType('codex')).toBe('codex')
  })

  it('maps missing and unknown values to generic', () => {
    expect(resolveAppType(undefined)).toBe('generic')
    expect(resolveAppType(null)).toBe('generic')
    expect(resolveAppType('something-new')).toBe('generic')
  })
})

describe('isLiquidTerminalCapable', () => {
  it('supports claudecode and opencode only', () => {
    expect(isLiquidTerminalCapable('claudecode')).toBe(true)
    expect(isLiquidTerminalCapable('opencode')).toBe(true)
    expect(isLiquidTerminalCapable('codex')).toBe(false)
    expect(isLiquidTerminalCapable('generic')).toBe(false)
  })
})
