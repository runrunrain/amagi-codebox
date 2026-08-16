import { expect, test } from '@playwright/test'

declare global {
  interface Window {
    __terminalTest: {
      emit: (name: string, data: unknown) => void
      encode: (value: string) => string
      snapshotCalls: () => number
      writeCalls: () => string[]
      jumpViewportOnNextResize: () => void
      viewportJumps: () => number
      resizeCalls: () => Array<{ cols: number; rows: number }>
      failNextResize: () => void
    }
  }
}

const terminalSession = {
  id: 'terminal-render-regression',
  appType: 'pi',
  providerName: 'test-provider',
  presetName: '',
  model: 'test-model',
  mode: 'terminal',
  status: 'running',
  workDir: '/tmp/terminal-render-workspace',
  title: 'terminal rendering regression',
  createdAt: '2026-08-06T00:00:00Z',
  startedAt: '2026-08-06T00:00:01Z',
  stoppedAt: '',
  pid: 4242,
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(({ session }) => {
    type Listener = (data: unknown) => void
    const listeners = new Map<string, Set<Listener>>()
    let snapshotCalls = 0
    const resizeCalls: Array<{ cols: number; rows: number }> = []
    let failNextResize = false
    const writeCalls: string[] = []
    let jumpViewportOnNextResize = false
    let viewportJumps = 0

    const emit = (name: string, data: unknown) => {
      for (const listener of listeners.get(name) ?? []) listener(data)
    }
    const encode = (value: string) => {
      const bytes = new TextEncoder().encode(value)
      let binary = ''
      for (const byte of bytes) binary += String.fromCharCode(byte)
      return btoa(binary)
    }

    const capabilities = {
      platformId: 'terminal-render-test',
      os: 'darwin',
      arch: 'arm64',
      embeddedTerminalSupported: true,
      standaloneTerminalSupported: true,
      systemTraySupported: false,
      fileOpenSupported: true,
      updateCheckSupported: false,
      updateInstallSupported: false,
      autoStartSupported: false,
      singleInstanceSupported: true,
      windowActivationSupported: true,
      hideOnCloseSupported: false,
      backgroundResidentSupported: false,
      closeAction: 'quit',
      secureSecretStoreKind: 'test',
      pathDiagnosticsSupported: true,
      supportedShells: [],
      defaultShellKey: '',
    }

    const history =
      'historical-noise-0123456789\r\n'.repeat(3000) +
      '\x1b[2J\x1b[HHISTORY_PARSED\r\n'

    const implementations: Record<string, (...args: unknown[]) => Promise<unknown>> = {
      GetSessions: async () => [session],
      GetSession: async () => session,
      GetPlatformCapabilities: async () => capabilities,
      GetAppInfo: async () => ({ version: 'test' }),
      GetRemoteWebUIStatus: async () => ({ openable: false, url: '', running: false }),
      GetOutputHistorySnapshot: async () => {
        snapshotCalls++
        return await new Promise<string>((resolve) => {
          setTimeout(() => {
            // seq=1 is covered by the snapshot and must be dropped. seq=3
            // arrives while history is still replaying and must follow it.
            emit(`pty:data:${session.id}`, {
              r: 'run-old',
              v: '7',
              s: 1,
              d: encode('\r\nDUPLICATE_MUST_NOT_RENDER'),
            })
            emit(`pty:data:${session.id}`, {
              r: 'run-old',
              v: '7',
              s: 3,
              d: encode('LIVE_AFTER_HISTORY'),
            })
            resolve(JSON.stringify({
              data: encode(history),
              seq: 2,
              runToken: 'run-old',
              runVersion: '7',
            }))
          }, 25)
        })
      },
      PtyWrite: async (_sessionId, data) => {
        writeCalls.push(String(data))
      },
      PtyResize: async (_sessionId, cols, rows) => {
        resizeCalls.push({ cols: Number(cols), rows: Number(rows) })
        if (failNextResize) {
          failNextResize = false
          throw new Error('synthetic transient resize failure')
        }
        if (jumpViewportOnNextResize) {
          jumpViewportOnNextResize = false
          requestAnimationFrame(() => {
            requestAnimationFrame(() => {
              const textarea = document.querySelector<HTMLTextAreaElement>('.xterm-helper-textarea')
              for (let page = 0; page < 100; page++) {
                const event = new KeyboardEvent('keydown', {
                  bubbles: true,
                  cancelable: true,
                  code: 'PageUp',
                  key: 'PageUp',
                  shiftKey: true,
                })
                Object.defineProperty(event, 'keyCode', { value: 33 })
                textarea?.dispatchEvent(event)
              }
              viewportJumps++
            })
          })
        }
      },
      GetPtyDimensions: async () => 120030,
      GetProviders: async () => [],
      GetTerminalPresets: async () => [],
      GetSavedWorkDirs: async () => [],
      GetSettings: async () => ({}),
    }

    const app = new Proxy(implementations, {
      get(target, property: string) {
        return target[property] ?? (async () => null)
      },
    })

    ;window.__terminalTest = {
      emit,
      encode,
      snapshotCalls: () => snapshotCalls,
      writeCalls: () => [...writeCalls],
      jumpViewportOnNextResize: () => {
        jumpViewportOnNextResize = true
      },
      viewportJumps: () => viewportJumps,
      resizeCalls: () => [...resizeCalls],
      failNextResize: () => {
        failNextResize = true
      },
    }
    ;(window as any).go = {
      main: { App: app },
      // webui Service 绑定 stub：T-2.3 a11y 测试需要 pi Web 平面切换控件可见
      // （ProbeWebUI → available；OpenWebPlane → about:blank 免真实服务）。
      webui: {
        Service: {
          GetWebUIStatus: async () => ({ state: 'unknown' }),
          ProbeWebUI: async () => ({ state: 'available', url: '', port: 0 }),
          OpenWebPlane: async () => 'about:blank',
          RegisterSession: async () => null,
          RemoveSession: async () => null,
          Invalidate: async () => null,
        },
      },
    }
    ;(window as any).runtime = new Proxy(
      {
        EventsOnMultiple(name: string, callback: Listener) {
          let bucket = listeners.get(name)
          if (!bucket) listeners.set(name, (bucket = new Set()))
          bucket.add(callback)
          return () => bucket?.delete(callback)
        },
      },
      {
        get(target, property: string) {
          return (target as any)[property] ?? (() => undefined)
        },
      },
    )
  }, { session: terminalSession })
})

