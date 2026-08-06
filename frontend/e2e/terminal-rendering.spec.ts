import { expect, test } from '@playwright/test'

const terminalSession = {
  id: 'terminal-render-regression',
  appType: 'opencode',
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

    const emit = (name: string, data: unknown) => {
      for (const listener of listeners.get(name) ?? []) listener(data)
    }
    const encode = (value: string) => btoa(value)

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

    ;(window as any).__terminalTest = {
      emit,
      encode,
      snapshotCalls: () => snapshotCalls,
    }
    ;(window as any).go = { main: { App: app } }
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
    const testApi = (window as any).__terminalTest
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
  await expect.poll(() => page.evaluate(() => (window as any).__terminalTest.snapshotCalls())).toBe(1)
})

test('same-session restart resets seq dedup and rejects late output from the old run', async ({ page }) => {
  await page.goto('/')
  await page.locator('.sess-item', { hasText: 'terminal-render-workspace' }).locator('.sess-info').click()
  await expect(page.locator('.xterm-rows')).toContainText('LIVE_AFTER_HISTORY')

  await page.evaluate(() => {
    const testApi = (window as any).__terminalTest
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
