import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// P4：piPlugin store 的包级/skill 级启停 action——在 api 边界打桩，
// 断言成功后的刷新语义（列表 + 当前详情重拉）与失败向外抛错。

const fns = vi.hoisted(() => ({
  refreshPiPackages: vi.fn(),
  getPiPackageDetails: vi.fn(),
  installPiPackage: vi.fn(),
  updatePiPackage: vi.fn(),
  removePiPackage: vi.fn(),
  setPiPackageEnabled: vi.fn(),
  setPiPackageSkillEnabled: vi.fn(),
}))

vi.mock('../../api/piPlugin', () => fns)

import { usePiPluginStore } from '../../stores/piPlugin'

function pkg(source: string, enabled = true) {
  return {
    id: source, source, sourceType: 'npm' as const, name: source,
    scope: 'user' as const, enabled,
  }
}

function detail(source: string) {
  return {
    ...pkg(source),
    resources: [
      { name: 'alpha', filePath: `/x/${source}/skills/alpha/SKILL.md`, type: 'skill' as const, disabled: true },
      { name: 'beta', filePath: `/x/${source}/skills/beta/SKILL.md`, type: 'skill' as const },
    ],
    manifestDeclared: false,
  }
}

describe('stores/piPlugin（P4 包级/skill 级启停）', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    for (const m of Object.values(fns)) m.mockReset()
  })

  it('setEnabled 成功后强制刷新列表并重拉当前详情', async () => {
    const store = usePiPluginStore()
    fns.getPiPackageDetails.mockResolvedValueOnce(detail('npm:demo'))
    await store.selectPackage('npm:demo')
    fns.setPiPackageEnabled.mockResolvedValueOnce({ success: true, output: '已禁用 pi 包 npm:demo' })
    fns.refreshPiPackages.mockResolvedValueOnce({ installed: [pkg('npm:demo', false)], warnings: [] })
    fns.getPiPackageDetails.mockResolvedValueOnce(detail('npm:demo'))

    const result = await store.setEnabled('npm:demo', false)
    expect(result.output).toContain('已禁用')
    // 列表已重拉（enabled=false 已反映）+ 详情重拉（skill 态更新）。
    expect(store.installed[0].enabled).toBe(false)
    expect(fns.refreshPiPackages).toHaveBeenCalledTimes(1)
    expect(fns.getPiPackageDetails).toHaveBeenCalledTimes(2)
  })

  it('setEnabled 失败向外抛错（面板负责 toast）', async () => {
    const store = usePiPluginStore()
    fns.setPiPackageEnabled.mockRejectedValueOnce(new Error('settings busy'))
    await expect(store.setEnabled('npm:demo', true)).rejects.toThrow('settings busy')
  })

  it('setSkillEnabled 只重拉当前包详情，不改列表选中', async () => {
    const store = usePiPluginStore()
    fns.getPiPackageDetails.mockResolvedValueOnce(detail('npm:demo'))
    await store.selectPackage('npm:demo')
    fns.setPiPackageSkillEnabled.mockResolvedValueOnce({ success: true, output: '已启用 skill alpha' })
    fns.getPiPackageDetails.mockResolvedValueOnce({
      ...detail('npm:demo'),
      resources: detail('npm:demo').resources.map(r => ({ ...r, disabled: false })),
    })

    await store.setSkillEnabled('npm:demo', 'alpha', true)
    expect(fns.setPiPackageSkillEnabled).toHaveBeenCalledWith('npm:demo', 'alpha', true)
    expect(fns.getPiPackageDetails).toHaveBeenCalledTimes(2)
    // 详情已更新：alpha 不再 disabled。
    const active = store.activePackage as ReturnType<typeof detail>
    expect(active.resources.find(r => r.name === 'alpha')?.disabled).toBe(false)
  })
})
