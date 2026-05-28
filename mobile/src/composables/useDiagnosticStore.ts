import { computed, ref } from 'vue'
import { createDiagnosticRecord, type TranscriptDiagnosticInput } from '../utils/transcriptNormalizer'
import type { DiagnosticRecordV2, DiagnosticRefPart, DiagnosticVisibility } from '../types/transcript'

const DEFAULT_MAX_DIAGNOSTICS = 80
const DEFAULT_MERGE_WINDOW_MS = 2000

export interface UseDiagnosticStoreOptions {
  maxDiagnostics?: number
  mergeWindowMs?: number
}

export interface RecordDiagnosticOptions {
  id?: string
  timestamp?: string
}

function nowIso(): string {
  return new Date().toISOString()
}

export function resolveDiagnosticVisibility(input: TranscriptDiagnosticInput): DiagnosticVisibility {
  if ((input.severity ?? 'warning') === 'error') return 'error-card'
  if (input.severity === 'info') return 'hidden-info'
  if (input.reason === 'classifier-overflow') return 'summary-card'
  if (['object-payload', 'control-characters', 'unrecoverable-raw-terminal', 'invalid-part', 'unknown-frame', 'ansi', 'tui'].includes(input.reason)) {
    return 'drawer-only'
  }
  return 'summary-card'
}

export function useDiagnosticStore(options: UseDiagnosticStoreOptions = {}) {
  const maxDiagnostics = options.maxDiagnostics ?? DEFAULT_MAX_DIAGNOSTICS
  const mergeWindowMs = options.mergeWindowMs ?? DEFAULT_MERGE_WINDOW_MS
  const diagnosticsById = ref<Record<string, DiagnosticRecordV2>>({})
  const diagnosticOrder = ref<string[]>([])
  let diagnosticSequence = 0

  const diagnostics = computed(() => diagnosticOrder.value.map((id) => diagnosticsById.value[id]).filter(Boolean))
  const visibleDiagnostics = computed(() => diagnostics.value.filter((record) => record.visibility !== 'hidden-info'))
  const drawerDiagnostics = computed(() => diagnostics.value.filter((record) => record.visibility !== 'hidden-info'))
  const drawerCount = computed(() => drawerDiagnostics.value.reduce((sum, record) => sum + record.count, 0))
  const cardDiagnostics = computed(() => diagnostics.value.filter((record) => record.visibility === 'summary-card' || record.visibility === 'error-card'))

  function findMergeCandidate(input: TranscriptDiagnosticInput, visibility: DiagnosticVisibility, timestamp: string): DiagnosticRecordV2 | undefined {
    const timestampMs = Date.parse(timestamp)
    for (let index = diagnosticOrder.value.length - 1; index >= 0; index -= 1) {
      const record = diagnosticsById.value[diagnosticOrder.value[index]]
      if (!record) continue
      if (record.reason !== input.reason || record.visibility !== visibility) continue
      const updatedMs = Date.parse(record.updatedAt)
      if (Number.isFinite(timestampMs) && Number.isFinite(updatedMs) && timestampMs - updatedMs <= mergeWindowMs) {
        return record
      }
      return undefined
    }
    return undefined
  }

  function trimDiagnostics() {
    if (diagnosticOrder.value.length <= maxDiagnostics) return
    const removeCount = diagnosticOrder.value.length - maxDiagnostics
    const removed = diagnosticOrder.value.slice(0, removeCount)
    diagnosticOrder.value = diagnosticOrder.value.slice(removeCount)
    const nextById = { ...diagnosticsById.value }
    for (const id of removed) delete nextById[id]
    diagnosticsById.value = nextById
  }

  function recordDiagnostic(input: TranscriptDiagnosticInput, recordOptions: RecordDiagnosticOptions = {}): DiagnosticRecordV2 {
    const timestamp = recordOptions.timestamp ?? nowIso()
    const visibility = resolveDiagnosticVisibility(input)
    const candidate = findMergeCandidate(input, visibility, timestamp)
    const id = recordOptions.id ?? `diagnostic-${diagnosticSequence}`
    if (!recordOptions.id) diagnosticSequence += 1

    const base = createDiagnosticRecord(input, id, timestamp)
    if (candidate) {
      const merged: DiagnosticRecordV2 = {
        ...candidate,
        summary: input.summary || candidate.summary,
        preview: base.preview || candidate.preview,
        redacted: candidate.redacted || base.redacted,
        count: candidate.count + 1,
        seq: input.seq ?? candidate.seq,
        firstSeq: candidate.firstSeq ?? candidate.seq ?? input.seq,
        lastSeq: input.seq ?? candidate.lastSeq ?? candidate.seq,
        updatedAt: timestamp,
      }
      diagnosticsById.value = { ...diagnosticsById.value, [candidate.id]: merged }
      return merged
    }

    const record: DiagnosticRecordV2 = {
      ...base,
      visibility,
      count: 1,
      firstSeq: input.seq,
      lastSeq: input.seq,
      updatedAt: timestamp,
    }
    diagnosticsById.value = { ...diagnosticsById.value, [record.id]: record }
    diagnosticOrder.value = [...diagnosticOrder.value, record.id]
    trimDiagnostics()
    return record
  }

  function toDiagnosticRef(record: DiagnosticRecordV2): DiagnosticRefPart {
    return {
      id: `${record.id}-ref`,
      type: 'diagnostic-ref',
      reason: record.reason,
      summary: record.summary,
      preview: record.preview,
      redacted: record.redacted,
      count: record.count,
      visibility: record.visibility,
      createdAt: record.createdAt,
      updatedAt: record.updatedAt,
    }
  }

  function reset() {
    diagnosticsById.value = {}
    diagnosticOrder.value = []
    diagnosticSequence = 0
  }

  return {
    diagnostics,
    visibleDiagnostics,
    drawerDiagnostics,
    drawerCount,
    cardDiagnostics,
    diagnosticsById,
    diagnosticOrder,
    recordDiagnostic,
    toDiagnosticRef,
    reset,
  }
}
