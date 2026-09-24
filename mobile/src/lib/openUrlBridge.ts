/**
 * mobile/src/lib/openUrlBridge.ts — pi webui → 移动宿主「打开外部链接」消息桥（跨仓冻结契约 v2.7.4）
 * ---------------------------------------------------------------------------
 * 背景：Web 平面 iframe 为 sandbox="allow-scripts allow-forms"（无 allow-popups，也无
 * allow-top-navigation），webui 页面内 markdown 链接的 target=_blank 在嵌入态被静默
 * 阻断。webui 改为向 parent 上抛 open-url 请求，由宿主代为打开；移动端此前完全缺失
 * 该监听（桌面端 WebPlaneHost.vue 1.3.87+ 已实现），本模块补齐宿主侧解析与打开动作。
 *
 * 守卫语义与桌面端 frontend/src/components/terminal/quickPathInsert.ts 严格对齐
 * （跨仓冻结契约，勿改常量值）：
 *   · type 精确等于 'amagi:open-url'；
 *   · token 与宿主构造 iframe URL 时持有的 capability token（fragment `#/t=<token>`）
 *     严格相等（origin 不参与——sandbox iframe 为 opaque origin，event.origin 恒为 'null'）；
 *   · url 为 string、http(s) 白名单、长度 ≤2048。
 * 任一不过返回 null（静默忽略：伪造/过期消息不是用户可感知事件）。
 *
 * 与桌面的有意差异（打开动作）：桌面经 Go 绑定 BrowserOpenURL 交系统浏览器；移动端
 * 无 Wails 桥，用本机 window.open 打开。选型证据（Capacitor 8 Android 源码级核实：
 * BridgeWebChromeClient 未覆写 onCreateWindow、未 setSupportMultipleWindows，
 * BridgeWebViewClient.shouldOverrideUrlLoading → Bridge.launchIntent → Intent.ACTION_VIEW
 * 交系统浏览器）见 agent-outputs/luban/mobile-openurl-bridge-report.md。
 * ---------------------------------------------------------------------------
 */

/** webui → 宿主「打开外部链接」的 postMessage type（跨仓冻结契约 v2.7.4，勿改）。 */
export const OPEN_URL_MESSAGE_TYPE = 'amagi:open-url'

/** 上行链接长度上限（跨仓冻结契约，勿改；防御异常长 URL）。 */
export const MAX_OPEN_URL_LENGTH = 2048

/** capability token 形状（与 amagi-pi webui transport.ts CAPABILITY_PATTERN、桌面端一致）。 */
const CAPABILITY_PATTERN = /^[A-Za-z0-9_-]{22,}$/

/** http(s) 白名单（scheme 大小写不敏感，与桌面端 /^https?:\/\//i 一致）。 */
const HTTP_URL_PATTERN = /^https?:\/\//i

/**
 * 从 webui iframe URL（`${httpBase}/#/t=<token>`，契约 §6.5）提取 capability token。
 * fragment 缺失 / 格式不合法（非 [A-Za-z0-9_-]{22,}）/ 解码失败 → null。
 *
 * 实参应为宿主实际挂载的 iframe src（WebPlaneView 的 frameSrc，可能带 ?skin=light——
 * 该 query 插在 # 之前，不影响 `#/t=` 定位）。
 */
export function extractCapabilityToken(url: string): string | null {
  const idx = url.indexOf('#/t=')
  if (idx < 0) return null
  try {
    const token = decodeURIComponent(url.slice(idx + 4))
    return CAPABILITY_PATTERN.test(token) ? token : null
  } catch {
    return null
  }
}

/**
 * webui → 宿主「打开外部链接」消息解析（跨仓契约 v2.7.4）。
 *
 * `ownToken` 由调用方用 extractCapabilityToken 从当前 iframe src 提取；宿主无 token
 * （iframe 未挂载 / fragment 异常）时一律拒绝。全部守卫通过 → 返回 url（调用方交
 * openExternalUrl）；任一不满足 → null。
 */
export function parseOpenUrlMessage(
  data: unknown,
  ownToken: string | null,
): string | null {
  if (!ownToken) return null
  if (typeof data !== 'object' || data === null) return null
  const { type, token, url } = data as { type?: unknown; token?: unknown; url?: unknown }
  if (type !== OPEN_URL_MESSAGE_TYPE) return null
  if (typeof token !== 'string' || token !== ownToken) return null
  if (typeof url !== 'string' || url.length === 0 || url.length > MAX_OPEN_URL_LENGTH) return null
  if (!HTTP_URL_PATTERN.test(url)) return null
  return url
}

/**
 * 打开外部链接（唯一动作出口）。
 *
 * 二次白名单（对齐桌面端 Go 侧 ValidateExternalURL 的纵深防御）：即便调用方已由
 * parseOpenUrlMessage 校验，本函数仍只放行 http(s)，其余一律拒绝且不产生副作用。
 *
 * 打开方式为 `window.open(url, '_blank', 'noopener,noreferrer')`：
 *   · `_blank` + 默认多窗口策略（Capacitor 8 Android 未 setSupportMultipleWindows）
 *     → 交 WebView 导航层，经 Bridge.launchIntent → Intent.ACTION_VIEW 由系统浏览器打开；
 *   · `noopener` 切断被打开页面与宿主 window.opener 的关联（noopener 语义）；
 *   · `noreferrer` 不向外部站点泄漏宿主页面地址（桌面 Go 打开同样不带 referrer）。
 * window.open 带 noopener 时按规范恒返回 null，故不以返回值判定成败；返回 true 表示
 * 已发起打开动作（本机动作成败由系统浏览器接管，宿主不感知）。
 *
 * 选型备选（@capacitor/browser）未采用：需新增原生插件依赖，而本仓壳工程（mobile/android）
 * escrow 至 M4-C、无可注册的 native 实现，插件调用在 Android 上会 reject
 * "not implemented on android"，反而使链接点击彻底失效；详见实现报告。
 */
export function openExternalUrl(url: string): boolean {
  if (!HTTP_URL_PATTERN.test(url)) return false
  window.open(url, '_blank', 'noopener,noreferrer')
  return true
}
