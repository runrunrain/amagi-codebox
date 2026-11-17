/**
 * AgentCliSettings 交互逻辑测试。
 * 镜像 frontend/src/views/settings/agentCliSettings.ts。
 *
 * 在 wailsjs 绑定边界打桩（与 api 层测试同一边界），驱动视图关键交互：
 *  - 开关：SetAgentCLIEnabled 透传 + 失败回读真实状态；
 *  - 轮换：二次确认门——取消零副作用，确认后才调用后端；
 *  - 安装/清理：shim 与三个技能目标的调用参数、状态覆盖、失败记录；
 *  - 状态映射：五态徽标文案 + modified-by-user 明确提示。
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'

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

vi.mock('../../../../wailsjs/go/main/App', () => ({
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
  SKILL_TARGETS,
  ROTATE_CONFIRM_MESSAGE,
  MODIFIED_BY_USER_HINT,
  installStateMeta,
  usageExample,
  formatStartedAt,
  lastErrorText,
  createAgentCliSettingsController,
} from '../../../views/settings/agentCliSettings'

function makeController(confirmResult: boolean | Error = true) {
  const confirm = vi.fn().mockImplementation(() => {
    if (confirmResult instanceof Error) return Promise.reject(confirmResult)
    return Promise.resolve(confirmResult)
  })
  const notify = vi.fn()
  const c = createAgentCliSettingsController({ confirm, notify })
  return { c, confirm, notify }
}

const statusFixture = {
  enabled: true,
  running: true,
  url: 'http://127.0.0.1:53211',
  endpointPath: '',
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

beforeEach(() => {
  vi.clearAllMocks()
  getStatus.mockResolvedValue({ ...statusFixture })
  getInstallStatus.mockResolvedValue(structuredClone(installFixture))
})

describe('installStateMeta 安装状态映射', () => {
  it('五态文案与徽标 class 各自可辨', () => {
    expect(installStateMeta('not-installed')).toMatchObject({ text: '未安装', badgeClass: 'badge-missing' })
    expect(installStateMeta('up-to-date')).toMatchObject({ text: '已是最新', badgeClass: 'badge-ok' })
    expect(installStateMeta('outdated')).toMatchObject({ text: '有新版本', badgeClass: 'badge-warn' })
    expect(installStateMeta('modified-by-user')).toMatchObject({ text: '已被手动修改', badgeClass: 'badge-warn' })
    expect(installStateMeta('invalid')).toMatchObject({ text: '状态异常', badgeClass: 'badge-missing' })
  })

  it('modified-by-user 明确提示应用不会覆盖', () => {
    expect(installStateMeta('modified-by-user').hint).toBe(MODIFIED_BY_USER_HINT)
    expect(MODIFIED_BY_USER_HINT).toBe('已被手动修改，应用不会覆盖')
  })

  it('未知状态按 invalid 处理，不静默伪装成正常态', () => {
    expect(installStateMeta('whatever-new-state').text).toBe('状态异常')
    expect(installStateMeta('whatever-new-state').badgeClass).toBe('badge-missing')
  })

  it('安装按钮文案随状态变化（未安装→安装，过期→更新，手改→重新安装）', () => {
    expect(installStateMeta('not-installed').installLabel).toBe('安装')
    expect(installStateMeta('outdated').installLabel).toBe('更新')
    expect(installStateMeta('modified-by-user').installLabel).toBe('重新安装')
  })
})

describe('usageExample / formatStartedAt 展示辅助', () => {
  it('用 shim 路径拼出可复制示例；含空白路径加引号', () => {
    expect(usageExample('/home/u/.amagi-codebox/bin/codebox-agent', 'codebox-agent')).toBe(
      '/home/u/.amagi-codebox/bin/codebox-agent status --json',
    )
    expect(usageExample('/Users/My Name/bin/codebox-agent', 'codebox-agent.cmd')).toBe(
      '"/Users/My Name/bin/codebox-agent" status --json',
    )
  })

  it('shim 未安装时回退命令名/缺省名', () => {
    expect(usageExample('', 'codebox-agent.cmd')).toBe('codebox-agent.cmd status --json')
    expect(usageExample(undefined, undefined)).toBe('codebox-agent status --json')
  })

  it('启动时间格式化为本地可读文案；空/非法返回空串', () => {
    const s = formatStartedAt('2026-11-09T01:02:03Z')
    expect(s).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
    expect(formatStartedAt('')).toBe('')
    expect(formatStartedAt('not-a-date')).toBe('')
  })
})

describe('lastError 最近启动/发布失败原因', () => {
  it('refresh 透传后端 lastError 字段（视图按非空展示告警）', async () => {
    const { c } = makeController()
    getStatus.mockResolvedValueOnce({ ...statusFixture, lastError: '端点被其它实例占用' })

    await c.refreshStatus()

    expect(c.status.value?.lastError).toBe('端点被其它实例占用')
    expect(lastErrorText(c.status.value?.lastError)).toBe('端点被其它实例占用')
  })

  it('lastErrorText：空/undefined/纯空白视为无异常（告警隐藏），非空去除首尾空白', () => {
    expect(lastErrorText(undefined)).toBe('')
    expect(lastErrorText('')).toBe('')
    expect(lastErrorText('   ')).toBe('')
    expect(lastErrorText('  端口监听失败  ')).toBe('端口监听失败')
  })
})

describe('控制器：状态刷新与开关', () => {
  it('refresh 同时拉取运行状态与本机分发状态', async () => {
    const { c } = makeController()
    await c.refresh()
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(getInstallStatus).toHaveBeenCalledTimes(1)
    expect(c.status.value).toEqual(statusFixture)
    expect(c.install.value).toEqual(installFixture)
    expect(c.statusLoading.value).toBe(false)
  })

  it('开关透传布尔值并刷新运行状态', async () => {
    const { c, notify } = makeController()
    await c.refresh()
    setEnabled.mockResolvedValueOnce(undefined)
    getStatus.mockResolvedValueOnce({ ...statusFixture, enabled: false, running: false, url: '' })

    await c.toggleEnabled(false)

    expect(setEnabled).toHaveBeenCalledWith(false)
    expect(getStatus).toHaveBeenCalledTimes(2) // refresh + 开关后回读
    expect(c.status.value?.enabled).toBe(false)
    expect(c.toggling.value).toBe(false)
    expect(notify).toHaveBeenCalledWith('success', '命令行访问已关闭')
  })

  it('开关失败：记录错误 + 回读真实状态，开关不停留在乐观值', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { c, notify } = makeController()
    await c.refresh()
    setEnabled.mockRejectedValueOnce(new Error('settings service unavailable'))
    getStatus.mockResolvedValueOnce({ ...statusFixture, enabled: true }) // 后端仍是开启

    await c.toggleEnabled(false)

    expect(c.status.value?.enabled).toBe(true)
    expect(c.actionError.value).toContain('切换命令行访问失败')
    expect(c.actionError.value).toContain('settings service unavailable')
    expect(c.toggling.value).toBe(false)
    expect(notify).toHaveBeenCalledWith('error', expect.stringContaining('切换命令行访问失败'))
    errSpy.mockRestore()
  })
})

describe('控制器：轮换令牌二次确认', () => {
  it('取消确认：不产生任何后端调用（零副作用）', async () => {
    const { c, confirm } = makeController(false)
    await c.rotateToken()

    expect(confirm).toHaveBeenCalledTimes(1)
    expect(confirm).toHaveBeenCalledWith(ROTATE_CONFIRM_MESSAGE)
    expect(rotateToken).not.toHaveBeenCalled()
    expect(c.rotating.value).toBe(false)
    expect(getStatus).not.toHaveBeenCalled()
  })

  it('confirm 抛异常按取消处理', async () => {
    const { c } = makeController(new Error('dialog broken'))
    await c.rotateToken()
    expect(rotateToken).not.toHaveBeenCalled()
  })

  it('确认后调用轮换并刷新状态', async () => {
    const { c, notify } = makeController(true)
    rotateToken.mockResolvedValueOnce(undefined)
    getStatus.mockClear()

    await c.rotateToken()

    expect(rotateToken).toHaveBeenCalledTimes(1)
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(notify).toHaveBeenCalledWith('success', expect.stringContaining('已轮换'))
    expect(c.rotating.value).toBe(false)
  })

  it('轮换失败（如服务未运行）：记录错误，不崩溃', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { c } = makeController(true)
    rotateToken.mockRejectedValueOnce(new Error('agent CLI service not running'))

    await c.rotateToken()

    expect(c.actionError.value).toContain('轮换令牌失败')
    expect(c.rotating.value).toBe(false)
    errSpy.mockRestore()
  })
})

describe('控制器：shim 与技能的安装/清理', () => {
  it('安装 shim 覆盖安装面状态', async () => {
    const { c, notify } = makeController()
    await c.refresh()
    const after = { ...structuredClone(installFixture), shimStatus: 'up-to-date' }
    installShim.mockResolvedValueOnce(after)

    await c.installShim()

    expect(installShim).toHaveBeenCalledTimes(1)
    expect(c.install.value).toEqual(after)
    expect(c.shimBusy.value).toBeNull()
    expect(notify).toHaveBeenCalledWith('success', 'shim 已安装')
  })

  it('清理 shim 记录提示（启动不再自动装回）', async () => {
    const { c, notify } = makeController()
    const after = { ...structuredClone(installFixture), shimStatus: 'not-installed' }
    removeShim.mockResolvedValueOnce(after)

    await c.removeShim()

    expect(removeShim).toHaveBeenCalledTimes(1)
    expect(c.install.value).toEqual(after)
    expect(notify).toHaveBeenCalledWith('success', expect.stringContaining('启动不再自动装回'))
  })

  it('shim 操作失败：记录错误并回读安装面状态，忙碌标记复位', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { c } = makeController()
    await c.refresh()
    installShim.mockRejectedValueOnce(new Error('conflict: file modified'))

    await c.installShim()

    expect(c.actionError.value).toContain('安装 shim 失败')
    expect(c.actionError.value).toContain('file modified')
    expect(c.shimBusy.value).toBeNull()
    expect(getInstallStatus).toHaveBeenCalledTimes(2) // refresh + 失败回读
    errSpy.mockRestore()
  })

  it('技能安装/清理透传三个目标并各自复位忙碌标记', async () => {
    const { c } = makeController()
    for (const t of SKILL_TARGETS) {
      installSkill.mockResolvedValueOnce(structuredClone(installFixture))
      await c.installSkill(t.target)
      expect(installSkill).toHaveBeenLastCalledWith(t.target)
      expect(c.skillBusy[t.target]).toBeNull()

      removeSkill.mockResolvedValueOnce(structuredClone(installFixture))
      await c.removeSkill(t.target)
      expect(removeSkill).toHaveBeenLastCalledWith(t.target)
      expect(c.skillBusy[t.target]).toBeNull()
    }
    expect(installSkill).toHaveBeenCalledTimes(3)
    expect(removeSkill).toHaveBeenCalledTimes(3)
  })

  it('技能安装失败：错误信息带目标名，状态回读', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { c } = makeController()
    installSkill.mockRejectedValueOnce(new Error('target path invalid'))

    await c.installSkill('codex')

    expect(c.actionError.value).toContain('Codex')
    expect(c.actionError.value).toContain('安装技能失败')
    expect(getInstallStatus).toHaveBeenCalledTimes(1)
    errSpy.mockRestore()
  })
})
