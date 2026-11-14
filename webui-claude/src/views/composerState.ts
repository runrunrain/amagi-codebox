/**
 * composerState.ts — 输入面控制器（design §7.2 Composer、§6.1，t8）。
 *
 * 与 Composer.vue 解耦的纯逻辑 + Transport IO（可注入），便于在无 DOM 的
 * Vitest node 环境下单测（本工程不引 @vue/test-utils / jsdom；同主前端
 * webPlaneGate.ts / quickPathInsert.ts 的「纯逻辑模块 + 薄视图」惯例）。
 *
 * 状态机三态（由平面态派生，不自持）：
 *   idle   空闲：主按钮「发送」
 *   busy   生成中：主按钮「中断」（POST /api/interrupt，Esc 同效）+
 *          次按钮仍可发送——claude TUI 原生排队语义，提示「将排队」
 *   ended  会话已结束：发送与中断均不可用
 *
 * 写面（internal/claudewebui/input.go）：
 *   POST /api/input {text, submit:true}   服务端在 submit=true 时追加 \r
 *                                         （客户端不拼提交键）
 *   POST /api/interrupt {}                服务端写 \x1b
 * 两者共用同一车道与错误映射 → 同一分流：
 *   403 control.forbidden  设备失持控/远程无控制权 →「需先获取控制权」
 *   503 service.down       桌面控制服务未就绪     →「桌面控制服务不可用」
 *
 * busy 只影响 UI，不做客户端发送门控（design §4-D6：写 PTY 无条件，
 * 与用户在 xterm 打字等价）。
 *
 * 红线：URL 一律经 resolvePlaneBase 推导（双 base 硬约束）；请求带 Bearer。
 */

import { computed, reactive } from 'vue'
import { createDraftSync, type DraftSync } from '../api/draft'
import { resolvePlaneBase, type PlaneLocation } from '../data/basePath'
import type { JsonResponse, Transport } from '../data/source'

/** 输入上限：UTF-8 字节，与服务端 input.go maxTextBytes（128KiB）对齐。 */
export const MAX_INPUT_BYTES = 128 * 1024

/** 错误信封码值（{v:1,error:<code>}；与 input.go writeLane 映射对齐）。 */
export const CONTROL_FORBIDDEN_CODE = 'control.forbidden'
export const SERVICE_DOWN_CODE = 'service.down'

export type ComposerMode = 'idle' | 'busy' | 'ended'

/** 提交/中断失败的分流类别。 */
export type SendErrorKind = 'control' | 'down' | 'other'

const utf8Encoder = new TextEncoder()

/** 行内错误文案（design §7.2 输入错误分流；文案由单测锚定）。 */
export function sendErrorText(kind: SendErrorKind): string {
  switch (kind) {
    case 'control':
      return '需先获取控制权'
    case 'down':
      return '桌面控制服务不可用'
    default:
      return '发送失败，请重试'
  }
}

/**
 * 失败响应分流。403 必须同时命中码值才算控制权问题——同为 403 的
 * auth.forbidden（token 不等值）不是「去取控制权」能解决的，归 other。
 */
export function classifySendError(res: JsonResponse): SendErrorKind {
  const code = (res.body as { error?: unknown } | null | undefined)?.error
  if (res.status === 403 && code === CONTROL_FORBIDDEN_CODE) return 'control'
  if (res.status === 503 || code === SERVICE_DOWN_CODE) return 'down'
  return 'other'
}

/**
 * 键位裁决（纯函数）：Enter 提交、Shift+Enter 换行（不裁决，走默认行为）、
 * busy 态 Esc 中断。输入法组合态一律不裁决——否则中文输入法用 Enter
 * 上屏候选词会被当成提交。
 */
export function composerKeyEvent(
  e: { key: string; shiftKey?: boolean; isComposing?: boolean },
  mode: ComposerMode,
): 'submit' | 'interrupt' | null {
  if (e.isComposing === true) return null
  if (e.key === 'Enter') return e.shiftKey === true ? null : 'submit'
  if (e.key === 'Escape') return mode === 'busy' ? 'interrupt' : null
  return null
}

