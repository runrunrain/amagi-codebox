import { looksLikeMarkdown, renderMarkdownToHtml } from '../../utils/renderMarkdown'

describe('renderMarkdownToHtml', () => {
  it('renders markdown headings and emphasis', async () => {
    const html = await renderMarkdownToHtml('# Title\n\n**bold** text')
    expect(html).toContain('<h1>Title</h1>')
    expect(html).toContain('<strong>bold</strong>')
  })

  it('sanitizes dangerous html', async () => {
    const html = await renderMarkdownToHtml('hello<script>alert(1)</script>')
    expect(html).toContain('<p>hello</p>')
    expect(html).not.toContain('<script>')
  })
})

describe('looksLikeMarkdown', () => {
  it('detects common markdown structures', () => {
    expect(looksLikeMarkdown('# Title')).toBe(true)
    expect(looksLikeMarkdown('- item')).toBe(true)
    expect(looksLikeMarkdown('1. item')).toBe(true)
    expect(looksLikeMarkdown('> quote')).toBe(true)
    expect(looksLikeMarkdown('```ts\nconst x = 1\n```')).toBe(true)
  })

  it('does not misclassify plain prose', () => {
    expect(looksLikeMarkdown('plain terminal output')).toBe(false)
  })
})
