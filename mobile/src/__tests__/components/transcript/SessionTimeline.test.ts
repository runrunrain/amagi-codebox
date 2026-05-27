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
    expect(wrapper.text()).toContain('暂无输出')
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
    expect(wrapper.text()).toContain('正在建立结构化输出')
    expect(wrapper.find('.timeline-state--loading').exists()).toBe(true)
    // Loading takes priority over empty turns
    expect(wrapper.text()).not.toContain('暂无输出')
  })

  it('renders error state when error is provided', () => {
    // Arrange
    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns: [], error: 'connection lost' },
    })

    // Assert
    expect(wrapper.text()).toContain('结构化解析失败')
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
    expect(wrapper.text()).toContain('结构化解析失败')
    expect(wrapper.find('.timeline-state--error').exists()).toBe(true)
  })

  it('renders turns with role and metadata headers', () => {
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
    expect(wrapper.find('.turn-header').exists()).toBe(true)
    expect(wrapper.find('.turn-role').text()).toBe('assistant')
    expect(wrapper.find('.turn-meta').text()).toContain('opencode')
    expect(wrapper.find('.turn-meta').text()).toContain('completed')
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

  it('renders turn header metadata for system role', () => {
    // Arrange
    const turns = [
      makeTurn({ id: 'turn-sys', role: 'system', appType: 'claudecode', status: 'streaming' }),
    ]

    // Act
    const wrapper = mount(SessionTimeline, {
      props: { turns },
    })

    // Assert
    expect(wrapper.find('.turn-role').text()).toBe('system')
    expect(wrapper.find('.turn-meta').text()).toContain('claudecode')
    expect(wrapper.find('.turn-meta').text()).toContain('streaming')
  })

  it('renders section with correct aria-label', () => {
    // Arrange & Act
    const wrapper = mount(SessionTimeline, {
      props: { turns: [] },
    })

    // Assert
    expect(wrapper.find('section[aria-label="Session transcript"]').exists()).toBe(true)
  })
})
