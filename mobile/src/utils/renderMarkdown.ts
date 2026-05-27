import DOMPurify from 'dompurify'
import { marked } from 'marked'

marked.setOptions({
  breaks: true,
  gfm: true,
})

export async function renderMarkdownToHtml(markdown: string): Promise<string> {
  const rawHtml = await marked.parse(markdown)
  const sanitized = DOMPurify.sanitize(rawHtml, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'input', 'textarea', 'button', 'select', 'option', 'svg', 'math'],
    FORBID_ATTR: ['style', 'onerror', 'onload', 'onclick', 'onmouseover', 'onfocus'],
    SANITIZE_NAMED_PROPS: true,
    ALLOWED_URI_REGEXP: /^(?:(?:(?:f|ht)tps?|mailto|tel):|[^a-z]|[a-z+.-]+(?:[^a-z+.-:]|$))/i,
  })
  return hardenLinks(sanitized)
}

function hardenLinks(html: string): string {
  if (typeof document === 'undefined') return html
  const template = document.createElement('template')
  template.innerHTML = html
  template.content.querySelectorAll('a[href]').forEach((anchor) => {
    const href = anchor.getAttribute('href') || ''
    if (/^(?:https?:|mailto:|tel:)/i.test(href)) {
      anchor.setAttribute('target', '_blank')
      anchor.setAttribute('rel', 'noopener noreferrer')
    }
  })
  return template.innerHTML
}

const MARKDOWN_HINT_PATTERN = /(^|\n)(#{1,6}\s|[-*+]\s|>\s|\d+\.\s|```|\|.+\|)/m

export function looksLikeMarkdown(text: string): boolean {
  return MARKDOWN_HINT_PATTERN.test(text)
}
