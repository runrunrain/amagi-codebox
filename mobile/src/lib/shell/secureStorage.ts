/**
 * mobile/src/lib/shell/secureStorage.ts — 安全存储能力探测 + Web 回落（M4-D / R1 修复）。
 * ---------------------------------------------------------------------------
 * · 原生路径（pending M4-C）：Capacitor secure-storage 类插件在真实壳内经
 *   `registerPlugin` 接线后可用，经 Keychain/Keystore 存取机密。今天无平台目录、
 *   无插件安装——R1 修复删除了原先 `@vite-ignore` 动态 import 死路径（且经
 *   `npm view @capacitor-community/secure-storage` 终判 E404：该社区包不存在；替代包
 *   `capacitor-secure-storage-plugin` 仅 Cap 7 且 web 实现用 LocalStorage+base64，违反
 *   凭据纪律——secure-storage 在 Cap 8 生态暂无满足条件的包）。`detectSecureStorageCapability`
 *   在原生平台如实返回 typed `native-pending-shell`，声明 native 接线待 M4-C。
 * · Web 回落：**如实声明**——Web 端没有由应用管理的安全存储；v1 设备 Cookie 由
 *   浏览器 cookie jar（HttpOnly）承载，浏览器自动随请求发送，应用无需也无法在
 *   客户端持久化该机密。本模块**不引入** localStorage 凭据存储（现有 legacy 客户端
 *   把 Bearer Token 写入 localStorage('server_token')，那是 legacy 现状而非本层
 *   引入；v1 迁移后该路径应随 M1-D 移除，详见 design §11）。
 *
 * API 形态为 M4-C registerPlugin 预留稳定接缝：公共签名（`detectSecureStorageCapability` /
 * `secureGet|Set|Remove` / typed 结果联合）不变；M4-C 只替换实现。
 */
import { NATIVE_PLUGIN_PENDING_DETAIL, currentGate } from './capability'

export type SecureStorageUnavailableReason = 'not-native' | 'native-pending-shell'

/** 安全存储能力探测结果。Web 回落即「cookie jar 现状如实声明」。 */
export type SecureStorageCapability =
  | { available: true; source: 'capacitor-secure-storage' }
  | {
      available: false
      source: 'web-cookie-jar'
      reason: SecureStorageUnavailableReason
      detail?: string
    }

/**
 * 读结果：成功带值；否则带类型化原因。
 * · `unsupported-web-cookie-jar`：Web 端无客户端机密（cookie jar 承载）。
 * · `native-pending-shell`：原生平台但壳插件接线待 M4-C。
 * · `not-found` / `plugin-error`：M4-C 接线后的运行期结果（今天不可达）。
 */
export type SecureGetResult =
  | { ok: true; value: string }
  | { ok: false; reason: 'unsupported-web-cookie-jar' | 'native-pending-shell' | 'plugin-missing' | 'not-found' | 'plugin-error'; capability: SecureStorageCapability }

/** 写/删结果：成功/失败带类型化原因（语义同 SecureGetResult.reason）。 */
export type SecureOpResult =
  | { ok: true }
  | { ok: false; reason: 'unsupported-web-cookie-jar' | 'native-pending-shell' | 'plugin-missing' | 'plugin-error'; capability: SecureStorageCapability }

/**
 * 探测安全存储能力。
 * · Web/非原生平台 → `not-native`：如实声明 cookie jar 现状。
 * · 原生平台 → `native-pending-shell`：native 壳插件接线待 M4-C。绝不动态 import 未安装包。
 * 两条分支都落到带原因的 Web 回落，绝不抛。
 */
export async function detectSecureStorageCapability(): Promise<SecureStorageCapability> {
  const g = currentGate()
  if (!g.isNativePlatform()) {
    return { available: false, source: 'web-cookie-jar', reason: 'not-native', detail: 'web relies on browser cookie jar (HttpOnly); no client-side secret store' }
  }
  return {
    available: false,
    source: 'web-cookie-jar',
    reason: 'native-pending-shell',
    detail: NATIVE_PLUGIN_PENDING_DETAIL,
  }
}

/**
 * 读取机密。今天探测恒为不可用：Web 端恒返回 `unsupported-web-cookie-jar`（机密不在
 * 客户端，由 cookie jar 承载）；原生端返回 `native-pending-shell`。
 *
 * 稳定接缝：当 M4-C 使探测在原生平台返回 `available:true` 时，本函数的 `available`
 * 分支是 native 读操作的实现替换点——M4-C 在此调用经 `registerPlugin` 解析的插件的
 * `get(key)` 并映射结果。今天该分支不可达，返回 typed pending，不交付假数据。
 */
export async function secureGet(_key: string): Promise<SecureGetResult> {
  const capability = await detectSecureStorageCapability()
  if (capability.available) {
    // M4-C seam —— native 插件已解析但读动作尚未接线。今天不可达（探测恒 unavailable）。
    return { ok: false, reason: 'native-pending-shell', capability: { available: false, source: 'web-cookie-jar', reason: 'native-pending-shell', detail: 'native get action pending M4-C wiring (registerPlugin)' } }
  }
  return { ok: false, reason: capability.reason === 'not-native' ? 'unsupported-web-cookie-jar' : 'native-pending-shell', capability }
}

/**
 * 写入机密。Web 端恒返回 `unsupported-web-cookie-jar`——本模块**不**在 Web 端把凭据
 * 写入 localStorage（避免扩大客户端凭据面）；原生端 `native-pending-shell`。稳定接缝
 * 语义同 secureGet。
 */
export async function secureSet(_key: string, _value: string): Promise<SecureOpResult> {
  const capability = await detectSecureStorageCapability()
  if (capability.available) {
    // M4-C seam —— 今天不可达。
    return { ok: false, reason: 'native-pending-shell', capability: { available: false, source: 'web-cookie-jar', reason: 'native-pending-shell', detail: 'native set action pending M4-C wiring (registerPlugin)' } }
  }
  return { ok: false, reason: capability.reason === 'not-native' ? 'unsupported-web-cookie-jar' : 'native-pending-shell', capability }
}

/** 删除机密。语义同 set；Web 端无客户端机密可删。稳定接缝语义同 secureGet。 */
export async function secureRemove(_key: string): Promise<SecureOpResult> {
  const capability = await detectSecureStorageCapability()
  if (capability.available) {
    // M4-C seam —— 今天不可达。
    return { ok: false, reason: 'native-pending-shell', capability: { available: false, source: 'web-cookie-jar', reason: 'native-pending-shell', detail: 'native remove action pending M4-C wiring (registerPlugin)' } }
  }
  return { ok: false, reason: capability.reason === 'not-native' ? 'unsupported-web-cookie-jar' : 'native-pending-shell', capability }
}
