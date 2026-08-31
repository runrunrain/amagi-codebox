import { describe, expect, it } from 'vitest'
import { createMarkdownStreamCollector } from '../../composables/useMarkdownStreamCollector'

describe('useMarkdownStreamCollector', () => {
  it('correctly emits deltas across buffer overflow without stalling', () => {
    const collector = createMarkdownStreamCollector('test-part', { maxBufferChars: 10 })

    const res1 = collector.append('12345\n', 'line')
    expect(res1.committedSource).toBe('12345\n')
    expect(res1.committedDelta).toBe('12345\n')
    expect(res1.shouldRender).toBe(true)

    const res2 = collector.append('67890\n', 'line')
    expect(res2.committedSource).toBe('345\n67890\n')
    expect(res2.committedDelta).toBe('67890\n')
    expect(res2.shouldRender).toBe(true)
  })

  it('handles partial streaming and line commits', () => {
    const collector = createMarkdownStreamCollector('test-part')

    const res1 = collector.append('hello', 'partial')
    expect(res1.committedDelta).toBe('')
    expect(res1.liveTail).toBe('hello')
    expect(res1.shouldRender).toBe(false)

    const res2 = collector.append(' world\n', 'partial')
    expect(res2.committedDelta).toBe('hello world\n')
    expect(res2.liveTail).toBe('')
    expect(res2.shouldRender).toBe(true)
  })
})
