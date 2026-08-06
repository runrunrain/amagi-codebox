import { Buffer } from 'node:buffer'
import { performance as nodePerformance } from 'node:perf_hooks'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test'
import {
  ctl,
  closeDevice,
  GUIDE_KEY,
  startHarness,
  stopHarness,
  type CreatedSession,
  type HarnessInfo,
} from '../helpers/harness'
import { installWsRelay, type WsRelayController } from '../helpers/network'
import { summarize, writeCsv, writeJson } from './metrics'

type HarnessHandle = Awaited<ReturnType<typeof startHarness>>

type TimingMeasurement = {
  status: string
  durationMs: number | null
  budgetMs: number
  budgetStatus: string
  invalidReason: string | null
}

type TimingReport = {
  schemaVersion: number
  unit: string
  measurements: {
    T0_T1: TimingMeasurement
    R0_R1: TimingMeasurement
  }
}

interface LongTaskSample {
  phase: string
  startTime: number
  duration: number
}

interface BrowserPerfState {
  navigationStart: number
  currentPhase: string
  pairSubmitStart: number | null
  workspaceStart: number | null
  disconnectStart: number | null
  onlineEvents: Array<{ at: number; trusted: boolean }>
  longTasks: LongTaskSample[]
}

interface ResourceTotals {
  count: number
  transferSize: number
  encodedBodySize: number
  decodedBodySize: number
  paths: Array<{
    path: string
    initiatorType: string
    duration: number
    transferSize: number
    encodedBodySize: number
    decodedBodySize: number
  }>
}

interface CoreSample {
  status: 'ok'
  round: number
  sample: number
  startedAt: string
  relayProfile: {
    clientToServerLatencyMs: number
    serverToClientLatencyMs: number
    jitterMs: number
    seed: number
  }
  coldPairToLobbyMs: number
  pairSubmitToLobbyMs: number
  pairLobbyT0T1Ms: number
  pairedOpenToLobbyMs: number
  pairedOpenT0T1Ms: number
  workspaceOpenToOperableMs: number
  workspaceFirstFrameConfirmed: true
  recoveryDisconnectToOperableMs: number
  recoveryOnlineToOperableMs: number
  recoveryR0R1Ms: number
  recoveryOnlineEventTrusted: boolean
  recoveryFirstFrameConfirmed: true
  coldLongTasks: LongTaskSample[]
  pairedOpenLongTasks: LongTaskSample[]
  workspaceLongTasks: LongTaskSample[]
  recoveryLongTasks: LongTaskSample[]
  coldResources: ResourceTotals
  pairedOpenResources: ResourceTotals
  workspaceResources: ResourceTotals
  relayCounts: ReturnType<typeof relayCountsSnapshot>
  consoleErrors: string[]
}

interface FailedSample {
  status: 'failed'
  round: number
  sample: number
  startedAt: string
  error: string
}

type CoreSampleResult = CoreSample | FailedSample

interface ScrollResult {
  durationMs: number
  frameCount: number
  fps: number
  frameDeltaP50Ms: number
  frameDeltaP95Ms: number
  frameDeltaMaxMs: number
  intervalsOver25Ms: number
  intervalsOver50Ms: number
  maxRenderedRows: number
  scrollHeight: number
  clientHeight: number
}

interface HistorySample {
  status: 'ok'
  round: number
  sample: number
  fixture: '4000-error-frames'
  frameCount: 4000
  rawPayloadBytes: number
  injectionMs: number
  workspaceOpenToOperableMs: number
  timelineItemCount: number
  appliedSeqCount: number
  renderedRowsAtRest: number
  attachFrameBytes: number
  attachLongTasks: LongTaskSample[]
  scroll: ScrollResult
  scrollLongTasks: LongTaskSample[]
  consoleErrors: string[]
}

interface ByteWindowSample {
  status: 'ok'
  round: number
  sample: number
  fixture: '1mib-32x32k-lines'
  frameCount: 32
  rawPayloadBytes: 1048576
  injectionMs: number
  workspaceOpenToOperableMs: number
  timelineItemCount: number
  attachFrameBytes: number
  attachLongTasks: LongTaskSample[]
  consoleErrors: string[]
}

const round = Number(process.env.M4B_ROUND ?? '0')
const sampleCount = Number(process.env.M4B_SAMPLES ?? '10')
const historySampleCount = Number(process.env.M4B_HISTORY_SAMPLES ?? '5')
const byteWindowSampleCount = Number(process.env.M4B_BYTE_SAMPLES ?? '3')
const artifactDir = path.resolve(process.env.M4B_ARTIFACT_DIR ?? 'test-results/m4-b-performance')
const rawDir = path.join(artifactDir, 'raw')

const AC01_BUDGET_MS = 3000
const AC02_BUDGET_MS = 5000
const HISTORY_FRAME_COUNT = 4000 as const
const HISTORY_RAW_BYTES = Array.from({ length: HISTORY_FRAME_COUNT }, (_, i) => Buffer.byteLength(`Error-${i + 1}\n`)).reduce(
  (sum, bytes) => sum + bytes,
  0,
)
const BYTE_FRAME_COUNT = 32 as const
const BYTE_FRAME_PAYLOAD = `${'x'.repeat(32767)}\n`
const BYTE_WINDOW_RAW_BYTES = Buffer.byteLength(BYTE_FRAME_PAYLOAD) * BYTE_FRAME_COUNT

