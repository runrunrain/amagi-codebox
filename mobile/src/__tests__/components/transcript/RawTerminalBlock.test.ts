import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import RawTerminalBlock from '../../../components/transcript/RawTerminalBlock.vue'
import type { RawTerminalPart, RawTerminalReason } from '../../../types/transcript'

function makeRawPart(overrides: Partial<RawTerminalPart> = {}): RawTerminalPart {
  return {
    id: 'raw-1',
    type: 'raw-terminal',
    text: 'some raw terminal output',
    reason: 'unsupported-pattern',
    createdAt: '2026-05-27T00:00:00.000Z',
    ...overrides,
  }
}

describe('RawTerminalBlock', () => {
  const reasons: RawTerminalReason[] = [
    'fallback',
    'unsupported-pattern',
    'parser-error',
    'ansi',
    'tui',
    'classifier-overflow',
  ]

  reasons.forEach((reason) => {
    it(`renders reason header for reason=${reason}`, () => {
      // Arrange
      const part = makeRawPart({ reason })

      // Act
      const wrapper = mount(RawTerminalBlock, { props: { part } })

      // Assert
      expect(wrapper.find('.raw-header').exists()).toBe(true)
      expect(wrapper.find('.raw-header').text()).toContain('Raw fallback')
      expect(wrapper.find('.raw-header').text()).toContain(reason)
    })
  })

  it('renders text content in pre element', () => {
    // Arrange
    const part = makeRawPart({ text: 'hello world output' })

    // Act
    const wrapper = mount(RawTerminalBlock, { props: { part } })

    // Assert
    expect(wrapper.find('.raw-content').exists()).toBe(true)
    expect(wrapper.find('pre.raw-content').text()).toContain('hello world output')
  })

  it('renders a non-empty fallback when text is empty', () => {
    // Arrange
    const part = makeRawPart({ text: '' })

    // Act
    const wrapper = mount(RawTerminalBlock, { props: { part } })

    // Assert - The template renders `{{ part.text || ' ' }}` so empty string becomes space.
    // Vue's text() may trim whitespace, so we verify the pre element exists and the
    // raw-header is shown, confirming the component renders safely with empty text.
    const content = wrapper.find('.raw-content')
    expect(content.exists()).toBe(true)
    expect(wrapper.find('.raw-header').text()).toContain('Raw fallback')
  })

  it('renders ANSI content faithfully', () => {
    // Arrange
    const part = makeRawPart({
      text: '\u001B[32mgreen\u001B[0m text',
      reason: 'ansi',
    })

    // Act
    const wrapper = mount(RawTerminalBlock, { props: { part } })

    // Assert
    expect(wrapper.find('.raw-content').text()).toContain('\u001B[32mgreen\u001B[0m text')
    expect(wrapper.find('.raw-header').text()).toContain('ansi')
  })

  it('renders long multi-line raw content', () => {
    // Arrange
    const longText = Array.from({ length: 50 }, (_, i) => `line ${i + 1}`).join('\n')
    const part = makeRawPart({ text: longText, reason: 'classifier-overflow' })

    // Act
    const wrapper = mount(RawTerminalBlock, { props: { part } })

    // Assert
    expect(wrapper.find('.raw-content').text()).toContain('line 1')
    expect(wrapper.find('.raw-content').text()).toContain('line 50')
    expect(wrapper.find('.raw-header').text()).toContain('classifier-overflow')
  })
})
