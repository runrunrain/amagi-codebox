import type { TerminalFrame } from '../../api/websocket'

describe('TerminalFrame structured-part type', () => {
  it('accepts output frame compatibility fields and structured-part payloads', () => {
    const outputFrame: TerminalFrame = {
      type: 'output',
      data: 'YWJj',
      seq: 1,
      structuredExpected: true,
    }

    const structuredFrame: TerminalFrame = {
      type: 'structured-part',
      seq: 1,
      part: {
        id: 'pty-1-text',
        type: 'text',
        text: 'abc',
        source: { kind: 'pty', seqStart: 1, seqEnd: 1 },
        createdAt: '2026-05-27T00:00:00.000Z',
      },
    }

    expect(outputFrame.structuredExpected).toBe(true)
    expect(structuredFrame.part?.type).toBe('text')
  })
})
