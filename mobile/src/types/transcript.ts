import type { AppType } from './terminal'

export type TranscriptRole = 'user' | 'assistant' | 'system'
export type TranscriptStatus = 'streaming' | 'completed' | 'error'
export type ToolPartState = 'pending' | 'running' | 'completed' | 'error'
export type RawTerminalReason = 'fallback' | 'unsupported-pattern' | 'parser-error' | 'ansi' | 'tui' | 'classifier-overflow'

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
