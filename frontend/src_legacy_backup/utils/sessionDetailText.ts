export interface DetailOutputChunk {
  seq: number
  text: string
  byteLength: number
}

export interface DetailOutputState {
  sessionId: string
  chunks: DetailOutputChunk[]
  historyStatus: 'idle' | 'loading' | 'loaded' | 'unavailable' | 'error'
  decodeError: boolean
  totalBytes: number
  totalChunks: number
  lastSeq: number
}

export interface DiffBlock {
  id: string
  text: string
  startLine: number
  endLine: number
}

export interface ContextSummary {
  lineCount: number
  signalLines: string[]
}

export const DETAIL_OUTPUT_MAX_CHARS = 60000

export function base64ToUint8(base64: string): Uint8Array {
  const bin = atob(base64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = bin.charCodeAt(i)
  }
  return bytes
}

export function decodeHistoryData(data: unknown): Uint8Array | null {
  if (data == null) return null
  if (data instanceof Uint8Array) return data
  if (typeof data === 'string') {
    if (data.length === 0) return new Uint8Array()
    try {
      return base64ToUint8(data)
    } catch {
      return null
    }
  }
  if (Array.isArray(data)) {
    if (data.length === 0) return new Uint8Array()
    try {
      return new Uint8Array(data)
    } catch {
      return null
    }
  }
  return null
}

export function decodeTerminalBytes(bytes: Uint8Array): string {
  try {
    return new TextDecoder().decode(bytes)
  } catch {
    let result = ''
    for (let i = 0; i < bytes.length; i++) result += String.fromCharCode(bytes[i])
    return result
  }
}

export function stripAnsi(text: string): string {
  return text
    .replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, '')
    .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
}

export function createDetailOutputState(sessionId: string): DetailOutputState {
  return {
    sessionId,
    chunks: [],
    historyStatus: 'idle',
    decodeError: false,
    totalBytes: 0,
    totalChunks: 0,
    lastSeq: 0,
  }
}

export function appendDetailOutputBytes(state: DetailOutputState, seq: number, bytes: Uint8Array): DetailOutputState {
  const text = stripAnsi(decodeTerminalBytes(bytes))
  if (!text) return state
  if (seq > 0 && state.chunks.some((chunk) => chunk.seq === seq)) return state

  const chunks = [...state.chunks, { seq, text, byteLength: bytes.length }]
  let totalChars = chunks.reduce((sum, chunk) => sum + chunk.text.length, 0)
  while (totalChars > DETAIL_OUTPUT_MAX_CHARS && chunks.length > 1) {
    const removed = chunks.shift()
    totalChars -= removed?.text.length || 0
  }

  return {
    ...state,
    chunks,
    totalBytes: state.totalBytes + bytes.length,
    totalChunks: state.totalChunks + 1,
    lastSeq: Math.max(state.lastSeq, seq || 0),
  }
}

export function mergeHistorySnapshot(state: DetailOutputState, jsonStr: string | null | undefined): DetailOutputState {
  if (!jsonStr) {
    return {
      ...state,
      historyStatus: 'unavailable',
    }
  }

  try {
    const snapshot = JSON.parse(jsonStr)
    const decoded = decodeHistoryData(snapshot.data)
    if (decoded === null) {
      return {
        ...state,
        historyStatus: 'error',
        decodeError: true,
      }
    }

    const text = stripAnsi(decodeTerminalBytes(decoded))
    const liveChunks = state.chunks.filter((chunk) => !snapshot.seq || chunk.seq > snapshot.seq)
    const historyChunk = text
      ? [{ seq: snapshot.seq || 0, text, byteLength: decoded.length }]
      : []
    return {
      ...state,
      chunks: [...historyChunk, ...liveChunks],
      historyStatus: 'loaded',
      decodeError: false,
      totalBytes: Math.max(state.totalBytes, decoded.length + liveChunks.reduce((sum, chunk) => sum + chunk.byteLength, 0)),
      totalChunks: Math.max(state.totalChunks, historyChunk.length + liveChunks.length),
      lastSeq: Math.max(state.lastSeq, snapshot.seq || 0),
    }
  } catch {
    return {
      ...state,
      historyStatus: 'error',
      decodeError: true,
    }
  }
}

export function buildTranscriptText(state: DetailOutputState): string {
  if (state.chunks.length === 0) return ''
  const joined = state.chunks.map((chunk) => chunk.text).join('')
  return joined.length > DETAIL_OUTPUT_MAX_CHARS
    ? joined.slice(joined.length - DETAIL_OUTPUT_MAX_CHARS)
    : joined
}

export function extractDiffBlocks(text: string): DiffBlock[] {
  if (!text.trim()) return []
  const lines = text.split('\n')
  const blocks: DiffBlock[] = []
  let start = -1
  let buffer: string[] = []
  let hasHeader = false
  let hasHunk = false
  let hasChange = false

  const flush = (endIndex: number) => {
    if (start >= 0 && buffer.length > 0 && (hasHunk || (hasHeader && hasChange))) {
      blocks.push({
        id: `${start + 1}-${endIndex + 1}-${blocks.length}`,
        text: buffer.join('\n'),
        startLine: start + 1,
        endLine: endIndex + 1,
      })
    }
    start = -1
    buffer = []
    hasHeader = false
    hasHunk = false
    hasChange = false
  }

  const isDiffLine = (line: string) =>
    line.startsWith('diff --git ') ||
    line.startsWith('index ') ||
    line.startsWith('--- ') ||
    line.startsWith('+++ ') ||
    line.startsWith('@@') ||
    line.startsWith('+') ||
    line.startsWith('-') ||
    line.startsWith(' ')

  lines.forEach((line, index) => {
    const startsBlock = line.startsWith('diff --git ') || (line.startsWith('--- ') && lines[index + 1]?.startsWith('+++ '))
    if (startsBlock) {
      if (start >= 0) flush(index - 1)
      start = index
    }

    if (start >= 0 && isDiffLine(line)) {
      buffer.push(line)
      if (line.startsWith('diff --git ') || line.startsWith('--- ') || line.startsWith('+++ ')) hasHeader = true
      if (line.startsWith('@@')) hasHunk = true
      if ((line.startsWith('+') && !line.startsWith('+++')) || (line.startsWith('-') && !line.startsWith('---'))) hasChange = true
      return
    }

    if (start >= 0) flush(index - 1)
  })

  if (start >= 0) flush(lines.length - 1)
  return blocks.slice(-8)
}

export function buildContextSummary(text: string): ContextSummary {
  const lines = text.split('\n').map((line) => line.trim()).filter(Boolean)
  const signalPattern = /\b(context|tool|function|mcp|token|tokens|model|provider|workspace|workdir|reading|read file|edit|write|bash|grep|glob)\b/i
  return {
    lineCount: lines.length,
    signalLines: lines.filter((line) => signalPattern.test(line)).slice(-12),
  }
}

export function detailOutputStatusLabel(state: DetailOutputState): string {
  if (state.historyStatus === 'loading') return 'loading history'
  if (state.decodeError || state.historyStatus === 'error') return 'history error'
  if (state.totalChunks > 0 || state.chunks.length > 0) return 'real output'
  if (state.historyStatus === 'unavailable') return 'live only'
  return 'waiting'
}

export function detailOutputStatusClass(state: DetailOutputState): string {
  if (state.decodeError || state.historyStatus === 'error') return 'detail-source-pill--error'
  if (state.totalChunks > 0 || state.chunks.length > 0) return 'detail-source-pill--active'
  return ''
}