function finite(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
    throw new Error(`${label} must be a finite nonnegative number; got ${String(value)}`)
  }
  return value
}

function errorText(error: unknown): string {
  if (error instanceof Error) return `${error.name}: ${error.message}`
  return String(error)
}

function relayCountsSnapshot(controller: WsRelayController) {
  const counts = controller.counts
  return {
    connections: counts.connections,
    clientToServerForwarded: counts.clientToServerForwarded,
    serverToClientForwarded: counts.serverToClientForwarded,
    clientToServerDropped: counts.clientToServerDropped,
    serverToClientDropped: counts.serverToClientDropped,
    closes: counts.closes,
  }
}

async function newMeasuredPage(browser: Browser): Promise<{ context: BrowserContext; page: Page; consoleErrors: string[] }> {
  const context = await browser.newContext({
    viewport: { width: 360, height: 800 },
    isMobile: true,
    hasTouch: true,
  })
  const page = await context.newPage()
  const consoleErrors: string[] = []
  page.on('console', (message) => {
    if (message.type() !== 'error') return
    if (message.text().startsWith('Failed to load resource')) return
    consoleErrors.push(message.text())
  })
  page.on('pageerror', (error) => consoleErrors.push(String(error)))

  await page.addInitScript((guideKey: string) => {
    try {
      localStorage.setItem(guideKey, '1')
    } catch {
      // The performance harness remains usable when localStorage is unavailable.
    }
    const state: BrowserPerfState = {
      navigationStart: performance.now(),
      currentPhase: 'cold',
      pairSubmitStart: null,
      workspaceStart: null,
      disconnectStart: null,
      onlineEvents: [],
      longTasks: [],
    }
    ;(window as unknown as { __m4bPerf: BrowserPerfState }).__m4bPerf = state

    window.addEventListener(
      'online',
      (event) => state.onlineEvents.push({ at: performance.now(), trusted: event.isTrusted }),
      { capture: true },
    )
    document.addEventListener(
      'click',
      (event) => {
        const target = event.target
        if (target instanceof Element && target.closest('.card-open')) state.workspaceStart = performance.now()
      },
      { capture: true },
    )

    if (typeof PerformanceObserver !== 'undefined' && PerformanceObserver.supportedEntryTypes.includes('longtask')) {
      const observer = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          state.longTasks.push({
            phase: state.currentPhase,
            startTime: entry.startTime,
            duration: entry.duration,
          })
        }
      })
      observer.observe({ type: 'longtask', buffered: true })
    }
  }, GUIDE_KEY)

  return { context, page, consoleErrors }
}

async function readPerfState(page: Page): Promise<BrowserPerfState> {
  return page.evaluate(() => {
    const state = (window as unknown as { __m4bPerf: BrowserPerfState }).__m4bPerf
    return structuredClone(state)
  })
}

async function setPhase(page: Page, phase: string): Promise<void> {
  await page.evaluate((nextPhase) => {
    const state = (window as unknown as { __m4bPerf: BrowserPerfState }).__m4bPerf
    state.currentPhase = nextPhase
    state.longTasks = []
  }, phase)
}

async function longTasks(page: Page): Promise<LongTaskSample[]> {
  await page.waitForTimeout(60)
  return (await readPerfState(page)).longTasks
}

async function twoAnimationFrames(page: Page): Promise<number> {
  return page.evaluate(
    () =>
      new Promise<number>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve(performance.now())))
      }),
  )
}

async function resourceTotals(page: Page): Promise<ResourceTotals> {
  return page.evaluate(() => {
    const resources = performance.getEntriesByType('resource') as PerformanceResourceTiming[]
    const paths = resources.map((entry) => {
      let resourcePath = entry.name
      try {
        const url = new URL(entry.name)
        resourcePath = url.pathname
      } catch {
        // Keep the non-URL resource name; no query values are copied.
      }
      return {
        path: resourcePath,
        initiatorType: entry.initiatorType,
        duration: Number(entry.duration.toFixed(3)),
        transferSize: entry.transferSize,
        encodedBodySize: entry.encodedBodySize,
        decodedBodySize: entry.decodedBodySize,
      }
    })
    return {
      count: paths.length,
      transferSize: paths.reduce((sum, entry) => sum + entry.transferSize, 0),
      encodedBodySize: paths.reduce((sum, entry) => sum + entry.encodedBodySize, 0),
      decodedBodySize: paths.reduce((sum, entry) => sum + entry.decodedBodySize, 0),
      paths,
    }
  })
}

function piniaLookupBody(storeID: string): string {
  return `(() => {
    const app = document.querySelector('#app')?.__vue_app__;
    const pinia = app?.config?.globalProperties?.$pinia;
    return pinia?._s?.get(${JSON.stringify(storeID)}) ?? null;
  })()`
}

