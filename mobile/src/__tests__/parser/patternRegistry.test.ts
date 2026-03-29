import type { TerminalTextBlock } from '../../types/terminal-blocks'
import { createPatternRegistry, type TerminalBlockMatcher } from '../../parser/patternRegistry'

function createTextBlock(id: string, content: string): TerminalTextBlock {
  return {
    id,
    type: 'text',
    appType: 'claudecode',
    raw: content,
    content,
    createdAt: 1,
  }
}

describe('createPatternRegistry', () => {
  it('returns null when nothing matches', () => {
    const registry = createPatternRegistry()
    const result = registry.detect({
      appType: 'claudecode',
      lines: ['plain'],
      raw: 'plain',
      createdAt: 1,
    })

    expect(result).toBeNull()
  })

  it('returns the first matching block', () => {
    const first: TerminalBlockMatcher = {
      name: 'first',
      match: () => true,
      build: () => createTextBlock('first', 'matched-first'),
    }
    const second: TerminalBlockMatcher = {
      name: 'second',
      match: () => true,
      build: () => createTextBlock('second', 'matched-second'),
    }

    const registry = createPatternRegistry([first, second])
    const result = registry.detect({
      appType: 'claudecode',
      lines: ['plain'],
      raw: 'plain',
      createdAt: 1,
    })

    expect(result).toEqual(createTextBlock('first', 'matched-first'))
  })

  it('supports runtime matcher registration', () => {
    const registry = createPatternRegistry()
    registry.register({
      name: 'opencode-only',
      match: (context) => context.appType === 'opencode',
      build: () => createTextBlock('opencode', 'matched-opencode'),
    })

    expect(registry.list()).toHaveLength(1)
    expect(registry.detect({
      appType: 'opencode',
      lines: ['plain'],
      raw: 'plain',
      createdAt: 1,
    })).toEqual(createTextBlock('opencode', 'matched-opencode'))
  })
})
