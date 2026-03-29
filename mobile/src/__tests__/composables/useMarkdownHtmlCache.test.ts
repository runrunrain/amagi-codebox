import { vi } from 'vitest'

const renderMarkdownToHtml = vi.fn()

vi.mock('../../utils/renderMarkdown', () => ({
  renderMarkdownToHtml,
}))

describe('useMarkdownHtmlCache', () => {
  beforeEach(() => {
    renderMarkdownToHtml.mockReset()
  })

  it('renders markdown blocks into html cache', async () => {
    renderMarkdownToHtml.mockResolvedValueOnce('<h1>Title</h1>')

    const { useMarkdownHtmlCache } = await import('../../composables/useMarkdownHtmlCache')
    const cache = useMarkdownHtmlCache()

    await cache.refreshMarkdownBlocks([
      {
        id: 'md-1',
        type: 'markdown',
        appType: 'claudecode',
        raw: '# Title',
        content: '# Title',
        createdAt: 1,
      },
    ])

    expect(cache.markdownHtmlById.value).toEqual({
      'md-1': '<h1>Title</h1>',
    })
  })

  it('clears cache when no markdown blocks exist', async () => {
    const { useMarkdownHtmlCache } = await import('../../composables/useMarkdownHtmlCache')
    const cache = useMarkdownHtmlCache()
    cache.markdownHtmlById.value = { stale: '<p>old</p>' }

    await cache.refreshMarkdownBlocks([])

    expect(cache.markdownHtmlById.value).toEqual({})
  })

  it('ignores stale async results when a newer refresh finishes later', async () => {
    let resolveFirst: ((value: string) => void) | undefined
    renderMarkdownToHtml
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve }))
      .mockResolvedValueOnce('<p>fresh</p>')

    const { useMarkdownHtmlCache } = await import('../../composables/useMarkdownHtmlCache')
    const cache = useMarkdownHtmlCache()

    const firstRefresh = cache.refreshMarkdownBlocks([
      {
        id: 'md-old',
        type: 'markdown',
        appType: 'claudecode',
        raw: 'old',
        content: 'old',
        createdAt: 1,
      },
    ])

    await cache.refreshMarkdownBlocks([
      {
        id: 'md-new',
        type: 'markdown',
        appType: 'claudecode',
        raw: 'new',
        content: 'new',
        createdAt: 2,
      },
    ])

    resolveFirst?.('<p>old</p>')
    await firstRefresh

    expect(cache.markdownHtmlById.value).toEqual({
      'md-new': '<p>fresh</p>',
    })
  })

  it('reset invalidates pending renders and clears cache', async () => {
    let resolvePending: ((value: string) => void) | undefined
    renderMarkdownToHtml.mockImplementationOnce(() => new Promise((resolve) => { resolvePending = resolve }))

    const { useMarkdownHtmlCache } = await import('../../composables/useMarkdownHtmlCache')
    const cache = useMarkdownHtmlCache()

    const pendingRefresh = cache.refreshMarkdownBlocks([
      {
        id: 'md-1',
        type: 'markdown',
        appType: 'opencode',
        raw: 'hello',
        content: 'hello',
        createdAt: 1,
      },
    ])

    cache.resetMarkdownCache()
    resolvePending?.('<p>late</p>')
    await pendingRefresh

    expect(cache.markdownHtmlById.value).toEqual({})
  })
})
