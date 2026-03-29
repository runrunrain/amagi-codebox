import hljs from 'highlight.js'

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/**
 * 对代码进行语法高亮，返回包含 highlight.js CSS class 的 HTML 字符串。
 *
 * - 指定 language 且 highlight.js 支持时，使用精确高亮
 * - language 为空或不支持时，使用 auto-detect
 * - 任何异常均 fallback 到纯文本（HTML 转义）
 */
export function highlightCode(code: string, language?: string): string {
  try {
    if (language) {
      const langId = language.toLowerCase()
      if (hljs.getLanguage(langId)) {
        return hljs.highlight(code, { language: langId }).value
      }
    }
    return hljs.highlightAuto(code).value
  } catch {
    return escapeHtml(code)
  }
}
