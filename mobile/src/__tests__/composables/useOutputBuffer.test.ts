import { useOutputBuffer } from '../../composables/useOutputBuffer'

describe('useOutputBuffer', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.clearAllTimers()
    vi.useRealTimers()
  })

  it('buffers output without flushing immediately', () => {
    const onFlush = vi.fn()
    const { bufferOutput } = useOutputBuffer({ onFlush })

    bufferOutput(new Uint8Array([1, 2, 3]))

    expect(onFlush).not.toHaveBeenCalled()
  })

  it('merges multiple chunks during scheduled flush', () => {
    const onFlush = vi.fn()
    const { bufferOutput } = useOutputBuffer({ onFlush })

    bufferOutput(new Uint8Array([1, 2]))
    bufferOutput(new Uint8Array([3, 4]))
    vi.advanceTimersByTime(80)

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(Array.from(onFlush.mock.calls[0][0])).toEqual([1, 2, 3, 4])
  })

  it('resets buffer after flush', () => {
    const onFlush = vi.fn()
    const { bufferOutput } = useOutputBuffer({ onFlush })

    bufferOutput(new Uint8Array([1]))
    vi.advanceTimersByTime(80)
    bufferOutput(new Uint8Array([2]))
    vi.advanceTimersByTime(80)

    expect(onFlush).toHaveBeenCalledTimes(2)
    expect(Array.from(onFlush.mock.calls[1][0])).toEqual([2])
  })

  it('flushNow forces immediate flush', () => {
    const onFlush = vi.fn()
    const { bufferOutput, flushNow } = useOutputBuffer({ onFlush })

    bufferOutput(new Uint8Array([9, 8]))
    flushNow()

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(Array.from(onFlush.mock.calls[0][0])).toEqual([9, 8])
  })

  it('dispose cancels pending flush', () => {
    const onFlush = vi.fn()
    const { bufferOutput, dispose } = useOutputBuffer({ onFlush })

    bufferOutput(new Uint8Array([7]))
    dispose()
    vi.advanceTimersByTime(80)

    expect(onFlush).not.toHaveBeenCalled()
  })

  it('respects custom flush interval', () => {
    const onFlush = vi.fn()
    const { bufferOutput } = useOutputBuffer({ onFlush, flushInterval: 200 })

    bufferOutput(new Uint8Array([5]))
    vi.advanceTimersByTime(80)
    expect(onFlush).not.toHaveBeenCalled()

    vi.advanceTimersByTime(120)
    expect(onFlush).toHaveBeenCalledTimes(1)
  })
})