test('large history settles before live output and Darwin uses the compatible renderer', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()

  const rows = page.locator('.xterm-rows')
  await expect(rows).toContainText('HISTORY_PARSED')
  await expect(rows).toContainText('LIVE_AFTER_HISTORY')
  await expect(rows).not.toContainText('DUPLICATE_MUST_NOT_RENDER')

  // xterm 6's built-in DOM renderer owns the macOS surface. The incompatible
  // xterm-5 CanvasAddon must never create a canvas layer.
  await expect(page.locator('.xterm-screen canvas')).toHaveCount(0)
})

test('route changes preserve the terminal and repaint output received while hidden', async ({ page }) => {
  await page.goto('/')
  const sessionItem = page.locator('.sess-item', { hasText: 'terminal-render-workspace' })
  await sessionItem.locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  await page.getByRole('link', { name: 'Provider Center' }).click()
  await expect(page).toHaveURL(/#\/provider$/)
  await page.evaluate(() => {
    const testApi = window.__terminalTest
    testApi.emit('pty:data:terminal-render-regression', {
      r: 'run-old',
      v: '7',
      s: 4,
      d: testApi.encode('\r\nLIVE_WHILE_HIDDEN'),
    })
  })

  await sessionItem.locator('.sess-info').click()
  await expect(page).toHaveURL(/#\/terminal$/)
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_WHILE_HIDDEN')
  await expect.poll(() => page.evaluate(() => window.__terminalTest.snapshotCalls())).toBe(1)
})

test('same-session restart resets seq dedup and rejects late output from the old run', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  await page.evaluate(() => {
    const testApi = window.__terminalTest
    testApi.emit('pty:data:terminal-render-regression', {
      r: 'run-new',
      v: '8',
      s: 1,
      d: testApi.encode('\x1b[2J\x1b[HNEW_RUN_SEQ_ONE'),
    })
    testApi.emit('pty:data:terminal-render-regression', {
      r: 'run-old',
      v: '7',
      s: 999,
      d: testApi.encode('\r\nSTALE_OLD_RUN_MUST_NOT_RENDER'),
    })
  })

  const rows = page.locator('.xterm-rows')
  await expect(rows).toContainText('NEW_RUN_SEQ_ONE')
  await expect(rows).not.toContainText('STALE_OLD_RUN_MUST_NOT_RENDER')
})

test('ANSI colours and CJK input remain in one terminal grid', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  await page.evaluate(() => {
    const testApi = window.__terminalTest
    testApi.emit('pty:data:terminal-render-regression', {
      r: 'run-old',
      v: '7',
      s: 4,
      d: testApi.encode('\x1b[2J\x1b[H\x1b[31mRED\x1b[0m 你好\r\nPROMPT> 输入'),
    })
  })

  const rows = page.locator('.xterm-rows')
  await expect(rows).toContainText('RED 你好')
  await expect(rows).toContainText('PROMPT> 输入')
  const red = rows.locator('.xterm-fg-1').filter({ hasText: 'RED' })
  await expect(red).toHaveCount(1)

  const grid = await page.evaluate(() => {
    const rowElements = Array.from(document.querySelectorAll<HTMLElement>('.xterm-rows > div'))
    const cjkRow = rowElements.find((row) => row.textContent?.includes('PROMPT> 输入'))
    const style = document.querySelector<HTMLElement>('.xterm-rows')
    return {
      cjkRows: rowElements.filter((row) => row.textContent?.includes('PROMPT> 输入')).length,
      ligatures: style ? getComputedStyle(style).fontVariantLigatures : '',
      cjkText: cjkRow?.textContent ?? '',
    }
  })
  expect(grid.cjkRows).toBe(1)
  expect(grid.cjkText).toContain('PROMPT> 输入')
  expect(grid.ligatures).toBe('none')
})

