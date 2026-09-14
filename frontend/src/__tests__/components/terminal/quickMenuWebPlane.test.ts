import { describe, expect, it, vi } from 'vitest'
import {
  INSERT_INPUT_MESSAGE_TYPE,
  MAX_INSERT_TEXT_BYTES,
  buildInsertPayload,
  dispatchPathConfirm,
  extractCapabilityToken,
  postToWebFrame,
  quickMenuStateFor,
  withWebPlaneSkinParam,
  type InsertInputMessage,
  type InsertInputPayload,
  type PathConfirmDeps,
  type WebFrameLike,
} from '../../../components/terminal/quickPathInsert'

// ---- 桩工具：iframe.contentWindow / 终端引擎 ----

const TOKEN = '70f099bb842115cf89448785acfe9942'

/** buildInsertPayload 产物 + WebPlaneHost 注入的 token（= 实际投递消息）。 */
function msg(payload: InsertInputPayload): InsertInputMessage {
  return { ...payload, token: TOKEN }
}

function stubFrame() {
  const postMessage = vi.fn()
  const frame: WebFrameLike = { contentWindow: { postMessage } }
  return { frame, postMessage }
}

function stubDeps(overrides: Partial<PathConfirmDeps> = {}) {
  const postToFrame = vi.fn<(payload: InsertInputPayload) => boolean>(() => true)
  const insertTextToTerminal = vi.fn<(sessionId: string, text: string) => Promise<void>>(
    async () => {},
  )
  if (overrides.postToFrame) postToFrame.mockImplementation(overrides.postToFrame)
  if (overrides.insertTextToTerminal) {
    insertTextToTerminal.mockImplementation(overrides.insertTextToTerminal)
  }
  return { postToFrame, insertTextToTerminal }
}

// ---- 跨仓契约：宿主 → webui postMessage ----

describe('postToWebFrame（宿主 → webui 桥，契约 v2 带 token）', () => {
  it('loaded 态投递成功：消息为 {type, token, text}，targetOrigin 为 *', () => {
    const { frame, postMessage } = stubFrame()
    const payload = buildInsertPayload(['/Users/me/proj'])
    expect(payload).not.toBeNull()

    expect(postToWebFrame(frame, 'loaded', msg(payload!))).toBe(true)

    expect(postMessage).toHaveBeenCalledTimes(1)
    const [message, targetOrigin] = postMessage.mock.calls[0]
    expect(targetOrigin).toBe('*')
    expect(message.type).toBe(INSERT_INPUT_MESSAGE_TYPE)
    expect(message.token).toBe(TOKEN)
    expect(message.text).toBe('关联工作路径：/Users/me/proj')
    expect(message.text).toMatch(/^关联工作路径：/)
  })

  it('多路径：逐行「关联工作路径：」，保持勾选顺序', () => {
    const { frame, postMessage } = stubFrame()
    const payload = buildInsertPayload(['/w/a', '/w/b'])
    expect(postToWebFrame(frame, 'loaded', msg(payload!))).toBe(true)
    expect(postMessage.mock.calls[0][0].text).toBe(
      '关联工作路径：/w/a\n关联工作路径：/w/b',
    )
  })

  it('loading / error 态：不投递且返回 false（与 happy-path 成对）', () => {
    const { frame, postMessage } = stubFrame()
    const payload = msg(buildInsertPayload(['/w/a'])!)

    expect(postToWebFrame(frame, 'loading', payload)).toBe(false)
    expect(postToWebFrame(frame, 'error', payload)).toBe(false)
    expect(postMessage).not.toHaveBeenCalled()
  })

  it('iframe 未挂载 / contentWindow 缺失：返回 false 不抛错', () => {
    const payload = msg(buildInsertPayload(['/w/a'])!)
    expect(postToWebFrame(null, 'loaded', payload)).toBe(false)
    expect(postToWebFrame(undefined, 'loaded', payload)).toBe(false)
    expect(postToWebFrame({ contentWindow: null }, 'loaded', payload)).toBe(false)
  })
})

// ---- 凭证提取 + 注入链路（WebPlaneHost.postToFrame 的纯函数等价物） ----

describe('extractCapabilityToken（iframe URL → 消息凭证）', () => {
  it('标准契约 URL（${httpBase}/#/t=<token>）→ 提取成功', () => {
    expect(
      extractCapabilityToken(`http://127.0.0.1:54758/#/t=${TOKEN}`),
    ).toBe(TOKEN)
  })

  it('带查询串 / 百分号编码 token：仍正确提取解码', () => {
    expect(extractCapabilityToken(`http://127.0.0.1:54758/?x=1#/t=${TOKEN}`)).toBe(TOKEN)
    expect(
      extractCapabilityToken('http://127.0.0.1:54758/#/t=abc-ABC_123%2Ddef456ghi789jkl'),
    ).toBe('abc-ABC_123-def456ghi789jkl')
  })

  it('无 fragment / token 过短 / 非法字符 → null（与 happy-path 成对）', () => {
    expect(extractCapabilityToken('http://127.0.0.1:54758/')).toBeNull()
    expect(extractCapabilityToken('http://127.0.0.1:54758/#/t=short')).toBeNull()
    expect(extractCapabilityToken('http://127.0.0.1:54758/#/t=has%20space%20and%20too長')).toBeNull()
  })

  it('WebPlaneHost 投递等价链路：URL 提 token → 消息带 token；无 token 时不投递', () => {
    const { frame, postMessage } = stubFrame()
    const payload = buildInsertPayload(['/w/a'])!

    const token = extractCapabilityToken(`http://127.0.0.1:54758/#/t=${TOKEN}`)
    expect(token).not.toBeNull()
    expect(postToWebFrame(frame, 'loaded', { ...payload, token: token! })).toBe(true)
    expect(postMessage.mock.calls[0][0].token).toBe(TOKEN)

    const badToken = extractCapabilityToken('http://127.0.0.1:54758/') // 无 fragment
    expect(badToken).toBeNull() // 等价于 WebPlaneHost.postToFrame 提前返回 false，不投递
    expect(postMessage).toHaveBeenCalledTimes(1)
  })
})

