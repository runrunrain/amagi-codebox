import { describe, expect, it } from 'vitest'
import appLayoutSource from '../../components/AppLayout.vue?raw'

describe('AppLayout router view lifecycle', () => {
  it('keys routed pages by fullPath so terminal session changes remount the page', () => {
    expect(appLayoutSource).toContain('<router-view :key="route.fullPath" />')
  })
})
