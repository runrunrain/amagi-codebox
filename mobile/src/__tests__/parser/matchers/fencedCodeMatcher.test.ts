import { buildFencedCodeBlock, fencedCodeMatcher, isFencedCodeBlock } from '../../../parser/matchers/fencedCodeMatcher'

describe('fencedCodeMatcher', () => {
  it('detects complete fenced code blocks', () => {
    expect(isFencedCodeBlock(['```ts', 'const x = 1', '```'])).toBe(true)
    expect(isFencedCodeBlock(['```', 'plain', '```'])).toBe(true)
  })

  it('rejects incomplete or non-fenced content', () => {
    expect(isFencedCodeBlock(['```ts', 'const x = 1'])).toBe(false)
    expect(isFencedCodeBlock(['plain text'])).toBe(false)
  })

  it('builds code blocks with language and filename metadata when present', () => {
    const block = buildFencedCodeBlock({
      appType: 'claudecode',
      lines: ['```ts src/main.ts', 'const x = 1', 'console.log(x)', '```'],
      raw: '```ts src/main.ts\nconst x = 1\nconsole.log(x)\n```',
      createdAt: 7,
    })

    expect(block).toEqual({
      id: 'code-7',
      type: 'code',
      appType: 'claudecode',
      raw: '```ts src/main.ts\nconst x = 1\nconsole.log(x)\n```',
      code: 'const x = 1\nconsole.log(x)',
      language: 'ts',
      filename: 'src/main.ts',
      createdAt: 7,
    })
  })

  it('matcher exposes the expected registry contract', () => {
    const context = {
      appType: 'opencode' as const,
      lines: ['```js', 'console.log(1)', '```'],
      raw: '```js\nconsole.log(1)\n```',
      createdAt: 11,
    }

    expect(fencedCodeMatcher.match(context)).toBe(true)
    expect(fencedCodeMatcher.build(context).type).toBe('code')
  })
})
