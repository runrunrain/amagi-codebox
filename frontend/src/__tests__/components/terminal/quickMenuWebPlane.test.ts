import { describe, expect, it, vi } from 'vitest'
import {
  INSERT_INPUT_MESSAGE_TYPE,
  MAX_INSERT_TEXT_BYTES,
  buildInsertPayload,
  dispatchPathConfirm,
  postToWebFrame,
  quickMenuStateFor,
  type InsertInputPayload,
  type PathConfirmDeps,
  type WebFrameLike,
} from '../../../components/terminal/quickPathInsert'

// ---- 桩工具：iframe.contentWindow / 终端引擎 ----

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

describe('postToWebFrame（宿主 → webui 桥）', () => {
  it('loaded 态投递成功：payload 为 {type, text}，targetOrigin 为 *', () => {
    const { frame, postMessage } = stubFrame()
    const payload = buildInsertPayload(['/Users/me/proj'])
    expect(payload).not.toBeNull()

    expect(postToWebFrame(frame, 'loaded', payload!)).toBe(true)

    expect(postMessage).toHaveBeenCalledTimes(1)
    const [message, targetOrigin] = postMessage.mock.calls[0]
    expect(targetOrigin).toBe('*')
    expect(message.type).toBe(INSERT_INPUT_MESSAGE_TYPE)
    expect(message.text).toBe('关联工作路径：/Users/me/proj')
    expect(message.text).toMatch(/^关联工作路径：/)
  })

  it('多路径：逐行「关联工作路径：」，保持勾选顺序', () => {
    const { frame, postMessage } = stubFrame()
    const payload = buildInsertPayload(['/w/a', '/w/b'])
    expect(postToWebFrame(frame, 'loaded', payload!)).toBe(true)
    expect(postMessage.mock.calls[0][0].text).toBe(
      '关联工作路径：/w/a\n关联工作路径：/w/b',
    )
  })

  it('loading / error 态：不投递且返回 false（与 happy-path 成对）', () => {
    const { frame, postMessage } = stubFrame()
    const payload = buildInsertPayload(['/w/a'])!

    expect(postToWebFrame(frame, 'loading', payload)).toBe(false)
    expect(postToWebFrame(frame, 'error', payload)).toBe(false)
    expect(postMessage).not.toHaveBeenCalled()
  })

  it('iframe 未挂载 / contentWindow 缺失：返回 false 不抛错', () => {
    const payload = buildInsertPayload(['/w/a'])!
    expect(postToWebFrame(null, 'loaded', payload)).toBe(false)
    expect(postToWebFrame(undefined, 'loaded', payload)).toBe(false)
    expect(postToWebFrame({ contentWindow: null }, 'loaded', payload)).toBe(false)
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
