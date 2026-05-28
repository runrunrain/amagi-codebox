import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import DiagnosticRefCard from '../../../components/transcript/DiagnosticRefCard.vue'
import type { DiagnosticRefPart } from '../../../types/transcript'

function makePart(): DiagnosticRefPart {
  return {
    id: 'diagnostic-1-ref',
    type: 'diagnostic-ref',
    reason: 'object-payload',
    summary: '收到疑似对象或 JSON 负载，已隔离。',
    preview: '{"large":"payload"}',
    redacted: false,
    createdAt: '2026-05-27T00:00:00.000Z',
  }
}

describe('DiagnosticRefCard', () => {
  it('keeps preview out of the card and points users to the drawer', () => {
    const wrapper = mount(DiagnosticRefCard, { props: { part: makePart() } })

    expect(wrapper.find('.diagnostic-preview').exists()).toBe(false)
    expect(wrapper.find('.diagnostic-toggle').exists()).toBe(false)
    expect(wrapper.text()).toContain('诊断抽屉')
    expect(wrapper.text()).not.toContain('{"large"')
  })
})
