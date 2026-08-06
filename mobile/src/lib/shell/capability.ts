/**
 * mobile/src/lib/shell/capability.ts — shared native-capability gate (M4-D / R1 修复).
 * ---------------------------------------------------------------------------
 * 三项壳能力（扫码 / 安全存储 / 前后台恢复）共用本门。设计铁律：
 *
 * 1. 只有 `@capacitor/core` 是真实依赖（已在 mobile/package.json 中安装），本模块
 *    对它的静态 import 可被 Vite 完整静态解析——这是唯一的 native 接触点。
 * 2. 原生平台判定唯一来源是 `Capacitor.isNativePlatform()` / `getPlatform()`。
 *    本模块把 Capacitor 全局抽象为 `NativeGate`，测试通过 `__setNativeGateForTest`
 *    替换以模拟 android/ios/web（即任务要求的「mock Capacitor 全局」）。
 * 3. 壳增强插件（barcode-scanner / secure-storage / app）当前**均未安装**，且本机
 *    无 android/ios 平台目录——native 壳工程 escrow 至 M4-C。R1 修复（diting M4-003 /
 *    路线 c）删除了原先「非字面量 specifier + `@vite-ignore` 动态 import」的死路径：
 *    该写法既不可被 Vite 静态解析（bare specifier 永不进产物），其加载范式也与
 *    Capacitor 插件模型不符（M4-C 应改用 `@capacitor/core` 的 `registerPlugin` 静态
 *    导入创建代理）。本层现在如实声明 native 插件接线「pending M4-C」，由各能力模块
 *    的 `detect*Capability` 在原生平台返回 typed `native-pending-shell` 状态。
 *
 * 物理边界声明：本机无 android/ios 平台目录、无 Android SDK，原生构建验证
 * （插件真实安装、真机扫码/Keychain/resume）escrow 至 M4-C。本层交付的是能力探测
 * API + 类型化回落路径 + Vitest；原生运行期证据不在本任务。
 *
 * M4-C 接线纪律：引入平台目录与壳插件后，用 `registerPlugin('PluginName')`（静态
 * import 自已安装的 `@capacitor/core`）创建代理并替换各 `detect*Capability` 的原生
 * 分支实现；公共 API 签名（`detect*Capability` / `scanBarcode` / `secure*` /
 * `onAppForeground|Background`）保持不变，仅替换实现。
 */
import { Capacitor } from '@capacitor/core'

/** Capacitor 报告的运行平台（web 含 SSR/桌面浏览器）。 */
export type Platform = 'android' | 'ios' | 'web'

/** 原生平台门：唯一从 `@capacitor/core` 读取 Capacitor 全局的地方。 */
export interface NativeGate {
  /** 仅在真实 Capacitor 原生壳（android/ios）内为 true；web/SSR 为 false。 */
  isNativePlatform(): boolean
  /** 返回 'android' | 'ios' | 'web'。 */
  getPlatform(): Platform
}

/** 生产门：直接转发到 @capacitor/core 的 Capacitor 全局。 */
const productionGate: NativeGate = {
  isNativePlatform: () => Capacitor.isNativePlatform(),
  getPlatform: () => Capacitor.getPlatform() as Platform,
}

// 内部可覆盖门（测试 seam）。生产代码不得调用下面的 setter。
let gate: NativeGate = productionGate

/**
 * 仅测试用：替换原生平台门。返回上一个门以便测试恢复。生产代码禁止调用。
 */
export function __setNativeGateForTest(next: NativeGate): NativeGate {
  const prev = gate
  gate = next
  return prev
}

/** 读取当前门（模块内私有访问点）。 */
export function currentGate(): NativeGate {
  return gate
}

/** 把任意 unknown 错误归一为字符串（不抛、不泄漏细节给上层，仅用于 detail 文案）。 */
export function describeError(err: unknown): string {
  if (err instanceof Error) return err.message
  return typeof err === 'string' ? err : 'unknown error'
}

/**
 * native 壳插件接线的统一 pending 声明文案。各 `detect*Capability` 在原生平台返回
 * `native-pending-shell` 时引用本常量作为 detail，确保 M4-C 接线点的指向单一来源。
 * 今天没有动态 import / registerPlugin 调用——native 插件访问是 M4-C 的实现替换点。
 */
export const NATIVE_PLUGIN_PENDING_DETAIL =
  'native shell plugin wiring pending M4-C (registerPlugin via @capacitor/core); see diting M4-003 / cangjie r1-native-loading-analysis route (c)'
