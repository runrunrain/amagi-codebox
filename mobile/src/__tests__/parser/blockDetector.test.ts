import type { TerminalBlockMatcher } from '../../parser/patternRegistry'
import { createPatternRegistry } from '../../parser/patternRegistry'
import { detectTerminalBlocks } from '../../parser/blockDetector'

describe('detectTerminalBlocks', () => {
  it('returns empty array for empty input', () => {
    expect(detectTerminalBlocks({ appType: 'claudecode', lines: [] })).toEqual([])
  })

  it('falls back to a plain text block when no matcher handles the input', () => {
    const blocks = detectTerminalBlocks({
      appType: 'claudecode',
      lines: ['hello', 'world'],
      createdAt: 42,
    })

    expect(blocks).toEqual([
      {
        id: 'text-42',
        type: 'text',
        appType: 'claudecode',
        raw: 'hello\nworld',
        content: 'hello\nworld',
        createdAt: 42,
      },
    ])
  })

  it('returns matcher-produced blocks when registry detects one', () => {
    const matcher: TerminalBlockMatcher = {
      name: 'markdown-block',
      match: () => true,
      build: (context) => ({
        id: 'markdown-1',
        type: 'markdown',
        appType: context.appType,
        raw: context.raw,
        content: '# Title',
        createdAt: context.createdAt,
      }),
    }

    const registry = createPatternRegistry([matcher])
    const blocks = detectTerminalBlocks({
      appType: 'opencode',
      lines: ['# Title'],
      createdAt: 99,
      registry,
    })

    expect(blocks).toEqual([
      {
        id: 'markdown-1',
        type: 'markdown',
        appType: 'opencode',
        raw: '# Title',
        content: '# Title',
        createdAt: 99,
      },
    ])
  })

  it('uses the default registry to detect fenced code blocks', () => {
    const blocks = detectTerminalBlocks({
      appType: 'claudecode',
      lines: ['```ts src/demo.ts', 'const x = 1', '```'],
      createdAt: 12,
    })

    expect(blocks).toEqual([
      {
        id: 'code-12',
        type: 'code',
        appType: 'claudecode',
        raw: '```ts src/demo.ts\nconst x = 1\n```',
        code: 'const x = 1',
        language: 'ts',
        filename: 'src/demo.ts',
        createdAt: 12,
      },
    ])
  })
})
