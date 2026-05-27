import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PartRenderer from '../../../components/transcript/PartRenderer.vue'
import type {
  TranscriptPart,
  TextPart,
  MarkdownPart,
  ToolPart,
  DiffPart,
  ErrorPart,
  FilePart,
  StepPart,
  ReasoningPart,
  RawTerminalPart,
} from '../../../types/transcript'

function basePart(): { id: string; createdAt: string } {
  return { id: 'p1', createdAt: '2026-05-27T00:00:00.000Z' }
}

// Mock renderMarkdownToHtml so MarkdownRenderer doesn't try real marked/DOMPurify
vi.mock('../../../utils/renderMarkdown', () => ({
  renderMarkdownToHtml: vi.fn().mockResolvedValue('<p>mocked html</p>'),
}))

describe('PartRenderer', () => {
  it('renders markdown type via MarkdownRenderer', () => {
    // Arrange
    const part: MarkdownPart = { ...basePart(), type: 'markdown', markdown: '# Hello' }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.findComponent({ name: 'MarkdownRenderer' }).exists()).toBe(true)
    expect(wrapper.find('.markdown-card').exists()).toBe(true)
  })

  it('renders tool type via ToolCard', () => {
    // Arrange
    const part: ToolPart = {
      ...basePart(),
      type: 'tool',
      name: 'Read',
      state: 'completed',
      title: 'Read src/main.ts',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-card').exists()).toBe(true)
    expect(wrapper.text()).toContain('Read')
    expect(wrapper.text()).toContain('completed')
  })

  it('renders raw-terminal type via RawTerminalBlock', () => {
    // Arrange
    const part: RawTerminalPart = {
      ...basePart(),
      type: 'raw-terminal',
      text: 'ansi output here',
      reason: 'ansi',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.raw-block').exists()).toBe(true)
    expect(wrapper.text()).toContain('Raw fallback')
    expect(wrapper.text()).toContain('ansi')
  })

  it('renders diff type in a pre element with diff class', () => {
    // Arrange
    const part: DiffPart = {
      ...basePart(),
      type: 'diff',
      diff: 'diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt',
      additions: 1,
      deletions: 0,
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('pre.part--diff').exists()).toBe(true)
    expect(wrapper.find('pre.part--diff').text()).toContain('diff --git')
  })

  it('renders error type with error class and message', () => {
    // Arrange
    const part: ErrorPart = {
      ...basePart(),
      type: 'error',
      message: 'Something went wrong',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.part--error').exists()).toBe(true)
    expect(wrapper.text()).toContain('Something went wrong')
  })

  it('renders text type as default fallback', () => {
    // Arrange
    const part: TextPart = {
      ...basePart(),
      type: 'text',
      text: 'plain text content',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.part--text').exists()).toBe(true)
    expect(wrapper.text()).toContain('plain text content')
  })

  it('renders file type with action and path', () => {
    // Arrange
    const part: FilePart = {
      ...basePart(),
      type: 'file',
      path: 'src/index.ts',
      action: 'edit',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.part--file').exists()).toBe(true)
    expect(wrapper.text()).toContain('edit')
    expect(wrapper.text()).toContain('src/index.ts')
  })

  it('renders file type with default action when action is missing', () => {
    // Arrange
    const part: FilePart = {
      ...basePart(),
      type: 'file',
      path: 'src/app.ts',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.part--file').exists()).toBe(true)
    expect(wrapper.text()).toContain('file')
    expect(wrapper.text()).toContain('src/app.ts')
  })

  it('renders step type with title and status', () => {
    // Arrange
    const part: StepPart = {
      ...basePart(),
      type: 'step',
      title: 'Build project',
      status: 'completed',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.part--step').exists()).toBe(true)
    expect(wrapper.text()).toContain('Build project')
    expect(wrapper.text()).toContain('completed')
  })

  it('renders reasoning type with reasoning class', () => {
    // Arrange
    const part: ReasoningPart = {
      ...basePart(),
      type: 'reasoning',
      text: 'I should check the file first because...',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert
    expect(wrapper.find('.part--reasoning').exists()).toBe(true)
    expect(wrapper.text()).toContain('I should check the file first')
  })

  it('safely falls back to text for unknown type', () => {
    // Arrange - simulate an unrecognized type by casting
    const part = {
      ...basePart(),
      type: 'unknown-future-type',
      text: 'some fallback text',
    } as unknown as TranscriptPart

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert - falls through all v-if/v-else-if to the final v-else (text fallback)
    expect(wrapper.find('.part--text').exists()).toBe(true)
    expect(wrapper.text()).toContain('some fallback text')
  })

  it('safely handles part with missing optional fields (minimal text part)', () => {
    // Arrange
    const part: TextPart = {
      ...basePart(),
      type: 'text',
      text: '',
    }

    // Act
    const wrapper = mount(PartRenderer, { props: { part } })

    // Assert - renders without crashing, empty text is fine
    expect(wrapper.find('.part--text').exists()).toBe(true)
  })
})
