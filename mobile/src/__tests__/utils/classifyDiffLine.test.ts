import { classifyDiffLine } from '../../utils/classifyDiffLine'

describe('classifyDiffLine', () => {
  it('classifies hunk headers', () => {
    expect(classifyDiffLine('@@ -1,2 +1,2 @@')).toBe('hunk')
  })

  it('classifies file headers', () => {
    expect(classifyDiffLine('--- a/src/app.ts')).toBe('file')
    expect(classifyDiffLine('+++ b/src/app.ts')).toBe('file')
  })

  it('classifies additions and deletions', () => {
    expect(classifyDiffLine('+new line')).toBe('add')
    expect(classifyDiffLine('-old line')).toBe('delete')
  })

  it('falls back to context', () => {
    expect(classifyDiffLine(' unchanged')).toBe('context')
    expect(classifyDiffLine('')).toBe('context')
  })
})
