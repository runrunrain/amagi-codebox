import { ref } from 'vue'
import type { TerminalBlock } from '../types/terminal-blocks'
import { renderMarkdownToHtml } from '../utils/renderMarkdown'

type RenderableBlock = Extract<TerminalBlock, { type: 'markdown' | 'table' }>

function isRenderableBlock(block: TerminalBlock): block is RenderableBlock {
  return block.type === 'markdown' || block.type === 'table'
}

export function useMarkdownHtmlCache() {
  const markdownHtmlById = ref<Record<string, string>>({})
  let renderVersion = 0

  async function refreshMarkdownBlocks(blocks: TerminalBlock[]) {
    const currentVersion = ++renderVersion
    const markdownBlocks = blocks.filter(isRenderableBlock)

    if (markdownBlocks.length === 0) {
      markdownHtmlById.value = {}
      return
    }

    const entries = await Promise.all(markdownBlocks.map(async (block) => [
      block.id,
      await renderMarkdownToHtml(block.content),
    ] as const))

    if (currentVersion !== renderVersion) return
    markdownHtmlById.value = Object.fromEntries(entries)
  }

  function resetMarkdownCache() {
    renderVersion += 1
    markdownHtmlById.value = {}
  }

  return {
    markdownHtmlById,
    refreshMarkdownBlocks,
    resetMarkdownCache,
  }
}
