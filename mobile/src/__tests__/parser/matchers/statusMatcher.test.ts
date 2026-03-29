import { buildStatusBlock, isStatusBlock, statusMatcher } from '../../../parser/matchers/statusMatcher'

describe('statusMatcher', () => {
  it('detects terminal lifecycle status lines', () => {
    expect(isStatusBlock(['Connected to session.'])).toBe(true)
    expect(isStatusBlock(['Disconnected. Reconnecting...'])).toBe(true)
    expect(isStatusBlock(['Connection error.'])).toBe(true)
    expect(isStatusBlock(['Session exited with code 1'])).toBe(true)
  })

  it('rejects multi-line or unrelated content', () => {
    expect(isStatusBlock(['Connected to session.', 'extra'])).toBe(false)
    expect(isStatusBlock(['plain content'])).toBe(false)
  })

  it('builds status blocks with preserved raw content', () => {
    const block = buildStatusBlock({
      appType: 'claudecode',
      lines: ['Connected to session.'],
      raw: 'Connected to session.',
      createdAt: 5,
    })

    expect(block).toEqual({
      id: 'status-5',
      type: 'status',
      appType: 'claudecode',
      raw: 'Connected to session.',
      content: 'Connected to session.',
      createdAt: 5,
    })
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'opencode' as const,
      lines: ['Connection error.'],
      raw: 'Connection error.',
      createdAt: 8,
    }

    expect(statusMatcher.match(context)).toBe(true)
    expect(statusMatcher.build(context).type).toBe('status')
  })
})
