import { isPathLikeLine } from '../../utils/isPathLikeLine'

describe('isPathLikeLine', () => {
  it('detects windows, unix, UNC and relative paths', () => {
    expect(isPathLikeLine('X:\\WorkSpace\\src\\main.ts')).toBe(true)
    expect(isPathLikeLine('/workspace/src/main.ts')).toBe(true)
    expect(isPathLikeLine('\\\\server\\share\\file.txt')).toBe(true)
    expect(isPathLikeLine('./src/main.ts')).toBe(true)
    expect(isPathLikeLine('../src/main.ts')).toBe(true)
  })

  it('rejects ordinary prose and tool labels', () => {
    expect(isPathLikeLine('summary line')).toBe(false)
    expect(isPathLikeLine('Read 3 files')).toBe(false)
    expect(isPathLikeLine('')).toBe(false)
  })
})
