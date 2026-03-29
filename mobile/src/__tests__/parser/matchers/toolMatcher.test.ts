import { buildToolBlock, isToolBlock, normalizeToolSummaryLines, toolMatcher } from '../../../parser/matchers/toolMatcher'

describe('toolMatcher', () => {
  it('detects bullet-prefixed tool lines', () => {
    expect(isToolBlock(['● Bash(echo $AMAGI_WORKSPACE_ROOT)'])).toBe(true)
    expect(isToolBlock(['Read 3 files (ctrl+o to expand)'])).toBe(true)
  })

  it('rejects regular prose', () => {
    expect(isToolBlock(['plain text'])).toBe(false)
    expect(isToolBlock(['> Try "hello"'])).toBe(false)
  })

  it('builds tool blocks with optional summary and shortcut hint', () => {
    expect(buildToolBlock({
      appType: 'claudecode',
      lines: ['Read 3 files (ctrl+o to expand)', '⎿  X:\\WorkSpace', '  src/main.ts'],
      raw: 'Read 3 files (ctrl+o to expand)\n⎿  X:\\WorkSpace\n  src/main.ts',
      createdAt: 12,
    })).toEqual({
      id: 'tool-12',
      type: 'tool',
      appType: 'claudecode',
      raw: 'Read 3 files (ctrl+o to expand)\n⎿  X:\\WorkSpace\n  src/main.ts',
      toolName: 'Read',
      title: 'Read 3 files (ctrl+o to expand)',
      summary: 'X:\\WorkSpace\nsrc/main.ts',
      shortcutHint: '(ctrl+o to expand)',
      createdAt: 12,
    })
  })

  it('strips ansi color noise from tool lines and summaries', () => {
    expect(buildToolBlock({
      appType: 'opencode',
      lines: ['\u001B[36mBash(echo hi)\u001B[0m', '\u001B[32m⎿  hi\u001B[0m'],
      raw: '\u001B[36mBash(echo hi)\u001B[0m\n\u001B[32m⎿  hi\u001B[0m',
      createdAt: 13,
    })).toEqual({
      id: 'tool-13',
      type: 'tool',
      appType: 'opencode',
      raw: '\u001B[36mBash(echo hi)\u001B[0m\n\u001B[32m⎿  hi\u001B[0m',
      toolName: 'Bash',
      title: 'Bash(echo hi)',
      summary: 'hi',
      shortcutHint: undefined,
      createdAt: 13,
    })
  })

  it('normalizes tool summary lines by stripping prefixes and deduping adjacent repeats', () => {
    expect(normalizeToolSummaryLines([
      '⎿  X:\\WorkSpace\\src\\main.ts',
      '│  X:\\WorkSpace\\src\\main.ts',
      '  X:\\WorkSpace\\src\\app.ts',
      '',
      '\u001B[32m⎿  X:\\WorkSpace\\src\\app.ts\u001B[0m',
      'summary line',
    ])).toEqual([
      'X:\\WorkSpace\\src\\main.ts',
      'X:\\WorkSpace\\src\\app.ts',
      'summary line',
    ])
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'claudecode' as const,
      lines: ['● Bash(echo $AMAGI_WORKSPACE_ROOT)'],
      raw: '● Bash(echo $AMAGI_WORKSPACE_ROOT)',
      createdAt: 6,
    }
    expect(toolMatcher.match(context)).toBe(true)
    expect(toolMatcher.build(context).type).toBe('tool')
  })
})
