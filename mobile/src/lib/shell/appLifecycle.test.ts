/**
 * mobile/src/lib/shell/appLifecycle.test.ts — 前后台恢复能力探测 Vitest（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * R1 修复后不再注入假插件模块（旧 `__setNativeModuleLoaderForTest` seam 已删除——它
 * 承载的异步原生注册路径已裁剪）。今天 native 壳插件接线 pending，所有平台统一走
 * `visibilitychange` 回落（无死代码、无异步原生分支）。验证：
 *   · Web 端回落到 visibilitychange（not-native），visible→foreground、hidden→background；
 *   · 原生端探测如实声明 native-pending-shell，但动作同样回落到 visibilitychange；
 *   · 注册的取消函数可移除监听。
 */
import { afterEach, describe, expect, it } from 'vitest'
import {
  __setNativeGateForTest,
  detectLifecycleCapability,
  onAppBackground,
  onAppForeground,
  type NativeGate,
} from './index'

const webGate: NativeGate = { isNativePlatform: () => false, getPlatform: () => 'web' }
const androidGate: NativeGate = { isNativePlatform: () => true, getPlatform: () => 'android' }

function setVisible(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: state })
  document.dispatchEvent(new Event('visibilitychange'))
}

afterEach(() => {
  __setNativeGateForTest(webGate)
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
})

describe('detectLifecycleCapability', () => {
  it('web 端回落到 visibilitychange (not-native)', async () => {
    __setNativeGateForTest(webGate)
    const cap = await detectLifecycleCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) {
      expect(cap.source).toBe('web-visibilitychange')
      expect(cap.reason).toBe('not-native')
    }
  })

  it('原生端如实声明 native-pending-shell（不动态 import 未安装包）', async () => {
    __setNativeGateForTest(androidGate)
    const cap = await detectLifecycleCapability()
    expect(cap.available).toBe(false)
    if (!cap.available) {
      expect(cap.source).toBe('web-visibilitychange')
      expect(cap.reason).toBe('native-pending-shell')
    }
  })
})

describe('onAppForeground / onAppBackground — visibilitychange 回落（web + native-pending 统一）', () => {
  it('visible 触发 foreground，不触发 background（web gate）', () => {
    __setNativeGateForTest(webGate)
    let fg = 0
    let bg = 0
    const offFg = onAppForeground(() => { fg++ })
    const offBg = onAppBackground(() => { bg++ })
    setVisible('visible')
    expect(fg).toBe(1)
    expect(bg).toBe(0)
    offFg()
    offBg()
    setVisible('visible')
    expect(fg).toBe(1) // 已取消，不再触发
  })

  it('hidden 触发 background，不触发 foreground（web gate）', () => {
    __setNativeGateForTest(webGate)
    let fg = 0
    let bg = 0
    const offFg = onAppForeground(() => { fg++ })
    const offBg = onAppBackground(() => { bg++ })
    setVisible('hidden')
    expect(bg).toBe(1)
    expect(fg).toBe(0)
    offFg()
    offBg()
  })

  it('native-pending 同样回落到 visibilitychange（native 壳插件接线待 M4-C）', () => {
    __setNativeGateForTest(androidGate)
    let fg = 0
    let bg = 0
    const offFg = onAppForeground(() => { fg++ })
    const offBg = onAppBackground(() => { bg++ })
    setVisible('visible')
    expect(fg).toBe(1)
    setVisible('hidden')
    expect(bg).toBe(1)
    offFg()
    offBg()
  })
})
