import { normalizeTranscriptChunk, type TranscriptDiagnosticInput } from '../utils/transcriptNormalizer'
import type { StructuredPartFramePayload } from '../api/websocket'
import type { AppType } from '../types/terminal'
import type { EventSourceRef, NormalizedTranscriptEvent, ProviderEventAdapter, TranscriptPart } from '../types/transcript'

export interface LegacyOutputFrameLike {
  sessionId: string
  seq?: number
  text: string
  appType?: AppType
}

function nowIso(): string {
  return new Date().toISOString()
}

function diagnosticEvent(sessionId: string, diagnostic: TranscriptDiagnosticInput, source: EventSourceRef): NormalizedTranscriptEvent {
  return {
    type: 'diagnostic.recorded',
    sessionId,
    diagnostic: {
      id: `adapter-diagnostic-${source.seq ?? Date.now()}`,
      reason: diagnostic.reason,
      severity: diagnostic.severity ?? 'warning',
      visibility: diagnostic.severity === 'error' ? 'error-card' : diagnostic.severity === 'info' ? 'hidden-info' : 'drawer-only',
      summary: diagnostic.summary,
      preview: diagnostic.text ?? '',
      redacted: false,
      count: 1,
      seq: diagnostic.seq,
      firstSeq: diagnostic.seq,
      lastSeq: diagnostic.seq,
      createdAt: nowIso(),
      updatedAt: nowIso(),
    },
    source,
  }
}

export class LegacyOutputAdapter implements ProviderEventAdapter<LegacyOutputFrameLike> {
  readonly name = 'legacy-output'

  normalize(input: LegacyOutputFrameLike): NormalizedTranscriptEvent[] {
    const source: EventSourceRef = { kind: 'legacy-output', seq: input.seq, provider: input.appType }
    const turnId = `${input.sessionId}-raw-turn`
    const partId = `${input.sessionId}-${input.seq ?? 'local'}-legacy-output`
    const normalized = normalizeTranscriptChunk(input.text)
    const events: NormalizedTranscriptEvent[] = [
      { type: 'turn.created', sessionId: input.sessionId, turnId, source },
      ...normalized.diagnostics.map((diagnostic) => diagnosticEvent(input.sessionId, diagnostic, source)),
    ]
    if (normalized.cleanText) {
      events.push({
        type: 'part.delta',
        sessionId: input.sessionId,
        turnId,
        partId,
        field: 'text',
        delta: normalized.cleanText,
        commitHint: normalized.cleanText.includes('\n') ? 'line' : 'partial',
        source,
      })
    }
    return events
  }
}

export class LegacyStructuredPartAdapter implements ProviderEventAdapter<{ sessionId: string; part?: StructuredPartFramePayload; seq?: number }> {
  readonly name = 'legacy-structured-part'

  normalize(input: { sessionId: string; part?: StructuredPartFramePayload; seq?: number }): NormalizedTranscriptEvent[] {
    const source: EventSourceRef = { kind: 'legacy-structured-part', seq: input.seq }
    const turnId = `${input.sessionId}-raw-turn`
    if (!input.part || !input.part.id || !input.part.type) {
      return [diagnosticEvent(input.sessionId, {
        reason: 'schema-invalid',
        summary: '结构化片段缺少必要字段，已由 session.v2 adapter skeleton 隔离。',
        text: JSON.stringify(input.part ?? null),
        seq: input.seq,
        severity: 'error',
      }, source)]
    }
    return [{
      type: 'part.created',
      sessionId: input.sessionId,
      turnId,
      part: input.part as unknown as TranscriptPart,
      source,
    }]
  }
}

export interface SessionV2Adapter extends ProviderEventAdapter<unknown> {
  readonly name: 'session-v2'
}

export interface FutureProviderEventAdapter extends ProviderEventAdapter<unknown> {
  readonly provider: AppType | string
}
