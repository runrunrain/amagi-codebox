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
      expect(wrapper.find('.tool-state').text()).not.toHaveLength(0)
    })
  })

  it('renders tool name and title', () => {
    // Arrange
    const part = makeToolPart({ name: 'Write', title: 'Write src/app.ts' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-name').text()).toBe('修改文件')
    expect(wrapper.find('.tool-title').text()).toBe('Write src/app.ts')
  })

  it('renders inputPreview when provided', () => {
    // Arrange
    const part = makeToolPart({ inputPreview: 'file content preview' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-detail-toggle').exists()).toBe(true)
    expect(wrapper.find('.tool-preview').exists()).toBe(false)
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

  it('renders outputPreview when provided', async () => {
    // Arrange
    const part = makeToolPart({ outputPreview: 'result output' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })
    await wrapper.find('.tool-detail-toggle').trigger('click')

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

  it('renders both inputPreview and outputPreview together', async () => {
    // Arrange
    const part = makeToolPart({
      inputPreview: 'input data',
      outputPreview: 'output data',
    })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })
    await wrapper.find('.tool-detail-toggle').trigger('click')

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
    expect(wrapper.find('.tool-name').text()).toBe('读取上下文')
    expect(wrapper.find('.tool-title').text()).toBe('Read src/main.ts')
    expect(wrapper.find('.tool-state').text()).toBe('完成')
  })

  it('toggles detail region collapsed after expanding', async () => {
    // Arrange
    const part = makeToolPart({ inputPreview: 'input', outputPreview: 'output' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Expand first
    await wrapper.find('.tool-detail-toggle').trigger('click')
    expect(wrapper.find('.tool-detail-region').exists()).toBe(true)
    expect(wrapper.find('.tool-preview--input').exists()).toBe(true)

    // Collapse back
    await wrapper.find('.tool-detail-toggle').trigger('click')
    expect(wrapper.find('.tool-detail-region').exists()).toBe(false)
  })

  it('maps shell command names to localized displayName', () => {
    // Arrange & Act
    const bashPart = makeToolPart({ name: 'bash' })
    const shellPart = makeToolPart({ name: 'shell' })
    const runPart = makeToolPart({ name: 'run' })

    const bashWrapper = mount(ToolCard, { props: { part: bashPart } })
    const shellWrapper = mount(ToolCard, { props: { part: shellPart } })
    const runWrapper = mount(ToolCard, { props: { part: runPart } })

    // Assert - all should map to '运行命令'
    expect(bashWrapper.find('.tool-name').text()).toBe('运行命令')
    expect(shellWrapper.find('.tool-name').text()).toBe('运行命令')
    expect(runWrapper.find('.tool-name').text()).toBe('运行命令')
  })

  it('maps edit/write tool names to localized displayName', () => {
    // Arrange & Act
    const editPart = makeToolPart({ name: 'edit' })
    const patchPart = makeToolPart({ name: 'patch' })
    const applyPatchPart = makeToolPart({ name: 'apply_patch' })

    const editWrapper = mount(ToolCard, { props: { part: editPart } })
    const patchWrapper = mount(ToolCard, { props: { part: patchPart } })
    const applyPatchWrapper = mount(ToolCard, { props: { part: applyPatchPart } })

    // Assert - all should map to '修改文件'
    expect(editWrapper.find('.tool-name').text()).toBe('修改文件')
    expect(patchWrapper.find('.tool-name').text()).toBe('修改文件')
    expect(applyPatchWrapper.find('.tool-name').text()).toBe('修改文件')
  })

  it('maps todo/plan tool names to localized displayName', () => {
    // Arrange & Act
    const todoPart = makeToolPart({ name: 'todo' })
    const planPart = makeToolPart({ name: 'plan' })
    const todoWritePart = makeToolPart({ name: 'todowrite' })

    const todoWrapper = mount(ToolCard, { props: { part: todoPart } })
    const planWrapper = mount(ToolCard, { props: { part: planPart } })
    const todoWriteWrapper = mount(ToolCard, { props: { part: todoWritePart } })

    // Assert - all should map to '任务清单'
    expect(todoWrapper.find('.tool-name').text()).toBe('任务清单')
    expect(planWrapper.find('.tool-name').text()).toBe('任务清单')
    expect(todoWriteWrapper.find('.tool-name').text()).toBe('任务清单')
  })

  it('falls back to raw name for unrecognized tools', () => {
    // Arrange
    const part = makeToolPart({ name: 'CustomTool' })

    // Act
    const wrapper = mount(ToolCard, { props: { part } })

    // Assert
    expect(wrapper.find('.tool-name').text()).toBe('CustomTool')
  })
})
