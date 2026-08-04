/**
 * lib/rawTerminal.test.ts — PG-04 诊断视图纯逻辑（M2-D）
 * 覆盖：RawTranscript 追加/有界裁剪/清空、createBatchedWriter 分批/聚合/
 * flushAll/dispose、buildXtermTheme 令牌映射与缺槽回退。
 */
import { describe, expect, it } from 'vitest';
import {
  RawTranscript,
  buildXtermTheme,
  createBatchedWriter,
} from './rawTerminal';

describe('RawTranscript', () => {
  it('追加与读出（原文保留，含 ANSI 序列）', () => {
    const t = new RawTranscript({ maxChars: 1000 });
    t.append('\x1b[31mred\x1b[0m ');
    t.append('plain\n');
    expect(t.text()).toBe('\x1b[31mred\x1b[0m plain\n');
    expect(t.length).toBe(t.text().length);
  });

  it('超出上限从头部裁剪（保留最新）', () => {
    const t = new RawTranscript({ maxChars: 10 });
    t.append('0123456789');
    t.append('abcde');
    expect(t.length).toBeLessThanOrEqual(10 + 64); // ANSI 避让允许有限超出
    expect(t.text().endsWith('abcde')).toBe(true);
    expect(t.text()).not.toContain('01234');
  });

  it('裁剪避让：截断点后的 ANSI 序列整体保留', () => {
    const t = new RawTranscript({ maxChars: 20 });
    t.append('AAAAAAAAAA\x1b[32mBBBBBBBBBB\x1b[0m');
    // 裁剪点落在 ESC 之前不远处时，裁剪推进到 ESC，不制造半个转义序列。
    expect(t.text().startsWith('A\x1b')).toBe(false);
  });

  it('清空', () => {
    const t = new RawTranscript({ maxChars: 100 });
    t.append('data');
    t.clear();
    expect(t.text()).toBe('');
    expect(t.length).toBe(0);
  });
});

describe('createBatchedWriter', () => {
  it('聚合多次 push 为批量 flush（schedule 后才写出）', () => {
    const written: string[] = [];
    const queue: (() => void)[] = [];
    const w = createBatchedWriter((c) => written.push(c), {
      maxBatchChars: 100,
      schedule: (cb) => queue.push(cb),
    });
    w.push('aaa');
    w.push('bbb');
    expect(written).toEqual([]);
    expect(w.pendingChars).toBe(6);
    queue.shift()!();
    expect(written).toEqual(['aaabbb']);
    expect(w.pendingChars).toBe(0);
  });

  it('长回放按 maxBatchChars 分批（批间让出调度）', () => {
    const written: string[] = [];
    const queue: (() => void)[] = [];
    const w = createBatchedWriter((c) => written.push(c), {
      maxBatchChars: 4,
      schedule: (cb) => queue.push(cb),
    });
    w.push('0123456789');
    queue.shift()!(); // batch 1: 0123
    expect(written).toEqual(['0123']);
    expect(queue.length).toBe(1); // 余量已排下一批
    queue.shift()!(); // batch 2: 4567
    queue.shift()!(); // batch 3: 89
    expect(written).toEqual(['0123', '4567', '89']);
    expect(w.pendingChars).toBe(0);
  });

  it('flushAll 立即排空（卸载防丢尾部）', () => {
    const written: string[] = [];
    const w = createBatchedWriter((c) => written.push(c), { maxBatchChars: 3 });
    w.push('1234567');
    w.flushAll();
    expect(written.join('')).toBe('1234567');
    expect(w.pendingChars).toBe(0);
  });

  it('dispose 后丢弃缓冲且不再写出', () => {
    const written: string[] = [];
    const queue: (() => void)[] = [];
    const w = createBatchedWriter((c) => written.push(c), {
      maxBatchChars: 10,
      schedule: (cb) => queue.push(cb),
    });
    w.push('abc');
    w.dispose();
    queue.forEach((cb) => cb());
    w.push('def');
    expect(written).toEqual([]);
    expect(w.pendingChars).toBe(0);
  });
});

describe('buildXtermTheme', () => {
  it('VT 令牌 → xterm theme 映射（无硬编码色值，全部取自令牌表）', () => {
    const theme = buildXtermTheme({
      '--VT-surface-dark': '#1F1E1B',
      '--VT-ansi-foreground': '#FAF9F5',
      '--VT-ansi-bright-foreground': '#FFFFFF',
      '--VT-ansi-black': '#5A564F',
      '--VT-ansi-red': '#E07A5F',
      '--VT-ansi-green': '#7FBF8E',
      '--VT-ansi-yellow': '#D9A441',
      '--VT-ansi-blue': '#7FA8D9',
      '--VT-ansi-magenta': '#C48BC0',
      '--VT-ansi-cyan': '#6FBDB3',
      '--VT-accent': '#C15F3C',
    });
    expect(theme.background).toBe('#1F1E1B');
    expect(theme.foreground).toBe('#FAF9F5');
    expect(theme.red).toBe('#E07A5F');
    expect(theme.brightWhite).toBe('#FFFFFF');
    expect(theme.cursor).toBe('#C15F3C');
  });

  it('缺槽位回退前景/背景，不产生空色值', () => {
    const theme = buildXtermTheme({
      '--VT-surface-dark': '#1F1E1B',
      '--VT-ansi-foreground': '#FAF9F5',
    });
    for (const value of Object.values(theme)) {
      expect(value.length).toBeGreaterThan(0);
    }
    expect(theme.red).toBe('#FAF9F5');
    expect(theme.background).toBe('#1F1E1B');
  });
});
