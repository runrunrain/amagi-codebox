/**
 * 快捷功能「输入工作路径」的平面决策与跨平面分发（纯逻辑，便于单测覆盖）。
 *
 * 与 UI 解耦的纯函数/可注入依赖逻辑：
 * - buildInsertPayload：把选中路径组装为宿主 → webui 的插入指令 payload
 * - extractCapabilityToken：从 webui iframe URL 提取 capability token（消息凭证）
 * - postToWebFrame：向 sandbox iframe（webui）投递插入指令
 * - quickMenuStateFor：快捷功能入口的禁用态/提示语
 * - dispatchPathConfirm：确认后的平面分流（Web → postMessage，TUI → 终端写入）
 *
 * 跨仓冻结契约 v2（宿主侧，勿改）：
 *   iframe.contentWindow.postMessage({ type: 'amagi:insert-input', token, text }, '*')
 * token = iframe URL fragment `#/t=<token>` 的 capability token（宿主构造者持有，
 * 接收端以「token 与自身 fragment 严格相等」校验来源）；text 为非空 string、
 * UTF-8 ≤128KiB；接收端还要求嵌入态（webui-embedded）。
 *
 * v1 → v2 修复（2026-09-11）：v1 消息不带 token、接收端误用
 * `event.origin === 'null'` 识别宿主——event.origin 是发送方（宿主壳）origin，
 * codebox 壳为 http://wails.localhost，永远非 'null'，导致真机全部消息被拒。
 * 现改为 token 凭证，与 origin 语义解耦。
 */

import { buildAssociatedPathLines } from '../../utils/quickFunctions'

/** 宿主 → webui 插入指令的 postMessage type（跨仓冻结契约，勿改）。 */
export const INSERT_INPUT_MESSAGE_TYPE = 'amagi:insert-input'

/** webui → 宿主「打开外部链接」的 postMessage type（跨仓冻结契约 v2.7.4，勿改）。 */
export const OPEN_URL_MESSAGE_TYPE = 'amagi:open-url'

/** 上行链接长度上限（防御：异常长 URL 不投 Go 侧）。 */
const MAX_OPEN_URL_LENGTH = 2048

/** 插入文本的 UTF-8 字节上限（契约：≤128KiB）。超限直接不发指令。 */
export const MAX_INSERT_TEXT_BYTES = 128 * 1024

/** 宿主 → webui 的插入指令 payload（token 由 WebPlaneHost 投递时注入）。 */
export interface InsertInputPayload {
  type: typeof INSERT_INPUT_MESSAGE_TYPE
  text: string
}

/** 实际投递消息：payload + capability token（接收端以 token 与自身 fragment 严格相等校验来源）。 */
export interface InsertInputMessage extends InsertInputPayload {
  token: string
}

/** capability token 形状（与 amagi-pi webui transport.ts CAPABILITY_PATTERN 一致）。 */
const CAPABILITY_PATTERN = /^[A-Za-z0-9_-]{22,}$/

/**
 * 从 webui iframe URL（`${httpBase}/#/t=<token>`，契约 §6.5）提取 capability token。
 * fragment 缺失/格式不合法（非 [A-Za-z0-9_-]{22,}）/解码失败 → null。
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
 * 接收端守卫（与插入指令同构）：token 须与宿主构造 iframe URL 时持有的凭证严格
 * 相等；type 精确匹配；url 为 string、http(s) 白名单、长度 ≤2048。
 * 全部通过 → 返回 url（调用方交 Go 侧 BrowserOpenURL，Go 侧再校一次白名单）；
 * 任一不满足 → null（静默忽略，不报错——伪造/过期消息不是用户可威知事件）。
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
  if (!/^https?:\/\//i.test(url)) return null
  return url
}

/**
 * 按 Web 平面配色方向注入 skin 参数：light → 在 fragment 前插入/合并 skin=light
 * query（webui 页面命中后叠加 webui-embedded-light：浅底深字）；dark → 原样
 * 返回（缺省深色嵌入，不带参数）。插入点在 # 前，不污染 #/t=<token> 契约
 * fragment；token 提取（extractCapabilityToken 按 #/t= 定位）不受影响。
 */
export function withWebPlaneSkinParam(url: string, skin: 'dark' | 'light'): string {
  if (skin !== 'light') return url;
  const hashIdx = url.indexOf('#');
  const base = hashIdx === -1 ? url : url.slice(0, hashIdx);
  const hash = hashIdx === -1 ? '' : url.slice(hashIdx);
  const sep = base.includes('?') ? '&' : '?';
  return `${base}${sep}skin=light${hash}`;
}

