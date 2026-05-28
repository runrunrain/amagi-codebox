import { mount, VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import DiagnosticDrawer from '../../../components/transcript/DiagnosticDrawer.vue'
import type { DiagnosticRecordV2 } from '../../../types/transcript'

function makeRecord(): DiagnosticRecordV2 {
  return {
    id: 'diagnostic-object-payload',
    reason: 'object-payload',
    severity: 'warning',
    visibility: 'drawer-only',
    summary: '收到疑似对象或 JSON 负载，已隔离。',
    preview: '{"token":"[REDACTED]"}',
    redacted: true,
    count: 2,
    createdAt: '2026-05-27T00:00:00.000Z',
    updatedAt: '2026-05-27T00:00:00.000Z',
  }
}

describe('DiagnosticDrawer', () => {
  let mountedWrappers: VueWrapper[] = []

  afterEach(() => {
    mountedWrappers.forEach((wrapper) => wrapper.unmount())
    mountedWrappers = []
    document.body.innerHTML = ''
  })

  function mountDrawer(props: { open: boolean; returnFocusTo?: HTMLElement | null }) {
    const wrapper = mount(DiagnosticDrawer, {
      attachTo: document.body,
      props: {
        records: [makeRecord()],
        count: 1,
        ...props,
      },
    })
    mountedWrappers.push(wrapper)
    return wrapper
  }

  it('renders as a labelled modal dialog and focuses the close button when opened', async () => {
    const wrapper = mountDrawer({ open: true })
    await nextTick()

    const dialog = wrapper.get('[role="dialog"]')
    const title = wrapper.get('#diagnostic-drawer-title')
    const closeButton = wrapper.get('button.diagnostic-drawer-close')

    expect(dialog.attributes('aria-modal')).toBe('true')
    expect(dialog.attributes('aria-labelledby')).toBe('diagnostic-drawer-title')
    expect(title.text()).toBe('诊断详情')
    expect(document.activeElement).toBe(closeButton.element)
  })

  it('emits close for Escape, backdrop click, and close button click', async () => {
    const wrapper = mountDrawer({ open: true })
    await nextTick()

    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    await wrapper.get('.diagnostic-drawer-backdrop').trigger('click')
    await wrapper.get('button.diagnostic-drawer-close').trigger('click')

    expect(wrapper.emitted('close')).toHaveLength(3)
  })

  it('restores focus to the trigger when closed', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = '诊断 1'
    document.body.appendChild(trigger)
    trigger.focus()

    const wrapper = mountDrawer({ open: false, returnFocusTo: trigger })
    await wrapper.setProps({ open: true })
    await nextTick()
    expect(document.activeElement).toBe(wrapper.get('button.diagnostic-drawer-close').element)

    await wrapper.setProps({ open: false })
    await nextTick()

    expect(document.activeElement).toBe(trigger)
  })

  it('keeps Tab focus inside the drawer', async () => {
    const wrapper = mountDrawer({ open: true })
    await nextTick()

    const closeButton = wrapper.get('button.diagnostic-drawer-close')
    const closeButtonElement = closeButton.element as HTMLButtonElement
    closeButtonElement.focus()
    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Tab' })

    expect(document.activeElement).toBe(closeButtonElement)
  })
})
