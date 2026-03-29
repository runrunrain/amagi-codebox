import type { AppType } from '../types/terminal'
import type { TerminalTextBlock } from '../types/terminal-blocks'
import { createDefaultPatternRegistry } from './defaultRegistry'
import type { PatternRegistry, TerminalMatcherContext } from './patternRegistry'

export interface DetectTerminalBlocksOptions {
  appType: AppType
  lines: string[]
  createdAt?: number
  registry?: PatternRegistry
}

function createFallbackTextBlock(context: TerminalMatcherContext): TerminalTextBlock {
  return {
    id: `text-${context.createdAt}`,
    type: 'text',
    appType: context.appType,
    raw: context.raw,
    content: context.raw,
    createdAt: context.createdAt,
  }
}

export function detectTerminalBlocks(options: DetectTerminalBlocksOptions) {
  const { appType, lines, createdAt = Date.now(), registry = createDefaultPatternRegistry() } = options
  if (lines.length === 0) return []

  const raw = lines.join('\n')
  const context: TerminalMatcherContext = {
    appType,
    lines,
    raw,
    createdAt,
  }

  const detected = registry.detect(context)
  if (detected) {
    return [detected]
  }

  return [createFallbackTextBlock(context)]
}
