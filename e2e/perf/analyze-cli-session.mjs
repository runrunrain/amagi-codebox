#!/usr/bin/env node
import { promises as fs } from 'node:fs'
import path from 'node:path'

const outputFile = process.argv[2]
if (!outputFile) {
  console.error('usage: node e2e/perf/analyze-cli-session.mjs <output.json>')
  process.exit(2)
}

const ONE_MIB = 1 << 20
const FRAME_CAP = 4096
const encoder = new TextEncoder()
const byteLength = (value) => encoder.encode(value).length

function emptyMetric() {
  return { assistantBytes: 0, toolResultBytes: 0, userInputBytes: 0, semanticEvents: 0 }
}

function addMetric(target, source) {
  target.assistantBytes += source.assistantBytes
  target.toolResultBytes += source.toolResultBytes
  target.userInputBytes += source.userInputBytes
  target.semanticEvents += source.semanticEvents
  return target
}

function payloadBytes(value) {
  if (typeof value === 'string') return byteLength(value)
  if (value === null || value === undefined) return 0
  try {
    return byteLength(JSON.stringify(value))
  } catch {
    return 0
  }
}

function classifyContent(content, defaultCategory) {
  const metric = emptyMetric()
  const add = (category, bytes) => {
    if (bytes <= 0) return
    metric[category] += bytes
    metric.semanticEvents += 1
  }

  const visit = (value, category) => {
    if (typeof value === 'string') {
      add(category, byteLength(value))
      return
    }
    if (!value || typeof value !== 'object') return
    if (Array.isArray(value)) {
      for (const item of value) visit(item, category)
      return
    }

    const type = value.type
    if (type === 'image' || type === 'image_url') return
    if (type === 'tool_result') {
      visit(value.content, 'toolResultBytes')
      return
    }
    if (type === 'toolCall' || type === 'tool_use') {
      add('assistantBytes', payloadBytes(value.arguments ?? value.input ?? ''))
      return
    }
    if (type === 'text' && typeof value.text === 'string') {
      add(category, byteLength(value.text))
      return
    }
    if (type === 'thinking' && typeof value.thinking === 'string') {
      add('assistantBytes', byteLength(value.thinking))
      return
    }
    if (typeof value.text === 'string') add(category, byteLength(value.text))
    else if (typeof value.content === 'string' || Array.isArray(value.content)) visit(value.content, category)
  }

  visit(content, defaultCategory)
  return metric
}

