/**
 * mobile/src/lib/shell/appLifecycle.ts — 前后台恢复能力探测 + Web 回落（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * · 原生路径（pending M4-C）：`@capacitor/app` 在真实 Capacitor 壳内经 `registerPlugin`
 *   接线后监听 'resume'（回前台）/ 'pause'（退后台）。今天该插件未安装、无平台目录——
 *   R1 修复删除了原先 `@vite-ignore` 动态 import 死路径。`detectLifecycleCapability`
 *   在原生平台如实返回 typed `native-pending-shell`，声明 native 接线待 M4-C。
 * · Web 回落：`document.visibilitychange`——`visibilityState==='visible'` 视作回前台，
 *   `'hidden'` 视作退后台。该回落覆盖 Web 端标签页切走/系统休眠后返回的恢复场景。
 *   今天 native 壳插件接线 pending，原生平台同样回落到 visibilitychange（本机无原生
 *   运行时，visibilitychange 是当前唯一可用机制；M4-C 接线后替换为原生 App 监听）。
 *
 * API 形态为 M4-C registerPlugin 预留稳定接缝：公共签名（`detectLifecycleCapability` /
 * `onAppForeground|Background` / typed `LifecycleCapability`）不变；M4-C 在
 * `detectLifecycleCapability` 原生分支用 `registerPlugin('App')` 解析并在
 * `trackNativeOrWeb` 原生分支注册真实 `App.addListener`——只替换实现。
 */
import { NATIVE_PLUGIN_PENDING_DETAIL, currentGate } from './capability'

export type LifecycleUnavailableReason = 'not-native' | 'native-pending-shell'

/** 前后台恢复能力探测结果。Web 回落即 visibilitychange。 */
export type LifecycleCapability =
  | { available: true; source: 'capacitor-app' }
  | {
      available: false
      source: 'web-visibilitychange'
      reason: LifecycleUnavailableReason
      detail?: string
    }

/**
 * 探测前后台恢复能力。
 * · Web/非原生平台 → `not-native`：回落到 visibilitychange。
 * · 原生平台 → `native-pending-shell`：native 壳插件接线待 M4-C。不动态 import 未安装包。
 * 两条分支都回落，绝不抛。
 */
export async function detectLifecycleCapability(): Promise<LifecycleCapability> {
  const g = currentGate()
  if (!g.isNativePlatform()) {
    return { available: false, source: 'web-visibilitychange', reason: 'not-native', detail: g.getPlatform() }
  }
  return {
    available: false,
    source: 'web-visibilitychange',
    reason: 'native-pending-shell',
    detail: NATIVE_PLUGIN_PENDING_DETAIL,
  }
}

/**
 * 注册「回前台」回调，返回取消订阅函数。今天（native pending）用 `visibilitychange`
 * （仅 visible 触发）；M4-C 接线后原生分支改用 `App 'resume'`。调用方总能拿到一个
 * 有效的取消函数。
 */
export function onAppForeground(callback: () => void): () => void {
  return registerVisibilityListener(callback, 'visible')
}

/**
 * 注册「退后台」回调，返回取消订阅函数。今天（native pending）用 `visibilitychange`
 * （仅 hidden 触发）；M4-C 接线后原生分支改用 `App 'pause'`。语义/回落策略同 onAppForeground。
 */
export function onAppBackground(callback: () => void): () => void {
  return registerVisibilityListener(callback, 'hidden')
}

/**
 * Web visibilitychange 监听器：仅在匹配的 visibilityState 触发回调。返回取消函数。
 *
 * 稳定接缝（M4-C）：本函数是 visibilitychange 回落的唯一实现。M4-C 接线后新增一个
 * 「原生平台 → registerPlugin('App').addListener(eventName, cb)」分支与本函数并列
 * （按 `currentGate().isNativePlatform()` 分流），公共签名不变。今天无原生分支——
 * native 壳插件接线 pending，所有平台统一走 visibilitychange，无死代码。
 */
function registerVisibilityListener(callback: () => void, when: 'visible' | 'hidden'): () => void {
  if (typeof document === 'undefined') {
    // SSR/非 DOM 环境：无法监听，返回空取消函数（不抛）。
    return () => {}
  }
  const handler = () => {
    if (document.visibilityState === when) callback()
  }
  document.addEventListener('visibilitychange', handler)
  return () => document.removeEventListener('visibilitychange', handler)
}