/** 会话显示平面。 */
export type SessionPlane = 'tui' | 'web'

/** WebPlaneHost iframe 的加载阶段（与其内部 Phase 一致）。 */
export type WebFramePhase = 'loading' | 'loaded' | 'error'

/**
 * iframe.contentWindow 的最小结构类型。用结构化类型而非 HTMLIFrameElement，
 * 便于纯逻辑单测注入桩对象；DOM 的 Window 天然满足该形状。
 */
export interface WebFrameLike {
  contentWindow: {
    postMessage(message: unknown, targetOrigin: string): void
  } | null
}

/** 快捷功能入口的禁用态与按钮提示语。 */
export interface QuickMenuState {
  disabled: boolean
  title: string
}

/** 确认路径选择后的分发结果。 */
export type PathConfirmResult = 'web-inserted' | 'web-not-ready' | 'tui-inserted' | 'noop'

/** dispatchPathConfirm 的注入依赖（终端引擎 / iframe 桥接）。 */
export interface PathConfirmDeps {
  /** Web 平面：投递到 webui iframe；未就绪返回 false（不抛错）。 */
  postToFrame(payload: InsertInputPayload): boolean
  /** TUI 平面：既有终端写入出口（bracketed paste 由引擎按模式包裹）。 */
  insertTextToTerminal(sessionId: string, text: string): Promise<void>
}

const utf8Encoder = new TextEncoder()

/**
 * 组装插入指令 payload；空路径（空文本）或文本超 128KiB 返回 null（不发指令）。
 */
export function buildInsertPayload(paths: string[]): InsertInputPayload | null {
  const text = buildAssociatedPathLines(paths)
  if (!text) return null
  if (utf8Encoder.encode(text).length > MAX_INSERT_TEXT_BYTES) return null
  return { type: INSERT_INPUT_MESSAGE_TYPE, text }
}

/**
 * 向 webui iframe 投递插入指令（含 token 凭证）。
 *
 * targetOrigin 固定 '*'：iframe 为 sandbox（无 allow-same-origin），其 opaque origin
 * 不能被指定为 targetOrigin；安全由接收端守卫兜底（嵌入态 + token 与自身 fragment
 * 严格相等，见 amagi-pi webui/src/host-bridge.ts），且本通道只承载「插入输入框文本」
 * 单向指令、不回传数据。
 *
 * iframe 未挂载（null/undefined）、未处于 loaded 态或 contentWindow 不可用时
 * 返回 false，绝不抛错（由调用方 toast 兜底）。
 */
export function postToWebFrame(
  frame: WebFrameLike | null | undefined,
  phase: WebFramePhase,
  payload: InsertInputMessage,
): boolean {
  if (phase !== 'loaded') return false
  const win = frame?.contentWindow
  if (!win) return false
  win.postMessage(payload, '*')
  return true
}

/**
 * 快捷功能入口的禁用态/提示语（所有内嵌终端会话通用）。
 *
 * Web 平面经 postMessage 注入 webui 对话框（不再落隐藏的 xterm），因此只要
 * 会话 running 就可用；非 running 时两个平面都禁用。
 */
export function quickMenuStateFor(
  plane: SessionPlane,
  status: string | undefined,
): QuickMenuState {
  if (status !== 'running') {
    return { disabled: true, title: '会话未运行，无法使用快捷功能' }
  }
  if (plane === 'web') {
    return { disabled: false, title: '所选路径将插入网页对话框' }
  }
  return { disabled: false, title: '快捷功能' }
}

/**
 * 确认路径选择后的分发：
 * - Web 平面：postMessage 进 webui 对话框（追加 + 聚焦，不自动发送）
 * - TUI 平面：既有终端写入路径（行为不变）
 * - 空选择/超长文本：'noop'，两个平面都不写
 */
export async function dispatchPathConfirm(
  plane: SessionPlane,
  sessionId: string,
  paths: string[],
  deps: PathConfirmDeps,
): Promise<PathConfirmResult> {
  const payload = buildInsertPayload(paths)
  if (!payload) return 'noop'
  if (plane === 'web') {
    return deps.postToFrame(payload) ? 'web-inserted' : 'web-not-ready'
  }
  await deps.insertTextToTerminal(sessionId, payload.text)
  return 'tui-inserted'
}