async function waitForLobbyTiming(page: Page): Promise<TimingReport> {
  await page.waitForFunction(
    (storeExpression) => {
      const store = (0, eval)(storeExpression) as { listTimingSnapshot?: () => TimingReport | null } | null
      return store?.listTimingSnapshot?.()?.measurements?.T0_T1?.status === 'observed'
    },
    piniaLookupBody('remote-lobby'),
  )
  return page.evaluate((storeExpression) => {
    const store = (0, eval)(storeExpression) as { listTimingSnapshot: () => TimingReport } | null
    if (!store) throw new Error('remote-lobby Pinia store is unavailable in the production bundle')
    return store.listTimingSnapshot()
  }, piniaLookupBody('remote-lobby'))
}

async function waitForRecoveryTiming(page: Page): Promise<TimingReport> {
  await page.waitForFunction(
    (storeExpression) => {
      const store = (0, eval)(storeExpression) as { timingSnapshot?: () => TimingReport } | null
      return store?.timingSnapshot?.()?.measurements?.R0_R1?.status === 'observed'
    },
    piniaLookupBody('remote-workspace'),
  )
  return page.evaluate((storeExpression) => {
    const store = (0, eval)(storeExpression) as { timingSnapshot: () => TimingReport } | null
    if (!store) throw new Error('remote-workspace Pinia store is unavailable in the production bundle')
    return store.timingSnapshot()
  }, piniaLookupBody('remote-workspace'))
}

async function workspaceProjection(page: Page): Promise<{
  wsState: string
  timelineItemCount: number
  appliedSeqCount: number
}> {
  return page.evaluate((storeExpression) => {
    const store = (0, eval)(storeExpression) as
      | {
          wsState: string
          timelineItems: unknown[]
          continuitySnapshot: () => { appliedSeqCounts: Record<string, number> }
        }
      | null
    if (!store) throw new Error('remote-workspace Pinia store is unavailable in the production bundle')
    const continuity = store.continuitySnapshot()
    return {
      wsState: store.wsState,
      timelineItemCount: store.timelineItems.length,
      appliedSeqCount: Object.values(continuity.appliedSeqCounts).reduce((sum, count) => sum + count, 0),
    }
  }, piniaLookupBody('remote-workspace'))
}

async function waitForLobbyOperable(page: Page): Promise<void> {
  await expect(page).toHaveURL(/#\/lobby$/)
  await expect(page.locator('.lobby-title')).toHaveText('会话大厅')
  await expect(page.locator('.card-open').first()).toBeVisible()
  await expect(page.locator('.card-open').first()).toBeEnabled()
  await waitForLobbyTiming(page)
}

async function waitForWorkspaceOperable(page: Page, expectedText?: string): Promise<void> {
  await expect(page).toHaveURL(/#\/workspace\//)
  await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 15_000 })
  await expect(page.locator('.timeline')).toBeVisible()
  await expect(page.locator('.back-btn')).toBeEnabled()
  await expect(page.locator('.menu-btn').first()).toBeEnabled()
  await expect(page.locator('.control-action')).toBeEnabled()
  await expect(page.locator('.composer-input')).toBeVisible()
  if (expectedText) await expect(page.locator('.timeline')).toContainText(expectedText, { timeout: 15_000 })
}

async function pairIntoLobby(page: Page, info: HarnessInfo, deviceName: string): Promise<void> {
  const win = await ctl<{ code: string; expiresAt: string }>(info, 'POST', '/pairing-window')
  await page.goto(`${info.origin}/#/connect?code=${encodeURIComponent(win.code)}&expiresAt=${encodeURIComponent(win.expiresAt)}`)
  await expect(page.locator('#pair-code')).toHaveValue(win.code)
  await page.locator('#pair-device-name').fill(deviceName)
  await page.evaluate(() => {
    const state = (window as unknown as { __m4bPerf: BrowserPerfState }).__m4bPerf
    state.pairSubmitStart = performance.now()
  })
  await page.getByRole('button', { name: '完成配对' }).click()
  await waitForLobbyOperable(page)
}

