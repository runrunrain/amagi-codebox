import { actionMatcher } from './matchers/actionMatcher'
import { diffMatcher } from './matchers/diffMatcher'
import { fencedCodeMatcher } from './matchers/fencedCodeMatcher'
import { markdownMatcher } from './matchers/markdownMatcher'
import { promptMatcher } from './matchers/promptMatcher'
import { statusMatcher } from './matchers/statusMatcher'
import { summaryMatcher } from './matchers/summaryMatcher'
import { tableMatcher } from './matchers/tableMatcher'
import { todoMatcher } from './matchers/todoMatcher'
import { toolMatcher } from './matchers/toolMatcher'
import { createPatternRegistry } from './patternRegistry'

export function createDefaultPatternRegistry() {
  return createPatternRegistry([
    summaryMatcher,
    statusMatcher,
    toolMatcher,
    diffMatcher,
    fencedCodeMatcher,
    todoMatcher,
    tableMatcher,
    markdownMatcher,
    promptMatcher,
    actionMatcher,
  ])
}
