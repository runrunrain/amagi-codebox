import type { TerminalTextBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

const STATUS_PATTERNS = [
  'Connected to session.',
  'Disconnected. Reconnecting...',
  'Connection error.',
  'Session exited with code',
] as const

export function isStatusBlock(lines: string[]): boolean {
  if (lines.length !== 1) return false
  const line = lines[0].trim()
  return STATUS_PATTERNS.some((pattern) => line.includes(pattern))
}

export function buildStatusBlock(context: TerminalMatcherContext): TerminalTextBlock {
  return {
    id: `status-${context.createdAt}`,
    type: 'status',
    appType: context.appType,
    raw: context.raw,
    content: context.raw,
    createdAt: context.createdAt,
  }
}

export const statusMatcher: TerminalBlockMatcher = {
  name: 'status',
  match: (context) => isStatusBlock(context.lines),
  build: (context) => buildStatusBlock(context),
}