function parseTimestamp(value) {
  const timestamp = typeof value === 'string' ? Date.parse(value) : Number(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

async function analyzeFile(file, format, profileID) {
  const input = await fs.readFile(file, 'utf8')
  const records = []
  let malformedLines = 0
  for (const line of input.split('\n')) {
    if (!line.trim()) continue
    let entry
    try {
      entry = JSON.parse(line)
    } catch {
      malformedLines += 1
      continue
    }

    const timestamp = parseTimestamp(entry.timestamp)
    if (timestamp === null) continue
    let metric = emptyMetric()
    if (format === 'pi') {
      if (entry.type !== 'message' || !entry.message) continue
      const role = entry.message.role
      const category = role === 'user' ? 'userInputBytes' : role === 'toolResult' ? 'toolResultBytes' : 'assistantBytes'
      metric = classifyContent(entry.message.content, category)
    } else {
      const role = entry.message?.role ?? entry.type
      const category = role === 'assistant' ? 'assistantBytes' : 'userInputBytes'
      metric = classifyContent(entry.message?.content, category)
    }
    if (metric.semanticEvents > 0) records.push({ timestamp, ...metric })
  }

  const totals = records.reduce((sum, record) => addMetric(sum, record), emptyMetric())
  const outputBytes = totals.assistantBytes + totals.toolResultBytes
  const timestamps = records.map((record) => record.timestamp)
  const first = timestamps.length ? Math.min(...timestamps) : null
  const last = timestamps.length ? Math.max(...timestamps) : null
  const durationSec = first !== null && last !== null ? Math.max(1, (last - first) / 1000) : 0
  const outputBytesPerSec = durationSec > 0 ? outputBytes / durationSec : 0

  const minuteBuckets = new Map()
  for (const record of records) {
    const output = record.assistantBytes + record.toolResultBytes
    if (output === 0) continue
    const bucket = Math.floor(record.timestamp / 60_000)
    minuteBuckets.set(bucket, (minuteBuckets.get(bucket) ?? 0) + output)
  }
  const activeMinuteRates = [...minuteBuckets.values()].map((bytes) => bytes / 60).sort((a, b) => a - b)
  const quantile = (q) => {
    if (!activeMinuteRates.length) return 0
    const index = (activeMinuteRates.length - 1) * q
    const lo = Math.floor(index)
    const hi = Math.ceil(index)
    return lo === hi
      ? activeMinuteRates[lo]
      : activeMinuteRates[lo] + (activeMinuteRates[hi] - activeMinuteRates[lo]) * (index - lo)
  }
  const p50ActiveMinuteBytesPerSec = quantile(0.5)
  const p95ActiveMinuteBytesPerSec = quantile(0.95)
  const round = (value) => Number(value.toFixed(3))
  const coverage = (bytesPerSec) => (bytesPerSec > 0 ? round(ONE_MIB / bytesPerSec) : null)

  return {
    profileID,
    format,
    sourceBytes: byteLength(input),
    malformedLines,
    durationSec: round(durationSec),
    semanticEventCount: totals.semanticEvents,
    assistantBytes: totals.assistantBytes,
    toolResultBytes: totals.toolResultBytes,
    userInputBytes: totals.userInputBytes,
    outputSemanticBytes: outputBytes,
    meanOutputBytesPerSec: round(outputBytesPerSec),
    activeMinuteCount: activeMinuteRates.length,
    p50ActiveMinuteOutputBytesPerSec: round(p50ActiveMinuteBytesPerSec),
    p95ActiveMinuteOutputBytesPerSec: round(p95ActiveMinuteBytesPerSec),
    estimatedOneMiBCoverageSecAtMean: coverage(outputBytesPerSec),
    estimatedOneMiBCoverageSecAtP50ActiveMinute: coverage(p50ActiveMinuteBytesPerSec),
    estimatedOneMiBCoverageSecAtP95ActiveMinute: coverage(p95ActiveMinuteBytesPerSec),
  }
}

async function listClaudeSessions(root) {
  const found = []
  async function walk(dir) {
    let entries
    try {
      entries = await fs.readdir(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const entry of entries) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) await walk(full)
      else if (entry.isFile() && entry.name.endsWith('.jsonl') && !entry.name.startsWith('agent-')) {
        const stat = await fs.stat(full)
        found.push({ file: full, mtimeMs: stat.mtimeMs, size: stat.size })
      }
    }
  }
  await walk(root)
  return found.sort((a, b) => b.mtimeMs - a.mtimeMs)
}

const profiles = []
const piSession = process.env.PI_SESSION_FILE
if (piSession) {
  try {
    profiles.push(await analyzeFile(piSession, 'pi', 'pi-current-01'))
  } catch (error) {
    profiles.push({ profileID: 'pi-current-01', format: 'pi', error: error instanceof Error ? error.message : String(error) })
  }
}

const claudeCandidates = await listClaudeSessions(path.join(process.env.HOME ?? '', '.claude', 'projects'))
let acceptedClaude = 0
for (const candidate of claudeCandidates.slice(0, 30)) {
  const analyzed = await analyzeFile(candidate.file, 'claude-code', `claude-recent-${String(acceptedClaude + 1).padStart(2, '0')}`)
  if (analyzed.durationSec < 60 || analyzed.outputSemanticBytes < 10_000) continue
  profiles.push(analyzed)
  acceptedClaude += 1
  if (acceptedClaude >= 10) break
}

const valid = profiles.filter((profile) => !profile.error && profile.meanOutputBytesPerSec > 0)
const aggregateRate = valid.reduce((sum, profile) => sum + profile.meanOutputBytesPerSec, 0) / Math.max(1, valid.length)
const output = {
  schemaVersion: 1,
  generatedAt: new Date().toISOString(),
  privacy: 'Aggregate byte counts only. No session path, ID, prompt, response, tool payload, or transcript content is persisted.',
  interpretation: {
    byteMetric: 'UTF-8 bytes of semantic assistant/tool-result payloads in local CLI JSONL, excluding image/base64 bodies and terminal ANSI/redraw overhead.',
    limit: 'This is a workload-rate proxy, not captured PTY wire bytes. It can estimate byte-cap coverage; it cannot derive the backend 4096 PTY-frame cap.',
    oneMiBBytes: ONE_MIB,
    backendFrameCap: FRAME_CAP,
  },
  selectedProfileCount: profiles.length,
  aggregateMeanOutputBytesPerSec: Number(aggregateRate.toFixed(3)),
  aggregateEstimatedOneMiBCoverageSec: aggregateRate > 0 ? Number((ONE_MIB / aggregateRate).toFixed(3)) : null,
  profiles,
}

await fs.mkdir(path.dirname(path.resolve(outputFile)), { recursive: true })
await fs.writeFile(outputFile, `${JSON.stringify(output, null, 2)}\n`, 'utf8')
console.log(`wrote ${outputFile}; profiles=${profiles.length}`)
