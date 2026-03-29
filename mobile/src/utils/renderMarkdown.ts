import DOMPurify from 'dompurify'
import { marked } from 'marked'

marked.setOptions({
  breaks: true,
  gfm: true,
})

export async function renderMarkdownToHtml(markdown: string): Promise<string> {
  const rawHtml = await marked.parse(markdown)
  return DOMPurify.sanitize(rawHtml, {
    USE_PROFILES: { html: true },
  })
}

const MARKDOWN_HINT_PATTERN = /(^|\n)(#{1,6}\s|[-*+]\s|>\s|\d+\.\s|```|\|.+\|)/m

export function looksLikeMarkdown(text: string): boolean {
  return MARKDOWN_HINT_PATTERN.test(text)
}
