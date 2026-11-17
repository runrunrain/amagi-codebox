/**
 * hostBridge.test.ts — 宿主桥守卫（design §7.2、§11 SPA 行「host-bridge
 * 凭证校验」、t8 任务卡：凭证拒绝 / 长度拒绝 / 正常追加）。
 *
 * 覆盖：insert-input 三守卫（嵌入态 + token 与自身 fragment 严格相等 +
 * 文本 ≤128KiB UTF-8 字节）、origin 不参与裁决（pi 实证陷阱锚）、
 * open-url 上抛守卫（http(s) 白名单 ≤2048 + 凭证）、a[target=_blank]
 * 点击拦截改 postMessage、卸载静默。
 */
import { describe, expect, it, vi } from 'vitest'
import {
  buildOpenUrlMessage,
  findTargetBlankAnchor,
  INSERT_INPUT_MESSAGE_TYPE,
  installHostBridge,
  isValidOpenUrl,
  MAX_INSERT_TEXT_BYTES,
  MAX_OPEN_URL_LENGTH,
  OPEN_URL_MESSAGE_TYPE,
  parseInsertMessage,
  type BridgeClickEvent,
  type BridgeMessageEvent,
  type BridgeWindow,
} from '../host-bridge'

const TOKEN = 'd0a1d0a1d0a1d0a1d0a1d0a1d0a1d0a1'

function insertMsg(text: string, token: unknown = TOKEN): Record<string, unknown> {
  return { type: INSERT_INPUT_MESSAGE_TYPE, token, text }
}

// ---------------------------------------------------------------------------
// parseInsertMessage：三守卫（纯函数）
// ---------------------------------------------------------------------------

describe('insert-input 守卫', () => {
  it('正常通过：返回待插入文本', () => {
    expect(parseInsertMessage(insertMsg('X:\\path\\to'), TOKEN, true)).toBe('X:\\path\\to')
  })

  it('凭证拒绝：token 与自身 fragment 严格相等才放行', () => {
    expect(parseInsertMessage(insertMsg('t', 'another-token-1234567890abcdef'), TOKEN, true)).toBeNull()
    expect(parseInsertMessage(insertMsg('t', 123), TOKEN, true)).toBeNull() // 非 string 同值也不行
    expect(parseInsertMessage(insertMsg('t'), null, true)).toBeNull() // 自身无凭证 → 全拒
    expect(parseInsertMessage(insertMsg('t', ''), TOKEN, true)).toBeNull() // 空凭证
  })

  it('嵌入态拒绝：非 iframe 内运行整体静默', () => {
    expect(parseInsertMessage(insertMsg('t'), TOKEN, false)).toBeNull()
  })

  it('形状拒绝：type / 载荷形状', () => {
    expect(parseInsertMessage({ ...insertMsg('t'), type: 'amagi:other' }, TOKEN, true)).toBeNull()
    expect(parseInsertMessage('raw string', TOKEN, true)).toBeNull()
    expect(parseInsertMessage(123, TOKEN, true)).toBeNull()
    expect(parseInsertMessage(null, TOKEN, true)).toBeNull()
    expect(parseInsertMessage({ type: INSERT_INPUT_MESSAGE_TYPE, token: TOKEN }, TOKEN, true)).toBeNull() // 无 text
    expect(parseInsertMessage(insertMsg(''), TOKEN, true)).toBeNull() // 空文本
  })

  it('长度拒绝：UTF-8 字节 ≤128KiB（ASCII 边界恰好通过）', () => {
    expect(parseInsertMessage(insertMsg('x'.repeat(MAX_INSERT_TEXT_BYTES)), TOKEN, true)).toBe(
      'x'.repeat(MAX_INSERT_TEXT_BYTES),
    )
    expect(parseInsertMessage(insertMsg('x'.repeat(MAX_INSERT_TEXT_BYTES + 1)), TOKEN, true)).toBeNull()
  })

  it('长度拒绝：多字节按字节口径（UTF-16 长度远小于上限也拒）', () => {
    // 43691 × 3B = 131073B > 131072B；而其 .length 只有 43691。
    expect(parseInsertMessage(insertMsg('你'.repeat(43691)), TOKEN, true)).toBeNull()
    // 43690 × 3B = 131070B ≤ 上限，通过。
    expect(parseInsertMessage(insertMsg('你'.repeat(43690)), TOKEN, true)).toBe('你'.repeat(43690))
  })
})

