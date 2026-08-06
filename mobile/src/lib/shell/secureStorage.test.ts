/**
 * mobile/src/lib/shell/secureStorage.test.ts — 安全存储能力探测 Vitest（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * R1 修复后不再注入假插件模块（旧 `__setNativeModuleLoaderForTest` seam 已删除）。
 * 仅通过能力门 seam 模拟平台。验证：
 *   · Web 端恒不可用并如实声明 cookie jar（not-native），且 get/set/remove 永不落
 *     localStorage（断言 localStorage 不被写入）；
 *   · 原生端如实声明 native-pending-shell（native 壳插件接线待 M4-C，不动态 import）。
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import {
  __setNativeGateForTest,
  detectSecureStorageCapability,
  secureGet,
  secureRemove,
  secureSet,
  type NativeGate,
} from './index'

const webGate: NativeGate = { isNativePlatform: () => false, getPlatform: () => 'web' }
const androidGate: NativeGate = { isNativePlatform: () => true, getPlatform: () => 'android' }

afterEach(() => __setNativeGateForTest(webGate))

describe('detectSecureStorageCapability', () => {
  it('web 端回落到 cookie jar 并如实声明 (not-native)', async () => {
    __setNativeGateForTest(webGate)
    const cap = await detectSecureStorageCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) {
      expect(cap.source).toBe('web-cookie-jar')
      expect(cap.reason).toBe('not-native')
    }
  })

  it('原生端如实声明 native-pending-shell（不动态 import 未安装包）', async () => {
    __setNativeGateForTest(androidGate)
    const cap = await detectSecureStorageCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) {
      expect(cap.source).toBe('web-cookie-jar')
      expect(cap.reason).toBe('native-pending-shell')
    }
  })
})

describe('secureGet / secureSet / secureRemove — web 不落 localStorage', () => {
  beforeEach(() => {
    // 确保测试起始无残留；同时验证本模块不会写入 localStorage。
    localStorage.clear()
  })

  it('web 端 secureGet 恒为 unsupported-web-cookie-jar 且不碰 localStorage', async () => {
    __setNativeGateForTest(webGate)
    const res = await secureGet('server_token')
    expect(res.ok).toBe(false)
    if (!res.ok) expect(res.reason).toBe('unsupported-web-cookie-jar')
    expect(localStorage.getItem('server_token')).toBeNull()
  })

  it('web 端 secureSet 恒为 unsupported-web-cookie-jar 且不碰 localStorage', async () => {
    __setNativeGateForTest(webGate)
    const res = await secureSet('server_token', 'secret')
    expect(res.ok).toBe(false)
    if (!res.ok) expect(res.reason).toBe('unsupported-web-cookie-jar')
    expect(localStorage.getItem('server_token')).toBeNull()
  })

  it('web 端 secureRemove 恒为 unsupported-web-cookie-jar', async () => {
    __setNativeGateForTest(webGate)
    const res = await secureRemove('server_token')
    expect(res.ok).toBe(false)
    if (!res.ok) expect(res.reason).toBe('unsupported-web-cookie-jar')
  })
})

describe('secureGet / secureSet / secureRemove — 原生 native-pending-shell', () => {
  it('原生 secureGet 返回 native-pending-shell', async () => {
    __setNativeGateForTest(androidGate)
    const res = await secureGet('k')
    expect(res.ok).toBe(false)
    if (!res.ok) expect(res.reason).toBe('native-pending-shell')
  })

  it('原生 secureSet 返回 native-pending-shell', async () => {
    __setNativeGateForTest(androidGate)
    const res = await secureSet('k', 'v')
    expect(res.ok).toBe(false)
    if (!res.ok) expect(res.reason).toBe('native-pending-shell')
  })

  it('原生 secureRemove 返回 native-pending-shell', async () => {
    __setNativeGateForTest(androidGate)
    const res = await secureRemove('k')
    expect(res.ok).toBe(false)
    if (!res.ok) expect(res.reason).toBe('native-pending-shell')
  })
})
