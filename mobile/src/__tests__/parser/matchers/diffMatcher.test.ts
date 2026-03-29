import { buildDiffBlock, diffMatcher, isDiffBlock } from '../../../parser/matchers/diffMatcher'

describe('diffMatcher', () => {
  it('detects unified diff blocks', () => {
    expect(isDiffBlock([
      '--- a/src/app.ts',
      '+++ b/src/app.ts',
      '@@ -1,2 +1,2 @@',
      '-old',
      '+new',
    ])).toBe(true)
  })

  it('rejects ordinary multiline text', () => {
    expect(isDiffBlock(['hello', 'world', 'plain'])).toBe(false)
  })

  it('builds diff blocks with filename and change counts', () => {
    expect(buildDiffBlock({
      appType: 'opencode',
      lines: [
        '--- a/src/app.ts',
        '+++ b/src/app.ts',
        '@@ -1,2 +1,3 @@',
        '-old',
        '+new',
        '+extra',
      ],
      raw: '--- a/src/app.ts\n+++ b/src/app.ts\n@@ -1,2 +1,3 @@\n-old\n+new\n+extra',
      createdAt: 21,
    })).toEqual({
      id: 'diff-21',
      type: 'diff',
      appType: 'opencode',
      raw: '--- a/src/app.ts\n+++ b/src/app.ts\n@@ -1,2 +1,3 @@\n-old\n+new\n+extra',
      filename: 'src/app.ts',
      additions: 2,
      deletions: 1,
      diff: '--- a/src/app.ts\n+++ b/src/app.ts\n@@ -1,2 +1,3 @@\n-old\n+new\n+extra',
      createdAt: 21,
    })
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'claudecode' as const,
      lines: ['--- a/file.ts', '+++ b/file.ts', '@@ -1 +1 @@', '-a', '+b'],
      raw: '--- a/file.ts\n+++ b/file.ts\n@@ -1 +1 @@\n-a\n+b',
      createdAt: 9,
    }
    expect(diffMatcher.match(context)).toBe(true)
    expect(diffMatcher.build(context).type).toBe('diff')
  })
})
