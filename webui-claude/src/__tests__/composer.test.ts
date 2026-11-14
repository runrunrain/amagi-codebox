/**
 * composer.test.ts — 输入面控制器（design §7.2、§11 SPA 行「composer（含
 * 403/503 错误分流与 draft 静默）」、t8 任务卡）。
 *
 * 覆盖：状态机三态（idle/busy/ended）、提交字节流经 fake transport
 * （URL 双 base 推导 / Bearer / 精确 JSON 体）、PUT /api/draft 防抖 500ms、
 * 403 control.forbidden 与 503 service.down 分流文案、draft 一切错误静默
 * （含写面门 403 → 恢复后下一次变更自动重写）、挂载恢复竞态、键位裁决、
 * 宿主桥插入（追加 + 聚焦 + 不自动发送）。
 *
 * 纯逻辑测试：composerState 依赖全部注入（FakeTransport 的 fetchJson 与
 * schedule 都是手动推进的桩——不依赖真实网络与定时器）。
 */
import { describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import {
  classifySendError,
  composerKeyEvent,
  createComposer,
  MAX_INPUT_BYTES,
  sendErrorText,
  type ComposerDeps,
} from '../views/composerState'
import type { JsonResponse, Transport, TransportRequest, WsConnection } from '../data/source'

const TOKEN = 'd0a1d0a1d0a1d0a1d0a1d0a1d0a1d0a1'
const OK: JsonResponse = { status: 200, body: { v: 1, ok: true } }

const ROOT_LOC = { protocol: 'http:' as const, host: '127.0.0.1:51719', pathname: '/' }
const PROXY_LOC = {
  protocol: 'https:' as const,
  host: 'remote.example.com',
  pathname: '/webui/s1/',
}

class FakeTransport implements Transport {
  readonly requests: Array<{ url: string; init?: TransportRequest }> = []
  readonly timers: Array<{ fn: () => void; delayMs: number; done: boolean }> = []
  handler: (url: string, init?: TransportRequest) => JsonResponse | Promise<JsonResponse> = () => OK

  async fetchJson(url: string, init?: TransportRequest): Promise<JsonResponse> {
    this.requests.push({ url, init })
    return this.handler(url, init)
  }

  connectWs(): WsConnection {
    throw new Error('composer 不使用 WS')
  }

  schedule(fn: () => void, delayMs: number): () => void {
    const t = { fn, delayMs, done: false }
    this.timers.push(t)
    return () => {
      t.done = true
    }
  }

  now(): number {
    return 0
  }

  /** 手动推进时钟：执行所有未取消的防抖定时器。 */
  runTimers(): void {
    for (const t of [...this.timers]) {
      if (t.done) continue
      t.done = true
      t.fn()
    }
  }

  pendingTimers(): Array<{ delayMs: number }> {
    return this.timers.filter((t) => !t.done)
  }

  posts(): Array<{ url: string; init?: TransportRequest }> {
    return this.requests.filter((r) => r.init?.method === 'POST')
  }

  puts(): Array<{ url: string; init?: TransportRequest }> {
    return this.requests.filter((r) => r.init?.method === 'PUT')
  }
}

function makeComposer(overrides: Partial<ComposerDeps> = {}) {
  const transport = new FakeTransport()
  const busy = ref(false)
  const ended = ref(false)
  const focus = vi.fn()
  const composer = createComposer({
    transport,
    loc: ROOT_LOC,
    token: TOKEN,
    busy: () => busy.value,
    ended: () => ended.value,
    focusInput: focus,
    ...overrides,
  })
  return { composer, transport, busy, ended, focus }
}

// ---------------------------------------------------------------------------
// 状态机三态
// ---------------------------------------------------------------------------

describe('composer 状态机三态', () => {
  it('idle：空文本不可发送；有文本可发送、不可中断', () => {
    const { composer } = makeComposer()
    expect(composer.mode).toBe('idle')
    expect(composer.canSubmit).toBe(false)
    composer.setText('你好')
    expect(composer.canSubmit).toBe(true)
    expect(composer.canInterrupt).toBe(false)
  })

  it('busy：主按钮中断可用，发送仍可用（claude TUI 原生排队语义）', () => {
    const { composer, busy } = makeComposer()
    composer.setText('排队消息')
    busy.value = true
    expect(composer.mode).toBe('busy')
    expect(composer.canInterrupt).toBe(true)
    expect(composer.canSubmit).toBe(true)
  })

  it('ended：发送与中断均不可用，submit 为 no-op（零请求）', async () => {
    const { composer, ended, transport } = makeComposer()
    composer.setText('来不及')
    ended.value = true
    expect(composer.mode).toBe('ended')
    expect(composer.canSubmit).toBe(false)
    expect(composer.canInterrupt).toBe(false)
    await composer.submit()
    await composer.interrupt()
    expect(transport.posts()).toHaveLength(0)
  })

  it('超 128KiB（UTF-8 字节）禁发', () => {
    const { composer } = makeComposer()
    composer.setText('x'.repeat(MAX_INPUT_BYTES))
    expect(composer.overLimit).toBe(false)
    expect(composer.canSubmit).toBe(true)
    composer.setText('x'.repeat(MAX_INPUT_BYTES + 1))
    expect(composer.overLimit).toBe(true)
    expect(composer.canSubmit).toBe(false)
  })

  it('字节口径而非字符口径：多字节文本按 UTF-8 计', () => {
    const { composer } = makeComposer()
    // 43690 × 3B = 131070B ≤ 131072B
    composer.setText('你'.repeat(43690))
    expect(composer.overLimit).toBe(false)
    // 43691 × 3B = 131073B > 131072B（UTF-16 长度 43691 仍远小于上限）
    composer.setText('你'.repeat(43691))
    expect(composer.overLimit).toBe(true)
  })

  it('提交在途防重复：第二次 submit 不再发请求', async () => {
    const { composer, transport } = makeComposer()
    let resolveInput!: (r: JsonResponse) => void
    transport.handler = () => new Promise<JsonResponse>((res) => (resolveInput = res))
    composer.setText('hi')
    const p1 = composer.submit()
    const p2 = composer.submit()
    expect(transport.posts()).toHaveLength(1)
    resolveInput(OK)
    await Promise.all([p1, p2])
    expect(composer.sending).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// 提交字节流（fake transport）
// ---------------------------------------------------------------------------

describe('composer 提交字节流', () => {
  it('桌面形态：POST /api/input，Bearer + 精确 JSON 体（submit 键由服务端追加 \\r）', async () => {
    const { composer, transport } = makeComposer()
    composer.setText('你好，claude')
    await composer.submit()
    const posts = transport.posts()
    expect(posts).toHaveLength(1)
    expect(posts[0].url).toBe('/api/input')
    expect(posts[0].init?.method).toBe('POST')
    expect(posts[0].init?.headers).toEqual({
      Authorization: `Bearer ${TOKEN}`,
      'Content-Type': 'application/json',
    })
    expect(posts[0].init?.body).toBe('{"text":"你好，claude","submit":true}')
  })

  it('远程形态：URL 落在代理子路径前缀下（双 base 硬约束）', async () => {
    const { composer, transport } = makeComposer({ loc: PROXY_LOC })
    composer.setText('remote')
    await composer.submit()
    expect(transport.posts()[0].url).toBe('/webui/s1/api/input')
    composer.setText('again')
    transport.runTimers()
    expect(transport.puts()[0].url).toBe('/webui/s1/api/draft')
  })

  it('发送成功：清空输入，并把服务端草稿清为空串（空串即清除）', async () => {
    const { composer, transport } = makeComposer()
    composer.setText('发完即弃')
    await composer.submit()
    expect(composer.text).toBe('')
    expect(transport.pendingTimers()).toHaveLength(1)
    expect(transport.pendingTimers()[0].delayMs).toBe(500)
    transport.runTimers()
    const puts = transport.puts()
    expect(puts).toHaveLength(1)
    expect(puts[0].init?.body).toBe('{"draft":""}')
  })

  it('busy 态中断：POST /api/interrupt，体为 {}，同 Bearer 头', async () => {
    const { composer, transport, busy } = makeComposer()
    busy.value = true
    await composer.interrupt()
    const posts = transport.posts()
    expect(posts).toHaveLength(1)
    expect(posts[0].url).toBe('/api/interrupt')
    expect(posts[0].init?.headers).toEqual({
      Authorization: `Bearer ${TOKEN}`,
      'Content-Type': 'application/json',
    })
    expect(posts[0].init?.body).toBe('{}')
  })
})

// ---------------------------------------------------------------------------
// 防抖（PUT /api/draft 500ms）
// ---------------------------------------------------------------------------

describe('draft 防抖', () => {
  it('连续变更合并：只 PUT 最后一次文本，窗口 500ms', () => {
    const { composer, transport } = makeComposer()
    composer.setText('a')
    expect(transport.pendingTimers()).toHaveLength(1)
    composer.setText('ab')
    composer.setText('abc')
    expect(transport.pendingTimers()).toHaveLength(1) // 前两次已被取消
    expect(transport.pendingTimers()[0].delayMs).toBe(500)
    transport.runTimers()
    const puts = transport.puts()
    expect(puts).toHaveLength(1)
    expect(puts[0].init?.method).toBe('PUT')
    expect(puts[0].init?.body).toBe('{"draft":"abc"}')
  })

  it('dispose 取消未发的防抖写', () => {
    const { composer, transport } = makeComposer()
    composer.setText('x')
    composer.dispose()
    expect(transport.pendingTimers()).toHaveLength(0)
    transport.runTimers()
    expect(transport.puts()).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// 草稿挂载恢复与静默容错
// ---------------------------------------------------------------------------

describe('draft 恢复与静默容错', () => {
  it('挂载恢复：GET /api/draft 的文本落进输入框', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = (url) =>
      url.endsWith('/api/draft')
        ? { status: 200, body: { v: 1, draft: '上次没发完的话' } }
        : OK
    await composer.restoreDraft()
    expect(composer.text).toBe('上次没发完的话')
  })

  it('恢复竞态：GET 在途时用户已编辑，不覆盖', async () => {
    const { composer, transport } = makeComposer()
    let resolveGet!: (r: JsonResponse) => void
    transport.handler = () => new Promise<JsonResponse>((res) => (resolveGet = res))
    const pending = composer.restoreDraft()
    composer.setText('用户先动了')
    resolveGet({ status: 200, body: { v: 1, draft: '旧草稿' } })
    await pending
    expect(composer.text).toBe('用户先动了')
  })

  it('GET 失败静默返回空（不抛错、不留错误态）', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = () => ({ status: 500, body: null })
    await expect(composer.restoreDraft()).resolves.toBeUndefined()
    expect(composer.text).toBe('')
    expect(composer.error).toBeNull()
  })

  it('PUT 403（远程写面门拒绝）静默：不进错误分流，下一次变更自动重写', () => {
    const { composer, transport } = makeComposer()
    transport.handler = (url, init) =>
      init?.method === 'PUT'
        ? { status: 403, body: { v: 1, error: 'control.forbidden' } }
        : OK
    composer.setText('第一版')
    transport.runTimers()
    expect(composer.error).toBeNull() // 静默：本地回显不受影响
    // 恢复持控后：下一次变更照常调度防抖写
    composer.setText('第二版')
    transport.runTimers()
    const puts = transport.puts()
    expect(puts).toHaveLength(2)
    expect(puts[1].init?.body).toBe('{"draft":"第二版"}')
  })

  it('PUT 网络异常静默吞掉（不抛出、不重试轰炸）', () => {
    const { composer, transport } = makeComposer()
    transport.handler = (url, init) => {
      if (init?.method === 'PUT') throw new TypeError('network down')
      return OK
    }
    composer.setText('x')
    expect(() => transport.runTimers()).not.toThrow()
    expect(composer.error).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// 403 / 503 错误分流（文案锚定）
// ---------------------------------------------------------------------------

describe('提交/中断错误分流', () => {
  it('403 control.forbidden → 「需先获取控制权」，文本保留', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = () => ({ status: 403, body: { v: 1, error: 'control.forbidden' } })
    composer.setText('给 claude 的指令')
    await composer.submit()
    expect(composer.error).toBe('control')
    expect(composer.errorText).toBe('需先获取控制权')
    expect(composer.text).toBe('给 claude 的指令')
  })

  it('503 service.down → 「桌面控制服务不可用」', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = () => ({ status: 503, body: { v: 1, error: 'service.down' } })
    composer.setText('hi')
    await composer.submit()
    expect(composer.error).toBe('down')
    expect(composer.errorText).toBe('桌面控制服务不可用')
  })

  it('中断失败同分流：403 control.forbidden → 同一文案', async () => {
    const { composer, transport, busy } = makeComposer()
    transport.handler = () => ({ status: 403, body: { v: 1, error: 'control.forbidden' } })
    busy.value = true
    await composer.interrupt()
    expect(composer.errorText).toBe('需先获取控制权')
  })

  it('同为 403 的 auth.forbidden（token 失效）不是控制权问题 → 通用文案', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = () => ({ status: 403, body: { v: 1, error: 'auth.forbidden' } })
    composer.setText('hi')
    await composer.submit()
    expect(composer.error).toBe('other')
    expect(composer.errorText).toBe('发送失败，请重试')
  })

  it('网络异常 → 通用文案，文本保留', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = () => {
      throw new TypeError('fetch failed')
    }
    composer.setText('hi')
    await composer.submit()
    expect(composer.error).toBe('other')
    expect(composer.text).toBe('hi')
  })

  it('下一次编辑清除错误态', async () => {
    const { composer, transport } = makeComposer()
    transport.handler = () => ({ status: 403, body: { v: 1, error: 'control.forbidden' } })
    composer.setText('hi')
    await composer.submit()
    expect(composer.error).toBe('control')
    composer.setText('hi，再次')
    expect(composer.error).toBeNull()
    expect(composer.errorText).toBeNull()
  })

  it('classifySendError / sendErrorText 分流表', () => {
    expect(classifySendError({ status: 403, body: { v: 1, error: 'control.forbidden' } })).toBe('control')
    expect(classifySendError({ status: 503, body: { v: 1, error: 'service.down' } })).toBe('down')
    expect(classifySendError({ status: 503, body: null })).toBe('down')
    expect(classifySendError({ status: 500, body: { v: 1, error: 'input.failed' } })).toBe('other')
    expect(sendErrorText('control')).toBe('需先获取控制权')
    expect(sendErrorText('down')).toBe('桌面控制服务不可用')
    expect(sendErrorText('other')).toBe('发送失败，请重试')
  })
})

// ---------------------------------------------------------------------------
// 键位裁决与宿主桥插入
// ---------------------------------------------------------------------------

describe('composerKeyEvent 键位裁决', () => {
  it('Enter 提交、Shift+Enter 换行（不裁决）', () => {
    expect(composerKeyEvent({ key: 'Enter' }, 'idle')).toBe('submit')
    expect(composerKeyEvent({ key: 'Enter', shiftKey: true }, 'idle')).toBeNull()
  })

  it('Esc 仅 busy 态中断', () => {
    expect(composerKeyEvent({ key: 'Escape' }, 'busy')).toBe('interrupt')
    expect(composerKeyEvent({ key: 'Escape' }, 'idle')).toBeNull()
    expect(composerKeyEvent({ key: 'Escape' }, 'ended')).toBeNull()
  })

  it('输入法组合态不裁决（中文 Enter 上屏不触发提交）', () => {
    expect(composerKeyEvent({ key: 'Enter', isComposing: true }, 'idle')).toBeNull()
    expect(composerKeyEvent({ key: 'Escape', isComposing: true }, 'busy')).toBeNull()
  })
})

describe('宿主桥插入（insertText）', () => {
  it('追加进输入框并聚焦，不自动发送', () => {
    const { composer, transport, focus } = makeComposer()
    composer.setText('已有 ')
    composer.insertText('X:\\WorkSpace\\demo')
    expect(composer.text).toBe('已有 X:\\WorkSpace\\demo')
    expect(focus).toHaveBeenCalledTimes(1)
    composer.insertText('\n第二行')
    expect(focus).toHaveBeenCalledTimes(2)
    expect(composer.text).toBe('已有 X:\\WorkSpace\\demo\n第二行')
    expect(transport.posts()).toHaveLength(0) // 绝不自动发送
    transport.runTimers()
    expect(transport.puts()[0].init?.body).toBe('{"draft":"已有 X:\\\\WorkSpace\\\\demo\\n第二行"}')
  })
})
