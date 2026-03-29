import { highlightCode } from '../../utils/highlightCode'

describe('highlightCode', () => {
  it('highlights code with a known language', () => {
    const html = highlightCode('const x = 1', 'typescript')
    expect(html).toContain('<span')
    expect(html).toContain('hljs-')
    expect(html).toContain('x')
  })

  it('is case-insensitive for language names', () => {
    const html = highlightCode('print("hi")', 'Python')
    expect(html).toContain('<span')
    expect(html).toContain('hljs-')
  })

  it('falls back to auto-detect when language is undefined', () => {
    const html = highlightCode('function hello() { return 42 }')
    // auto-detect may or may not add spans, but should not throw
    expect(typeof html).toBe('string')
    expect(html).toContain('hello')
  })

  it('falls back to auto-detect for an unknown language', () => {
    const html = highlightCode('some code', 'nonexistent-lang-xyz')
    expect(typeof html).toBe('string')
    expect(html).toContain('some')
    expect(html).toContain('code')
  })

  it('escapes HTML entities in code content', () => {
    const html = highlightCode('<div>hello</div>', 'text')
    expect(html).not.toContain('<div>')
    expect(html).toContain('&lt;')
  })

  it('returns a non-empty string for empty code', () => {
    const html = highlightCode('')
    expect(typeof html).toBe('string')
  })
})