test('Pi receives distinct submit and multiline input sequences', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  const textarea = page.locator('.xterm-helper-textarea')
  await textarea.focus()
  await page.keyboard.press('Shift+Enter')
  await page.keyboard.press('Enter')

  await expect.poll(async () => {
    return page.evaluate(() => {
      return window.__terminalTest.writeCalls().map((value: string) => {
        return Array.from(atob(value), (character) => character.charCodeAt(0))
      })
    })
  }).toEqual([[27, 91, 49, 51, 59, 50, 117], [13]])
})

test('refresh keeps a terminal that was following output at the bottom', async ({ page }) => {
  await page.goto('/')
  const sessionItem = page.locator('.sess-item', { hasText: 'terminal-render-workspace' })
  await sessionItem.locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')
  await page.evaluate(() => {
    const testApi = window.__terminalTest
    testApi.emit('pty:data:terminal-render-regression', {
      r: 'run-old',
      v: '7',
      s: 4,
      d: testApi.encode(`${'scrollback-line\r\n'.repeat(500)}LATEST_OUTPUT`),
    })
  })
  await expect(page.locator('.xterm-rows')).toContainText('LATEST_OUTPUT')
  await expect.poll(() => page.evaluate(() => {
    return window.__terminalTest.resizeCalls().length
  })).toBeGreaterThanOrEqual(2)
  await page.getByRole('link', { name: 'Provider Center' }).click()
  await sessionItem.locator('.sess-info').click()

  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
  await expect(page.locator('.xterm-rows')).toContainText('LATEST_OUTPUT')
  await page.evaluate(() => {
    window.__terminalTest.jumpViewportOnNextResize()
  })
  await page.setViewportSize({ width: 1200, height: 700 })
  await expect.poll(() => page.evaluate(() => {
    return window.__terminalTest.viewportJumps()
  })).toBe(1)
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
  await expect(page.locator('.xterm-rows')).toContainText('LATEST_OUTPUT')
})

test('web plane toggle exposes aria-pressed and the iframe an accessible title (T-2.3 a11y)', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  // 切换控件：role=group + 两个 pill 选项以 aria-pressed 表达当前平面（交互文档 §8）
  const group = page.getByRole('group', { name: '会话显示平面切换' })
  await expect(group).toBeVisible()
  const tuiBtn = group.getByRole('button', { name: '终端' })
  const webBtn = group.getByRole('button', { name: '网页' })
  await expect(tuiBtn).toHaveAttribute('aria-pressed', 'true')
  await expect(webBtn).toHaveAttribute('aria-pressed', 'false')

  // 键盘等效路径：按钮均为原生 button，click 即 Enter/Space 激活
  await webBtn.click()
  await expect(tuiBtn).toHaveAttribute('aria-pressed', 'false')
  await expect(webBtn).toHaveAttribute('aria-pressed', 'true')

  // Web 平面 iframe：可访问 title + sandbox 边界（契约 §6.5）
  const frame = page.locator('.web-plane-host iframe.web-frame')
  await expect(frame).toHaveAttribute('title', 'Pi Web 会话平面')
  await expect(frame).toHaveAttribute('sandbox', 'allow-scripts allow-forms')

  // 切回终端后 iframe 保留不销毁（v-show），aria-pressed 回落
  await tuiBtn.click()
  await expect(tuiBtn).toHaveAttribute('aria-pressed', 'true')
  await expect(frame).toBeHidden()
})

test('PTY geometry retries transient failures and fills the terminal height', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  const before = await page.evaluate(() => {
    const testApi = window.__terminalTest
    testApi.failNextResize()
    return testApi.resizeCalls().length
  })
  await page.setViewportSize({ width: 1280, height: 760 })

  await expect.poll(async () => {
    return page.evaluate((start) => {
      const calls = window.__terminalTest.resizeCalls()
      return calls.length >= start + 2 &&
        calls[calls.length - 1].cols === calls[calls.length - 2].cols &&
        calls[calls.length - 1].rows === calls[calls.length - 2].rows
    }, before)
  }).toBe(true)

  const geometry = await page.evaluate(() => {
    const body = document.querySelector<HTMLElement>('.term-body')!
    const screen = document.querySelector<HTMLElement>('.xterm-screen')!
    const renderedRows = document.querySelectorAll('.xterm-rows > div').length
    const calls = window.__terminalTest.resizeCalls()
    const last = calls[calls.length - 1]
    return {
      renderedRows,
      resizedRows: last.rows,
      bottomRemainder: body.getBoundingClientRect().height - screen.getBoundingClientRect().height,
      rowHeight: screen.getBoundingClientRect().height / renderedRows,
    }
  })

  expect(geometry.resizedRows).toBe(geometry.renderedRows)
  // fit may leave only the unavoidable fractional remainder (< one cell),
  // never the multi-row strip seen in the OpenCode regression screenshot.
  expect(geometry.bottomRemainder).toBeGreaterThanOrEqual(0)
  expect(geometry.bottomRemainder).toBeLessThan(geometry.rowHeight + 1)
})
