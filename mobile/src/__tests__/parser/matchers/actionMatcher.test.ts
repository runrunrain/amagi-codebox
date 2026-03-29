import { actionMatcher, buildActionBlock, isActionBlock } from '../../../parser/matchers/actionMatcher'

describe('actionMatcher', () => {
  it('detects action rows with double chevrons', () => {
    expect(isActionBlock(['⏵⏵ bypass permissions on (shift+tab to cycle)'])).toBe(true)
  })

  it('rejects non-action content', () => {
    expect(isActionBlock(['plain text'])).toBe(false)
    expect(isActionBlock(['> Try "hello"'])).toBe(false)
  })

  it('builds action blocks', () => {
    expect(buildActionBlock({
      appType: 'claudecode',
      lines: ['⏵⏵ bypass permissions on (shift+tab to cycle)'],
      raw: '⏵⏵ bypass permissions on (shift+tab to cycle)',
      createdAt: 10,
    })).toEqual({
      id: 'action-10',
      type: 'action',
      appType: 'claudecode',
      raw: '⏵⏵ bypass permissions on (shift+tab to cycle)',
      content: '⏵⏵ bypass permissions on',
      shortcutHint: '(shift+tab to cycle)',
      createdAt: 10,
    })
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'opencode' as const,
      lines: ['⏵⏵ continue'],
      raw: '⏵⏵ continue',
      createdAt: 4,
    }
    expect(actionMatcher.match(context)).toBe(true)
    expect(actionMatcher.build(context).type).toBe('action')
  })
})
