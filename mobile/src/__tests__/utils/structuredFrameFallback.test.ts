import { StructuredFrameFallbacks } from '../../utils/structuredFrameFallback'

describe('StructuredFrameFallbacks', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('lets structured-part consume pending raw text before timeout', () => {
    const fallbacks: Array<{ seq: number; text: string }> = []
    const manager = new StructuredFrameFallbacks(350, (seq, text) => fallbacks.push({ seq, text }))

    manager.schedule(1, 'raw output')
    const rawText = manager.consume(1)
    vi.advanceTimersByTime(350)

    expect(rawText).toBe('raw output')
    expect(fallbacks).toEqual([])
    expect(manager.size()).toBe(0)
    expect(manager.hasResolved(1)).toBe(true)
  })

  it('falls back to raw chunk when structured-part does not arrive in time', () => {
    const fallbacks: Array<{ seq: number; text: string }> = []
    const manager = new StructuredFrameFallbacks(350, (seq, text) => fallbacks.push({ seq, text }))

    manager.schedule(2, 'legacy raw')
    vi.advanceTimersByTime(349)
    expect(fallbacks).toEqual([])

    vi.advanceTimersByTime(1)
    expect(fallbacks).toEqual([{ seq: 2, text: 'legacy raw' }])
    expect(manager.size()).toBe(0)
    expect(manager.hasResolved(2)).toBe(true)
    expect(manager.consume(2)).toBeUndefined()
  })

  it('flushes pending raw chunks during reconnect or unmount', () => {
    const fallbacks: Array<{ seq: number; text: string }> = []
    const manager = new StructuredFrameFallbacks(350, (seq, text) => fallbacks.push({ seq, text }))

    manager.schedule(3, 'pending raw')
    manager.flush()

    expect(fallbacks).toEqual([{ seq: 3, text: 'pending raw' }])
    expect(manager.size()).toBe(0)
  })
})
