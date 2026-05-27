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
})
