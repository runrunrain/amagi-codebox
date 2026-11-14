/**
 * 轻量 JSON/JSONC 语法高亮：先转义 HTML，再以单遍正则同时消费字符串令牌与行注释，
 * 避免字符串字面量内的 `//`（如 URL、glob 模式）被二次匹配为注释，也避免吞掉注释前导字符。
 *
 * 样式类名约定（对应 OpenCodePresets.vue `.oc-json`）：
 * - `.k`：JSON 键名
 * - `.s`：JSON 字符串值
 * - `.c`：`// ...` 行注释
 */
export function highlightJsonWithComments(raw: string): string {
  if (!raw) return '';
  const escaped = raw
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');

  return escaped.replace(
    /("(?:\\.|[^"\\])*")(\s*:)?|(\/\/[^\r\n]*)/gm,
    (
      match: string,
      str: string | undefined,
      colon: string | undefined,
      comment: string | undefined
    ): string => {
      if (str !== undefined) {
        return colon !== undefined
          ? `<span class="k">${str}</span>${colon}`
          : `<span class="s">${str}</span>`;
      }
      if (comment !== undefined) {
        return `<span class="c">${comment}</span>`;
      }
      return match;
    }
  );
}
