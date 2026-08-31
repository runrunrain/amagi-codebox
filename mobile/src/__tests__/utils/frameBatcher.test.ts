import { describe, expect, it, vi } from 'vitest'
import { createFrameBatcher } from '../../utils/frameBatcher'

describe('createFrameBatcher', () => {
  it('coalesces small chunks into a single flush', () => {
    vi.useFakeTimers()
    const onFlush = vi.fn()
    const batcher = createFrameBatcher<string>({ onFlush, flushInterval: 50 })

    batcher.enqueue('a')
    batcher.enqueue('b')
    batcher.enqueue('c')
    vi.advanceTimersByTime(60)

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush).toHaveBeenCalledWith(['a', 'b', 'c'])
    batcher.dispose()
    vi.useRealTimers()
  })

  it('cancels pending timeouts on dispose without firing late flushes', () => {
    vi.useFakeTimers()
    const onFlush = vi.fn()
    const batcher = createFrameBatcher<string>({ onFlush, flushInterval: 50 })

    batcher.enqueue('first')
    vi.advanceTimersByTime(60) // flushed at t=60, lastFlushAt=60
    expect(onFlush).toHaveBeenCalledTimes(1)

    // Second enqueue at t=70 (elapsed = 10 < 50), scheduled via setTimeout
    vi.advanceTimersByTime(10)
    batcher.enqueue('second')

    // Dispose before timer fires
    batcher.dispose()

    vi.advanceTimersByTime(100)
    expect(onFlush).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })
})
