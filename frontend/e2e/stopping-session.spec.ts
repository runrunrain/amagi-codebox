import { expect, test } from '@playwright/test'

const stoppingSession = {
  id: 'stopping-desktop-session',
  appType: 'claudecode',
  providerName: 'test-provider',
  presetName: '',
  model: 'claude-test',
  mode: 'terminal',
  status: 'stopping',
  workDir: '/tmp/stopping-workspace',
  title: 'durable cleanup still active',
  createdAt: '2026-08-04T00:00:00Z',
  startedAt: '2026-08-04T00:00:01Z',
  stoppedAt: '',
  pid: 4242,
}

test.beforeEach(async ({ page }, testInfo) => {
  await page.addInitScript(({ session, startsRunning }) => {
    let status = startsRunning ? 'running' : 'stopping'
    const capabilities = {
      platformId: 'desktop-stopping-test',
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
    const implementations: Record<string, (...args: unknown[]) => Promise<unknown>> = {
      GetSessions: async () => [{ ...session, status }],
      GetSession: async () => ({ ...session, status }),
      StopSession: async () => { status = 'stopping' },
      GetPlatformCapabilities: async () => capabilities,
      GetAppInfo: async () => ({ version: 'test' }),
      GetRemoteWebUIStatus: async () => ({ openable: false, url: '', running: false }),
      GetOutputHistorySnapshot: async () => '',
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
    ;(window as any).go = { main: { App: app } }
    ;(window as any).runtime = new Proxy({}, {
      get: () => () => () => undefined,
    })
  }, { session: stoppingSession, startsRunning: testInfo.title.includes('accepted Stop') })
})

test('stopping remains active, visible, read-only, and routable', async ({ page }) => {
  await page.goto('/')

  const item = page.locator('.sess-item', { hasText: 'stopping-workspace' })
  await expect(item).toBeVisible()
  await expect(item).toContainText('停止中')
  await expect(page.locator('.count-pill')).toHaveText('1')
  await expect(page.locator('.sess-empty')).toHaveCount(0)
  await expect(item.locator('.close-btn')).toBeDisabled()

  await item.locator('.sess-info').click()
  await expect(page).toHaveURL(/#\/terminal$/)
  const stopButton = page.getByRole('button', { name: '停止中…' })
  await expect(stopButton).toBeVisible()
  await expect(stopButton).toBeDisabled()
})

test('accepted Stop reports requested/stopping until terminal receipt', async ({ page }) => {
  await page.goto('/')
  const item = page.locator('.sess-item', { hasText: 'stopping-workspace' })
  await expect(item).toContainText('运行中')
  await item.locator('.sess-info').click()

  const stopButton = page.getByRole('button', { name: '停止' })
  await stopButton.click()
  await expect(page.getByRole('button', { name: '停止中…' })).toBeDisabled()
  await expect(page.getByText('已请求停止，正在等待进程退出')).toBeVisible()
  await expect(page.getByText('会话已停止')).toHaveCount(0)
})