async function measureCoreSample(browser: Browser, sample: number): Promise<CoreSample> {
  let harness: HarnessHandle | null = null
  let context: BrowserContext | null = null
  let page: Page | null = null
  let cleanupRelay: (() => Promise<void>) | null = null
  try {
    harness = await startHarness()
    const created = await ctl<CreatedSession>(harness.info, 'POST', '/control/session', { cliType: 'claudecode' })
    const measured = await newMeasuredPage(browser)
    context = measured.context
    page = measured.page

    const seed = 0x4b0000 + round * 1000 + sample
    const relayProfile = {
      clientToServerLatencyMs: 80,
      serverToClientLatencyMs: 80,
      jitterMs: 30,
      seed,
    }
    const relay = await installWsRelay(page, '**/ws/v1', {
      clientToServerLatencyMs: relayProfile.clientToServerLatencyMs,
      serverToClientLatencyMs: relayProfile.serverToClientLatencyMs,
      clientToServerJitterMs: relayProfile.jitterMs,
      serverToClientJitterMs: relayProfile.jitterMs,
      seed,
    })
    cleanupRelay = relay.cleanup

    // Requested cold chain: first document navigation → pairing submit → lobby is operable + two rendered frames.
    await pairIntoLobby(page, harness.info, `M4-B-R${round}-${sample}`)
    const pairLobbyEnd = await twoAnimationFrames(page)
    const pairState = await readPerfState(page)
    const pairTiming = await waitForLobbyTiming(page)
    const pairResources = await resourceTotals(page)
    const pairLongTasks = await longTasks(page)

    // Canonical AC-01 shape: already-paired device opens a fresh document, immediately follows the explicit enter action.
    await page.goto(`${harness.info.origin}/`)
    await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
    await page.getByRole('button', { name: '进入会话大厅' }).click()
    await waitForLobbyOperable(page)
    const pairedOpenEnd = await twoAnimationFrames(page)
    const pairedState = await readPerfState(page)
    const pairedTiming = await waitForLobbyTiming(page)
    const pairedResources = await resourceTotals(page)
    const pairedLongTasks = await longTasks(page)

    // T0w is captured on the real card-open click event (capture phase, immediately before router/store work).
    await setPhase(page, 'workspace')
    const resourcesBeforeWorkspace = await resourceTotals(page)
    await page.locator('.card-open', { hasText: created.title }).click()
    await waitForWorkspaceOperable(page)
    const workspaceEnd = await twoAnimationFrames(page)
    const workspaceState = await readPerfState(page)
    const workspaceResourcesAll = await resourceTotals(page)
    const workspaceLongTasks = await longTasks(page)
    const workspaceResources: ResourceTotals = {
      count: workspaceResourcesAll.count - resourcesBeforeWorkspace.count,
      transferSize: workspaceResourcesAll.transferSize - resourcesBeforeWorkspace.transferSize,
      encodedBodySize: workspaceResourcesAll.encodedBodySize - resourcesBeforeWorkspace.encodedBodySize,
      decodedBodySize: workspaceResourcesAll.decodedBodySize - resourcesBeforeWorkspace.decodedBodySize,
      paths: workspaceResourcesAll.paths.slice(resourcesBeforeWorkspace.paths.length),
    }

    // Keep a visible timeline payload, then create a real browser offline→online transition while relay closes the WS.
    await ctl(harness.info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'recovery-before\n' })
    await expect(page.locator('.timeline')).toContainText('recovery-before')
    await setPhase(page, 'recovery')
    await context.setOffline(true)
    await page.waitForFunction(() => navigator.onLine === false)
    await page.evaluate(() => {
      const state = (window as unknown as { __m4bPerf: BrowserPerfState }).__m4bPerf
      state.disconnectStart = performance.now()
    })
    await relay.controller.disconnectAll(1011)
    const banner = page.locator('[data-testid=continuity-banner]')
    await expect(banner).toBeVisible({ timeout: 10_000 })
    await expect(banner).toHaveAttribute('data-state', 'reconnecting')
    await ctl(harness.info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'recovery-after\n' })

    await context.setOffline(false)
    await page.waitForFunction(() => {
      const state = (window as unknown as { __m4bPerf: BrowserPerfState }).__m4bPerf
      return navigator.onLine === true && state.onlineEvents.length > 0
    })
    await expect(banner).toHaveAttribute('data-state', 'restored', { timeout: 10_000 })
    await waitForWorkspaceOperable(page, 'recovery-after')
    const recoveryTiming = await waitForRecoveryTiming(page)
    const recoveryEnd = await twoAnimationFrames(page)
    const recoveryState = await readPerfState(page)
    const recoveryLongTasks = await longTasks(page)

    const latestOnline = recoveryState.onlineEvents.at(-1)
    if (!latestOnline) throw new Error('no browser online event was captured')
    const tPair = pairTiming.measurements.T0_T1
    const tPaired = pairedTiming.measurements.T0_T1
    const rRecovery = recoveryTiming.measurements.R0_R1
    if (tPair.status !== 'observed' || tPair.budgetMs !== AC01_BUDGET_MS) throw new Error('pair lobby T0/T1 anchor invalid')
    if (tPaired.status !== 'observed' || tPaired.budgetMs !== AC01_BUDGET_MS) throw new Error('paired-open T0/T1 anchor invalid')
    if (rRecovery.status !== 'observed' || rRecovery.budgetMs !== AC02_BUDGET_MS) throw new Error('recovery R0/R1 anchor invalid')
    if (workspaceState.workspaceStart === null) throw new Error('T0w card-open capture was not observed')
    if (recoveryState.disconnectStart === null) throw new Error('disconnect start capture was not observed')
    if (pairState.pairSubmitStart === null) throw new Error('pair submit start capture was not observed')

    return {
      status: 'ok',
      round,
      sample,
      startedAt: new Date().toISOString(),
      relayProfile,
      coldPairToLobbyMs: finite(pairLobbyEnd - pairState.navigationStart, 'coldPairToLobbyMs'),
      pairSubmitToLobbyMs: finite(pairLobbyEnd - pairState.pairSubmitStart, 'pairSubmitToLobbyMs'),
      pairLobbyT0T1Ms: finite(tPair.durationMs, 'pairLobbyT0T1Ms'),
      pairedOpenToLobbyMs: finite(pairedOpenEnd - pairedState.navigationStart, 'pairedOpenToLobbyMs'),
      pairedOpenT0T1Ms: finite(tPaired.durationMs, 'pairedOpenT0T1Ms'),
      workspaceOpenToOperableMs: finite(workspaceEnd - workspaceState.workspaceStart, 'workspaceOpenToOperableMs'),
      workspaceFirstFrameConfirmed: true,
      recoveryDisconnectToOperableMs: finite(recoveryEnd - recoveryState.disconnectStart, 'recoveryDisconnectToOperableMs'),
      recoveryOnlineToOperableMs: finite(recoveryEnd - latestOnline.at, 'recoveryOnlineToOperableMs'),
      recoveryR0R1Ms: finite(rRecovery.durationMs, 'recoveryR0R1Ms'),
      recoveryOnlineEventTrusted: latestOnline.trusted,
      recoveryFirstFrameConfirmed: true,
      coldLongTasks: pairLongTasks,
      pairedOpenLongTasks: pairedLongTasks,
      workspaceLongTasks,
      recoveryLongTasks,
      coldResources: pairResources,
      pairedOpenResources: pairedResources,
      workspaceResources,
      relayCounts: relayCountsSnapshot(relay.controller),
      consoleErrors: measured.consoleErrors,
    }
  } finally {
    if (context) await context.setOffline(false).catch(() => {})
    if (cleanupRelay) await cleanupRelay().catch(() => {})
    if (page) await closeDevice(page)
    if (harness) await stopHarness(harness.proc)
  }
}

