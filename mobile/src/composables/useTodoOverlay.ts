import { computed, ref } from 'vue'
import type { Ref } from 'vue'
import type { TerminalBlock, TerminalTodoBlock } from '../types/terminal-blocks'

export function useTodoOverlay(blocks: Ref<TerminalBlock[]>) {
  const expanded = ref(false)

  const todoBlocks = computed(() =>
    blocks.value.filter((b): b is TerminalTodoBlock => b.type === 'todo'),
  )

  const items = computed(() =>
    todoBlocks.value.flatMap((block) => block.items),
  )

  const completedCount = computed(() => items.value.filter((i) => i.completed).length)
  const totalCount = computed(() => items.value.length)
  const hasItems = computed(() => totalCount.value > 0)

  function toggle() {
    expanded.value = !expanded.value
  }

  return { items, expanded, completedCount, totalCount, hasItems, toggle }
}
