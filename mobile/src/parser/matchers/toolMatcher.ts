import type { TerminalToolBlock } from '../../types/terminal-blocks'
import { stripAnsi } from '../../utils/stripAnsi'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

const TOOL_NAME_PATTERN = /^(Bash|Read|Write|Edit|Grep|Glob|Task|Search|Open|Patch)\b/i
const SHORTCUT_PATTERN = /\((?:ctrl|cmd|shift|alt)[^)]+\)/i

function cleanToolLine(line: string): string {
  return stripAnsi(line).replace(/^●\s*/, '').trim()
}

function cleanToolSummaryLine(line: string): string {
  return cleanToolLine(line)
    .replace(/^⎿\s*/, '')
    .replace(/^[│└├┌─\s]+/, '')
    .trim()
}

export function normalizeToolSummaryLines(lines: string[]): string[] {
  const normalized = lines
    .map(cleanToolSummaryLine)
    .filter(Boolean)

  const deduped: string[] = []
  for (const line of normalized) {
    if (deduped[deduped.length - 1] === line) continue
    deduped.push(line)
  }

  return deduped
}

export function isToolBlock(lines: string[]): boolean {
  if (lines.length === 0) return false
  const firstLine = cleanToolLine(lines[0])
  return TOOL_NAME_PATTERN.test(firstLine)
}

export function buildToolBlock(context: TerminalMatcherContext): TerminalToolBlock {
  const cleanedLines = context.lines.map(cleanToolLine).filter(Boolean)
  const firstLine = cleanedLines[0] ?? 'Tool'
  const toolName = firstLine.match(TOOL_NAME_PATTERN)?.[1] ?? 'Tool'
  const shortcutHint = firstLine.match(SHORTCUT_PATTERN)?.[0]
  const summaryLines = normalizeToolSummaryLines(context.lines.slice(1))

  return {
    id: `tool-${context.createdAt}`,
    type: 'tool',
    appType: context.appType,
    raw: context.raw,
    toolName,
    title: firstLine,
    summary: summaryLines.length > 0 ? summaryLines.join('\n') : undefined,
    shortcutHint,
    createdAt: context.createdAt,
  }
}

export const toolMatcher: TerminalBlockMatcher = {
  name: 'tool',
  match: (context) => isToolBlock(context.lines),
  build: (context) => buildToolBlock(context),
}