// ---------------------------------------------------------------------------
// open-url 上抛守卫（纯函数）
// ---------------------------------------------------------------------------

describe('open-url 守卫', () => {
  it('http(s) 白名单 + 长度 ≤2048 + 持凭证', () => {
    expect(isValidOpenUrl('https://example.com/a')).toBe(true)
    expect(isValidOpenUrl('http://example.com')).toBe(true)
    expect(isValidOpenUrl('ftp://example.com')).toBe(false)
    expect(isValidOpenUrl('mailto:a@b.c')).toBe(false)
    expect(isValidOpenUrl('javascript:alert(1)')).toBe(false)
    expect(isValidOpenUrl('')).toBe(false)
    expect(isValidOpenUrl(`https://e/${'a'.repeat(2048 - 'https://e/'.length)}`)).toBe(true)
    expect(isValidOpenUrl(`https://e/${'a'.repeat(2049 - 'https://e/'.length)}`)).toBe(false)
  })

  it('buildOpenUrlMessage：形状冻结 {type,url,token}；无凭证/非法 url 不组装', () => {
    expect(buildOpenUrlMessage('https://example.com/x', TOKEN)).toEqual({
      type: OPEN_URL_MESSAGE_TYPE,
      url: 'https://example.com/x',
      token: TOKEN,
    })
    expect(buildOpenUrlMessage('https://example.com/x', null)).toBeNull()
    expect(buildOpenUrlMessage('file:///etc/passwd', TOKEN)).toBeNull()
    expect(buildOpenUrlMessage('https://e/' + 'a'.repeat(MAX_OPEN_URL_LENGTH), TOKEN)).toBeNull()
  })

  it('findTargetBlankAnchor：仅 a[target=_blank] 命中', () => {
    const blank = { href: 'https://e/x', closest: () => blank }
    expect(findTargetBlankAnchor(blank)?.href).toBe('https://e/x')
    const span = { closest: () => blank } // 嵌套目标向上命中
    expect(findTargetBlankAnchor(span)?.href).toBe('https://e/x')
    const plain = { closest: () => null } // 非 _blank 锚点
    expect(findTargetBlankAnchor(plain)).toBeNull()
    expect(findTargetBlankAnchor(null)).toBeNull()
    expect(findTargetBlankAnchor({})).toBeNull() // 无 closest（文本节点等）
  })
})

// ---------------------------------------------------------------------------
// installHostBridge：安装 / 卸载 / origin 免疫
// ---------------------------------------------------------------------------

interface FakeWin {
  win: BridgeWindow
  posted: Array<{ message: unknown; targetOrigin: string }>
  dispatchMessage(data: unknown, origin?: string): void
  /** 派发一次点击；返回是否被 preventDefault。 */
  dispatchClick(target: unknown): boolean
}

function fakeWin(embedded = true): FakeWin {
  const messageListeners: Array<(ev: BridgeMessageEvent) => void> = []
  const clickListeners: Array<(ev: BridgeClickEvent) => void> = []
  const posted: Array<{ message: unknown; targetOrigin: string }> = []
  const self = {}
  const win: BridgeWindow = {
    addEventListener(type, listener) {
      if (type === 'message') messageListeners.push(listener)
    },
    removeEventListener(type, listener) {
      if (type === 'message') {
        const i = messageListeners.indexOf(listener)
        if (i >= 0) messageListeners.splice(i, 1)
      }
    },
    document: {
      addEventListener(type, listener) {
        if (type === 'click') clickListeners.push(listener)
      },
      removeEventListener(type, listener) {
        if (type === 'click') {
          const i = clickListeners.indexOf(listener)
          if (i >= 0) clickListeners.splice(i, 1)
        }
      },
    },
    parent: {
      postMessage(message, targetOrigin) {
        posted.push({ message, targetOrigin })
      },
    },
    self,
    top: embedded ? {} : self, // self === top → 顶层直开（非嵌入）
  }
  return {
    win,
    posted,
    dispatchMessage(data, origin) {
      const ev = { data, origin }
      for (const l of [...messageListeners]) l(ev)
    },
    dispatchClick(target) {
      let prevented = false
      const ev = { target, preventDefault: () => (prevented = true) }
      for (const l of [...clickListeners]) l(ev)
      return prevented
    },
  }
}

