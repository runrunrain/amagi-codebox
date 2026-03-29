import { hasAnsi, stripAnsi } from '../../utils/stripAnsi'

describe('stripAnsi', () => {
  it('removes SGR color sequences', () => {
    expect(stripAnsi('\u001B[31merror\u001B[39m')).toBe('error')
  })

  it('removes cursor and erase control sequences', () => {
    expect(stripAnsi('hello\u001B[2K\rworld')).toBe('hello\rworld')
  })

  it('removes OSC hyperlink sequences and preserves text', () => {
    const value = '\u001B]8;;https://example.com\u0007docs\u001B]8;;\u0007'
    expect(stripAnsi(value)).toBe('docs')
  })

  it('leaves plain text untouched', () => {
    expect(stripAnsi('plain text')).toBe('plain text')
  })
})

describe('hasAnsi', () => {
  it('detects ansi sequences when present', () => {
    expect(hasAnsi('\u001B[32mok\u001B[0m')).toBe(true)
  })

  it('returns false for plain text', () => {
    expect(hasAnsi('plain text')).toBe(false)
  })
})