async function captureAttachedFrameBytes(page: Page): Promise<() => number> {
  let attachedFrameBytes = 0
  page.on('websocket', (socket) => {
    socket.on('framereceived', (frame) => {
      const payload = frame.payload
      const text = typeof payload === 'string' ? payload : payload.toString()
      if (text.includes('"type":"session.attached"')) attachedFrameBytes = Math.max(attachedFrameBytes, Buffer.byteLength(text))
    })
  })
  return () => attachedFrameBytes
}

async function measureScroll(page: Page): Promise<ScrollResult> {
  return page.evaluate(async () => {
    const timeline = document.querySelector<HTMLElement>('.timeline')
    if (!timeline) throw new Error('timeline element not found')
    const deltas: number[] = []
    let maxRenderedRows = 0
    let previous = performance.now()
    const started = previous
    const duration = 3000

    await new Promise<void>((resolve) => {
      const step = (now: number) => {
        const elapsed = now - started
        deltas.push(now - previous)
        previous = now
        const progress = Math.min(1, elapsed / duration)
        // Down then back up: one triangular sweep across the complete virtual range.
        const triangle = progress <= 0.5 ? progress * 2 : (1 - progress) * 2
        timeline.scrollTop = (timeline.scrollHeight - timeline.clientHeight) * triangle
        maxRenderedRows = Math.max(maxRenderedRows, document.querySelectorAll('[data-testid=timeline-item]').length)
        if (progress < 1) requestAnimationFrame(step)
        else resolve()
      }
      requestAnimationFrame(step)
    })

    const sorted = [...deltas].sort((a, b) => a - b)
    const q = (p: number) => {
      if (sorted.length === 0) return 0
      const index = (sorted.length - 1) * p
      const lo = Math.floor(index)
      const hi = Math.ceil(index)
      return lo === hi ? sorted[lo] : sorted[lo] + (sorted[hi] - sorted[lo]) * (index - lo)
    }
    const elapsed = performance.now() - started
    return {
      durationMs: Number(elapsed.toFixed(3)),
      frameCount: deltas.length,
      fps: Number(((deltas.length / elapsed) * 1000).toFixed(3)),
      frameDeltaP50Ms: Number(q(0.5).toFixed(3)),
      frameDeltaP95Ms: Number(q(0.95).toFixed(3)),
      frameDeltaMaxMs: Number(Math.max(...deltas).toFixed(3)),
      intervalsOver25Ms: deltas.filter((delta) => delta > 25).length,
      intervalsOver50Ms: deltas.filter((delta) => delta > 50).length,
      maxRenderedRows,
      scrollHeight: timeline.scrollHeight,
      clientHeight: timeline.clientHeight,
    }
  })
}

