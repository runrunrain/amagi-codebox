import { computed, ref } from 'vue'
import { stripTuiChars } from '../utils/stripTuiChars'

export interface UseRawTerminalSinkOptions {
  maxChars?: number
  maxLines?: number
  flushIntervalMs?: number
}

const DEFAULT_MAX_CHARS = 160_000
const DEFAULT_MAX_LINES = 4000
const DEFAULT_FLUSH_INTERVAL_MS = 32

function boundByLines(value: string, maxLines: number): string {
  const lines = value.split('\n')
  if (lines.length <= maxLines) return value
  return lines.slice(lines.length - maxLines).join('\n')
}

function normalizeLineBuffer(value: string): string {
  const lines: string[] = []
  for (const chunk of value.replace(/\r\n/g, '\n').split('\n')) {
    const carriageSegments = chunk.split('\r')
    const lastSegment = carriageSegments[carriageSegments.length - 1]
    lines.push(lastSegment.replace(/\u001B\[2K/g, '').replace(/\u001B\[[ABCD]/g, ''))
  }
  return stripTuiChars(lines.join('\n'))
}

export function useRawTerminalSink(options: UseRawTerminalSinkOptions = {}) {
  const maxChars = options.maxChars ?? DEFAULT_MAX_CHARS
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES
  const flushIntervalMs = options.flushIntervalMs ?? DEFAULT_FLUSH_INTERVAL_MS
  const rawText = ref('')
  const pendingChunks: string[] = []
  const writeCount = ref(0)
  const flushCount = ref(0)
  let flushTimer: ReturnType<typeof setTimeout> | null = null
  let xtermWriter: ((chunk: string) => void) | null = null

  const displayText = computed(() => rawText.value || '')

  function commit(merged: string) {
    if (!merged) return
    xtermWriter?.(merged)
    writeCount.value += 1
    const next = normalizeLineBuffer(rawText.value + merged)
    const boundedByChars = next.length > maxChars ? next.slice(next.length - maxChars) : next
    rawText.value = boundByLines(boundedByChars, maxLines)
  }

  function flushNow() {
    if (flushTimer) {
      clearTimeout(flushTimer)
      flushTimer = null
    }
    const merged = pendingChunks.splice(0).join('')
    if (!merged) return
    flushCount.value += 1
    commit(merged)
  }

  function scheduleFlush() {
    if (flushTimer) return
    flushTimer = setTimeout(flushNow, flushIntervalMs)
  }

  function write(chunk: string) {
    if (!chunk) return
    pendingChunks.push(chunk)
    scheduleFlush()
  }

  function replace(value: string) {
    pendingChunks.splice(0)
    if (flushTimer) {
      clearTimeout(flushTimer)
      flushTimer = null
    }
    rawText.value = boundByLines(normalizeLineBuffer(value).slice(-maxChars), maxLines)
  }

  function attachXtermWriter(writer: ((chunk: string) => void) | null) {
    xtermWriter = writer
  }

  function reset() {
    pendingChunks.splice(0)
    if (flushTimer) {
      clearTimeout(flushTimer)
      flushTimer = null
    }
    rawText.value = ''
    writeCount.value = 0
    flushCount.value = 0
  }

  function dispose() {
    flushNow()
    xtermWriter = null
  }

  return {
    rawText,
    displayText,
    writeCount,
    flushCount,
    write,
    replace,
    flushNow,
    attachXtermWriter,
    reset,
    dispose,
  }
}

export type RawTerminalSink = ReturnType<typeof useRawTerminalSink>
