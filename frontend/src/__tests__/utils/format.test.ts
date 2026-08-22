import { describe, it, expect } from 'vitest'
import { basename } from '../../utils/format'

/**
 * Smoke test for the frontend vitest setup; also documents the actual
 * basename() behavior including edge cases.
 */
describe('basename', () => {
  it('returns empty string for empty input', () => {
    expect(basename('')).toBe('')
  })

  it('returns a bare filename as-is (no separator)', () => {
    expect(basename('file.txt')).toBe('file.txt')
  })

  it('returns the last segment of a POSIX path', () => {
    expect(basename('foo/bar.txt')).toBe('bar.txt')
    expect(basename('/usr/local/bin/codebox')).toBe('codebox')
  })

  it('returns the last segment of a Windows backslash path', () => {
    expect(basename('C:\\Users\\mao\\file.txt')).toBe('file.txt')
  })

  it('splits on mixed separators', () => {
    expect(basename('mixed/path\\file')).toBe('file')
  })

  // Trailing separator: the last split segment is '' (falsy), so the `|| path`
  // fallback returns the WHOLE path — not POSIX basename semantics ('bar').
  // Documented as-is; product code intentionally left untouched.
  it('returns the whole path for trailing-separator input (fallback quirk)', () => {
    expect(basename('foo/bar/')).toBe('foo/bar/')
    expect(basename('/')).toBe('/')
  })
})
