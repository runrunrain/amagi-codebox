import { computed, ref, type Ref } from 'vue'
import { looksLikeMarkdown } from '../utils/renderMarkdown'
import type { AppType } from '../types/terminal'
import type { TranscriptPart, TranscriptTurn } from '../types/transcript'
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
}

const DEFAULT_MAX_PARTS = 160
const DEFAULT_MAX_RAW_CHARS = 160_000
const DEFAULT_MAX_LINES = 4000
const MAX_SEGMENT_LENGTH = 8000
const SEGMENT_DELIMITER_PATTERN = /\n{2,}/g

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

function normalizeChunk(chunk: string): string {
  return chunk.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
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
  const turns = ref<TranscriptTurn[]>([])
  const error = ref<string | null>(null)
  const rawText = ref('')
  const maxRawChars = options.maxRawChars ?? DEFAULT_MAX_RAW_CHARS
  const maxParts = options.maxParts ?? DEFAULT_MAX_PARTS
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES
  let lastSnapshot = ''
  let pendingSegment = ''
  let completedParts: TranscriptPart[] = []
  let partSequence = 0
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
  })

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
        return text ? { id: part.id, type: 'raw-terminal', text, reason: part.raw?.reason ?? 'unsupported-pattern', createdAt } : null
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

  function flushPendingSegment(timestamp: string): boolean {
    if (!pendingSegment.trim()) return false

    completedParts.push(classifySegment(pendingSegment, partSequence, timestamp))
    partSequence += 1
    pendingSegment = ''
    return true
  }

  function updateTurn(timestamp: string) {
    ensureTurnCreatedAt(timestamp)

    const currentText = pendingSegment.trim()
    const visibleParts = completedParts.slice(-maxParts)
    if (currentText) {
      const currentPart = classifySegment(pendingSegment, partSequence, timestamp)
      visibleParts.push(replacePartId(currentPart, 'part-current', true))
    }

    turns.value = visibleParts.length > 0 ? [{
      id: `${options.sessionId}-raw-turn`,
      sessionId: options.sessionId,
      role: 'assistant',
      appType: options.appType.value,
      parts: visibleParts.slice(-maxParts),
      status: 'streaming',
      createdAt: turnCreatedAt || timestamp,
      updatedAt: timestamp,
    }] : []
  }

  function appendRawChunk(chunk: string, _metadata: AppendRawChunkMetadata = {}) {
    if (!chunk) return

    const timestamp = nowIso()
    ensureTurnCreatedAt(timestamp)

    try {
      const normalized = normalizeChunk(chunk)
      const statsPatch: Partial<StructuredTranscriptDebugStats> = {
        appendCalls: debugStats.value.appendCalls + 1,
        lastAppendChars: normalized.length,
      }
      rawText.value = boundRawText(rawText.value + normalized, maxRawChars, maxLines)

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

      updateTurn(timestamp)
      refreshDebugStats(statsPatch)
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Transcript parser failed'
      const fallbackText = rawText.value || chunk
      turns.value = [{
        id: `${options.sessionId}-fallback-turn`,
        sessionId: options.sessionId,
        role: 'assistant',
        appType: options.appType.value,
        parts: [{
          id: 'part-parser-error',
          type: 'raw-terminal',
          text: fallbackText,
          reason: 'parser-error',
          createdAt: timestamp,
        }],
        status: 'error',
        createdAt: turnCreatedAt || timestamp,
        updatedAt: timestamp,
      }]
    }
  }

  function appendStructuredPart(part: StructuredPartFramePayload, metadata: AppendStructuredPartMetadata = {}) {
    const timestamp = nowIso()
    ensureTurnCreatedAt(timestamp)

    try {
      const transcriptPart = toTranscriptPart(part)
      if (!transcriptPart) {
        return
      }

      const rawChunk = metadata.rawChunk ?? structuredPartText(part)
      if (rawChunk) {
        const normalized = normalizeChunk(rawChunk)
        rawText.value = boundRawText(rawText.value + normalized, maxRawChars, maxLines)
      }

      const flushedPendingSegment = flushPendingSegment(timestamp)
      completedParts.push(transcriptPart)
      partSequence += 1
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
    pendingSegment = ''
    completedParts = []
    partSequence = 0
    turnCreatedAt = null
    lastSnapshot = snapshotCacheValue
    error.value = null
    turns.value = []
    refreshDebugStats({
      appendCalls: 0,
      classifiedSegments: 0,
      structuredParts: 0,
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
    if (turns.value.length === 0) return
    turns.value = turns.value.map((turn) => ({ ...turn, appType: options.appType.value, updatedAt: nowIso() }))
  }

  const partCount = computed(() => turns.value.reduce((sum, turn) => sum + turn.parts.length, 0))

  return {
    turns,
    error,
    rawText,
    partCount,
    debugStats,
    appendStructuredPart,
    appendRawChunk,
    ingestRawSnapshot,
    refreshAppType,
    reset,
    normalizeTranscriptLine,
  }
}
