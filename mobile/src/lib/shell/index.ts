/**
 * mobile/src/lib/shell/index.ts — 壳增强抽象层公共导出面（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * 三项壳能力（扫码 / 安全存储 / 前后台恢复）的唯一入口。消费方从这里导入类型与
 * 探测/动作 API，不深 import 各子模块，保证能力探测与回落路径单一来源。
 *
 * 能力探测 seam（`__setNativeGateForTest`）仅测试使用，从 capability 子模块显式
 * 再导出以便测试就近导入。R1 修复删除了原先的动态原生模块加载器 seam
 * （`loadNativeModule` / `NativeModuleLoader` / `__setNativeModuleLoaderForTest`）——
 * 它承载的 `@vite-ignore` 动态 import 是不可解析的死路径（diting M4-003）。
 */
export {
  NATIVE_PLUGIN_PENDING_DETAIL,
  currentGate,
  describeError,
  __setNativeGateForTest,
  type NativeGate,
  type Platform,
} from './capability'

export {
  detectBarcodeCapability,
  scanBarcode,
  type BarcodeCapability,
  type BarcodeScanFallback,
  type BarcodeScanResult,
  type BarcodeScanSuccess,
  type BarcodeUnavailableReason,
} from './barcode'

export {
  detectSecureStorageCapability,
  secureGet,
  secureRemove,
  secureSet,
  type SecureGetResult,
  type SecureOpResult,
  type SecureStorageCapability,
  type SecureStorageUnavailableReason,
} from './secureStorage'

export {
  detectLifecycleCapability,
  onAppBackground,
  onAppForeground,
  type LifecycleCapability,
  type LifecycleUnavailableReason,
} from './appLifecycle'