async function measureHistorySample(browser: Browser, sample: number): Promise<HistorySample> {
  let harness: HarnessHandle | null = null
  let page: Page | null = null
  try {
    harness = await startHarness()
    const created = await ctl<CreatedSession>(harness.info, 'POST', '/control/session', { cliType: 'claudecode' })
    const injectStart = nodePerformance.now()
    const injected = await ctl<{ count: number }>(harness.info, 'POST', `/control/session/${created.sessionId}/output-many`, {
      count: HISTORY_FRAME_COUNT,
      prefix: 'Error',
    })
    const injectionMs = nodePerformance.now() - injectStart
    if (injected.count !== HISTORY_FRAME_COUNT) throw new Error(`history fixture injected ${injected.count} frames`)

    const measured = await newMeasuredPage(browser)
    page = measured.page
    const attachedFrameBytes = await captureAttachedFrameBytes(page)
    await pairIntoLobby(page, harness.info, `M4-B-H-R${round}-${sample}`)
    await setPhase(page, 'history-attach')
    await page.locator('.card-open', { hasText: created.title }).click()
    await waitForWorkspaceOperable(page, 'Error-1')
    await page.waitForFunction(
      (storeExpression) => {
        const store = (0, eval)(storeExpression) as { timelineItems?: unknown[] } | null
        return store?.timelineItems?.length === 4000
      },
      piniaLookupBody('remote-workspace'),
    )
    const attachEnd = await twoAnimationFrames(page)
    const state = await readPerfState(page)
    const projection = await workspaceProjection(page)
    const attachLongTasks = await longTasks(page)
    if (state.workspaceStart === null) throw new Error('history T0w card-open capture was not observed')

    await setPhase(page, 'history-scroll')
    const scroll = await measureScroll(page)
    const scrollLongTasks = await longTasks(page)
    const renderedRowsAtRest = await page.locator('[data-testid=timeline-item]').count()

    return {
      status: 'ok',
      round,
      sample,
      fixture: '4000-error-frames',
      frameCount: HISTORY_FRAME_COUNT,
      rawPayloadBytes: HISTORY_RAW_BYTES,
      injectionMs: Number(injectionMs.toFixed(3)),
      workspaceOpenToOperableMs: finite(attachEnd - state.workspaceStart, 'history workspaceOpenToOperableMs'),
      timelineItemCount: projection.timelineItemCount,
      appliedSeqCount: projection.appliedSeqCount,
      renderedRowsAtRest,
      attachFrameBytes: attachedFrameBytes(),
      attachLongTasks,
      scroll,
      scrollLongTasks,
      consoleErrors: measured.consoleErrors,
    }
  } finally {
    if (page) await closeDevice(page)
    if (harness) await stopHarness(harness.proc)
  }
}

async function measureByteWindowSample(browser: Browser, sample: number): Promise<ByteWindowSample> {
  let harness: HarnessHandle | null = null
  let page: Page | null = null
  try {
    harness = await startHarness()
    const created = await ctl<CreatedSession>(harness.info, 'POST', '/control/session', { cliType: 'claudecode' })
    const injectStart = nodePerformance.now()
    for (let i = 0; i < BYTE_FRAME_COUNT; i++) {
      await ctl(harness.info, 'POST', `/control/session/${created.sessionId}/output`, { text: BYTE_FRAME_PAYLOAD })
    }
    const injectionMs = nodePerformance.now() - injectStart

    const measured = await newMeasuredPage(browser)
    page = measured.page
    const attachedFrameBytes = await captureAttachedFrameBytes(page)
    await pairIntoLobby(page, harness.info, `M4-B-B-R${round}-${sample}`)
    await setPhase(page, 'byte-window-attach')
    await page.locator('.card-open', { hasText: created.title }).click()
    await waitForWorkspaceOperable(page)
    await page.waitForFunction(
      (storeExpression) => {
        const store = (0, eval)(storeExpression) as { continuitySnapshot?: () => { frontier: number } } | null
        return store?.continuitySnapshot?.().frontier === 32
      },
      piniaLookupBody('remote-workspace'),
    )
    const attachEnd = await twoAnimationFrames(page)
    const state = await readPerfState(page)
    const projection = await workspaceProjection(page)
    const attachLongTasks = await longTasks(page)
    if (state.workspaceStart === null) throw new Error('byte-window T0w card-open capture was not observed')

    return {
      status: 'ok',
      round,
      sample,
      fixture: '1mib-32x32k-lines',
      frameCount: BYTE_FRAME_COUNT,
      rawPayloadBytes: BYTE_WINDOW_RAW_BYTES as 1048576,
      injectionMs: Number(injectionMs.toFixed(3)),
      workspaceOpenToOperableMs: finite(attachEnd - state.workspaceStart, 'byte-window workspaceOpenToOperableMs'),
      timelineItemCount: projection.timelineItemCount,
      attachFrameBytes: attachedFrameBytes(),
      attachLongTasks,
      consoleErrors: measured.consoleErrors,
    }
  } finally {
    if (page) await closeDevice(page)
    if (harness) await stopHarness(harness.proc)
  }
}

