import type { AppType } from '../types/terminal'
import type { TerminalBlock } from '../types/terminal-blocks'

export interface TerminalMatcherContext {
  appType: AppType
  lines: string[]
  raw: string
  createdAt: number
}

export interface TerminalBlockMatcher {
  name: string
  match(context: TerminalMatcherContext): boolean
  build(context: TerminalMatcherContext): TerminalBlock
}

export interface PatternRegistry {
  register(matcher: TerminalBlockMatcher): void
  list(): TerminalBlockMatcher[]
  detect(context: TerminalMatcherContext): TerminalBlock | null
}

export function createPatternRegistry(initialMatchers: TerminalBlockMatcher[] = []): PatternRegistry {
  const matchers = [...initialMatchers]

  return {
    register(matcher) {
      matchers.push(matcher)
    },
    list() {
      return [...matchers]
    },
    detect(context) {
      for (const matcher of matchers) {
        if (matcher.match(context)) {
          return matcher.build(context)
        }
      }
      return null
    },
  }
}
