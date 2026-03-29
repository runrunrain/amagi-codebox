import { buildPromptBlock, isPromptBlock, promptMatcher } from '../../../parser/matchers/promptMatcher'

describe('promptMatcher', () => {
  it('detects Claude/OpenCode suggestion prompts', () => {
    expect(isPromptBlock(['> Try "how do I log an error?"'])).toBe(true)
    expect(isPromptBlock(['> try something'])).toBe(true)
  })

  it('rejects unrelated single-line text', () => {
    expect(isPromptBlock(['plain text'])).toBe(false)
    expect(isPromptBlock(['> cd src'])).toBe(false)
  })

  it('builds prompt blocks', () => {
    expect(buildPromptBlock({
      appType: 'claudecode',
      lines: ['> Try "test"'],
      raw: '> Try "test"',
      createdAt: 9,
    })).toEqual({
      id: 'prompt-9',
      type: 'prompt',
      appType: 'claudecode',
      raw: '> Try "test"',
      content: '> Try "test"',
      primaryAction: 'test',
      createdAt: 9,
    })
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'opencode' as const,
      lines: ['> Try "hello"'],
      raw: '> Try "hello"',
      createdAt: 3,
    }
    expect(promptMatcher.match(context)).toBe(true)
    expect(promptMatcher.build(context).type).toBe('prompt')
  })
})