function coreCsvRows(samples: CoreSample[]): Record<string, unknown>[] {
  return samples.map((sample) => ({
    round: sample.round,
    sample: sample.sample,
    coldPairToLobbyMs: sample.coldPairToLobbyMs,
    pairSubmitToLobbyMs: sample.pairSubmitToLobbyMs,
    pairLobbyT0T1Ms: sample.pairLobbyT0T1Ms,
    pairedOpenToLobbyMs: sample.pairedOpenToLobbyMs,
    pairedOpenT0T1Ms: sample.pairedOpenT0T1Ms,
    workspaceOpenToOperableMs: sample.workspaceOpenToOperableMs,
    recoveryDisconnectToOperableMs: sample.recoveryDisconnectToOperableMs,
    recoveryOnlineToOperableMs: sample.recoveryOnlineToOperableMs,
    recoveryR0R1Ms: sample.recoveryR0R1Ms,
    recoveryOnlineEventTrusted: sample.recoveryOnlineEventTrusted,
    coldLongTaskCount: sample.coldLongTasks.length,
    workspaceLongTaskCount: sample.workspaceLongTasks.length,
    recoveryLongTaskCount: sample.recoveryLongTasks.length,
    consoleErrorCount: sample.consoleErrors.length,
  }))
}

function historyCsvRows(samples: HistorySample[], byteSamples: ByteWindowSample[]): Record<string, unknown>[] {
  return [
    ...samples.map((sample) => ({
      round: sample.round,
      sample: sample.sample,
      fixture: sample.fixture,
      frameCount: sample.frameCount,
      rawPayloadBytes: sample.rawPayloadBytes,
      injectionMs: sample.injectionMs,
      workspaceOpenToOperableMs: sample.workspaceOpenToOperableMs,
      timelineItemCount: sample.timelineItemCount,
      appliedSeqCount: sample.appliedSeqCount,
      renderedRowsAtRest: sample.renderedRowsAtRest,
      attachFrameBytes: sample.attachFrameBytes,
      attachLongTaskCount: sample.attachLongTasks.length,
      scrollFps: sample.scroll.fps,
      scrollFrameDeltaP95Ms: sample.scroll.frameDeltaP95Ms,
      scrollIntervalsOver50Ms: sample.scroll.intervalsOver50Ms,
      scrollMaxRenderedRows: sample.scroll.maxRenderedRows,
      scrollLongTaskCount: sample.scrollLongTasks.length,
      consoleErrorCount: sample.consoleErrors.length,
    })),
    ...byteSamples.map((sample) => ({
      round: sample.round,
      sample: sample.sample,
      fixture: sample.fixture,
      frameCount: sample.frameCount,
      rawPayloadBytes: sample.rawPayloadBytes,
      injectionMs: sample.injectionMs,
      workspaceOpenToOperableMs: sample.workspaceOpenToOperableMs,
      timelineItemCount: sample.timelineItemCount,
      attachFrameBytes: sample.attachFrameBytes,
      attachLongTaskCount: sample.attachLongTasks.length,
      consoleErrorCount: sample.consoleErrors.length,
    })),
  ]
}

