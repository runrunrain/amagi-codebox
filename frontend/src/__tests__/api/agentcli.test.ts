import { describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传参数 + callApi 语义
//（失败 log + 原样 rethrow，见 api/internal/call.ts）。
const {
  getStatus,
  setEnabled,
  rotateToken,
  getInstallStatus,
  installShim,
  removeShim,
  installSkill,
  removeSkill,
} = vi.hoisted(() => ({
  getStatus: vi.fn(),
  setEnabled: vi.fn(),
  rotateToken: vi.fn(),
  getInstallStatus: vi.fn(),
  installShim: vi.fn(),
  removeShim: vi.fn(),
  installSkill: vi.fn(),
  removeSkill: vi.fn(),
}))

vi.mock('../../../wailsjs/go/main/App', () => ({
  GetAgentCLIStatus: getStatus,
  SetAgentCLIEnabled: setEnabled,
  RotateAgentCLIToken: rotateToken,
  GetAgentCLIInstallStatus: getInstallStatus,
  InstallAgentCLIShim: installShim,
  RemoveAgentCLIShim: removeShim,
  InstallAgentCLISkill: installSkill,
  RemoveAgentCLISkill: removeSkill,
}))

import {
  getAgentCLIStatus,
  setAgentCLIEnabled,
  rotateAgentCLIToken,
  getAgentCLIInstallStatus,
  installAgentCLIShim,
  removeAgentCLIShim,
  installAgentCLISkill,
  removeAgentCLISkill,
} from '../../api/agentcli'

const statusFixture = {
  enabled: true,
  running: true,
  url: 'http://127.0.0.1:53211',
  endpointPath: '/home/u/.amagi-codebox/run/endpoint.json',
  pid: 4242,
  startedAt: '2026-11-09T01:02:03Z',
  lastError: '',
}

const installFixture = {
  shimPath: '/home/u/.amagi-codebox/bin/codebox-agent',
  shimStatus: 'up-to-date',
  skills: [
    { target: 'agents', path: '/home/u/.agents/skills/amagi-codebox', status: 'up-to-date' },
    { target: 'claude', path: '/home/u/.claude/skills/amagi-codebox', status: 'not-installed' },
    { target: 'codex', path: '/home/u/.codex/skills/amagi-codebox', status: 'modified-by-user' },
  ],
  commandCount: 42,
  command: 'codebox-agent',
}

describe('api.agentcli 透传与错误语义', () => {
  it('状态/安装查询透传并返回后端载荷', async () => {
    getStatus.mockResolvedValueOnce(statusFixture)
    getInstallStatus.mockResolvedValueOnce(installFixture)

    await expect(getAgentCLIStatus()).resolves.toEqual(statusFixture)
    expect(getStatus).toHaveBeenCalledTimes(1)

    await expect(getAgentCLIInstallStatus()).resolves.toEqual(installFixture)
    expect(getInstallStatus).toHaveBeenCalledTimes(1)
  })

  it('开关与轮换透传布尔参数', async () => {
    setEnabled.mockResolvedValueOnce(undefined)
    await expect(setAgentCLIEnabled(false)).resolves.toBeUndefined()
    expect(setEnabled).toHaveBeenCalledWith(false)

    rotateToken.mockResolvedValueOnce(undefined)
    await expect(rotateAgentCLIToken()).resolves.toBeUndefined()
    expect(rotateToken).toHaveBeenCalledTimes(1)
  })

  it('shim 安装/清理透传并返回更新后的状态', async () => {
    const afterInstall = { ...installFixture, shimStatus: 'up-to-date' }
    installShim.mockResolvedValueOnce(afterInstall)
    await expect(installAgentCLIShim()).resolves.toEqual(afterInstall)

    const afterRemove = { ...installFixture, shimPath: '', shimStatus: 'not-installed' }
    removeShim.mockResolvedValueOnce(afterRemove)
    await expect(removeAgentCLIShim()).resolves.toEqual(afterRemove)
  })

  it('技能安装/清理透传 target 字符串', async () => {
    installSkill.mockResolvedValueOnce(installFixture)
    await expect(installAgentCLISkill('claude')).resolves.toEqual(installFixture)
    expect(installSkill).toHaveBeenCalledWith('claude')

    removeSkill.mockResolvedValueOnce(installFixture)
    await expect(removeAgentCLISkill('agents')).resolves.toEqual(installFixture)
    expect(removeSkill).toHaveBeenCalledWith('agents')
  })

  it('绑定失败：log 上下文后原样 rethrow（不包装错误对象）', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const boom = new Error('boom')
    getStatus.mockRejectedValueOnce(boom)

    await expect(getAgentCLIStatus()).rejects.toBe(boom)
    expect(errSpy).toHaveBeenCalledWith('[api.agentcli.getAgentCLIStatus]', boom)
    errSpy.mockRestore()
  })
})
