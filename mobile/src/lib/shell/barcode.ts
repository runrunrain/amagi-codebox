/**
 * mobile/src/lib/shell/barcode.ts — 扫码能力探测 + Web 回落（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * · 原生路径（pending M4-C）：`@capacitor/barcode-scanner` 在真实 Capacitor 壳内经
 *   `registerPlugin` 接线后可用。今天该插件未安装、无平台目录——R1 修复删除了原先
 *   `@vite-ignore` 动态 import 的死路径（bare specifier 不可被 Vite 静态解析，加载
 *   范式也与 Capacitor 插件模型不符）。`detectBarcodeCapability` 在原生平台如实返回
 *   typed `native-pending-shell`，声明 native 接线待 M4-C 壳工程。
 * · Web 回落：手动输入引导——用户手输/粘贴 Server URL + Token。ConnectPage 现有
 *   的 html5-qrcode 页内 Web 扫码不在本模块职责内，本模块只报告能力并提供动作接缝。
 *
 * API 形态为 M4-C registerPlugin 预留稳定接缝：公共签名（`detectBarcodeCapability` /
 * `scanBarcode` / typed `BarcodeCapability` 判别联合）不变；M4-C 只替换实现——在
 * `detectBarcodeCapability` 原生分支用 `registerPlugin('BarcodeScanner')` 解析插件，
 * 并在 `scanBarcode` 的 available 分支调用真实 `scanBarcode(options)`（注意真实 API 是
 * `scanBarcode` 返回 `{ ScanResult }`，非旧的 `scan`/`{hasContent,content}`）。
 */
import { NATIVE_PLUGIN_PENDING_DETAIL, currentGate } from './capability'

/** 原生扫码不可用时的类型化原因。`native-pending-shell` = native 壳插件接线待 M4-C。 */
export type BarcodeUnavailableReason = 'not-native' | 'native-pending-shell'

/** 扫码能力探测结果（判别联合 on `available`）。 */
export type BarcodeCapability =
  | { available: true; source: 'capacitor-barcode-scanner'; platform: 'android' | 'ios' }
  | {
      available: false
      source: 'web-manual-input'
      reason: BarcodeUnavailableReason
      detail?: string
    }

/** 扫码成功。 */
export interface BarcodeScanSuccess {
  ok: true
  value: string
}

/** 扫码回落：原生不可用，调用方应引导手动输入。 */
export interface BarcodeScanFallback {
  ok: false
  capability: BarcodeCapability
}

export type BarcodeScanResult = BarcodeScanSuccess | BarcodeScanFallback

/**
 * 探测扫码能力。
 * · Web/非原生平台 → `not-native`，回落到手动输入引导。
 * · 原生平台 → `native-pending-shell`：native 壳插件接线待 M4-C（今天无平台目录、
 *   无插件安装）。绝不尝试动态 import 未安装的 bare 包（旧死路径已删）。
 * 两条分支都落到带原因的 Web 回落，绝不抛出。
 */
export async function detectBarcodeCapability(): Promise<BarcodeCapability> {
  const g = currentGate()
  if (!g.isNativePlatform()) {
    return { available: false, source: 'web-manual-input', reason: 'not-native', detail: g.getPlatform() }
  }
  return {
    available: false,
    source: 'web-manual-input',
    reason: 'native-pending-shell',
    detail: NATIVE_PLUGIN_PENDING_DETAIL,
  }
}

/**
 * 执行扫码。今天探测恒为不可用（Web→not-native；原生→native-pending-shell），故恒
 * 返回类型化回落交调用方引导手动输入。
 *
 * 稳定接缝：当 M4-C 使 `detectBarcodeCapability` 在原生平台返回 `available:true` 时，
 * 本函数的 `available` 分支是 native 扫码的实现替换点——M4-C 在此调用经
 * `registerPlugin('BarcodeScanner')` 解析的插件的 `scanBarcode(options)` 并把
 * `{ ScanResult }` 映射为 `BarcodeScanSuccess`。今天该分支不可达，返回 typed pending
 * 保持签名稳定，不交付假数据/mock。
 */
export async function scanBarcode(): Promise<BarcodeScanResult> {
  const capability = await detectBarcodeCapability()
  if (capability.available) {
    // M4-C seam —— native 插件已解析但扫码动作尚未接线。今天不可达（探测恒 unavailable）。
    return {
      ok: false,
      capability: {
        available: false,
        source: 'web-manual-input',
        reason: 'native-pending-shell',
        detail: 'native scan action pending M4-C wiring (registerPlugin)',
      },
    }
  }
  return { ok: false, capability }
}