export interface ComposerDeps {
  transport: Transport
  loc: PlaneLocation
  token: string
  /**
   * 平面忙闲 / 结束态取值器。须读响应式来源（组件 props / ref）——
   * 派生态用 computed 缓存，非响应式来源变化不会触发重算。
   */
  busy(): boolean
  ended(): boolean
  /** 聚焦输入框（宿主桥插入后调用）。 */
  focusInput(): void
  /** 草稿同步；缺省按同一 transport/loc/token 构造（测试可注入）。 */
  draft?: DraftSync
}

export interface Composer {
  readonly text: string
  /** 提交在途（防重复提交）。 */
  readonly sending: boolean
  readonly interrupting: boolean
  readonly error: SendErrorKind | null
  readonly mode: ComposerMode
  /** 文本超 128KiB（禁发 + 提示）。 */
  readonly overLimit: boolean
  readonly canSubmit: boolean
  readonly canInterrupt: boolean
  readonly errorText: string | null
  /** 用户编辑：更新文本、清掉旧错误、防抖写草稿。 */
  setText(next: string): void
  /** 宿主桥插入：追加进输入框并聚焦，绝不自动发送（冻结契约）。 */
  insertText(text: string): void
  /** POST /api/input {text, submit:true}；成功后清空输入与服务端草稿。 */
  submit(): Promise<void>
  /** POST /api/interrupt {}（仅 busy 态）。 */
  interrupt(): Promise<void>
  /** 挂载恢复草稿；恢复期间用户已编辑则放弃覆盖。 */
  restoreDraft(): Promise<void>
  dispose(): void
}

export function createComposer(deps: ComposerDeps): Composer {
  const base = resolvePlaneBase(deps.loc)
  const draft =
    deps.draft ?? createDraftSync({ transport: deps.transport, loc: deps.loc, token: deps.token })

  // 草稿恢复竞态守卫：GET 在途时用户已动过输入框，则不拿旧草稿覆盖。
  let touched = false

  const st = reactive({
    text: '',
    sending: false,
    interrupting: false,
    error: null as SendErrorKind | null,
  })

  const mode = computed<ComposerMode>(() =>
    deps.ended() ? 'ended' : deps.busy() ? 'busy' : 'idle',
  )
  const overLimit = computed(() => utf8Encoder.encode(st.text).length > MAX_INPUT_BYTES)
  const canSubmit = computed(
    () => mode.value !== 'ended' && !overLimit.value && st.text.trim() !== '' && !st.sending,
  )
  const canInterrupt = computed(() => mode.value === 'busy' && !st.interrupting)
  const errorText = computed(() => (st.error === null ? null : sendErrorText(st.error)))

  function post(path: string, body: unknown): Promise<JsonResponse> {
    return deps.transport.fetchJson(base.api(path), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${deps.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
    })
  }

  function setText(next: string): void {
    touched = true
    st.text = next
    st.error = null
    draft.schedule(next)
  }

  return {
    get text() {
      return st.text
    },
    get sending() {
      return st.sending
    },
    get interrupting() {
      return st.interrupting
    },
    get error() {
      return st.error
    },
    get mode() {
      return mode.value
    },
    get overLimit() {
      return overLimit.value
    },
    get canSubmit() {
      return canSubmit.value
    },
    get canInterrupt() {
      return canInterrupt.value
    },
    get errorText() {
      return errorText.value
    },

    setText,

    insertText(text: string): void {
      setText(st.text + text)
      deps.focusInput()
    },

    async submit(): Promise<void> {
      if (!canSubmit.value) return
      st.sending = true
      try {
        const res = await post('api/input', { text: st.text, submit: true })
        if (res.status === 200) {
          st.text = ''
          st.error = null
          draft.schedule('') // 空串即清除服务端草稿
          return
        }
        st.error = classifySendError(res)
      } catch {
        st.error = 'other' // 网络异常：文本保留，可重试
      } finally {
        st.sending = false
      }
    },

    async interrupt(): Promise<void> {
      if (!canInterrupt.value) return
      st.interrupting = true
      try {
        const res = await post('api/interrupt', {})
        st.error = res.status === 200 ? null : classifySendError(res)
      } catch {
        st.error = 'other'
      } finally {
        st.interrupting = false
      }
    },

    async restoreDraft(): Promise<void> {
      const saved = await draft.load()
      if (touched || saved === '') return
      st.text = saved // 直写：恢复不是用户编辑，不回写草稿
    },

    dispose(): void {
      draft.dispose()
    },
  }
}
