import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传参数 + callApi 语义
//（失败 log + 原样 rethrow，见 api/internal/call.ts）。
// 覆盖 Claude Code Web 视图开关（claude-webui 设计 §8）的两个包装函数。
const { getEnabled, setEnabled } = vi.hoisted(() => ({
  getEnabled: vi.fn(),
  setEnabled: vi.fn(),
}))

vi.mock('../../../wailsjs/go/settings/Service', () => ({
  GetClaudeWebUIEnabled: getEnabled,
  SetClaudeWebUIEnabled: setEnabled,
}))

import { getClaudeWebUIEnabled, setClaudeWebUIEnabled } from '../../api/settings'

describe('api.settings Claude Code Web 视图开关', () => {
  let errorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    getEnabled.mockReset()
    setEnabled.mockReset()
    errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    errorSpy.mockRestore()
  })

  it('getClaudeWebUIEnabled 无参调用绑定并原样返回布尔值', async () => {
    getEnabled.mockResolvedValueOnce(true)
    await expect(getClaudeWebUIEnabled()).resolves.toBe(true)

    getEnabled.mockResolvedValueOnce(false)
    await expect(getClaudeWebUIEnabled()).resolves.toBe(false)

    expect(getEnabled).toHaveBeenCalledTimes(2)
    expect(getEnabled).toHaveBeenNthCalledWith(1)
    expect(errorSpy).not.toHaveBeenCalled()
  })

  it('setClaudeWebUIEnabled 透传布尔参数（开与关）', async () => {
    setEnabled.mockResolvedValue(undefined)

    await expect(setClaudeWebUIEnabled(true)).resolves.toBeUndefined()
    await expect(setClaudeWebUIEnabled(false)).resolves.toBeUndefined()

    expect(setEnabled).toHaveBeenCalledTimes(2)
    expect(setEnabled).toHaveBeenNthCalledWith(1, true)
    expect(setEnabled).toHaveBeenNthCalledWith(2, false)
  })

  it('读取失败：打印带操作上下文的日志并原样 rethrow 同一错误对象', async () => {
    const boom = new Error('settings not loaded')
    getEnabled.mockRejectedValueOnce(boom)

    await expect(getClaudeWebUIEnabled()).rejects.toBe(boom)
    expect(errorSpy).toHaveBeenCalledWith('[api.settings.getClaudeWebUIEnabled]', boom)
  })

  it('写入失败：打印带操作上下文的日志并原样 rethrow 同一错误对象', async () => {
    const boom = new Error('save settings: disk full')
    setEnabled.mockRejectedValueOnce(boom)

    await expect(setClaudeWebUIEnabled(false)).rejects.toBe(boom)
    expect(setEnabled).toHaveBeenCalledWith(false)
    expect(errorSpy).toHaveBeenCalledWith('[api.settings.setClaudeWebUIEnabled]', boom)
  })
})
