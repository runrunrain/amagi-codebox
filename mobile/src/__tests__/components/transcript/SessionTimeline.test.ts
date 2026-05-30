import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SessionTimeline from '../../../components/transcript/SessionTimeline.vue'
import type { TranscriptTurn } from '../../../types/transcript'

function makeTurn(overrides: Partial<TranscriptTurn> = {}): TranscriptTurn {
  return {
    id: 'turn-1',
    sessionId: 'sess-1',
    role: 'assistant',
    appType: 'opencode',
    parts: [],
    status: 'completed',
    createdAt: '2026-05-27T00:00:00.000Z',
    updatedAt: '2026-05-27T00:00:00.000Z',
    ...overrides,
  }
}

describe('SessionTimeline', () => {
  it('renders empty state when turns is empty and not loading', () => {
    // Arrange
    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns: [] },
    })

    // Assert
    expect(wrapper.text()).toContain('暂无会话输出')
    expect(wrapper.find('.timeline-state').exists()).toBe(true)
    expect(wrapper.find('.timeline-state--loading').exists()).toBe(false)
    expect(wrapper.find('.timeline-state--error').exists()).toBe(false)
  })

  it('renders loading state when loading is true', () => {
    // Arrange
    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns: [], loading: true },
    })

    // Assert
    expect(wrapper.text()).toContain('正在连接会话')
    expect(wrapper.find('.timeline-state--loading').exists()).toBe(true)
    // Loading takes priority over empty turns
    expect(wrapper.text()).not.toContain('暂无会话输出')
  })

  it('renders error state when error is provided', () => {
    // Arrange
    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns: [], error: 'connection lost' },
    })

    // Assert
    expect(wrapper.text()).toContain('会话解析遇到问题')
    expect(wrapper.text()).toContain('connection lost')
    expect(wrapper.find('.timeline-state--error').exists()).toBe(true)
  })

  it('renders error state even with non-empty turns', () => {
    // Arrange
    const turns = [makeTurn()]

    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns, error: 'partial failure' },
    })

    // Assert - error takes priority over turns display
    expect(wrapper.text()).toContain('会话解析遇到问题')
    expect(wrapper.find('.timeline-state--error').exists()).toBe(true)
  })

  it('renders assistant turns with weak metadata line', () => {
    // Arrange
    const turns = [
      makeTurn({
        id: 'turn-1',
        role: 'assistant',
        appType: 'opencode',
        status: 'completed',
      }),
    ]

    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns },
    })

    // Assert
    expect(wrapper.find('.timeline-turn').exists()).toBe(true)
    expect(wrapper.find('.assistant-meta').exists()).toBe(true)
    expect(wrapper.find('.assistant-meta').text()).toContain('OpenCode')
    expect(wrapper.find('.assistant-meta').text()).toContain('完成')
  })

  it('renders multiple turns with correct keys', () => {
    // Arrange
    const turns = [
      makeTurn({ id: 'turn-a', role: 'user' }),
      makeTurn({ id: 'turn-b', role: 'assistant' }),
    ]

    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns },
    })

    // Assert
    const articles = wrapper.findAll('.timeline-turn')
    expect(articles).toHaveLength(2)
    expect(articles[0].classes()).toContain('timeline-turn--user')
    expect(articles[1].classes()).toContain('timeline-turn--assistant')
  })

  it('renders assistant-style metadata for system role', () => {
    // Arrange
    const turns = [
      makeTurn({ id: 'turn-sys', role: 'system', appType: 'claudecode', status: 'streaming' }),
    ]

    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns },
    })

    // Assert
    expect(wrapper.find('.assistant-meta').text()).toContain('Claude Code')
    expect(wrapper.find('.assistant-meta').text()).toContain('正在处理')
  })

  it('renders user role as right-aligned bubble', () => {
    const turns = [makeTurn({
      id: 'turn-user',
      role: 'user',
      parts: [{ id: 'p1', type: 'text', text: 'hello agent', createdAt: '2026-05-27T00:00:00.000Z' }],
    })]

    const wrapper = mount(SessionTimeline, { props: { turns } })

    expect(wrapper.find('.timeline-turn--user').exists()).toBe(true)
    expect(wrapper.find('.user-bubble').text()).toContain('hello agent')
    expect(wrapper.find('.assistant-meta').exists()).toBe(false)
  })

  it('does not render raw-terminal parts in assistant flow', () => {
    const turns = [makeTurn({
      parts: [
        { id: 'raw-1', type: 'raw-terminal', text: 'raw tui', reason: 'tui', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    const wrapper = mount(SessionTimeline, { props: { turns } })

    expect(wrapper.text()).not.toContain('raw tui')
  })

  it('renders section with correct aria-label', () => {
    // Arrange & Act
    const wrapper = mount(SessionTimeline, {
      props: { turns: [] },
    })

    // Assert
    expect(wrapper.find('section[aria-label="Session transcript"]').exists()).toBe(true)
  })

  it('filters diagnostic-ref with drawer-only visibility out of timeline', () => {
    // Arrange - drawer-only diagnostic-ref should NOT appear in the assistant flow
    const turns = [makeTurn({
      parts: [
        { id: 'text-1', type: 'text', text: 'visible text', createdAt: '2026-05-27T00:00:00.000Z' },
        { id: 'diag-1', type: 'diagnostic-ref', reason: 'ansi', summary: 'ANSI noise', preview: '', redacted: false, visibility: 'drawer-only', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    // Act
    const wrapper = mount(SessionTimeline, { props: { turns } })

    // Assert - only the text part should be visible
    expect(wrapper.text()).toContain('visible text')
    expect(wrapper.text()).not.toContain('ANSI noise')
  })

  it('shows a diagnostic state when turns contain only hidden raw or drawer-only parts', () => {
    const turns = [makeTurn({
      parts: [
        { id: 'raw-1', type: 'raw-terminal', text: 'raw tui', reason: 'tui', createdAt: '2026-05-27T00:00:00.000Z' },
        { id: 'diag-1', type: 'diagnostic-ref', reason: 'ansi', summary: 'ANSI noise', preview: '', redacted: false, visibility: 'drawer-only', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    const wrapper = mount(SessionTimeline, { props: { turns } })

    expect(wrapper.find('.timeline-state--diagnostic').exists()).toBe(true)
    expect(wrapper.text()).toContain('已收到终端输出')
    expect(wrapper.find('.assistant-meta').isVisible()).toBe(false)
  })

  it('renders diagnostic-ref with summary-card visibility in timeline', () => {
    // Arrange - summary-card diagnostic-ref SHOULD appear
    const turns = [makeTurn({
      parts: [
        { id: 'diag-sc', type: 'diagnostic-ref', reason: 'fallback', summary: 'summary card content', preview: '', redacted: false, visibility: 'summary-card', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    // Act
    const wrapper = mount(SessionTimeline, { props: { turns } })

    // Assert - summary-card should pass through to PartRenderer
    expect(wrapper.text()).toContain('诊断抽屉')
  })

  it('renders diagnostic-ref with error-card visibility in timeline', () => {
    // Arrange - error-card diagnostic-ref SHOULD appear
    const turns = [makeTurn({
      parts: [
        { id: 'diag-ec', type: 'diagnostic-ref', reason: 'parser-error', summary: 'error card content', preview: '', redacted: false, visibility: 'error-card', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    // Act
    const wrapper = mount(SessionTimeline, { props: { turns } })

    // Assert - error-card should pass through to PartRenderer
    expect(wrapper.text()).toContain('诊断抽屉')
  })

  it('filters diagnostic-ref with hidden-info visibility out of timeline', () => {
    // Arrange - hidden-info diagnostic-ref should NOT appear
    const turns = [makeTurn({
      parts: [
        { id: 'diag-hi', type: 'diagnostic-ref', reason: 'tui', summary: 'hidden info', preview: '', redacted: false, visibility: 'hidden-info', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    // Act
    const wrapper = mount(SessionTimeline, { props: { turns } })

    // Assert - hidden-info should be filtered out
    expect(wrapper.text()).not.toContain('hidden info')
  })

  it('renders user turn with mixed parts extracting only readable text', () => {
    // Arrange - user turn with raw-terminal (filtered) + text (visible)
    const turns = [makeTurn({
      id: 'turn-mixed',
      role: 'user',
      parts: [
        { id: 'text-u', type: 'text', text: 'user question', createdAt: '2026-05-27T00:00:00.000Z' },
        { id: 'raw-u', type: 'raw-terminal', text: 'raw noise', reason: 'tui', createdAt: '2026-05-27T00:00:00.000Z' },
      ],
    })]

    // Act
    const wrapper = mount(SessionTimeline, { props: { turns } })

    // Assert
    expect(wrapper.find('.user-bubble').text()).toContain('user question')
    expect(wrapper.text()).not.toContain('raw noise')
  })
})
