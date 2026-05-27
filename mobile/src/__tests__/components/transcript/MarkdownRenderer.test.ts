import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import MarkdownRenderer from '../../../components/transcript/MarkdownRenderer.vue'

// We'll mock renderMarkdownToHtml and control it per test
const mockRenderMarkdownToHtml = vi.fn()

vi.mock('../../../utils/renderMarkdown', () => ({
  renderMarkdownToHtml: (...args: unknown[]) => mockRenderMarkdownToHtml(...args),
}))

describe('MarkdownRenderer', () => {
  it('renders markdown as HTML when renderMarkdownToHtml succeeds', async () => {
    // Arrange
    mockRenderMarkdownToHtml.mockResolvedValueOnce('<h1>Hello</h1>')

    // Act
    const wrapper = mount(MarkdownRenderer, {
      props: { markdown: '# Hello' },
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.markdown-body').exists()).toBe(true)
    })

    // Assert
    expect(wrapper.find('.markdown-body').text()).toBe('Hello')
    expect(wrapper.find('.markdown-error').exists()).toBe(false)
  })

  it('shows raw pre fallback when renderMarkdownToHtml throws', async () => {
    // Arrange
    mockRenderMarkdownToHtml.mockRejectedValueOnce(new Error('render failed'))

    // Act
    const wrapper = mount(MarkdownRenderer, {
      props: { markdown: '# Broken' },
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.markdown-error').exists()).toBe(true)
    })

    // Assert
    expect(wrapper.find('.markdown-raw').exists()).toBe(true)
    expect(wrapper.find('.markdown-raw').text()).toContain('# Broken')
    expect(wrapper.find('.markdown-error').text()).toContain('Markdown 渲染失败')
  })

  it('applies streaming class when streaming prop is true', async () => {
    // Arrange
    mockRenderMarkdownToHtml.mockResolvedValueOnce('<p>streaming</p>')

    // Act
    const wrapper = mount(MarkdownRenderer, {
      props: { markdown: 'streaming content', streaming: true },
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.markdown-card--streaming').exists()).toBe(true)
    })

    // Assert
    expect(wrapper.find('.markdown-card--streaming').exists()).toBe(true)
  })

  it('does not apply streaming class when streaming prop is false or absent', async () => {
    // Arrange
    mockRenderMarkdownToHtml.mockResolvedValueOnce('<p>static</p>')

    // Act
    const wrapper = mount(MarkdownRenderer, {
      props: { markdown: 'static content', streaming: false },
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.markdown-body').exists()).toBe(true)
    })

    // Assert
    expect(wrapper.find('.markdown-card--streaming').exists()).toBe(false)
  })

  it('re-renders when markdown prop changes', async () => {
    // Arrange
    const callTracker = vi.fn()
    mockRenderMarkdownToHtml.mockImplementation(async (md: string) => {
      callTracker(md)
      if (md === 'first content') return '<p>first</p>'
      return '<h1>Updated</h1>'
    })

    // Act
    const wrapper = mount(MarkdownRenderer, {
      props: { markdown: 'first content' },
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.markdown-body').text()).toBe('first')
    })

    const callsBeforeChange = callTracker.mock.calls.length

    await wrapper.setProps({ markdown: '# Updated' })

    // Assert
    await vi.waitFor(() => {
      expect(wrapper.find('.markdown-body').text()).toBe('Updated')
    })
    // At least one additional call after prop change
    expect(callTracker.mock.calls.length).toBeGreaterThan(callsBeforeChange)
    expect(callTracker).toHaveBeenCalledWith('# Updated')
  })

  it('shows raw pre before async render completes', () => {
    // Arrange - make renderMarkdownToHtml never resolve (stays pending)
    mockRenderMarkdownToHtml.mockReturnValue(new Promise(() => {}))

    // Act
    const wrapper = mount(MarkdownRenderer, {
      props: { markdown: 'loading content' },
    })

    // Assert - before render completes, raw pre should be shown
    expect(wrapper.find('.markdown-raw').exists()).toBe(true)
    expect(wrapper.find('.markdown-raw').text()).toContain('loading content')
    expect(wrapper.find('.markdown-body').exists()).toBe(false)
  })
})
