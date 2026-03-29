import {
  isCollapsibleTerminalBlockType,
  isTerminalBlockType,
  prefersMarkdownRendering,
  TERMINAL_BLOCK_TYPES,
} from '../../types/terminal-blocks'

describe('terminal block types', () => {
  it('includes the expected parser-oriented block types', () => {
    expect(TERMINAL_BLOCK_TYPES).toEqual([
      'summary',
      'text',
      'prompt',
      'action',
      'markdown',
      'code',
      'tool',
      'diff',
      'todo',
      'table',
      'thinking',
      'status',
      'streaming',
      'raw-terminal',
    ])
  })

  it('recognizes valid block types only', () => {
    expect(isTerminalBlockType('markdown')).toBe(true)
    expect(isTerminalBlockType('raw-terminal')).toBe(true)
    expect(isTerminalBlockType('unknown')).toBe(false)
  })

  it('marks heavy display blocks as collapsible', () => {
    expect(isCollapsibleTerminalBlockType('code')).toBe(true)
    expect(isCollapsibleTerminalBlockType('tool')).toBe(true)
    expect(isCollapsibleTerminalBlockType('diff')).toBe(true)
    expect(isCollapsibleTerminalBlockType('table')).toBe(true)
    expect(isCollapsibleTerminalBlockType('thinking')).toBe(true)
    expect(isCollapsibleTerminalBlockType('raw-terminal')).toBe(true)
    expect(isCollapsibleTerminalBlockType('text')).toBe(false)
  })

  it('marks markdown-friendly block types correctly', () => {
    expect(prefersMarkdownRendering('markdown')).toBe(true)
    expect(prefersMarkdownRendering('thinking')).toBe(true)
    expect(prefersMarkdownRendering('status')).toBe(true)
    expect(prefersMarkdownRendering('streaming')).toBe(true)
    expect(prefersMarkdownRendering('code')).toBe(false)
    expect(prefersMarkdownRendering('raw-terminal')).toBe(false)
  })
})
