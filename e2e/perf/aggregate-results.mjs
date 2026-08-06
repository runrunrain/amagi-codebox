#!/usr/bin/env node
import { promises as fs } from 'node:fs'
import path from 'node:path'

const artifactDir = path.resolve(process.argv[2] ?? 'test-results/m4-b-performance')
const rawDir = path.join(artifactDir, 'raw')

function quantile(values, q) {
  if (!values.length) return null
  const sorted = [...values].sort((a, b) => a - b)
  const index = (sorted.length - 1) * q
  const lo = Math.floor(index)
  const hi = Math.ceil(index)
  return lo === hi ? sorted[lo] : sorted[lo] + (sorted[hi] - sorted[lo]) * (index - lo)
}

function summarize(values) {
  if (!values.length) return { count: 0, min: null, p50: null, p95: null, max: null, mean: null, stddev: null }
  const mean = values.reduce((sum, value) => sum + value, 0) / values.length
  const variance = values.reduce((sum, value) => sum + (value - mean) ** 2, 0) / values.length
  const round = (value) => Number(value.toFixed(3))
  return {
    count: values.length,
    min: round(Math.min(...values)),
    p50: round(quantile(values, 0.5)),
    p95: round(quantile(values, 0.95)),
    max: round(Math.max(...values)),
    mean: round(mean),
    stddev: round(Math.sqrt(variance)),
  }
}

