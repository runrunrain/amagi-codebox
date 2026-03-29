import type { TerminalTextBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

const ACTION_SHORTCUT_PATTERN = /\(([^)]+)\)\s*$/

export function isActionBlock(lines: string[]): boolean {
  if (lines.length === 0) return false
  return /^⏵+/.test(lines[0].trim())
}

function extractActionParts(raw: string) {
  const trimmed = raw.trim()
  const match = trimmed.match(ACTION_SHORTCUT_PATTERN)
  if (!match) {
    return {
      content: trimmed,
      shortcutHint: undefined,
    }
  }

  const shortcutHint = `(${match[1]})`
  const content = trimmed.slice(0, match.index ?? trimmed.length).trim()
  return {
    content,
    shortcutHint,
  }
}

export function buildActionBlock(context: TerminalMatcherContext): TerminalTextBlock {
  const [firstLine, ...restLines] = context.lines
  const { content, shortcutHint } = extractActionParts(firstLine ?? context.raw)
  const mergedContent = restLines.length > 0 ? `${content}\n${restLines.join('\n')}` : content
  return {
    id: `action-${context.createdAt}`,
    type: 'action',
    appType: context.appType,
    raw: context.raw,
    content: mergedContent,
    shortcutHint,
    createdAt: context.createdAt,
  }
}

export const actionMatcher: TerminalBlockMatcher = {
  name: 'action',
  match: (context) => isActionBlock(context.lines),
  build: (context) => buildActionBlock(context),
}