describe('withWebPlaneSkinParam（Web 平面配色方向 → iframe URL skin 参数）', () => {
  it('dark：原样返回，不携带任何参数（缺省深色嵌入，契约 fragment 不动）', () => {
    const url = `http://127.0.0.1:54758/#/t=${TOKEN}`
    expect(withWebPlaneSkinParam(url, 'dark')).toBe(url)
  })

  it('light：skin=light 插入在 fragment 前，token 提取不受影响', () => {
    const out = withWebPlaneSkinParam(`http://127.0.0.1:54758/#/t=${TOKEN}`, 'light')
    expect(out).toBe(`http://127.0.0.1:54758/?skin=light#/t=${TOKEN}`)
    expect(extractCapabilityToken(out)).toBe(TOKEN)
  })

  it('已有 query 时合并不覆盖；无 fragment 的 URL 追加在尾部', () => {
    expect(
      withWebPlaneSkinParam(`http://127.0.0.1:54758/?x=1#/t=${TOKEN}`, 'light'),
    ).toBe(`http://127.0.0.1:54758/?x=1&skin=light#/t=${TOKEN}`)
    expect(withWebPlaneSkinParam('http://127.0.0.1:54758/', 'light')).toBe(
      'http://127.0.0.1:54758/?skin=light',
    )
  })
})

describe('buildInsertPayload', () => {
  it('空选择 → null（不发指令）', () => {
    expect(buildInsertPayload([])).toBeNull()
  })

  it('UTF-8 超 128KiB → null；恰好 128KiB → 允许', () => {
    // 「关联工作路径：」前缀 7 字符 × 3 字节 = 21 字节
    const exact = 'a'.repeat(MAX_INSERT_TEXT_BYTES - 21)
    expect(buildInsertPayload([exact])).not.toBeNull()
    expect(buildInsertPayload(['a'.repeat(MAX_INSERT_TEXT_BYTES - 20)])).toBeNull()
  })
})

// ---- 快捷功能入口状态 ----

describe('quickMenuStateFor', () => {
  it('web + running：可用，提示「所选路径将插入网页对话框」', () => {
    expect(quickMenuStateFor('web', 'running')).toEqual({
      disabled: false,
      title: '所选路径将插入网页对话框',
    })
  })

  it('tui + running：可用，提示「快捷功能」（TUI 行为不变）', () => {
    expect(quickMenuStateFor('tui', 'running')).toEqual({
      disabled: false,
      title: '快捷功能',
    })
  })

  it('非 running（两个平面 / 状态未知）：禁用 + 未运行提示', () => {
    for (const plane of ['tui', 'web'] as const) {
      for (const status of ['stopped', 'stopping', undefined]) {
        expect(quickMenuStateFor(plane, status)).toEqual({
          disabled: true,
          title: '会话未运行，无法使用快捷功能',
        })
      }
    }
  })
})

// ---- 确认后的平面分流 ----

describe('dispatchPathConfirm', () => {
  it('web 平面：走 postToFrame（带关键词 payload），不落终端', async () => {
    const deps = stubDeps()

    const result = await dispatchPathConfirm('web', 's1', ['/w/a'], deps)

    expect(result).toBe('web-inserted')
    expect(deps.postToFrame).toHaveBeenCalledTimes(1)
    const payload = deps.postToFrame.mock.calls[0][0]
    expect(payload.type).toBe('amagi:insert-input')
    expect(payload.text).toMatch(/^关联工作路径：\/w\/a$/)
    expect(deps.insertTextToTerminal).not.toHaveBeenCalled()
  })

  it('web 平面 iframe 未就绪：postToFrame false → web-not-ready，不落终端', async () => {
    const deps = stubDeps({ postToFrame: vi.fn(() => false) })

    const result = await dispatchPathConfirm('web', 's1', ['/w/a'], deps)

    expect(result).toBe('web-not-ready')
    expect(deps.postToFrame).toHaveBeenCalledTimes(1)
    expect(deps.insertTextToTerminal).not.toHaveBeenCalled()
  })

  it('TUI 平面：仍走引擎插入终端文本，不经 postToFrame', async () => {
    const deps = stubDeps()

    const result = await dispatchPathConfirm('tui', 's1', ['/w/a', '/w/b'], deps)

    expect(result).toBe('tui-inserted')
    expect(deps.insertTextToTerminal).toHaveBeenCalledWith(
      's1',
      '关联工作路径：/w/a\n关联工作路径：/w/b',
    )
    expect(deps.postToFrame).not.toHaveBeenCalled()
  })

  it('空选择：noop，两个平面都不写', async () => {
    const webDeps = stubDeps()
    expect(await dispatchPathConfirm('web', 's1', [], webDeps)).toBe('noop')
    expect(webDeps.postToFrame).not.toHaveBeenCalled()

    const tuiDeps = stubDeps()
    expect(await dispatchPathConfirm('tui', 's1', [], tuiDeps)).toBe('noop')
    expect(tuiDeps.insertTextToTerminal).not.toHaveBeenCalled()
  })

  it('终端写入异常向上抛（调用方 toast 兜底）', async () => {
    const deps = stubDeps({
      insertTextToTerminal: vi.fn(async () => {
        throw new Error('write failed')
      }),
    })
    await expect(dispatchPathConfirm('tui', 's1', ['/w/a'], deps)).rejects.toThrow(
      'write failed',
    )
  })
})