describe('installHostBridge message 面', () => {
  it('正常追加：守卫通过 → onInsertText 收到文本', () => {
    const f = fakeWin()
    const onInsertText = vi.fn()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText })
    f.dispatchMessage(insertMsg('X:\\WorkSpace\\demo'))
    expect(onInsertText).toHaveBeenCalledTimes(1)
    expect(onInsertText).toHaveBeenCalledWith('X:\\WorkSpace\\demo')
  })

  it('凭证拒绝：token 不符 → 静默忽略（不报错不追加）', () => {
    const f = fakeWin()
    const onInsertText = vi.fn()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText })
    f.dispatchMessage(insertMsg('t', 'wrong-token-0000000000000000'))
    expect(onInsertText).not.toHaveBeenCalled()
  })

  it('长度拒绝：超 128KiB → 静默忽略', () => {
    const f = fakeWin()
    const onInsertText = vi.fn()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText })
    f.dispatchMessage(insertMsg('x'.repeat(MAX_INSERT_TEXT_BYTES + 1)))
    expect(onInsertText).not.toHaveBeenCalled()
  })

  it('嵌入态拒绝：顶层直开（self === top）不接收', () => {
    const f = fakeWin(false)
    const onInsertText = vi.fn()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText })
    f.dispatchMessage(insertMsg('t'))
    expect(onInsertText).not.toHaveBeenCalled()
  })

  it('origin 不参与裁决：伪造任意 origin 的消息仍按 token 凭证放行/拒绝', () => {
    const f = fakeWin()
    const onInsertText = vi.fn()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText })
    // 凭证正确：无论 origin 是什么都应放行（宿主壳 origin 是 wails.localhost 而非 'null'）
    f.dispatchMessage(insertMsg('ok'), 'https://evil.example')
    expect(onInsertText).toHaveBeenCalledWith('ok')
    // 凭证错误：即使 origin 伪装成 'null' 也拒绝
    f.dispatchMessage(insertMsg('bad', 'wrong-token-0000000000000000'), 'null')
    expect(onInsertText).toHaveBeenCalledTimes(1)
  })

  it('卸载后静默：不再接收消息与点击', () => {
    const f = fakeWin()
    const onInsertText = vi.fn()
    const uninstall = installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText })
    uninstall()
    f.dispatchMessage(insertMsg('t'))
    expect(onInsertText).not.toHaveBeenCalled()
    const anchor = { href: 'https://e/x', closest: () => anchor }
    expect(f.dispatchClick(anchor)).toBe(false)
    expect(f.posted).toHaveLength(0)
  })
})

describe('installHostBridge 点击拦截（open-url 上抛）', () => {
  it('a[target=_blank] 点击：preventDefault + 上抛 {type,url,token} 到 parent（targetOrigin *）', () => {
    const f = fakeWin()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText: () => {} })
    const anchor = { href: 'https://docs.example.com/claude', closest: () => anchor }
    expect(f.dispatchClick(anchor)).toBe(true)
    expect(f.posted).toEqual([
      { message: { type: OPEN_URL_MESSAGE_TYPE, url: 'https://docs.example.com/claude', token: TOKEN }, targetOrigin: '*' },
    ])
  })

  it('非 _blank 锚点/非锚点点击：放行（不拦截不上抛）', () => {
    const f = fakeWin()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText: () => {} })
    const plain = { href: 'https://e/x', closest: () => null }
    expect(f.dispatchClick(plain)).toBe(false)
    expect(f.dispatchClick({})).toBe(false)
    expect(f.posted).toHaveLength(0)
  })

  it('非 http(s) 与超长 href：拦截但不组装上抛', () => {
    const f = fakeWin()
    installHostBridge(f.win, { getOwnToken: () => TOKEN, onInsertText: () => {} })
    const mailto = { href: 'mailto:a@b.c', closest: () => mailto }
    expect(f.dispatchClick(mailto)).toBe(true)
    const long = { href: `https://e/${'a'.repeat(MAX_OPEN_URL_LENGTH)}`, closest: () => long }
    expect(f.dispatchClick(long)).toBe(true)
    expect(f.posted).toHaveLength(0)
  })

  it('无凭证：_blank 仍被拦截（sandbox 无 allow-popups，默认行为本就阻断），不上抛', () => {
    const f = fakeWin()
    installHostBridge(f.win, { getOwnToken: () => null, onInsertText: () => {} })
    const anchor = { href: 'https://e/x', closest: () => anchor }
    expect(f.dispatchClick(anchor)).toBe(true)
    expect(f.posted).toHaveLength(0)
  })
})
