import type { TerminalTextBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

function extractPromptAction(raw: string): string | undefined {
  const normalized = raw.trim().replace(/^>\s*/u, '')
  const quoted = normalized.match(/"([^"]+)"/)
  if (quoted?.[1]) return quoted[1]
  const trimmedTry = normalized.replace(/^try\s+/i, '').trim()
  return trimmedTry || undefined
}

export function isPromptBlock(lines: string[]): boolean {
  if (lines.length === 0) return false
  const line = lines[0].trim()
  return line.startsWith('>') && /\bTry\b/i.test(line)
}

export function buildPromptBlock(context: TerminalMatcherContext): TerminalTextBlock {
  return {
    id: `prompt-${context.createdAt}`,
    type: 'prompt',
    appType: context.appType,
    raw: context.raw,
    content: context.raw,
    primaryAction: extractPromptAction(context.raw),
    createdAt: context.createdAt,
  }
}

export const promptMatcher: TerminalBlockMatcher = {
  name: 'prompt',
  match: (context) => isPromptBlock(context.lines),
  build: (context) => buildPromptBlock(context),
}
