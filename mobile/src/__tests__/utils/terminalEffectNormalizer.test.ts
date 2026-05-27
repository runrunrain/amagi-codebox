import { describe, expect, it } from 'vitest'
import { createTerminalEffectNormalizer } from '../../utils/terminalEffectNormalizer'

describe('TerminalEffectNormalizer', () => {
  it('suppresses repeated carriage-return spinner lines as transient status', () => {
    const normalizer = createTerminalEffectNormalizer()

    const result = normalizer.normalize('\r\u001B[2KThinking...')

    expect(result.cleanText).toBe('')
    expect(result.transientStatus).toBe('Thinking...')
    expect(result.diagnostics.some((item) => item.reason === 'ansi' && item.severity === 'info')).toBe(true)
  })

  it('restores stable markdown while removing ANSI style sequences', () => {
    const normalizer = createTerminalEffectNormalizer()

    const result = normalizer.normalize('\u001B[32m# Plan\u001B[0m\n\n- inspect')

    expect(result.cleanText).toBe('# Plan\n\n- inspect')
    expect(result.cleanText).not.toContain('\u001B[')
  })

  it('handles cursor-up clear-line status redraw without appending visible noise', () => {
    const normalizer = createTerminalEffectNormalizer()

    const result = normalizer.normalize('\u001B[A\u001B[2K\rStatus: processing step 3/8')

    expect(result.cleanText).toBe('')
    expect(result.transientStatus).toBe('Status: processing step 3/8')
  })
})
