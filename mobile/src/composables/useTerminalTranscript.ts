import type { AppType } from '../types/terminal'
import type { TerminalBlock, TerminalTextBlock } from '../types/terminal-blocks'
import { detectTerminalBlocks } from '../parser/blockDetector'
import { isActionBlock } from '../parser/matchers/actionMatcher'
import { isTableBlock } from '../parser/matchers/tableMatcher'
import { isTodoBlock } from '../parser/matchers/todoMatcher'
import { looksLikeMarkdown } from '../utils/renderMarkdown'
import { isPromptBlock } from '../parser/matchers/promptMatcher'
import { isStatusBlock } from '../parser/matchers/statusMatcher'
import { isToolBlock } from '../parser/matchers/toolMatcher'

const DIVIDER_PATTERN = /^[\s\-─━═▁▂▃▄▅▆▇█]+$/

export function isTranscriptDividerLine(line: string): boolean {
  const trimmed = line.trim()
  return trimmed.length >= 12 && DIVIDER_PATTERN.test(trimmed)
}

export function isTranscriptCommandLine(line: string): boolean {
  const trimmed = line.trim()
  return trimmed.startsWith('>') || />\s*(claude|opencode|codex)(\s|$)/i.test(trimmed)
}

function currentSectionLooksMarkdown(lines: string[]): boolean {
  if (lines.length === 0) return false
  if (isTodoBlock(lines) || isTableBlock(lines)) return false
  return looksLikeMarkdown(lines.join('\n'))
}

function isPlainTextBlock(block: TerminalBlock): block is TerminalTextBlock {
  return block.type === 'text'
}

function nextNonEmptyLine(lines: string[], startIndex: number): string | undefined {
  for (let i = startIndex; i < lines.length; i++) {
    const line = lines[i]
    if (line.trim() !== '') {
      return line
    }
  }
  return undefined
}

function nextNonEmptyLineIndex(lines: string[], startIndex: number): number {
  for (let i = startIndex; i < lines.length; i++) {
    if (lines[i].trim() !== '') {
      return i
    }
  }
  return -1
}

function nextSectionLooksMarkdown(lines: string[], startIndex: number): boolean {
  const nextIndex = nextNonEmptyLineIndex(lines, startIndex)
  if (nextIndex < 0) return false

  const nextLine = lines[nextIndex]
  if (isTodoBlock([nextLine])) return false

  const nonEmptyUpcomingLines = lines.slice(nextIndex).filter((line) => line.trim() !== '')
  if (isTableBlock(nonEmptyUpcomingLines.slice(0, 2))) return false

  return looksLikeMarkdown(nextLine)
}

function currentSectionIsTool(lines: string[]): boolean {
  if (lines.length === 0) return false
  return isToolBlock([lines[0]])
}

function isToolContinuationLine(line: string): boolean {
  const trimmed = line.trim()
  if (!trimmed) return false
  return /^⎿/.test(trimmed) || /^[/A-Za-z]:/.test(trimmed) || /^[-+]/.test(trimmed) || /^\d+\s+(files?|dirs?|matches?)\b/i.test(trimmed) || line.startsWith('  ')
}

