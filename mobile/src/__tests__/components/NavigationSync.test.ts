import { describe, expect, it } from 'vitest'
import appLayoutSource from '../../components/AppLayout.vue?raw'
import drawerNavSource from '../../components/DrawerNav.vue?raw'

describe('Navigation Synchronization (P2-A AppLayout & DrawerNav)', () => {
  it('AppLayout bottom-nav 不含任何下线的死页链接（/providers 与 /dashboard）', () => {
    expect(appLayoutSource).not.toContain('to="/providers"')
    expect(appLayoutSource).not.toContain('to="/dashboard"')
    // 仅保留 Sessions 与 Settings
    expect(appLayoutSource).toContain('to="/lobby"')
    expect(appLayoutSource).toContain('to="/settings"')
  })

  it('DrawerNav 不含任何下线的死页链接（/providers 与 /dashboard）', () => {
    expect(drawerNavSource).not.toContain('to="/providers"')
    expect(drawerNavSource).not.toContain('to="/dashboard"')
    // 仅保留 Connect, Sessions, Settings
    expect(drawerNavSource).toContain('to="/connect"')
    expect(drawerNavSource).toContain('to="/lobby"')
    expect(drawerNavSource).toContain('to="/settings"')
  })

  it('DrawerNav 与 ConnectionStatus 使用 v1 useAuthStore 而非 legacy connection', () => {
    expect(drawerNavSource).toContain('useAuthStore')
    expect(drawerNavSource).not.toContain('useConnection')
  })
})
