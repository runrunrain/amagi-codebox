import type { TerminalTableBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

const TABLE_SEPARATOR_PATTERN = /^\|[\s:]*-{2,}[\s:]*([|][\s:]*-{2,}[\s:]*)*\|\s*$/

function getNonEmptyLines(lines: string[]): string[] {
  return lines.filter((line) => line.trim() !== '')
}

function hasInnerPipes(line: string): boolean {
  const trimmed = line.trim()
  return trimmed.startsWith('|') && trimmed.endsWith('|') && trimmed.slice(1, -1).includes('|')
}

function parseTableCells(line: string): string[] {
  return line
    .split('|')
    .map((cell) => cell.trim())
    .filter((cell) => cell.length > 0)
}

export function isTableBlock(lines: string[]): boolean {
  const nonEmptyLines = getNonEmptyLines(lines)
  if (nonEmptyLines.length < 2) return false

  return hasInnerPipes(nonEmptyLines[0]) && TABLE_SEPARATOR_PATTERN.test(nonEmptyLines[1].trim())
}

export function buildTableBlock(context: TerminalMatcherContext): TerminalTableBlock {
  const nonEmptyLines = getNonEmptyLines(context.lines)
  const headers = nonEmptyLines.length > 0 ? parseTableCells(nonEmptyLines[0]) : []
  const rows = nonEmptyLines.slice(2).map((line) => parseTableCells(line))

  return {
    id: `table-${context.createdAt}`,
    type: 'table',
    appType: context.appType,
    raw: context.raw,
    headers,
    rows,
    content: context.raw,
    createdAt: context.createdAt,
  }
}

export const tableMatcher: TerminalBlockMatcher = {
  name: 'table',
  match: (context) => isTableBlock(context.lines),
  build: (context) => buildTableBlock(context),
}