export function splitTranscriptSections(lines: string[]): string[][] {
  const sections: string[][] = []
  let current: string[] = []
  let insideFence = false

  function flushCurrent() {
    if (current.length > 0) {
      sections.push(current)
      current = []
    }
  }

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]
    const trimmed = line.trim()

    if (!insideFence && trimmed.startsWith('```')) {
      flushCurrent()
      current.push(line)
      insideFence = true
      continue
    }

    if (!insideFence && trimmed === '') {
      const nextLine = nextNonEmptyLine(lines, index + 1)
      const nextLineSuggestsMarkdown = nextLine ? nextSectionLooksMarkdown(lines, index + 1) : false
      const nextLineContinuesTool = nextLine ? (currentSectionIsTool(current) && isToolContinuationLine(nextLine)) : false

      if (current.length > 0 && (currentSectionLooksMarkdown(current) || nextLineSuggestsMarkdown || nextLineContinuesTool)) {
        current.push(line)
      } else {
        flushCurrent()
      }
      continue
    }

    if (!insideFence && isTranscriptDividerLine(line)) {
      flushCurrent()
      continue
    }

    if (!insideFence && isStatusBlock([line])) {
      flushCurrent()
      sections.push([line])
      continue
    }

    if (!insideFence && (isPromptBlock([line]) || isActionBlock([line]) || isTranscriptCommandLine(line))) {
      flushCurrent()
      sections.push([line])
      continue
    }

    if (!insideFence && currentSectionIsTool(current) && isToolContinuationLine(line)) {
      current.push(line)
      continue
    }

    current.push(line)

    if (insideFence && trimmed.startsWith('```')) {
      insideFence = !insideFence
      if (!insideFence) {
        flushCurrent()
      }
    }
  }

  flushCurrent()
  return sections
}

export function buildTranscriptBlocks(lines: string[], appType: AppType, createdAtBase = Date.now()): TerminalBlock[] {
  const sections = splitTranscriptSections(lines)
  if (sections.length === 0) return []

  const blocks: TerminalBlock[] = []
  let sequence = 0

  for (const section of sections) {
    blocks.push(...detectTerminalBlocks({
      appType,
      lines: section,
      createdAt: createdAtBase + sequence,
    }))
    sequence += 1
  }

  return promoteTextBlocksToMarkdown(dedupeAdjacentSemanticBlocks(blocks), appType)
}

function shouldPromotePlainTextBlockToMarkdown(block: TerminalTextBlock, appType: AppType): boolean {
  if (appType !== 'claudecode' && appType !== 'opencode') return false

  const trimmed = block.content.trim()
  if (!trimmed) return false
  if (!trimmed.includes('\n')) return false
  if (/^(>|PS\s|●|⎿|⏵|Bash\b|Read\b|Write\b|Edit\b|Task\b|Search\b)/m.test(trimmed)) {
    return false
  }

  return trimmed.length >= 12
}

export function promoteTextBlocksToMarkdown(blocks: TerminalBlock[], appType: AppType): TerminalBlock[] {
  return blocks.map((block): TerminalBlock => {
    if (!isPlainTextBlock(block)) {
      return block
    }

    if (!shouldPromotePlainTextBlockToMarkdown(block, appType)) {
      return block
    }

    const markdownBlock: TerminalTextBlock = {
      ...block,
      type: 'markdown',
    }

    return markdownBlock
  })
}

function blockSignature(block: TerminalBlock): string {
  switch (block.type) {
    case 'summary':
      return `${block.type}:${block.title}:${block.version ?? ''}:${block.subtitle ?? ''}:${block.workDir ?? ''}:${block.effort ?? ''}`
    case 'prompt':
    case 'action':
    case 'status':
    case 'streaming':
      return `${block.type}:${block.content}:${block.shortcutHint ?? ''}:${block.primaryAction ?? ''}`
    case 'tool':
      return `${block.type}:${block.toolName}:${block.title}:${block.summary ?? ''}:${block.shortcutHint ?? ''}`
    default:
      return `${block.type}:${block.raw}`
  }
}

function shouldDedupeBlock(block: TerminalBlock): boolean {
  return block.type === 'summary' || block.type === 'prompt' || block.type === 'action' || block.type === 'status' || block.type === 'tool'
}

export function dedupeAdjacentSemanticBlocks(blocks: TerminalBlock[]): TerminalBlock[] {
  const deduped: TerminalBlock[] = []

  for (const block of blocks) {
    const previous = deduped[deduped.length - 1]
    if (
      previous
      && shouldDedupeBlock(previous)
      && shouldDedupeBlock(block)
      && blockSignature(previous) === blockSignature(block)
    ) {
      continue
    }
    deduped.push(block)
  }

  return deduped
}

export function useTerminalTranscript() {
  return {
    buildTranscriptBlocks,
  }
}
