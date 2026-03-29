import type { TerminalDiffBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

function parseFilename(lines: string[]): string {
  const plusPlusLine = lines.find((line) => line.startsWith('+++ '))
  const minusMinusLine = lines.find((line) => line.startsWith('--- '))
  const candidate = plusPlusLine ?? minusMinusLine
  if (!candidate) return 'patch.diff'

  return candidate
    .replace(/^[+-]{3}\s+/, '')
    .replace(/^a\//, '')
    .replace(/^b\//, '')
    .trim() || 'patch.diff'
}

function countChanges(lines: string[]) {
  let additions = 0
  let deletions = 0

  for (const line of lines) {
    if (line.startsWith('+++ ') || line.startsWith('--- ')) continue
    if (line.startsWith('+')) additions += 1
    if (line.startsWith('-')) deletions += 1
  }

  return { additions, deletions }
}

export function isDiffBlock(lines: string[]): boolean {
  if (lines.length < 3) return false
  const hasFileHeaders = lines.some((line) => line.startsWith('--- ')) && lines.some((line) => line.startsWith('+++ '))
  const hasHunkHeader = lines.some((line) => line.startsWith('@@'))
  return hasFileHeaders || hasHunkHeader
}

export function buildDiffBlock(context: TerminalMatcherContext): TerminalDiffBlock {
  const filename = parseFilename(context.lines)
  const { additions, deletions } = countChanges(context.lines)

  return {
    id: `diff-${context.createdAt}`,
    type: 'diff',
    appType: context.appType,
    raw: context.raw,
    filename,
    additions,
    deletions,
    diff: context.raw,
    createdAt: context.createdAt,
  }
}

export const diffMatcher: TerminalBlockMatcher = {
  name: 'diff',
  match: (context) => isDiffBlock(context.lines),
  build: (context) => buildDiffBlock(context),
}
