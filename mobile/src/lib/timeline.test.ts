/**
 * lib/timeline.test.ts — PG-03 内容转化映射（M2-C §4.3.1 七类组件）
 * 纯函数行为测试：行数组 → 七类内容块；不含网络/store 依赖。
 */
import { describe, expect, it } from 'vitest';
import { FOLD_MIN_LINES, transformOutputLines } from './timeline';

const OPTS = { idPrefix: 't' };

describe('transformOutputLines · 权限确认 PromptAction', () => {
  it('问题行 + Yes/No 编号选项 → prompt-action（按钮即答输入）', () => {
    const items = transformOutputLines(
      ['Do you want to delete this file?', '❯ 1. Yes', '  2. No'],
      OPTS,
    );
    expect(items).toHaveLength(1);
    const item = items[0];
    expect(item.kind).toBe('prompt-action');
    if (item.kind !== 'prompt-action') return;
    expect(item.question).toBe('Do you want to delete this file?');
    expect(item.options.map((o) => o.label)).toEqual(['Yes', 'No']);
    expect(item.options.map((o) => o.input)).toEqual(['1\r', '2\r']);
  });

  it('单行 (y/n) 确认 → prompt-action（y/n 直发）', () => {
    const items = transformOutputLines(['Apply these changes? (y/n)'], OPTS);
    expect(items).toHaveLength(1);
    const item = items[0];
    expect(item.kind).toBe('prompt-action');
    if (item.kind !== 'prompt-action') return;
    expect(item.options.map((o) => o.input)).toEqual(['y', 'n']);
  });

  it('编号不连续 → 不识别为选项，落入普通文本', () => {
    const items = transformOutputLines(['1. first', '3. third'], OPTS);
    expect(items.every((i) => i.kind === 'mono' || i.kind === 'fold')).toBe(true);
  });
});

describe('transformOutputLines · 选项菜单 OptionCard', () => {
  it('标题 + ≥2 编号选项（非 Yes/No 语义）→ option 单选即答', () => {
    const items = transformOutputLines(
      ['Choose a theme:', '1. Solarized', '2. Nord', '3. Dracula'],
      OPTS,
    );
    expect(items).toHaveLength(1);
    const item = items[0];
    expect(item.kind).toBe('option');
    if (item.kind !== 'option') return;
    expect(item.title).toBe('Choose a theme:');
    expect(item.options).toHaveLength(3);
    expect(item.options[1].input).toBe('2\r');
  });

  it('Yes 开头但无 No → 选项菜单而非权限确认', () => {
    const items = transformOutputLines(['1. Yes we can', '2. Maybe later'], OPTS);
    // 无问题行：也不构成权限确认
    expect(items[0].kind).toBe('option');
  });
});

describe('transformOutputLines · 工具调用 ToolCallCard', () => {
  it('Bash 调用 + 摘要行 → tool（名称/标题/详情）', () => {
    const items = transformOutputLines(
      ['Bash(npm run build)', '⎿  compiled successfully'],
      OPTS,
    );
    expect(items).toHaveLength(1);
    const item = items[0];
    expect(item.kind).toBe('tool');
    if (item.kind !== 'tool') return;
    expect(item.toolName).toBe('Bash');
    expect(item.title).toContain('npm run build');
    expect(item.detail).toContain('compiled successfully');
  });
});

describe('transformOutputLines · 错误 ErrorCard', () => {
  it('Error 行 + 缩进详情 → error（原因 + 下一步指引）', () => {
    const items = transformOutputLines(
      ['Error: EACCES permission denied', '  at open (/usr/lib/node.js:12)'],
      OPTS,
    );
    expect(items).toHaveLength(1);
    const item = items[0];
    expect(item.kind).toBe('error');
    if (item.kind !== 'error') return;
    expect(item.reason).toContain('EACCES');
    expect(item.detail).toContain('at open');
    expect(item.nextStep).toContain('权限');
  });

  it('API key 类错误 → 下一步指向桌面端配置', () => {
    const items = transformOutputLines(['Error: 401 unauthorized: invalid api key'], OPTS);
    const item = items[0];
    if (item.kind !== 'error') throw new Error('expected error');
    expect(item.nextStep).toContain('桌面端');
  });
});

describe('transformOutputLines · 进度 ProgressCard', () => {
  it('braille spinner 行 → progress（文字化）', () => {
    const items = transformOutputLines(['⠋ Building project…'], OPTS);
    expect(items).toHaveLength(1);
    expect(items[0].kind).toBe('progress');
    if (items[0].kind !== 'progress') return;
    expect(items[0].text).toContain('Building project');
  });

  it('百分比行 → progress', () => {
    const items = transformOutputLines(['42%'], OPTS);
    expect(items[0].kind).toBe('progress');
  });
});

describe('transformOutputLines · 长输出 FoldBlock / 等宽兜底 MonoBlock', () => {
  it(`≥${FOLD_MIN_LINES} 行普通文本 → fold（摘要 + 行数 + 全文）`, () => {
    const lines = Array.from({ length: FOLD_MIN_LINES + 3 }, (_, i) => `line ${i + 1}`);
    const items = transformOutputLines(lines, OPTS);
    expect(items).toHaveLength(1);
    const item = items[0];
    expect(item.kind).toBe('fold');
    if (item.kind !== 'fold') return;
    expect(item.summary).toBe('line 1');
    expect(item.lineCount).toBe(FOLD_MIN_LINES + 3);
    expect(item.fullText).toContain(`line ${FOLD_MIN_LINES + 3}`);
  });

  it('短文本 → mono 等宽兜底', () => {
    const items = transformOutputLines(['hello world', 'second line'], OPTS);
    expect(items).toHaveLength(1);
    expect(items[0].kind).toBe('mono');
    if (items[0].kind !== 'mono') return;
    expect(items[0].text).toBe('hello world\nsecond line');
    expect(items[0].hadControlChars).toBe(false);
  });

  it('含 ANSI 控制序列 → mono 标记 hadControlChars（诊断视图指引依据）', () => {
    const items = transformOutputLines(['[31mcolored[0m text'], OPTS);
    const item = items[0];
    if (item.kind !== 'mono') throw new Error('expected mono');
    expect(item.hadControlChars).toBe(true);
    expect(item.text).toBe('colored text');
  });
});

describe('transformOutputLines · 混合流分块', () => {
  it('普通文本 → 错误 → 普通文本，分块顺序保持', () => {
    const items = transformOutputLines(
      ['some output', '', 'Error: boom', '', 'recovered'],
      OPTS,
    );
    expect(items.map((i) => i.kind)).toEqual(['mono', 'error', 'mono']);
  });

  it('空输入 → 空结果', () => {
    expect(transformOutputLines([], OPTS)).toEqual([]);
  });
});
