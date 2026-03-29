import type { TerminalTodoBlock } from '../../types/terminal-blocks'
import type { TerminalBlockMatcher, TerminalMatcherContext } from '../patternRegistry'

const TODO_LINE_PATTERN = /^\s*[-*+]\s\[[ xX]\]\s/
const TODO_ITEM_PATTERN = /^\s*[-*+]\s\[([ xX])\]\s(.*)$/

export function isTodoBlock(lines: string[]): boolean {
  return lines.some((line) => TODO_LINE_PATTERN.test(line))
}

export function buildTodoBlock(context: TerminalMatcherContext): TerminalTodoBlock {
  const items: TerminalTodoBlock['items'] = []

  for (const line of context.lines) {
    const match = line.match(TODO_ITEM_PATTERN)
    if (!match) continue

    const [, marker, text] = match
    items.push({
      text: text.trim(),
      completed: marker !== ' ',
    })
  }

  return {
    id: `todo-${context.createdAt}`,
    type: 'todo',
    appType: context.appType,
    raw: context.raw,
    items,
    content: context.raw,
    createdAt: context.createdAt,
  }
}

export const todoMatcher: TerminalBlockMatcher = {
  name: 'todo',
  match: (context) => isTodoBlock(context.lines),
  build: (context) => buildTodoBlock(context),
}
