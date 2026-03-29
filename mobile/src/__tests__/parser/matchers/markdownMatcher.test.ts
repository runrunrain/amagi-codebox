import { buildMarkdownBlock, isMarkdownBlock, markdownMatcher } from '../../../parser/matchers/markdownMatcher'

describe('markdownMatcher', () => {
  it('detects multi-line markdown sections', () => {
    expect(isMarkdownBlock(['# Title', '- item'], 'claudecode')).toBe(true)
    expect(isMarkdownBlock(['> quote', 'next line'], 'claudecode')).toBe(true)
  })

  it('does not match single-line or plain prose output', () => {
    expect(isMarkdownBlock(['# Title'], 'claudecode')).toBe(false)
    expect(isMarkdownBlock(['plain output', 'next line'], 'generic')).toBe(false)
  })

  it('treats multi-line Claude/OpenCode narrative replies as markdown-friendly blocks', () => {
    expect(isMarkdownBlock([
      '下面是方案：',
      '第一步先检查配置。',
      '第二步再执行迁移。',
    ], 'claudecode')).toBe(true)

    expect(isMarkdownBlock([
      'Here is the plan:',
      'First verify the environment.',
      'Then apply the migration.',
    ], 'opencode')).toBe(true)
  })

  it('does not treat terminal control/tool lines as markdown narrative', () => {
    expect(isMarkdownBlock([
      '● Bash(echo hello)',
      '⎿  hello',
    ], 'claudecode')).toBe(false)
  })

  it('builds markdown blocks preserving raw content', () => {
    expect(buildMarkdownBlock({
      appType: 'opencode',
      lines: ['# Title', '- item'],
      raw: '# Title\n- item',
      createdAt: 15,
    })).toEqual({
      id: 'markdown-15',
      type: 'markdown',
      appType: 'opencode',
      raw: '# Title\n- item',
      content: '# Title\n- item',
      createdAt: 15,
    })
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'claudecode' as const,
      lines: ['# Summary', '- one'],
      raw: '# Summary\n- one',
      createdAt: 8,
    }
    expect(markdownMatcher.match(context)).toBe(true)
    expect(markdownMatcher.build(context).type).toBe('markdown')
  })
})
