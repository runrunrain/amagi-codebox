import { describe, expect, it } from 'vitest';
import { highlightJsonWithComments } from '../components/provider/openCodeHighlight';

function assertBalancedFlatSpans(html: string): void {
  const openCount = (html.match(/<span\b/g) ?? []).length;
  const closeCount = (html.match(/<\/span>/g) ?? []).length;
  expect(openCount).toBe(closeCount);
  expect(html).not.toMatch(/<span\b[^>]*>(?:(?!<\/span>).)*<span\b/);
}

describe('highlightJsonWithComments', () => {
  it('returns empty string for empty input', () => {
    expect(highlightJsonWithComments('')).toBe('');
  });

  it('highlights keys, string values, and comments while preserving preceding indentation, commas, and characters', () => {
    const raw = [
      '// top-level comment',
      '{',
      '  // indented comment',
      '  "enabled": true, // trailing after comma',
      '\t"name" : "opencode", // tab and spaced colon',
      '  "escaped": "say \\"hi\\" // inside" // real comment',
      '}',
    ].join('\n');

    const result = highlightJsonWithComments(raw);
    expect(result).toBe(
      [
        '<span class="c">// top-level comment</span>',
        '{',
        '  <span class="c">// indented comment</span>',
        '  <span class="k">"enabled"</span>: true, <span class="c">// trailing after comma</span>',
        '\t<span class="k">"name"</span> : <span class="s">"opencode"</span>, <span class="c">// tab and spaced colon</span>',
        '  <span class="k">"escaped"</span>: <span class="s">"say \\"hi\\" // inside"</span> <span class="c">// real comment</span>',
        '}',
      ].join('\n')
    );
    assertBalancedFlatSpans(result);
  });

  it('preserves CRLF line endings without absorbing carriage returns into comment spans', () => {
    const raw = '{\r\n  "a": 1, // comment\r\n}';
    const result = highlightJsonWithComments(raw);
    expect(result).toBe('{\r\n  <span class="k">"a"</span>: 1, <span class="c">// comment</span>\r\n}');
    assertBalancedFlatSpans(result);
  });

  it('does NOT highlight // inside URLs or glob string values as comments and produces no broken nested spans', () => {
    const raw = [
      '{',
      '  "baseUrl": "https://api.example.com/v1",',
      '  "ignore": "glob // pattern",',
      '  "both": "https://cdn.example.com // path" // real trailing comment',
      '}',
    ].join('\n');

    const result = highlightJsonWithComments(raw);

    expect(result).toContain('<span class="s">"https://api.example.com/v1"</span>');
    expect(result).toContain('<span class="s">"glob // pattern"</span>');
    expect(result).toContain('<span class="s">"https://cdn.example.com // path"</span>');
    expect(result).toContain('<span class="c">// real trailing comment</span>');
    expect(result).not.toContain('<span class="c">// pattern');
    expect(result).not.toContain('<span class="c">// path');
    expect(result).not.toContain('<span class="c">//api.example.com');
    expect((result.match(/<span class="c">/g) ?? []).length).toBe(1);
    assertBalancedFlatSpans(result);
  });

  it('does NOT misinterpret quotes inside // line comments as JSON keys or strings', () => {
    const raw = '  // example: "key": "value"';
    const result = highlightJsonWithComments(raw);
    expect(result).toBe('  <span class="c">// example: "key": "value"</span>');
    expect(result).not.toContain('<span class="k">');
    expect(result).not.toContain('<span class="s">');
    assertBalancedFlatSpans(result);
  });

  it('escapes <script> and HTML special characters in keys, values, comments, and raw text', () => {
    const raw = [
      '{',
      '  "<script>": "<script>alert(1)</script> & <b>bold</b>",',
      '  // <script>alert(2)</script> </span>',
      '  "raw": "<img src=x onerror=alert(3)>"',
      '}',
    ].join('\n');

    const result = highlightJsonWithComments(raw);

    expect(result).not.toContain('<script>');
    expect(result).not.toContain('</script>');
    expect(result).not.toContain('<img');
    expect(result).not.toContain('<b>');
    expect(result).toContain('<span class="k">"&lt;script&gt;"</span>:');
    expect(result).toContain(
      '<span class="s">"&lt;script&gt;alert(1)&lt;/script&gt; &amp; &lt;b&gt;bold&lt;/b&gt;"</span>'
    );
    expect(result).toContain(
      '  <span class="c">// &lt;script&gt;alert(2)&lt;/script&gt; &lt;/span&gt;</span>'
    );
    expect(result).toContain(
      '<span class="s">"&lt;img src=x onerror=alert(3)&gt;"</span>'
    );
    assertBalancedFlatSpans(result);
  });
});
