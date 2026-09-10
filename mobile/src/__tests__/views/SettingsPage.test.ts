import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SettingsPage from '../../views/SettingsPage.vue'
import { useAuthStore } from '../../stores/auth'

const mockPush = vi.fn()
vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: mockPush,
    replace: vi.fn(),
  }),
}))

describe('SettingsPage (P2-A 桌面端专属管理引导与系统信息)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mockPush.mockClear()
    localStorage.clear()
  })

  it('渲染页头、返回大厅按钮及标题', async () => {
    const wrapper = mount(SettingsPage)

    expect(wrapper.find('.header-title').text()).toBe('设置与管理')
    const backBtn = wrapper.find('[data-testid="settings-back-btn"]')
    expect(backBtn.exists()).toBe(true)

    await backBtn.trigger('click')
    expect(mockPush).toHaveBeenCalledWith('/lobby')
  })

  it('渲染三项桌面端专属管理卡片（Providers / Server Settings / Dashboard）', () => {
    const wrapper = mount(SettingsPage)

    const mgmtSection = wrapper.find('[data-testid="desktop-mgmt-section"]')
    expect(mgmtSection.exists()).toBe(true)

    const providersCard = wrapper.find('[data-testid="desktop-providers-guide"]')
    expect(providersCard.exists()).toBe(true)
    expect(providersCard.text()).toContain('模型服务商配置 (Providers)')
    expect(providersCard.text()).toContain('Desktop Only')
    expect(providersCard.text()).toContain('钥匙串')

    const serverCard = wrapper.find('[data-testid="desktop-server-guide"]')
    expect(serverCard.exists()).toBe(true)
    expect(serverCard.text()).toContain('服务端与网络配置 (Host Settings)')
    expect(serverCard.text()).toContain('Desktop Only')

    const dashboardCard = wrapper.find('[data-testid="desktop-dashboard-guide"]')
    expect(dashboardCard.exists()).toBe(true)
    expect(dashboardCard.text()).toContain('使用量与统计大盘 (Dashboard)')
    expect(dashboardCard.text()).toContain('Desktop Only')
  })

  it('展示来自 v1 auth / lobby store 的系统与配对信息', () => {
    const auth = useAuthStore()

    auth.applyPairing({
      device: { id: 'dev-1', name: '测试手机', pairedAt: '2026-09-10T08:00:00Z' },
      host: {
        serverVersion: '1.3.65',
        apiVersion: 'v1',
        cliAvailability: [],
      },
    })

    const wrapper = mount(SettingsPage)

    const infoSection = wrapper.find('[data-testid="host-info-section"]')
    expect(infoSection.exists()).toBe(true)
    expect(infoSection.text()).toContain('v1.0.5')
    expect(infoSection.text()).toContain('1.3.65')
    expect(infoSection.text()).toContain('测试手机')
    expect(infoSection.text()).toContain('已配对 (Cookie 凭据受保护)')
  })

  it('底部快捷按钮：进入大厅跳转 /lobby；断开配对清空本地投影并跳转 /connect', async () => {
    const auth = useAuthStore()
    auth.applyPairing({
      device: { id: 'dev-1', name: '测试手机', pairedAt: '2026-09-10T08:00:00Z' },
      host: {
        serverVersion: '1.3.65',
        apiVersion: 'v1',
        cliAvailability: [],
      },
    })

    expect(auth.isPaired).toBe(true)
    const wrapper = mount(SettingsPage)

    const goLobbyBtn = wrapper.find('[data-testid="go-lobby-btn"]')
    await goLobbyBtn.trigger('click')
    expect(mockPush).toHaveBeenCalledWith('/lobby')

    const disconnectBtn = wrapper.find('[data-testid="disconnect-btn"]')
    await disconnectBtn.trigger('click')
    expect(auth.device).toBeNull()
    expect(auth.isPaired).toBe(false)
    expect(mockPush).toHaveBeenCalledWith('/connect')
  })
})
