import { describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传（P3-S1 WSL 配置供给状态
// 与视觉清单共享同意入口）。
const { statusBinding, consentBinding } = vi.hoisted(() => ({
  statusBinding: vi.fn(),
  consentBinding: vi.fn(),
}))

vi.mock('../../../wailsjs/go/main/App', () => ({
  GetWSLConfigSupplyStatus: statusBinding,
  GrantWSLVisionConsent: consentBinding,
}))

import { getWSLConfigSupplyStatus, grantWSLVisionConsent } from '../../api/wslSupply'

describe('api.wslSupply', () => {
  it('getWSLConfigSupplyStatus 透传脱敏快照', async () => {
    statusBinding.mockResolvedValueOnce({
      distro: 'Ubuntu',
      available: true,
      faces: [
        { key: 'providers', label: '托管 Provider (Pi)', state: 'synced', detail: '已同步', lastCheck: '2026-10-16T12:00:00Z' },
        { key: 'vision-export', label: '视觉清单共享', state: 'awaiting-consent', detail: '尚未授权向 WSL 共享密钥副本', lastCheck: '' },
      ],
      pendingPush: false,
    })

    const result = await getWSLConfigSupplyStatus()

    expect(statusBinding).toHaveBeenCalledTimes(1)
    expect(result.distro).toBe('Ubuntu')
    expect(result.faces).toHaveLength(2)
    expect(result.faces[1].state).toBe('awaiting-consent')
  })

  it('grantWSLVisionConsent 透传授权动作', async () => {
    consentBinding.mockResolvedValueOnce(undefined)

    await grantWSLVisionConsent()

    expect(consentBinding).toHaveBeenCalledTimes(1)
  })

  it('后端错误沿 Promise 拒绝面透出（授权失败可见）', async () => {
    consentBinding.mockRejectedValueOnce(new Error('授权失败：未找到可用的 WSL 发行版'))
    await expect(grantWSLVisionConsent()).rejects.toThrow('授权失败')
  })
})
