import type { TerminalCodeBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

function parseFenceInfo(fenceLine: string) {
  const meta = fenceLine.trim().slice(3).trim()
  if (!meta) {
    return { language: undefined, filename: undefined }
  }

  const parts = meta.split(/\s+/).filter(Boolean)
  const [language, ...rest] = parts
  const filename = rest.length > 0 ? rest.join(' ') : undefined
  return { language: language || undefined, filename }
}

export function isFencedCodeBlock(lines: string[]): boolean {
  if (lines.length < 2) return false
  if (!lines[0].trim().startsWith('```')) return false
  return lines.slice(1).some((line) => line.trim() === '```')
}

export function buildFencedCodeBlock(context: TerminalMatcherContext): TerminalCodeBlock {
  const closingIndex = context.lines.findIndex((line, index) => index > 0 && line.trim() === '```')
  const codeLines = closingIndex >= 0
    ? context.lines.slice(1, closingIndex)
    : context.lines.slice(1)

  const { language, filename } = parseFenceInfo(context.lines[0])

  return {
    id: `code-${context.createdAt}`,
    type: 'code',
    appType: context.appType,
    raw: context.raw,
    code: codeLines.join('\n'),
    language,
    filename,
    createdAt: context.createdAt,
  }
}

export const fencedCodeMatcher: TerminalBlockMatcher = {
  name: 'fenced-code',
  match: (context) => isFencedCodeBlock(context.lines),
  build: (context) => buildFencedCodeBlock(context),
}
