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

  it('removes image event handlers', async () => {
    const html = await renderMarkdownToHtml('<img src=x onerror=alert(1)>')
    expect(html).not.toContain('onerror')
    expect(html).not.toContain('alert(1)')
  })

  it('removes svg onload payloads', async () => {
    const html = await renderMarkdownToHtml('<svg onload=alert(1)><circle /></svg>')
    expect(html).not.toContain('<svg')
    expect(html).not.toContain('onload')
  })

  it('removes javascript link protocols', async () => {
    const html = await renderMarkdownToHtml('[click](javascript:alert(1))')
    expect(html).not.toContain('javascript:')
    expect(html).not.toContain('alert(1)')
  })

  it('adds safe rel attributes to external links', async () => {
    const html = await renderMarkdownToHtml('[docs](https://example.com)')
    expect(html).toContain('rel="noopener noreferrer"')
    expect(html).toContain('target="_blank"')
  })

  it('removes style and mutation-xss payload surfaces', async () => {
    const payload = '<math><mtext><table><mglyph><style><!--</style><img src=x onerror=alert(1)>'
    const html = await renderMarkdownToHtml(payload)
    expect(html).not.toContain('<style')
    expect(html).not.toContain('<math')
    expect(html).not.toContain('onerror')
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
