/**
 * host-bridge.ts — SPA ↔ 宿主桥（design §7.2 host-bridge，t8）。
 *
 * 跨仓冻结契约 v2（与主前端 frontend/src/components/terminal/quickPathInsert.ts
 * 同源，勿改消息形状）：
 *   宿主 → SPA：{ type: 'amagi:insert-input', token, text }
 *     接收端守卫（全部通过才放行，任一不满足静默忽略、不报错）：
 *       1. 嵌入态（iframe 内运行）；
 *       2. token 为 string 且与自身 URL fragment `#/t=<token>` 的 capability
 *          token **严格相等**（凭证校验来源）；
 *       3. text 为非空 string、UTF-8 ≤128KiB。
 *     通过后：追加进输入框并聚焦，**不自动发送**。
 *   SPA → 宿主：{ type: 'amagi:open-url', url, token }
 *     嵌入态 sandbox="allow-scripts allow-forms"（无 allow-popups），页面内
 *     a[target=_blank] 点击被浏览器静默阻断——改为拦截点击、postMessage 上抛，
 *     宿主校验 token 后经 Go BrowserOpenURL 走系统浏览器。url 须 http(s)
 *     白名单且 ≤2048（与宿主 parseOpenUrlMessage 的接收守卫同口径）。
 *
 * **绝不以 event.origin 判宿主**（pi 实证陷阱，quickPathInsert.ts v1→v2
 * 修复记录）：event.origin 是发送方 origin（codebox 壳为 http://wails.localhost），
 * 不是 'null'；sandbox 页面自身是 opaque origin 也无从预判宿主 origin。
 * 来源校验只认 token 凭证，与 origin 语义解耦。targetOrigin 固定 '*'：
 * 本页 origin 为 opaque，宿主 origin 不可指定，安全由宿主侧 token 守卫兜底。
 */

/** 宿主 → SPA 插入指令的 postMessage type（跨仓冻结契约，勿改）。 */
export const INSERT_INPUT_MESSAGE_TYPE = 'amagi:insert-input'

/** SPA → 宿主「打开外部链接」的 postMessage type（跨仓冻结契约 v2.7.4，勿改）。 */
export const OPEN_URL_MESSAGE_TYPE = 'amagi:open-url'

/** 插入文本的 UTF-8 字节上限（契约：≤128KiB；超限静默拒绝）。 */
export const MAX_INSERT_TEXT_BYTES = 128 * 1024

/** 上抛链接长度上限（与宿主 quickPathInsert MAX_OPEN_URL_LENGTH 一致）。 */
export const MAX_OPEN_URL_LENGTH = 2048

const utf8Encoder = new TextEncoder()

/** 上抛消息形状（冻结：{type, url, token}）。 */
export interface OpenUrlMessage {
  type: typeof OPEN_URL_MESSAGE_TYPE
  url: string
  token: string
}

/**
 * 解析宿主插入指令（纯函数，守卫见文件头）。
 * 返回待插入文本；任一守卫不满足返回 null（静默忽略——伪造/过期消息不是
 * 用户可感知事件）。
 */
export function parseInsertMessage(
  data: unknown,
  ownToken: string | null,
  isEmbedded: boolean,
): string | null {
  if (!isEmbedded) return null
  if (!ownToken) return null
  if (typeof data !== 'object' || data === null) return null
  const { type, token, text } = data as { type?: unknown; token?: unknown; text?: unknown }
  if (type !== INSERT_INPUT_MESSAGE_TYPE) return null
  if (typeof token !== 'string' || token !== ownToken) return null
  if (typeof text !== 'string' || text.length === 0) return null
  if (utf8Encoder.encode(text).length > MAX_INSERT_TEXT_BYTES) return null
  return text
}

/** 上抛链接守卫：http(s) 白名单 + 长度 1..2048。 */
export function isValidOpenUrl(url: string): boolean {
  if (url.length === 0 || url.length > MAX_OPEN_URL_LENGTH) return false
  return /^https?:\/\//i.test(url)
}

/** 组装上抛消息；凭证缺失或 url 不合法返回 null（不上抛）。 */
export function buildOpenUrlMessage(url: string, ownToken: string | null): OpenUrlMessage | null {
  if (!ownToken) return null
  if (!isValidOpenUrl(url)) return null
  return { type: OPEN_URL_MESSAGE_TYPE, url, token: ownToken }
}

