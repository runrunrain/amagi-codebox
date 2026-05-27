import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ToolCard from '../../../components/transcript/ToolCard.vue'
import type { ToolPart, ToolPartState } from '../../../types/transcript'

function makeToolPart(overrides: Partial<ToolPart> = {}): ToolPart {
  return {
    id: 'tool-1',
    type: 'tool',
    name: 'Read',
    state: 'completed',
    title: 'Read src/main.ts',
    createdAt: '2026-05-27T00:00:00.000Z',
    ...overrides,
  }
}

describe('ToolCard', () => {
  const states: ToolPartState[] = ['pending', 'running', 'completed', 'error']

  states.forEach((state) => {
    it(`renders state class tool-card--${state} for state=${state}`, () => {
      // Arrange
      const part = makeToolPart({ state })

      // Act
      const wrapper = mount(ToolCard, { props: { part } })

      // Assert
      expect(wrapper.find(`.tool-card--${state}`).exists()).toBe(true)
      expect(wrapper.find('.tool-state').text()).toBe(state)
    })
  })

  it('renders tool name and title', () => {
    // Arrange
    const part = makeToolPart({ name: 'Write', title: 'Write src/app.ts' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-name').text()).toBe('Write')
    expect(wrapper.find('.tool-title').text()).toBe('Write src/app.ts')
  })

  it('renders inputPreview when provided', () => {
    // Arrange
    const part = makeToolPart({ inputPreview: 'file content preview' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-preview').exists()).toBe(true)
    expect(wrapper.text()).toContain('file content preview')
  })

  it('hides inputPreview when not provided', () => {
    // Arrange
    const part = makeToolPart({})

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert - no tool-preview without inputPreview
    const previews = wrapper.findAll('.tool-preview')
    // inputPreview is undefined, so the v-if should be false
    expect(previews.some((el) => !el.classes().includes('tool-preview--output'))).toBe(false)
  })

  it('renders outputPreview when provided', () => {
    // Arrange
    const part = makeToolPart({ outputPreview: 'result output' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-preview--output').exists()).toBe(true)
    expect(wrapper.text()).toContain('result output')
  })

  it('hides outputPreview when not provided', () => {
    // Arrange
    const part = makeToolPart({})

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-preview--output').exists()).toBe(false)
  })

  it('renders both inputPreview and outputPreview together', () => {
    // Arrange
    const part = makeToolPart({
      inputPreview: 'input data',
      outputPreview: 'output data',
    })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    const previews = wrapper.findAll('.tool-preview')
    expect(previews).toHaveLength(2)
    expect(wrapper.find('.tool-preview--output').exists()).toBe(true)
    expect(wrapper.text()).toContain('input data')
    expect(wrapper.text()).toContain('output data')
  })

  it('renders minimal tool part with only required fields', () => {
    // Arrange
    const part = makeToolPart({ inputPreview: undefined, outputPreview: undefined, metadata: undefined })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-name').text()).toBe('Read')
    expect(wrapper.find('.tool-title').text()).toBe('Read src/main.ts')
    expect(wrapper.find('.tool-state').text()).toBe('completed')
  })
})
