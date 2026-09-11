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
