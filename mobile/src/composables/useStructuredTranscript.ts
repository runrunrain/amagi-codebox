import { computed, ref, type Ref } from 'vue'
import { looksLikeMarkdown } from '../utils/renderMarkdown'
import { normalizeTranscriptChunk, resetTranscriptNormalizerState, type TranscriptDiagnosticInput, type TranscriptDiagnosticRecord } from '../utils/transcriptNormalizer'
import { useDiagnosticStore } from './useDiagnosticStore'
import type { AppType } from '../types/terminal'
import type { DiagnosticRefPart, KeyedTranscriptTurn, TranscriptPart, TranscriptTurn } from '../types/transcript'
import type { StructuredPartFramePayload } from '../api/websocket'

interface UseStructuredTranscriptOptions {
  sessionId: string
  appType: Ref<AppType>
  maxRawChars?: number
  maxParts?: number
  maxLines?: number
}

interface AppendRawChunkMetadata {
  source?: 'websocket' | 'snapshot' | 'snapshot-reset' | 'fallback'
}

interface AppendStructuredPartMetadata {
  rawChunk?: string
}

export interface StructuredTranscriptDebugStats {
  appendCalls: number
  classifiedSegments: number
  snapshotResets: number
  lastAppendChars: number
  rawChars: number
  pendingChars: number
  retainedParts: number
  structuredParts: number
  transientStatusUpdates: number
  storePatchCount: number
  turnArrayRebuildCount: number
}

const DEFAULT_MAX_PARTS = 160
const DEFAULT_MAX_RAW_CHARS = 160_000
const DEFAULT_MAX_LINES = 4000
const MAX_SEGMENT_LENGTH = 8000
const SEGMENT_DELIMITER_PATTERN = /\n{2,}/g
const MARKDOWN_FENCE_PATTERN = /^```[\s\S]*```$/i

export function normalizeTranscriptLine(line: string): string {
  return line.replace(/^[^\p{L}\p{N}/\\]+/u, '').replace(/^PS\s+[A-Za-z]:\\[^>]+>\s+/i, '').trim()
}

function nowIso(): string {
  return new Date().toISOString()
}

function countDiffLines(text: string) {
  const lines = text.split('\n')
  return {
    additions: lines.filter((line) => line.startsWith('+') && !line.startsWith('+++')).length,
    deletions: lines.filter((line) => line.startsWith('-') && !line.startsWith('---')).length,
  }
}

function isToolLike(line: string): boolean {
  return /^(tool|call|run|exec|read|write|edit|grep|glob|bash|shell|todo)\b/i.test(line)
    || /^[•*]\s*(Read|Write|Edit|Bash|Grep|Glob|TodoWrite)\b/i.test(line)
}

function isLikelyProtocolObject(text: string): boolean {
  const trimmed = text.trim()
  if (/^\[object\s+/i.test(trimmed)) return true
  if (!((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']')))) {
    return false
  }
  if (MARKDOWN_FENCE_PATTERN.test(trimmed)) return false
  try {
    const parsed = JSON.parse(trimmed)
    if (!parsed || typeof parsed !== 'object') return false
    const keys = Object.keys(parsed as Record<string, unknown>)
    return keys.some((key) => ['type', 'seq', 'part', 'raw', 'source', 'event', 'data'].includes(key)) || keys.length <= 3
  } catch {
    return false
  }
}

