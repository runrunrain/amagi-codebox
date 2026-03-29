import { createLineAccumulator } from '../../parser/lineAccumulator'

describe('createLineAccumulator', () => {
  it('accumulates partial text until newline arrives', () => {
    const accumulator = createLineAccumulator()

    expect(accumulator.push('hello')).toEqual([])
    expect(accumulator.snapshot()).toEqual({ activeLine: 'hello' })
    expect(accumulator.push(' world\n')).toEqual(['hello world'])
    expect(accumulator.snapshot()).toEqual({ activeLine: '' })
  })

  it('handles multiple LF-delimited lines in one chunk', () => {
    const accumulator = createLineAccumulator()

    expect(accumulator.push('a\nb\n')).toEqual(['a', 'b'])
    expect(accumulator.snapshot()).toEqual({ activeLine: '' })
  })

  it('handles CRLF as a single line break', () => {
    const accumulator = createLineAccumulator()

    expect(accumulator.push('alpha\r\nbeta\r\n')).toEqual(['alpha', 'beta'])
    expect(accumulator.snapshot()).toEqual({ activeLine: '' })
  })

  it('treats bare carriage return as an in-place overwrite reset', () => {
    const accumulator = createLineAccumulator()

    expect(accumulator.push('progress 10%')).toEqual([])
    expect(accumulator.push('\rprogress 20%')).toEqual([])
    expect(accumulator.snapshot()).toEqual({ activeLine: 'progress 20%' })
    expect(accumulator.push('\n')).toEqual(['progress 20%'])
  })

  it('flushes the active line without adding empty entries', () => {
    const accumulator = createLineAccumulator()

    accumulator.push('tail line')
    expect(accumulator.flush()).toEqual(['tail line'])
    expect(accumulator.flush()).toEqual([])
  })

  it('resets buffered state explicitly', () => {
    const accumulator = createLineAccumulator()

    accumulator.push('stale')
    accumulator.reset()

    expect(accumulator.snapshot()).toEqual({ activeLine: '' })
    expect(accumulator.push('fresh\n')).toEqual(['fresh'])
  })
})