/** 事件目标的最小结构面（DOM Element 天然满足；测试可注入桩）。 */
interface AnchorLike {
  href: string
}

/** 从点击目标向上找 a[target=_blank]（非锚点点击返回 null）。 */
export function findTargetBlankAnchor(target: unknown): AnchorLike | null {
  if (typeof target !== 'object' || target === null) return null
  const closest = (target as { closest?: unknown }).closest
  if (typeof closest !== 'function') return null
  let anchor: unknown
  try {
    anchor = (closest as (selector: string) => unknown).call(target, 'a[target="_blank"]')
  } catch {
    return null
  }
  if (typeof anchor !== 'object' || anchor === null) return null
  const href = (anchor as { href?: unknown }).href
  if (typeof href !== 'string' || href === '') return null
  return anchor as AnchorLike
}

// ---------------------------------------------------------------------------
// 安装（window/document 侧效应；窄结构面便于无 DOM 单测）
// ---------------------------------------------------------------------------

/** message 事件的最小结构面（MessageEvent 天然满足）。 */
export interface BridgeMessageEvent {
  data?: unknown
}

/** click 事件的最小结构面（MouseEvent 天然满足）。 */
export interface BridgeClickEvent {
  target?: unknown
  preventDefault(): void
}

/** window 的最小结构面（DOM Window 天然满足；测试注入桩对象）。 */
export interface BridgeWindow {
  addEventListener(type: string, listener: (ev: BridgeMessageEvent) => void): void
  removeEventListener(type: string, listener: (ev: BridgeMessageEvent) => void): void
  document: {
    addEventListener(type: string, listener: (ev: BridgeClickEvent) => void): void
    removeEventListener(type: string, listener: (ev: BridgeClickEvent) => void): void
  }
  /** 上抛通道（通常即 window.parent）。 */
  parent: { postMessage(message: unknown, targetOrigin: string): void }
  self: unknown
  top: unknown
}

export interface HostBridgeOptions {
  /** 自身 fragment `#/t=` 的 capability token（缺失/非法为 null → 全拒）。 */
  getOwnToken(): string | null
  /** 通过守卫后的插入文本（由输入面追加 + 聚焦）。 */
  onInsertText(text: string): void
  /** 嵌入态判定；缺省 self !== top（顶层直开 = 非嵌入，桥整体静默）。 */
  isEmbedded?(): boolean
}

/**
 * 安装宿主桥：message 监听（insert-input）+ document click 拦截
 * （a[target=_blank] → open-url 上抛）。返回卸载函数。
 *
 * _blank 锚点一律 preventDefault（sandbox 无 allow-popups，默认行为本就被
 * 阻断）；仅当 url 过白名单且持有凭证时上抛，其余静默吞掉（非 http(s)
 * 链接不应经系统浏览器打开）。
 */
export function installHostBridge(win: BridgeWindow, opts: HostBridgeOptions): () => void {
  const isEmbedded = (): boolean =>
    opts.isEmbedded ? opts.isEmbedded() : defaultIsEmbedded(win)

  const onMessage = (ev: BridgeMessageEvent): void => {
    // 注意：绝不读 event.origin（见文件头陷阱记录）。
    const text = parseInsertMessage(ev.data, opts.getOwnToken(), isEmbedded())
    if (text === null) return
    opts.onInsertText(text)
  }

  const onClick = (ev: BridgeClickEvent): void => {
    const anchor = findTargetBlankAnchor(ev.target)
    if (!anchor) return
    ev.preventDefault()
    const message = buildOpenUrlMessage(anchor.href, opts.getOwnToken())
    if (message) win.parent.postMessage(message, '*')
  }

  win.addEventListener('message', onMessage)
  win.document.addEventListener('click', onClick)
  return () => {
    win.removeEventListener('message', onMessage)
    win.document.removeEventListener('click', onClick)
  }
}

/** 嵌入态缺省判定：iframe 内（self !== top）。跨源访问 top 只做等值比较，不会抛错。 */
function defaultIsEmbedded(win: BridgeWindow): boolean {
  try {
    return win.self !== win.top
  } catch {
    return false
  }
}