function classifySegment(rawSegment: string, index: number, timestamp: string): TranscriptPart {
  const text = rawSegment.trim()
  const id = `part-${index}`

  if (!text) {
    return { id, type: 'raw-terminal', text: rawSegment, reason: 'fallback', createdAt: timestamp }
  }

  if (/^```diff[\s\S]*```$/i.test(text) || /^diff --git\b/m.test(text) || /^@@\s/m.test(text)) {
    const stats = countDiffLines(text)
    const filename = text.match(/(?:^diff --git\s+a\/([^\s]+)|^\+\+\+\s+b\/([^\s]+))/m)
    return {
      id,
      type: 'diff',
      filename: filename?.[1] || filename?.[2],
      diff: text,
      additions: stats.additions,
      deletions: stats.deletions,
      createdAt: timestamp,
    }
  }

  if (isLikelyProtocolObject(text)) {
    return { id, type: 'raw-terminal', text: rawSegment, reason: 'unsupported-pattern', createdAt: timestamp }
  }

  if (/^```/.test(text) || looksLikeMarkdown(text)) {
    return { id, type: 'markdown', markdown: text, createdAt: timestamp }
  }

  const firstLine = normalizeTranscriptLine(text.split('\n')[0] || '')
  if (isToolLike(firstLine)) {
    const lines = text.split('\n').map((line) => normalizeTranscriptLine(line)).filter(Boolean)
    const name = (firstLine.match(/(?:Read|Write|Edit|Bash|Grep|Glob|TodoWrite|tool|call|run|exec|read|write|edit|grep|glob|bash|shell|todo)/i)?.[0] || 'Tool').replace(/^./, (char) => char.toUpperCase())
    return {
      id,
      type: 'tool',
      name,
      state: 'completed',
      title: firstLine,
      outputPreview: lines.slice(1, 6).join('\n'),
      createdAt: timestamp,
    }
  }

  if (/\x1b\[|[╭╮╯╰│]/u.test(rawSegment) || text.length > MAX_SEGMENT_LENGTH) {
    return { id, type: 'raw-terminal', text: rawSegment, reason: 'unsupported-pattern', createdAt: timestamp }
  }

  return { id, type: 'text', text, createdAt: timestamp }
}

function hasCompletedSegmentBoundary(text: string): boolean {
  SEGMENT_DELIMITER_PATTERN.lastIndex = 0
  return SEGMENT_DELIMITER_PATTERN.test(text)
}

function trimRawTextByChars(rawText: string, maxRawChars: number): string {
  if (rawText.length <= maxRawChars) return rawText
  return rawText.slice(rawText.length - maxRawChars)
}

function trimRawTextByLines(rawText: string, maxLines: number): string {
  const lines = rawText.split('\n')
  if (lines.length <= maxLines) return rawText
  return lines.slice(lines.length - maxLines).join('\n')
}

function boundRawText(rawText: string, maxRawChars: number, maxLines: number): string {
  return trimRawTextByLines(trimRawTextByChars(rawText, maxRawChars), maxLines)
}

export function useStructuredTranscript(options: UseStructuredTranscriptOptions) {
  const turnsById = ref<Record<string, KeyedTranscriptTurn>>({})
  const turnOrder = ref<string[]>([])
  const partsById = ref<Record<string, TranscriptPart>>({})
  const partOrderByTurnId = ref<Record<string, string[]>>({})
  const activeTurnId = `${options.sessionId}-raw-turn`
  const turns = computed<TranscriptTurn[]>(() => turnOrder.value.map((turnId) => {
    const turn = turnsById.value[turnId]
    if (!turn) return null
    return {
      ...turn,
      parts: (partOrderByTurnId.value[turnId] ?? []).map((partId) => partsById.value[partId]).filter(Boolean),
    } as TranscriptTurn
  }).filter(Boolean) as TranscriptTurn[])
  const error = ref<string | null>(null)
  const rawText = ref('')
  const diagnosticStore = useDiagnosticStore({ maxDiagnostics: 80 })
  const diagnostics = computed<TranscriptDiagnosticRecord[]>(() => diagnosticStore.diagnostics.value.map((record) => ({
    id: record.id,
    reason: record.reason,
    summary: record.summary,
    severity: record.severity,
    preview: record.preview,
    redacted: record.redacted,
    seq: record.seq,
    createdAt: record.createdAt,
  })))
  const maxRawChars = options.maxRawChars ?? DEFAULT_MAX_RAW_CHARS
  const maxParts = options.maxParts ?? DEFAULT_MAX_PARTS
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES
  let lastSnapshot = ''
  let pendingSegment = ''
  let completedParts: TranscriptPart[] = []
  let partSequence = 0
  let diagnosticSequence = 0
  let turnCreatedAt: string | null = null
  const debugStats = ref<StructuredTranscriptDebugStats>({
    appendCalls: 0,
    classifiedSegments: 0,
    snapshotResets: 0,
    lastAppendChars: 0,
    rawChars: 0,
    pendingChars: 0,
    retainedParts: 0,
    structuredParts: 0,
    transientStatusUpdates: 0,
    storePatchCount: 0,
    turnArrayRebuildCount: 0,
  })

  function patchStoreStats() {
    debugStats.value = { ...debugStats.value, storePatchCount: debugStats.value.storePatchCount + 1 }
  }

  function ensureTurn(timestamp: string) {
    ensureTurnCreatedAt(timestamp)
    if (!turnsById.value[activeTurnId]) {
      turnsById.value = {
        ...turnsById.value,
        [activeTurnId]: {
          id: activeTurnId,
          sessionId: options.sessionId,
          role: 'assistant',
          appType: options.appType.value,
          status: 'streaming',
          createdAt: turnCreatedAt || timestamp,
          updatedAt: timestamp,
        },
      }
      turnOrder.value = [...turnOrder.value, activeTurnId]
      partOrderByTurnId.value = { ...partOrderByTurnId.value, [activeTurnId]: [] }
      patchStoreStats()
      return
    }
    turnsById.value = {
      ...turnsById.value,
      [activeTurnId]: {
        ...turnsById.value[activeTurnId],
        appType: options.appType.value,
        updatedAt: timestamp,
      },
    }
  }

  function trimVisibleParts() {
    const order = partOrderByTurnId.value[activeTurnId] ?? []
    if (order.length <= maxParts) return
    const nextOrder = order.slice(order.length - maxParts)
    const keep = new Set(nextOrder)
    const nextPartsById = { ...partsById.value }
    for (const partId of order) {
      if (!keep.has(partId) && partId !== 'part-current') delete nextPartsById[partId]
    }
    partsById.value = nextPartsById
    partOrderByTurnId.value = { ...partOrderByTurnId.value, [activeTurnId]: nextOrder }
    patchStoreStats()
  }

  function upsertPart(part: TranscriptPart, timestamp: string) {
    ensureTurn(timestamp)
    const order = partOrderByTurnId.value[activeTurnId] ?? []
    const nextOrder = order.includes(part.id) ? order : [...order, part.id]
    partsById.value = { ...partsById.value, [part.id]: part }
    partOrderByTurnId.value = { ...partOrderByTurnId.value, [activeTurnId]: nextOrder }
    turnsById.value = {
      ...turnsById.value,
      [activeTurnId]: { ...turnsById.value[activeTurnId], updatedAt: timestamp, appType: options.appType.value },
    }
    trimVisibleParts()
    patchStoreStats()
  }

  function removePart(partId: string) {
    if (!partsById.value[partId]) return
    const nextPartsById = { ...partsById.value }
    delete nextPartsById[partId]
    partsById.value = nextPartsById
    const order = partOrderByTurnId.value[activeTurnId] ?? []
    partOrderByTurnId.value = { ...partOrderByTurnId.value, [activeTurnId]: order.filter((id) => id !== partId) }
    patchStoreStats()
  }

  function recordDiagnostic(input: TranscriptDiagnosticInput, timestamp = nowIso()): DiagnosticRefPart {
    const id = `diagnostic-${diagnosticSequence}`
    diagnosticSequence += 1
    const record = diagnosticStore.recordDiagnostic(input, { id, timestamp })
    return diagnosticStore.toDiagnosticRef(record)
  }

  function appendDiagnostic(input: TranscriptDiagnosticInput) {
    const timestamp = nowIso()
    ensureTurnCreatedAt(timestamp)
    const diagnosticRef = recordDiagnostic(input, timestamp)
    if (diagnosticRef.visibility !== 'hidden-info') {
      completedParts.push(diagnosticRef)
    }
    partSequence += 1
    trimCompletedParts()
    updateTurn(timestamp)
    refreshDebugStats({ appendCalls: debugStats.value.appendCalls + 1, lastAppendChars: input.text?.length ?? 0 })
  }

  function appendDiagnostics(inputs: TranscriptDiagnosticInput[], timestamp: string) {
    for (const input of inputs) {
      const diagnosticRef = recordDiagnostic(input, timestamp)
      if (diagnosticRef.visibility !== 'hidden-info') {
        completedParts.push(diagnosticRef)
      }
      partSequence += 1
    }
  }

  function appendRawTextToStableBuffer(normalized: string) {
    rawText.value = boundRawText(rawText.value + normalized, maxRawChars, maxLines)
  }

  function ingestNormalizedText(normalized: string, timestamp: string, statsPatch: Partial<StructuredTranscriptDebugStats>) {
    if (!normalized) return
    appendRawTextToStableBuffer(normalized)

    pendingSegment += normalized
    if (pendingSegment.length > maxRawChars) {
      pendingSegment = pendingSegment.slice(pendingSegment.length - maxRawChars)
    }

    if (hasCompletedSegmentBoundary(pendingSegment)) {
      const segments = pendingSegment.split(/\n{2,}/)
      pendingSegment = segments.pop() || ''

      for (const segment of segments) {
        if (!segment.trim()) continue
        completedParts.push(classifySegment(segment, partSequence, timestamp))
        partSequence += 1
        statsPatch.classifiedSegments = (statsPatch.classifiedSegments ?? debugStats.value.classifiedSegments) + 1
      }
      trimCompletedParts()
    }
  }

  function structuredPartText(part: StructuredPartFramePayload): string {
    switch (part.type) {
      case 'text':
        return part.text ?? ''
      case 'markdown':
        return part.markdown ?? ''
      case 'tool':
        return [part.tool?.title, part.tool?.inputPreview, part.tool?.outputPreview].filter(Boolean).join('\n')
      case 'diff':
        return part.diff?.text ?? ''
      case 'raw-terminal':
        return part.raw?.text ?? ''
      default:
        return ''
    }
  }

  function toTranscriptPart(part: StructuredPartFramePayload): TranscriptPart | null {
    const createdAt = part.createdAt || nowIso()
    switch (part.type) {
      case 'text': {
        const text = part.text ?? ''
        return text ? { id: part.id, type: 'text', text, createdAt } : null
      }
      case 'markdown': {
        const markdown = part.markdown ?? ''
        return markdown ? { id: part.id, type: 'markdown', markdown, createdAt } : null
      }
      case 'tool': {
        if (!part.tool?.name) return null
        return {
          id: part.id,
          type: 'tool',
          name: part.tool.name,
          state: part.tool.state,
          title: part.tool.title || part.tool.name,
          inputPreview: part.tool.inputPreview,
          outputPreview: part.tool.outputPreview,
          createdAt,
        }
      }
      case 'diff': {
        const diff = part.diff?.text ?? ''
        if (!diff) return null
        const stats = countDiffLines(diff)
        const filename = diff.match(/(?:^diff --git\s+a\/([^\s]+)|^\+\+\+\s+b\/([^\s]+))/m)
        return {
          id: part.id,
          type: 'diff',
          filename: filename?.[1] || filename?.[2],
          diff,
          additions: stats.additions,
          deletions: stats.deletions,
          createdAt,
        }
      }
      case 'raw-terminal': {
        const text = part.raw?.text ?? ''
        if (!text) return null
        const { cleanText } = normalizeTranscriptChunk(text)
        if (cleanText.trim()) {
          return replacePartId(classifySegment(cleanText, partSequence, createdAt), part.id)
        }
        return null
      }
      default:
        return null
    }
  }

  function refreshDebugStats(patch: Partial<StructuredTranscriptDebugStats> = {}) {
    debugStats.value = {
      ...debugStats.value,
      ...patch,
      rawChars: rawText.value.length,
      pendingChars: pendingSegment.length,
      retainedParts: completedParts.length,
    }
  }

  function ensureTurnCreatedAt(timestamp: string) {
    if (!turnCreatedAt) {
      turnCreatedAt = timestamp
    }
  }

  function replacePartId(part: TranscriptPart, id: string, streaming = false): TranscriptPart {
    if (part.type === 'markdown') {
      return { ...part, id, streaming, updatedAt: streaming ? nowIso() : part.updatedAt }
    }
    return { ...part, id, updatedAt: streaming ? nowIso() : part.updatedAt }
  }

  function trimCompletedParts() {
    if (completedParts.length > maxParts) {
      completedParts = completedParts.slice(completedParts.length - maxParts)
    }
  }

  function syncCompletedPartsToStore(timestamp: string) {
    const currentIds = new Set(completedParts.map((part) => part.id))
    const existingOrder = partOrderByTurnId.value[activeTurnId] ?? []
    for (const partId of existingOrder) {
      if (partId !== 'part-current' && !currentIds.has(partId)) {
        removePart(partId)
      }
    }
    for (const part of completedParts.slice(-maxParts)) {
      upsertPart(part, timestamp)
    }
  }

  function flushPendingSegment(timestamp: string): boolean {
    if (!pendingSegment.trim()) return false

    const part = classifySegment(pendingSegment, partSequence, timestamp)
    completedParts.push(part)
    upsertPart(part, timestamp)
    partSequence += 1
    pendingSegment = ''
    removePart('part-current')
    return true
  }

  function updateTurn(timestamp: string) {
    ensureTurnCreatedAt(timestamp)

    syncCompletedPartsToStore(timestamp)
    const currentText = pendingSegment.trim()
    if (currentText) {
      const currentPart = classifySegment(pendingSegment, partSequence, timestamp)
      upsertPart(replacePartId(currentPart, 'part-current', true), timestamp)
    } else {
      removePart('part-current')
    }
  }

  function appendRawChunk(chunk: string, _metadata: AppendRawChunkMetadata = {}) {
    if (!chunk) return

    const timestamp = nowIso()
    ensureTurnCreatedAt(timestamp)

    try {
      const { cleanText: normalized, diagnostics: chunkDiagnostics } = normalizeTranscriptChunk(chunk)
      appendDiagnostics(chunkDiagnostics, timestamp)
      const statsPatch: Partial<StructuredTranscriptDebugStats> = {
        appendCalls: debugStats.value.appendCalls + 1,
        lastAppendChars: normalized.length,
      }
      if (chunkDiagnostics.some((diagnostic) => diagnostic.severity === 'info') && !normalized) {
        statsPatch.transientStatusUpdates = debugStats.value.transientStatusUpdates + 1
      }
      if (!normalized) {
        trimCompletedParts()
        updateTurn(timestamp)
        refreshDebugStats(statsPatch)
        error.value = null
        return
      }
      ingestNormalizedText(normalized, timestamp, statsPatch)

      updateTurn(timestamp)
      refreshDebugStats(statsPatch)
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Transcript parser failed'
      completedParts.push(recordDiagnostic({
        reason: 'parser-error',
        summary: '会话正文解析失败，异常内容已隔离，未回退为原始终端正文。',
        text: rawText.value || chunk,
        severity: 'error',
      }, timestamp))
      trimCompletedParts()
      updateTurn(timestamp)
    }
  }

  function appendStructuredPart(part: StructuredPartFramePayload, metadata: AppendStructuredPartMetadata = {}) {
    const timestamp = nowIso()
    ensureTurnCreatedAt(timestamp)

    try {
      const transcriptPart = toTranscriptPart(part)
      if (!transcriptPart) {
        const isEmptyRawTerminal = part.type === 'raw-terminal' && !(part.raw?.text ?? '').trim()
        appendDiagnostics([{
          reason: isEmptyRawTerminal ? 'unrecoverable-raw-terminal' : 'invalid-part',
          summary: isEmptyRawTerminal ? '收到不可恢复的原始终端片段，已记录到诊断详情。' : '收到无法渲染的结构化片段，已隔离，未作为会话正文展示。',
          text: JSON.stringify({ id: part.id, type: part.type }),
          seq: part.source?.seqStart,
          severity: 'warning',
        }], timestamp)
        trimCompletedParts()
        updateTurn(timestamp)
        refreshDebugStats({
          appendCalls: debugStats.value.appendCalls + 1,
          lastAppendChars: 0,
        })
        return
      }

      const rawChunk = metadata.rawChunk ?? structuredPartText(part)
      if (rawChunk) {
        const { cleanText: normalized, diagnostics: chunkDiagnostics } = normalizeTranscriptChunk(rawChunk)
        appendDiagnostics(chunkDiagnostics, timestamp)
        appendRawTextToStableBuffer(normalized)
      }

      const flushedPendingSegment = flushPendingSegment(timestamp)
      const existingIndex = completedParts.findIndex((item) => item.id === transcriptPart.id)
      if (existingIndex >= 0) {
        completedParts[existingIndex] = transcriptPart
      } else {
        completedParts.push(transcriptPart)
        partSequence += 1
      }
      upsertPart(transcriptPart, timestamp)
      trimCompletedParts()
      updateTurn(timestamp)
      refreshDebugStats({
        appendCalls: debugStats.value.appendCalls + 1,
        lastAppendChars: rawChunk.length,
        classifiedSegments: flushedPendingSegment ? debugStats.value.classifiedSegments + 1 : debugStats.value.classifiedSegments,
        structuredParts: debugStats.value.structuredParts + 1,
      })
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Structured transcript append failed'
    }
  }

  function reset(rawInitialText = '', snapshotCacheValue = '') {
    const timestamp = nowIso()
    rawText.value = ''
    diagnosticStore.reset()
    resetTranscriptNormalizerState()
    pendingSegment = ''
    completedParts = []
    partSequence = 0
    diagnosticSequence = 0
    turnCreatedAt = null
    lastSnapshot = snapshotCacheValue
    error.value = null
    turnsById.value = {}
    turnOrder.value = []
    partsById.value = {}
    partOrderByTurnId.value = {}
    refreshDebugStats({
      appendCalls: 0,
      classifiedSegments: 0,
      structuredParts: 0,
      transientStatusUpdates: 0,
      storePatchCount: 0,
      turnArrayRebuildCount: 0,
      snapshotResets: rawInitialText ? debugStats.value.snapshotResets + 1 : 0,
      lastAppendChars: 0,
    })

    if (rawInitialText) {
      appendRawChunk(rawInitialText, { source: 'snapshot-reset' })
    } else {
      turnCreatedAt = timestamp
    }
  }

  function ingestRawSnapshot(rawText: string) {
    if (rawText === lastSnapshot) return
    if (rawText.startsWith(lastSnapshot)) {
      const delta = rawText.slice(lastSnapshot.length)
      lastSnapshot = rawText
      appendRawChunk(delta, { source: 'snapshot' })
      return
    }

    lastSnapshot = rawText
    reset(rawText, rawText)
  }

  function refreshAppType() {
    if (turnOrder.value.length === 0) return
    const timestamp = nowIso()
    const nextTurnsById = { ...turnsById.value }
    for (const turnId of turnOrder.value) {
      nextTurnsById[turnId] = { ...nextTurnsById[turnId], appType: options.appType.value, updatedAt: timestamp }
    }
    turnsById.value = nextTurnsById
    patchStoreStats()
  }

  const partCount = computed(() => turnOrder.value.reduce((sum, turnId) => sum + (partOrderByTurnId.value[turnId]?.length ?? 0), 0))

  return {
    turns,
    turnsById,
    turnOrder,
    partsById,
    partOrderByTurnId,
    error,
    rawText,
    diagnostics,
    diagnosticDrawerCount: diagnosticStore.drawerCount,
    diagnosticDrawerRecords: diagnosticStore.drawerDiagnostics,
    partCount,
    debugStats,
    appendStructuredPart,
    appendRawChunk,
    appendDiagnostic,
    ingestRawSnapshot,
    refreshAppType,
    reset,
    normalizeTranscriptLine,
  }
}
