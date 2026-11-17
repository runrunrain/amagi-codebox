import { beforeEach, describe, expect, it, vi } from 'vitest'

// P4：Codex 单插件更新 / pi 包启停 + skill 级启停的 api 绑定路由。
// 在 wailsjs 生成绑定边界打桩，断言参数透传与返回归一。

const cx = vi.hoisted(() => ({
  UpdatePlugin: vi.fn(),
  InstallPlugin: vi.fn(),
  UninstallPlugin: vi.fn(),
}))

const pi = vi.hoisted(() => ({
  SetPackageEnabled: vi.fn(),
  SetPackageSkillEnabled: vi.fn(),
}))

vi.mock('../../../wailsjs/go/codexplugin/Service', () => cx)
vi.mock('../../../wailsjs/go/piplugin/Service', () => ({
  ...pi,
  GetPackageDetails: vi.fn(),
  InstallPackage: vi.fn(),
  RefreshPackages: vi.fn(),
  RemovePackage: vi.fn(),
  UpdatePackage: vi.fn(),
}))

import * as codexApi from '../../api/codexPlugin'
import * as piApi from '../../api/piPlugin'

const okResult = { success: true, output: 'done' }

describe('api/codexPlugin.updateCodexPlugin（P4 单插件更新透传）', () => {
  beforeEach(() => {
    for (const m of Object.values(cx)) m.mockReset()
  })

  it('透传 selector 到 codexplugin/Service.UpdatePlugin 并原样返回结果', async () => {
    cx.UpdatePlugin.mockResolvedValueOnce(okResult)
    const selector = { pluginId: 'amagi@market', marketplace: 'market' }
    await expect(codexApi.updateCodexPlugin(selector)).resolves.toEqual(okResult)
    expect(cx.UpdatePlugin).toHaveBeenCalledWith(selector)
  })
})

describe('api/piPlugin 包/skill 启停绑定路由（P4）', () => {
  beforeEach(() => {
    for (const m of Object.values(pi)) m.mockReset()
  })

  it('setPiPackageEnabled 透传 (source, enabled)', async () => {
    pi.SetPackageEnabled.mockResolvedValueOnce(okResult)
    await expect(piApi.setPiPackageEnabled('npm:demo', false)).resolves.toEqual(okResult)
    expect(pi.SetPackageEnabled).toHaveBeenCalledWith('npm:demo', false)
  })

  it('setPiPackageSkillEnabled 透传 (source, skillName, enabled)', async () => {
    pi.SetPackageSkillEnabled.mockResolvedValueOnce(okResult)
    await expect(piApi.setPiPackageSkillEnabled('npm:demo', 'alpha', true)).resolves.toEqual(okResult)
    expect(pi.SetPackageSkillEnabled).toHaveBeenCalledWith('npm:demo', 'alpha', true)
  })
})
