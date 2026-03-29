import type { AppType } from './terminal'

export const TERMINAL_BLOCK_TYPES = [
  'summary',
  'text',
  'prompt',
  'action',
  'markdown',
  'code',
  'tool',
  'diff',
  'todo',
  'table',
  'thinking',
  'status',
  'streaming',
  'raw-terminal',
] as const

export type TerminalBlockType = typeof TERMINAL_BLOCK_TYPES[number]

export interface TerminalBlockBase {
  id: string
  type: TerminalBlockType
  appType: AppType
  raw: string
  createdAt: number
}

export interface TerminalTextBlock extends TerminalBlockBase {
  type: 'text' | 'prompt' | 'action' | 'markdown' | 'thinking' | 'status' | 'streaming'
  content: string
  shortcutHint?: string
  primaryAction?: string
}

export interface TerminalSummaryBlock extends TerminalBlockBase {
  type: 'summary'
  title: string
  version?: string
  subtitle?: string
  workDir?: string
  effort?: string
}

export interface TerminalCodeBlock extends TerminalBlockBase {
  type: 'code'
  code: string
  language?: string
  filename?: string
}

export interface TerminalToolBlock extends TerminalBlockBase {
  type: 'tool'
  toolName: string
  title: string
  summary?: string
  shortcutHint?: string
}

export interface TerminalDiffBlock extends TerminalBlockBase {
  type: 'diff'
  filename: string
  additions: number
  deletions: number
  diff: string
}

export interface TodoItem {
  text: string
  completed: boolean
}

export interface TerminalTodoBlock extends TerminalBlockBase {
  type: 'todo'
  items: TodoItem[]
  content: string
}

export interface TerminalTableBlock extends TerminalBlockBase {
  type: 'table'
  headers: string[]
  rows: string[][]
  content: string
}

export interface TerminalRawBlock extends TerminalBlockBase {
  type: 'raw-terminal'
  lines: string[]
}

export type TerminalBlock =
  | TerminalSummaryBlock
  | TerminalTextBlock
  | TerminalCodeBlock
  | TerminalToolBlock
  | TerminalDiffBlock
  | TerminalTodoBlock
  | TerminalTableBlock
  | TerminalRawBlock

const TERMINAL_BLOCK_TYPE_SET = new Set<string>(TERMINAL_BLOCK_TYPES)

export function isTerminalBlockType(value: string): value is TerminalBlockType {
  return TERMINAL_BLOCK_TYPE_SET.has(value)
}

export function isCollapsibleTerminalBlockType(type: TerminalBlockType): boolean {
  return type === 'code' || type === 'tool' || type === 'diff' || type === 'table' || type === 'thinking' || type === 'raw-terminal'
}

export function prefersMarkdownRendering(type: TerminalBlockType): boolean {
  return type === 'markdown' || type === 'thinking' || type === 'status' || type === 'streaming'
}