function csvCell(value) {
  if (value === null || value === undefined) return ''
  const text = String(value)
  return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

async function writeCsv(file, rows) {
  const columns = [...new Set(rows.flatMap((row) => Object.keys(row)))]
  const lines = [columns.map(csvCell).join(',')]
  for (const row of rows) lines.push(columns.map((column) => csvCell(row[column])).join(','))
  await fs.writeFile(file, `${lines.join('\n')}\n`, 'utf8')
}

const rounds = await Promise.all(
  [1, 2].map(async (round) => JSON.parse(await fs.readFile(path.join(rawDir, `round-${round}.json`), 'utf8'))),
)
const environments = await Promise.all(
  [
    'c007-round-1-pre.json',
    'c007-round-1-post.json',
    'c007-round-2-pre.json',
    'c007-round-2-post.json',
  ].map(async (name) => JSON.parse(await fs.readFile(path.join(rawDir, name), 'utf8'))),
)
const core = rounds.flatMap((round) => round.coreSamples.filter((sample) => sample.status === 'ok'))
const history = rounds.flatMap((round) => round.historySamples.filter((sample) => sample.status === 'ok'))
const byteWindow = rounds.flatMap((round) => round.byteWindowSamples.filter((sample) => sample.status === 'ok'))

const distributions = {
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
}

const sourceManifestHashes = [...new Set(environments.map((environment) => environment.productionDist.manifestSha256))]
const loadAverages1m = environments.map((environment) => environment.host.loadAverage1m5m15m[0])
const summary = {
  schemaVersion: 1,
  generatedAt: new Date().toISOString(),
  primaryDecisionRule: 'Each independent round must pass on its own; combined n=20 values below are descriptive only.',
  independentRoundSummaries: rounds.map((round) => ({
    round: round.round,
    sampleSuccess: round.sampleSuccess,
    distributions: round.distributions,
    verdict: round.verdict,
    failureCount: round.failures.length,
  })),
  combinedSampleCounts: { core: core.length, history4000: history.length, byteWindow1MiB: byteWindow.length },
  combinedDistributions: distributions,
  evidenceIntegrity: {
    allRoundVerdictsPass: rounds.every((round) => Object.values(round.verdict).every(Boolean)),
    failures: rounds.reduce((sum, round) => sum + round.failures.length, 0),
    trustedOnlineEvents: core.filter((sample) => sample.recoveryOnlineEventTrusted).length,
    firstFrameConfirmedWorkspace: core.filter((sample) => sample.workspaceFirstFrameConfirmed).length,
    firstFrameConfirmedRecovery: core.filter((sample) => sample.recoveryFirstFrameConfirmed).length,
    consoleErrors: [...core, ...history, ...byteWindow].reduce((sum, sample) => sum + sample.consoleErrors.length, 0),
    longTasks: {
      core: core.reduce(
        (sum, sample) =>
          sum + sample.coldLongTasks.length + sample.pairedOpenLongTasks.length + sample.workspaceLongTasks.length + sample.recoveryLongTasks.length,
        0,
      ),
      historyAttach: history.reduce((sum, sample) => sum + sample.attachLongTasks.length, 0),
      historyScroll: history.reduce((sum, sample) => sum + sample.scrollLongTasks.length, 0),
      byteWindowAttach: byteWindow.reduce((sum, sample) => sum + sample.attachLongTasks.length, 0),
    },
    historyTimelineItemCounts: [...new Set(history.map((sample) => sample.timelineItemCount))],
    historyMaxRenderedRows: Math.max(...history.map((sample) => sample.scroll.maxRenderedRows)),
    historyIntervalsOver50Ms: history.reduce((sum, sample) => sum + sample.scroll.intervalsOver50Ms, 0),
    byteWindowRawPayloadBytes: [...new Set(byteWindow.map((sample) => sample.rawPayloadBytes))],
    byteWindowAttachFrameBytes: [...new Set(byteWindow.map((sample) => sample.attachFrameBytes))],
  },
  environmentStability: {
    snapshots: environments.length,
    productionManifestHashes: sourceManifestHashes,
    sameProductionDist: sourceManifestHashes.length === 1,
    loadAverage1m: summarize(loadAverages1m),
    host: `${environments[0].host.cpuModel}/${environments[0].host.logicalCpuCount} logical CPUs/${environments[0].host.totalMemoryBytes} bytes`,
    browser: environments[0].toolchain.chromium,
    node: environments[0].toolchain.node,
  },
  optimizationDecision: {
    productCodeChanged: false,
    reason: 'Both rounds passed AC-01/AC-02 with large budget headroom; 4K/1MiB attach and virtual scrolling showed zero >=50ms long tasks. Scope rule forbids speculative optimization.',
  },
}

const coreRows = core.map((sample) => ({
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
  trustedOnlineEvent: sample.recoveryOnlineEventTrusted,
  firstFrameWorkspace: sample.workspaceFirstFrameConfirmed,
  firstFrameRecovery: sample.recoveryFirstFrameConfirmed,
}))
const historyRows = [
  ...history.map((sample) => ({
    round: sample.round,
    sample: sample.sample,
    fixture: sample.fixture,
    frameCount: sample.frameCount,
    rawPayloadBytes: sample.rawPayloadBytes,
    workspaceOpenToOperableMs: sample.workspaceOpenToOperableMs,
    timelineItemCount: sample.timelineItemCount,
    renderedRowsAtRest: sample.renderedRowsAtRest,
    maxRenderedRows: sample.scroll.maxRenderedRows,
    scrollFps: sample.scroll.fps,
    scrollFrameDeltaP95Ms: sample.scroll.frameDeltaP95Ms,
    intervalsOver50Ms: sample.scroll.intervalsOver50Ms,
    attachLongTasks: sample.attachLongTasks.length,
    scrollLongTasks: sample.scrollLongTasks.length,
  })),
  ...byteWindow.map((sample) => ({
    round: sample.round,
    sample: sample.sample,
    fixture: sample.fixture,
    frameCount: sample.frameCount,
    rawPayloadBytes: sample.rawPayloadBytes,
    workspaceOpenToOperableMs: sample.workspaceOpenToOperableMs,
    timelineItemCount: sample.timelineItemCount,
    attachFrameBytes: sample.attachFrameBytes,
    attachLongTasks: sample.attachLongTasks.length,
  })),
]

await fs.writeFile(path.join(rawDir, 'aggregate-summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8')
await writeCsv(path.join(rawDir, 'aggregate-core.csv'), coreRows)
await writeCsv(path.join(rawDir, 'aggregate-history.csv'), historyRows)
console.log(`wrote aggregate evidence: core=${core.length}, history=${history.length}, byteWindow=${byteWindow.length}`)
