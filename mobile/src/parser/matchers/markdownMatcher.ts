import type { AppType } from '../../types/terminal'
import type { TerminalTextBlock } from '../../types/terminal-blocks'
import { looksLikeMarkdown } from '../../utils/renderMarkdown'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

function isNarrativeAiBlock(lines: string[], appType: AppType): boolean {
  if (appType !== 'claudecode' && appType !== 'opencode') return false

  const cleaned = lines.map((line) => line.trim()).filter(Boolean)
  if (cleaned.length < 2) return false

  if (cleaned.some((line) => /^(>|PS\s|●|⎿|⏵|Bash\b|Read\b|Write\b|Edit\b|Task\b|Search\b)/.test(line))) {
    return false
  }

  const joined = cleaned.join('\n')
  const hasSentenceSignals = /[。！？.!?：:]/.test(joined)
  const longEnough = joined.length >= 40
  return hasSentenceSignals || longEnough
}

export function isMarkdownBlock(lines: string[], appType: AppType): boolean {
  if (lines.length < 2) return false

  const joined = lines.join('\n')
  if (looksLikeMarkdown(joined)) {
    return true
  }

  return isNarrativeAiBlock(lines, appType)
}

export function buildMarkdownBlock(context: TerminalMatcherContext): TerminalTextBlock {
  return {
    id: `markdown-${context.createdAt}`,
    type: 'markdown',
    appType: context.appType,
    raw: context.raw,
    content: context.raw,
    createdAt: context.createdAt,
  }
}

export const markdownMatcher: TerminalBlockMatcher = {
  name: 'markdown',
  match: (context) => isMarkdownBlock(context.lines, context.appType),
  build: (context) => buildMarkdownBlock(context),
}
