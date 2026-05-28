import { routeDecodedTerminalOutput } from '../../utils/terminalOutputRouting'

describe('routeDecodedTerminalOutput', () => {
  it('writes structuredExpected output to the raw terminal sink without duplicating transcript body', () => {
    const writeRawOutput = vi.fn()
    const scheduleStructuredFallback = vi.fn()
    const enqueueTranscriptChunk = vi.fn()
    const scheduleRawTextViewSync = vi.fn()

    routeDecodedTerminalOutput(
      { structuredExpected: true, seq: 42 },
      'raw structured pty output',
      {
        writeRawOutput,
        scheduleStructuredFallback,
        enqueueTranscriptChunk,
        scheduleRawTextViewSync,
      },
    )

    expect(writeRawOutput).toHaveBeenCalledTimes(1)
    expect(writeRawOutput).toHaveBeenCalledWith('raw structured pty output')
    expect(scheduleStructuredFallback).toHaveBeenCalledTimes(1)
    expect(scheduleStructuredFallback).toHaveBeenCalledWith(42, 'raw structured pty output')
    expect(enqueueTranscriptChunk).not.toHaveBeenCalled()
    expect(scheduleRawTextViewSync).not.toHaveBeenCalled()
  })

  it('routes legacy output to both raw sink and transcript fallback path', () => {
    const writeRawOutput = vi.fn()
    const scheduleStructuredFallback = vi.fn()
    const enqueueTranscriptChunk = vi.fn()
    const scheduleRawTextViewSync = vi.fn()

    routeDecodedTerminalOutput(
      { structuredExpected: false, seq: 7 },
      'legacy pty output',
      {
        writeRawOutput,
        scheduleStructuredFallback,
        enqueueTranscriptChunk,
        scheduleRawTextViewSync,
      },
    )

    expect(writeRawOutput).toHaveBeenCalledWith('legacy pty output')
    expect(scheduleStructuredFallback).not.toHaveBeenCalled()
    expect(enqueueTranscriptChunk).toHaveBeenCalledWith('legacy pty output')
    expect(scheduleRawTextViewSync).toHaveBeenCalledWith('legacy pty output')
  })
})
