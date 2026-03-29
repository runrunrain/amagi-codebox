import type { AppType } from '../../types/terminal'
import type { TerminalSummaryBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

const DECORATION_PREFIX = /^[^\p{L}\p{N}\\/]+/u
const VERSION_PATTERN = /\bv\d+(?:\.\d+){0,2}\b/i
const EFFORT_PATTERN = /\b(low|medium|high) effort\b/i

function cleanTerminalLine(line: string): string {
  return line.replace(DECORATION_PREFIX, '').trim()
}

function looksLikeWorkDir(line: string): boolean {
  return /[A-Za-z]:[\\/]/.test(line) || line.startsWith('/') || line.includes(':/')
}

function defaultTitleForAppType(appType: AppType): string {
  switch (appType) {
    case 'claudecode':
      return 'Claude Code'
    case 'opencode':
      return 'OpenCode'
    case 'codex':
      return 'Codex'
    default:
      return 'Terminal'
  }
}

function isHeaderLine(line: string, appType: AppType): boolean {
  const title = defaultTitleForAppType(appType)
  return VERSION_PATTERN.test(line) || line.includes(title)
}

function extractTitle(line: string, appType: AppType): string {
  const versionMatch = line.match(VERSION_PATTERN)
  if (versionMatch) {
    return line.slice(0, versionMatch.index).trim() || defaultTitleForAppType(appType)
  }
  return line || defaultTitleForAppType(appType)
}

function extractVersion(line: string): string | undefined {
  return line.match(VERSION_PATTERN)?.[0]
}

function extractEffort(line?: string): string | undefined {
  if (!line) return undefined
  return line.match(EFFORT_PATTERN)?.[0] ?? undefined
}

export function isSummaryBlock(lines: string[], appType: AppType): boolean {
  if (lines.length < 2 || lines.length > 4) return false
  if (appType !== 'claudecode' && appType !== 'opencode' && appType !== 'codex') return false

  const cleaned = lines.map(cleanTerminalLine).filter(Boolean)
  if (cleaned.length < 2) return false

  const headerLine = cleaned.find((line) => isHeaderLine(line, appType))
  if (!headerLine) return false

  const hasVersion = VERSION_PATTERN.test(headerLine)
  const hasKnownTitle = /(Claude Code|OpenCode|Codex)/i.test(headerLine)
  const hasWorkDir = cleaned.some(looksLikeWorkDir)
  return (hasVersion || hasKnownTitle) && hasWorkDir
}

export function buildSummaryBlock(context: TerminalMatcherContext): TerminalSummaryBlock {
  const cleaned = context.lines.map(cleanTerminalLine).filter(Boolean)
  const headerIndex = cleaned.findIndex((line) => isHeaderLine(line, context.appType))
  const headerLine = headerIndex >= 0 ? cleaned[headerIndex] : (cleaned[0] ?? defaultTitleForAppType(context.appType))
  const detailLine = cleaned.find((line, index) => index !== headerIndex && !looksLikeWorkDir(line))
  const workDir = cleaned.find(looksLikeWorkDir)

  return {
    id: `summary-${context.createdAt}`,
    type: 'summary',
    appType: context.appType,
    raw: context.raw,
    title: extractTitle(headerLine, context.appType),
    version: extractVersion(headerLine),
    subtitle: detailLine,
    workDir,
    effort: extractEffort(detailLine),
    createdAt: context.createdAt,
  }
}

export const summaryMatcher: TerminalBlockMatcher = {
  name: 'summary',
  match: (context) => isSummaryBlock(context.lines, context.appType),
  build: (context) => buildSummaryBlock(context),
}
