import { describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传参数；
// P4-A2 新增的 SetEnvVarPathAllow 绑定手工补齐（本机无 wails CLI）。
const { setEnvVarPathAllowBinding } = vi.hoisted(() => ({
  setEnvVarPathAllowBinding: vi.fn(),
}))

vi.mock('../../../wailsjs/go/main/App', () => ({
  GetEnvVars: vi.fn(),
  SetEnvVar: vi.fn(),
  DeleteEnvVar: vi.fn(),
  GetEnvVarsJSON: vi.fn(),
  SaveEnvVarsJSON: vi.fn(),
  ImportEnvVars: vi.fn(),
  ExportEnvVars: vi.fn(),
  ExportEnvVarsToFile: vi.fn(),
  ImportEnvVarsFromFile: vi.fn(),
  GetEnvVarsGlobalSyncStatus: vi.fn(),
  SetEnvVarsGlobalSyncEnabled: vi.fn(),
  SetEnvVarPathAllow: setEnvVarPathAllowBinding,
}))

import {
  UE5_ENV_TEMPLATE,
  isWSLReservedEnvKey,
  isWindowsDrivePathValue,
  setEnvVarPathAllow,
} from '../../api/envvars'

describe('api.envvars P4-A2 路径值放行门判定面', () => {
  it('isWindowsDrivePathValue：盘符反斜杠/正斜杠形态命中，标量与 Linux 路径不命中', () => {
    expect(isWindowsDrivePathValue('C:\\Program Files\\Epic Games\\UE_5.4')).toBe(true)
    expect(isWindowsDrivePathValue('D:/eng')).toBe(true)
    expect(isWindowsDrivePathValue('  E:\\x  ')).toBe(true) // 容忍首尾空白
    expect(isWindowsDrivePathValue('/mnt/c/Program Files')).toBe(false)
    expect(isWindowsDrivePathValue('5.4')).toBe(false)
    expect(isWindowsDrivePathValue('C:rel')).toBe(false) // 无分隔符的盘符前缀不算路径
    expect(isWindowsDrivePathValue('')).toBe(false)
  })

  it('isWSLReservedEnvKey：WSLENV/PATH/PATHEXT 大小写与空白不敏感', () => {
    expect(isWSLReservedEnvKey('PATH')).toBe(true)
    expect(isWSLReservedEnvKey('path')).toBe(true)
    expect(isWSLReservedEnvKey('  PatheXT ')).toBe(true)
    expect(isWSLReservedEnvKey('WSLENV')).toBe(true)
    expect(isWSLReservedEnvKey('UE_ROOT')).toBe(false)
    expect(isWSLReservedEnvKey('')).toBe(false)
  })

  it('setEnvVarPathAllow 透传 (key, allow) 原生参数到手工绑定', async () => {
    setEnvVarPathAllowBinding.mockResolvedValueOnce(undefined)
    await setEnvVarPathAllow('UE_ROOT', true)
    expect(setEnvVarPathAllowBinding).toHaveBeenCalledWith('UE_ROOT', true)
  })
})

describe('api.envvars UE5 模板（E5）', () => {
  it('静态清单含 UE_ROOT 与版本三件套，键名合法且无重复', () => {
    const keys = UE5_ENV_TEMPLATE.map((e) => e.key)
    expect(keys).toContain('UE_ROOT')
    expect(keys).toContain('UE_VERSION_MAJOR')
    expect(keys).toContain('UE_VERSION_MINOR')
    expect(new Set(keys).size).toBe(keys.length)
    for (const entry of UE5_ENV_TEMPLATE) {
      expect(entry.key).toMatch(/^[A-Z][A-Z0-9_]*$/)
      expect(entry.description.length).toBeGreaterThan(0)
    }
  })

  it('不写死本机路径：条目只携带键名/描述/pathLike，无预设 value 与放行标记', () => {
    for (const entry of UE5_ENV_TEMPLATE) {
      expect('value' in entry).toBe(false)
      expect('allowPathValue' in entry).toBe(false)
    }
  })

  it('仅 UE_ROOT 预期为路径值键（pathLike），版本键为标量', () => {
    const byKey = new Map(UE5_ENV_TEMPLATE.map((e) => [e.key, e]))
    expect(byKey.get('UE_ROOT')?.pathLike).toBe(true)
    expect(byKey.get('UE_VERSION_MAJOR')?.pathLike).toBe(false)
    expect(byKey.get('UE_VERSION_MINOR')?.pathLike).toBe(false)
  })
})
