/**
 * mobile/src/lib/shell/barcode.test.ts — 扫码能力探测 Vitest（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * R1 修复后不再注入假插件模块（旧 `__setNativeModuleLoaderForTest` seam 已删除——它
 * 承载的 `@vite-ignore` 动态 import 是不可解析死路径，diting M4-003）。仅通过能力门
 * seam（NativeGate）模拟平台。验证两条真实路径：
 *   · Web 平台 → not-native 回落到手动输入；
 *   · 原生平台 → native-pending-shell（native 壳插件接线待 M4-C，不动态 import）。
 * scanBarcode 今天恒返回类型化回落（探测恒不可用）。
 */
import { afterEach, describe, expect, it } from 'vitest'
import {
  NATIVE_PLUGIN_PENDING_DETAIL,
  __setNativeGateForTest,
  detectBarcodeCapability,
  scanBarcode,
  type NativeGate,
} from './index'

const webGate: NativeGate = { isNativePlatform: () => false, getPlatform: () => 'web' }
const androidGate: NativeGate = { isNativePlatform: () => true, getPlatform: () => 'android' }
const iosGate: NativeGate = { isNativePlatform: () => true, getPlatform: () => 'ios' }

afterEach(() => __setNativeGateForTest(webGate))

describe('detectBarcodeCapability', () => {
  it('web 平台回落到手动输入 (not-native)', async () => {
    __setNativeGateForTest(webGate)
    const cap = await detectBarcodeCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) {
      expect(cap.source).toBe('web-manual-input')
      expect(cap.reason).toBe('not-native')
      expect(cap.detail).toBe('web')
    }
  })

  it('原生平台如实声明 native-pending-shell（不动态 import 未安装包）', async () => {
    __setNativeGateForTest(androidGate)
    const cap = await detectBarcodeCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) {
      expect(cap.source).toBe('web-manual-input')
      expect(cap.reason).toBe('native-pending-shell')
      expect(cap.detail).toBe(NATIVE_PLUGIN_PENDING_DETAIL)
    }
  })

  it('原生 ios 平台同样 native-pending-shell', async () => {
    __setNativeGateForTest(iosGate)
    const cap = await detectBarcodeCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) expect(cap.reason).toBe('native-pending-shell')
  })

  it('探测绝不抛出（无动态 import / 无 bare-specifier 解析尝试）', async () => {
    __setNativeGateForTest(androidGate)
    await expect(detectBarcodeCapability()).resolves.toMatchObject({ available: false })
  })
})

describe('scanBarcode', () => {
  it('web 端返回回落，引导手动输入', async () => {
    __setNativeGateForTest(webGate)
    const res = await scanBarcode()
    expect(res.ok).toBe(false)
    if (!res.ok) {
      expect(res.capability.source).toBe('web-manual-input')
      if (!res.capability.available) expect(res.capability.reason).toBe('not-native')
    }
  })

  it('原生端返回 native-pending-shell 回落（不调用任何插件）', async () => {
    __setNativeGateForTest(androidGate)
    const res = await scanBarcode()
    expect(res.ok).toBe(false)
    if (!res.ok && !res.capability.available) {
      expect(res.capability.reason).toBe('native-pending-shell')
    }
  })
})