test.describe('M4-B performance measurement', () => {
  test('collects one independent round of AC-01/AC-02 and history-window evidence', async ({ browser }, testInfo) => {
    test.setTimeout(15 * 60_000)
    if (!Number.isInteger(round) || round < 1) throw new Error('M4B_ROUND must be a positive integer')
    fs.mkdirSync(rawDir, { recursive: true })

    const coreResults: CoreSampleResult[] = []
    for (let sample = 1; sample <= sampleCount; sample++) {
      try {
        coreResults.push(await measureCoreSample(browser, sample))
      } catch (error) {
        coreResults.push({ status: 'failed', round, sample, startedAt: new Date().toISOString(), error: errorText(error) })
      }
    }

    const historyResults: Array<HistorySample | FailedSample> = []
    for (let sample = 1; sample <= historySampleCount; sample++) {
      try {
        historyResults.push(await measureHistorySample(browser, sample))
      } catch (error) {
        historyResults.push({ status: 'failed', round, sample, startedAt: new Date().toISOString(), error: errorText(error) })
      }
    }

    const byteWindowResults: Array<ByteWindowSample | FailedSample> = []
    for (let sample = 1; sample <= byteWindowSampleCount; sample++) {
      try {
        byteWindowResults.push(await measureByteWindowSample(browser, sample))
      } catch (error) {
        byteWindowResults.push({ status: 'failed', round, sample, startedAt: new Date().toISOString(), error: errorText(error) })
      }
    }

    const core = coreResults.filter((sample): sample is CoreSample => sample.status === 'ok')
    const history = historyResults.filter((sample): sample is HistorySample => sample.status === 'ok')
    const byteWindow = byteWindowResults.filter((sample): sample is ByteWindowSample => sample.status === 'ok')
    const summary = {
      schemaVersion: 1,
      round,
      generatedAt: new Date().toISOString(),
      browserVersion: browser.version(),
      sampleTargets: { core: sampleCount, history: historySampleCount, byteWindow: byteWindowSampleCount },
      sampleSuccess: { core: core.length, history: history.length, byteWindow: byteWindow.length },
      budgetsMs: { AC01: AC01_BUDGET_MS, AC02: AC02_BUDGET_MS },
      distributions: {
        coldPairToLobbyMs: summarize(core.map((sample) => sample.coldPairToLobbyMs)),
        pairSubmitToLobbyMs: summarize(core.map((sample) => sample.pairSubmitToLobbyMs)),
        pairLobbyT0T1Ms: summarize(core.map((sample) => sample.pairLobbyT0T1Ms)),
        pairedOpenToLobbyMs: summarize(core.map((sample) => sample.pairedOpenToLobbyMs)),
        pairedOpenT0T1Ms: summarize(core.map((sample) => sample.pairedOpenT0T1Ms)),
        workspaceOpenToOperableMs: summarize(core.map((sample) => sample.workspaceOpenToOperableMs)),
        recoveryDisconnectToOperableMs: summarize(core.map((sample) => sample.recoveryDisconnectToOperableMs)),
        recoveryOnlineToOperableMs: summarize(core.map((sample) => sample.recoveryOnlineToOperableMs)),
        recoveryR0R1Ms: summarize(core.map((sample) => sample.recoveryR0R1Ms)),
        history4000AttachToOperableMs: summarize(history.map((sample) => sample.workspaceOpenToOperableMs)),
        history4000ScrollFps: summarize(history.map((sample) => sample.scroll.fps)),
        history4000ScrollFrameP95Ms: summarize(history.map((sample) => sample.scroll.frameDeltaP95Ms)),
        byteWindow1MiBAttachToOperableMs: summarize(byteWindow.map((sample) => sample.workspaceOpenToOperableMs)),
        byteWindowAttachedFrameBytes: summarize(byteWindow.map((sample) => sample.attachFrameBytes)),
      },
      verdict: {
        AC01ColdPair: core.length === sampleCount && summarize(core.map((sample) => sample.coldPairToLobbyMs)).p95 <= AC01_BUDGET_MS,
        AC01PairedOpen: core.length === sampleCount && summarize(core.map((sample) => sample.pairedOpenToLobbyMs)).p95 <= AC01_BUDGET_MS,
        AC01Workspace: core.length === sampleCount && summarize(core.map((sample) => sample.workspaceOpenToOperableMs)).p95 <= AC01_BUDGET_MS,
        AC02Anchor: core.length === sampleCount && summarize(core.map((sample) => sample.recoveryR0R1Ms)).p95 <= AC02_BUDGET_MS,
        AC02FrameConfirmed: core.length === sampleCount && summarize(core.map((sample) => sample.recoveryOnlineToOperableMs)).p95 <= AC02_BUDGET_MS,
      },
      failures: [
        ...coreResults.filter((sample) => sample.status === 'failed'),
        ...historyResults.filter((sample) => sample.status === 'failed'),
        ...byteWindowResults.filter((sample) => sample.status === 'failed'),
      ],
    }

    const rawPayload = {
      ...summary,
      coreSamples: coreResults,
      historySamples: historyResults,
      byteWindowSamples: byteWindowResults,
    }
    const rawFile = path.join(rawDir, `round-${round}.json`)
    const summaryFile = path.join(rawDir, `round-${round}-summary.json`)
    const coreCsvFile = path.join(rawDir, `round-${round}-core.csv`)
    const historyCsvFile = path.join(rawDir, `round-${round}-history.csv`)
    writeJson(rawFile, rawPayload)
    writeJson(summaryFile, summary)
    writeCsv(coreCsvFile, coreCsvRows(core))
    writeCsv(historyCsvFile, historyCsvRows(history, byteWindow))

    await testInfo.attach(`m4-b-round-${round}-summary`, {
      body: Buffer.from(JSON.stringify(summary)),
      contentType: 'application/json',
    })

    expect(BYTE_WINDOW_RAW_BYTES).toBe(1 << 20)
    expect(core, 'all core timing samples must complete').toHaveLength(sampleCount)
    expect(history, 'all 4K history samples must complete').toHaveLength(historySampleCount)
    expect(byteWindow, 'all 1MiB history samples must complete').toHaveLength(byteWindowSampleCount)
    expect(core.every((sample) => sample.recoveryOnlineEventTrusted), 'R0 must come from a trusted browser online event').toBe(true)
    expect(core.every((sample) => sample.consoleErrors.length === 0), 'core samples must have no page errors').toBe(true)
    expect(history.every((sample) => sample.timelineItemCount === HISTORY_FRAME_COUNT)).toBe(true)
    expect(history.every((sample) => sample.appliedSeqCount === HISTORY_FRAME_COUNT)).toBe(true)
    expect(history.every((sample) => sample.scroll.maxRenderedRows < 100), 'virtualizer must keep rendered rows bounded').toBe(true)
    expect(summary.verdict.AC01ColdPair, `AC-01 cold-pair p95 must be <=${AC01_BUDGET_MS}ms`).toBe(true)
    expect(summary.verdict.AC01PairedOpen, `AC-01 paired-open p95 must be <=${AC01_BUDGET_MS}ms`).toBe(true)
    expect(summary.verdict.AC01Workspace, `AC-01 workspace p95 must be <=${AC01_BUDGET_MS}ms`).toBe(true)
    expect(summary.verdict.AC02Anchor, `AC-02 production R0/R1 p95 must be <=${AC02_BUDGET_MS}ms`).toBe(true)
    expect(summary.verdict.AC02FrameConfirmed, `AC-02 frame-confirmed p95 must be <=${AC02_BUDGET_MS}ms`).toBe(true)
  })
})
