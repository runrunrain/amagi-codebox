import type { AppType } from './terminal'

export type TranscriptRole = 'user' | 'assistant' | 'system'
export type TranscriptStatus = 'streaming' | 'completed' | 'error'
export type ToolPartState = 'pending' | 'running' | 'completed' | 'error'
export type RawTerminalReason = 'fallback' | 'unsupported-pattern' | 'parser-error' | 'ansi' | 'tui' | 'classifier-overflow'
export type DiagnosticReason = RawTerminalReason | 'schema-invalid' | 'unknown-frame' | 'decode-error' | 'control-characters' | 'object-payload' | 'invalid-part' | 'unrecoverable-raw-terminal' | 'orphan-delta' | 'history-truncated'
export type DiagnosticVisibility = 'hidden-info' | 'drawer-only' | 'summary-card' | 'error-card'

export interface TranscriptTurn {
  id: string
  sessionId: string
  role: TranscriptRole
  appType: AppType
  parts: TranscriptPart[]
  status: TranscriptStatus
  createdAt: string
  updatedAt: string
}

export type TranscriptPart =
  | TextPart
  | MarkdownPart
  | ReasoningPart
  | ToolPart
  | DiffPart
  | FilePart
  | StepPart
  | ErrorPart
  | RawTerminalPart
  | DiagnosticRefPart

export interface TranscriptPartBase {
  id: string
  type: string
  createdAt: string
  updatedAt?: string
}

export interface TextPart extends TranscriptPartBase {
  type: 'text'
  text: string
}

export interface MarkdownPart extends TranscriptPartBase {
  type: 'markdown'
  markdown: string
  streaming?: boolean
}

export interface ReasoningPart extends TranscriptPartBase {
  type: 'reasoning'
  text: string
  collapsed?: boolean
}

export interface ToolPart extends TranscriptPartBase {
  type: 'tool'
  name: string
  state: ToolPartState
  title: string
  inputPreview?: string
  outputPreview?: string
  metadata?: Record<string, unknown>
}

export interface DiffPart extends TranscriptPartBase {
  type: 'diff'
  filename?: string
  diff: string
  additions: number
  deletions: number
}

export interface FilePart extends TranscriptPartBase {
  type: 'file'
  path: string
  action?: string
}

export interface StepPart extends TranscriptPartBase {
  type: 'step'
  title: string
  status: TranscriptStatus
}

export interface ErrorPart extends TranscriptPartBase {
  type: 'error'
  message: string
  detail?: string
}

export interface RawTerminalPart extends TranscriptPartBase {
  type: 'raw-terminal'
  text: string
  ansi?: string
  reason: RawTerminalReason
}

export interface DiagnosticRefPart extends TranscriptPartBase {
  type: 'diagnostic-ref'
  reason: DiagnosticReason
  summary: string
  preview?: string
  redacted: boolean
  count?: number
  visibility?: DiagnosticVisibility
}

export type KeyedTranscriptTurn = Omit<TranscriptTurn, 'parts'> & {
  parts?: TranscriptPart[]
}

export interface DiagnosticRecordV2 {
  id: string
  reason: DiagnosticReason
  severity: 'info' | 'warning' | 'error'
  visibility: DiagnosticVisibility
  summary: string
  preview: string
  redacted: boolean
  count: number
  seq?: number
  firstSeq?: number
  lastSeq?: number
  createdAt: string
  updatedAt: string
}

export interface EventSourceRef {
  kind: 'legacy-output' | 'legacy-structured-part' | 'session-v2' | 'provider' | 'internal'
  seq?: number
  provider?: AppType | string
  meta?: Record<string, unknown>
}

export interface TransientStatus {
  label: string
  kind?: 'idle' | 'running' | 'thinking' | 'error'
  updatedAt?: string
}

export type NormalizedTranscriptEvent =
  | { type: 'turn.created'; sessionId: string; turnId: string; source: EventSourceRef }
  | { type: 'part.created'; sessionId: string; turnId: string; part: TranscriptPart; source: EventSourceRef }
  | { type: 'part.delta'; sessionId: string; turnId: string; partId: string; field: 'text' | 'markdown'; delta: string; commitHint: 'partial' | 'line' | 'final'; source: EventSourceRef }
  | { type: 'part.completed'; sessionId: string; turnId: string; partId: string; source: EventSourceRef }
  | { type: 'status.updated'; sessionId: string; status: TransientStatus; source: EventSourceRef }
  | { type: 'diagnostic.recorded'; sessionId: string; diagnostic: DiagnosticRecordV2; source: EventSourceRef }

export interface ProviderEventAdapter<TInput = unknown> {
  readonly name: string
  normalize(input: TInput): NormalizedTranscriptEvent[]
}
