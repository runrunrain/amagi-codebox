export type MarkdownHoldback = 'none' | 'table' | 'fence'

export interface MarkdownStreamCollectorState {
  partId: string
  sourceBuffer: string
  committedSource: string
  liveTail: string
  holdback: MarkdownHoldback
  fenceLanguage?: string
  lastCommitAt: number
  finalized: boolean
}

export interface MarkdownCommitResult {
  committedSource: string
  committedDelta: string
  liveTail: string
  holdback: MarkdownHoldback
  finalized: boolean
  shouldRender: boolean
}

export interface MarkdownStreamCollectorOptions {
  maxBufferChars?: number
  tableHoldbackMs?: number
}

const DEFAULT_MAX_BUFFER_CHARS = 120_000
const DEFAULT_TABLE_HOLDBACK_MS = 750

function detectHoldback(text: string, tableHoldbackExpired: boolean): MarkdownHoldback {
  const fenceMatches = text.match(/^```/gm) ?? []
  if (fenceMatches.length % 2 === 1) return 'fence'

  const lines = text.split('\n')
  const lastNonEmpty = [...lines].reverse().find((line) => line.trim()) ?? ''
  const previousNonEmpty = [...lines].reverse().slice(1).find((line) => line.trim()) ?? ''
  const looksLikeTableHeader = /^\s*\|.+\|\s*$/.test(lastNonEmpty) && !/^\s*\|?\s*:?-{3,}:?/.test(lastNonEmpty)
  const previousLooksLikeTableHeader = /^\s*\|.+\|\s*$/.test(previousNonEmpty)
  const separatorArrived = /^\s*\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(lastNonEmpty)
  if (!tableHoldbackExpired && (looksLikeTableHeader || (previousLooksLikeTableHeader && !separatorArrived))) return 'table'

  return 'none'
}

export function createMarkdownStreamCollector(partId: string, options: MarkdownStreamCollectorOptions = {}) {
  const maxBufferChars = options.maxBufferChars ?? DEFAULT_MAX_BUFFER_CHARS
  const tableHoldbackMs = options.tableHoldbackMs ?? DEFAULT_TABLE_HOLDBACK_MS
  const state: MarkdownStreamCollectorState = {
    partId,
    sourceBuffer: '',
    committedSource: '',
    liveTail: '',
    holdback: 'none',
    lastCommitAt: 0,
    finalized: false,
  }

  function commit(nextCommittedSource: string, now: number, finalized = false): MarkdownCommitResult {
    const boundedCommitted = nextCommittedSource.length > maxBufferChars
      ? nextCommittedSource.slice(nextCommittedSource.length - maxBufferChars)
      : nextCommittedSource
    const committedDelta = boundedCommitted.slice(state.committedSource.length)
    state.committedSource = boundedCommitted
    state.liveTail = state.sourceBuffer.slice(state.committedSource.length)
    state.lastCommitAt = now
    state.finalized = finalized
    return {
      committedSource: state.committedSource,
      committedDelta,
      liveTail: state.liveTail,
      holdback: state.holdback,
      finalized: state.finalized,
      shouldRender: Boolean(committedDelta) || finalized,
    }
  }

  function append(delta: string, commitHint: 'partial' | 'line' | 'final' = 'partial', now = Date.now()): MarkdownCommitResult {
    if (state.finalized) {
      state.finalized = false
    }
    state.sourceBuffer += delta
    if (state.sourceBuffer.length > maxBufferChars) {
      state.sourceBuffer = state.sourceBuffer.slice(state.sourceBuffer.length - maxBufferChars)
      state.committedSource = state.committedSource.slice(Math.max(0, state.committedSource.length - state.sourceBuffer.length))
    }

    const tableExpired = state.holdback === 'table' && now - state.lastCommitAt >= tableHoldbackMs
    state.holdback = detectHoldback(state.sourceBuffer, tableExpired)
    if (commitHint === 'final') {
      state.holdback = 'none'
      return commit(state.sourceBuffer, now, true)
    }

    if (state.holdback !== 'none') {
      state.liveTail = state.sourceBuffer.slice(state.committedSource.length)
      return { committedSource: state.committedSource, committedDelta: '', liveTail: state.liveTail, holdback: state.holdback, finalized: false, shouldRender: false }
    }

    const lastNewline = state.sourceBuffer.lastIndexOf('\n')
    const shouldCommitLine = commitHint === 'line' || lastNewline >= 0
    if (!shouldCommitLine) {
      state.liveTail = state.sourceBuffer.slice(state.committedSource.length)
      return { committedSource: state.committedSource, committedDelta: '', liveTail: state.liveTail, holdback: state.holdback, finalized: false, shouldRender: false }
    }

    return commit(state.sourceBuffer.slice(0, lastNewline + 1), now)
  }

  function finalize(now = Date.now()): MarkdownCommitResult {
    state.holdback = 'none'
    return commit(state.sourceBuffer, now, true)
  }

  function reset(markdown = '') {
    state.sourceBuffer = markdown
    state.committedSource = markdown
    state.liveTail = ''
    state.holdback = 'none'
    state.lastCommitAt = Date.now()
    state.finalized = Boolean(markdown)
  }

  return { state, append, finalize, reset }
}

export function collectMarkdownForRender(previous: string, next: string): MarkdownCommitResult {
  const collector = createMarkdownStreamCollector('renderer')
  collector.reset(previous)
  const delta = next.startsWith(previous) ? next.slice(previous.length) : next
  return collector.append(delta, delta.includes('\n') ? 'line' : 'partial')
}
