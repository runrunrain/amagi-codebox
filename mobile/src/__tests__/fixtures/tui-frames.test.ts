/**
 * __tests__/fixtures/tui-frames.test.ts — 合成 TUI 帧样本自检 + oracle 语义验证（P1-C）
 * ---------------------------------------------------------------------------
 * 用迷你屏幕 oracle（renderTuiStream，80×24 视口 + scrollback 模型）对每个
 * 合成样本断言其目标语义，防止 fixture 本身被改坏：
 *   · 整屏重绘不堆积（旧帧标记在最终屏幕/scrollback 中为 0 次）；
 *   · \r 覆写无残留（spinner 旧帧被覆盖/擦除，终态单行）；
 *   · 局部重绘只改目标行（未变更行原样保留，不产生重复行）；
 *   · 备屏退出后主屏内容恢复（中间态备屏内容可见）；
 *   · 长输出滚入 scrollback（行数守恒、首行进历史、尾部在屏）。
 * 同时验证合成标注（synthetic: true）与 oracle 基本几何契约。
 * ---------------------------------------------------------------------------
 */
import { describe, expect, it } from 'vitest';
import {
  ALL_SYNTHETIC_TUI_FIXTURES,
  ALT_SCREEN_ROUNDTRIP,
  LONG_OUTPUT_SCROLLBACK,
  ORACLE_ROWS,
  PARTIAL_REDRAW_MENU,
  PI_FULLSCREEN_REDRAW,
  PI_SPINNER_CR,
  allLines,
  countOccurrences,
  renderTuiStream,
  screenLines,
  screenText,
} from './tui-frames';

describe('合成 TUI 帧样本（SYNTHETIC）元数据', () => {
  it('全部样本显式标注 synthetic=true 且帧非空', () => {
    expect(ALL_SYNTHETIC_TUI_FIXTURES.length).toBeGreaterThanOrEqual(5);
    for (const fixture of ALL_SYNTHETIC_TUI_FIXTURES) {
      expect(fixture.synthetic, `${fixture.name} 必须显式标注 synthetic`).toBe(true);
      expect(fixture.basis.trim()).not.toBe('');
      expect(fixture.frames.length).toBeGreaterThan(0);
    }
  });

  it('帧内容约束：单行 ≤ 40 列、整屏重绘帧 ≤ 8 行（e2e 视口安全）', () => {
    for (const fixture of ALL_SYNTHETIC_TUI_FIXTURES) {
      for (const frame of fixture.frames) {
        for (const line of frame.split('\r\n')) {
          // 控制序列剥离后按 40 列上限检查（粗略：去 ESC 序列与 \r）
          const visible = line.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '').replace(/\r/g, '');
          expect(visible.length, `${fixture.name} 行超宽: ${visible}`).toBeLessThanOrEqual(40);
        }
      }
    }
    expect(PI_FULLSCREEN_REDRAW.frames.every((f) => f.replace(/\r\n$/, '').split('\r\n').length <= 8)).toBe(true);
  });
});

describe('oracle 基本几何', () => {
  it('空输入 → 空屏幕；纯行输出按 \\r\\n 布行', () => {
    const empty = renderTuiStream([]);
    expect(screenLines(empty)).toHaveLength(ORACLE_ROWS);
    expect(screenText(empty).trim()).toBe('');

    const o = renderTuiStream(['alpha\r\nbeta\r\n']);
    expect(screenLines(o)[0]).toBe('alpha');
    expect(screenLines(o)[1]).toBe('beta');
    expect(o.scrollback).toHaveLength(0);
  });
});

describe('特征 1：整屏重绘不堆积（PI_FULLSCREEN_REDRAW）', () => {
  const oracle = renderTuiStream(PI_FULLSCREEN_REDRAW.frames);
  const screen = screenText(oracle);
  const everything = allLines(oracle).join('\n');

  it('最终屏幕只含最后一帧（frame 3 / status idle）', () => {
    expect(countOccurrences(screen, '[pi-tui] frame 3 ready')).toBe(1);
    expect(screen).toContain('status: idle');
  });

  it('旧帧不残留：frame 1/2 与旧 status 在屏幕与 scrollback 中均为 0 次', () => {
    expect(countOccurrences(everything, 'frame 1 ready')).toBe(0);
    expect(countOccurrences(everything, 'frame 2 ready')).toBe(0);
    expect(countOccurrences(everything, 'status: boot')).toBe(0);
    expect(countOccurrences(everything, 'status: thinking')).toBe(0);
  });

  it('重绘 3 帧不产生 3 倍内容堆积（画面行数恒定）', () => {
    // 非屏幕内容（堆叠垃圾）会滚入 scrollback；语义正确时 scrollback 为空
    expect(oracle.scrollback).toHaveLength(0);
  });
});

describe('特征 3：\\r 覆写无残留（PI_SPINNER_CR）', () => {
  const oracle = renderTuiStream(PI_SPINNER_CR.frames);
  const everything = allLines(oracle).join('\n');

  it('终态单行「✓ done in 1.2s」，spinner 旧帧与 braille 字符零残留', () => {
    expect(screenLines(oracle)[0]).toBe('✓ done in 1.2s');
    expect(everything).not.toContain('generating');
    for (const braille of ['⠋', '⠙', '⠹']) {
      expect(countOccurrences(everything, braille)).toBe(0);
    }
  });
});

describe('特征 2：局部重绘只改目标行（PARTIAL_REDRAW_MENU）', () => {
  const oracle = renderTuiStream(PARTIAL_REDRAW_MENU.frames);
  const lines = screenLines(oracle);

  it('选中态精确改写目标行，未变更行原样保留、不重复', () => {
    expect(lines[0]).toBe('Pick a tool:');
    expect(lines[1]).toBe('  [ ] read files');
    expect(lines[2]).toBe('  [x] write files');
    expect(lines[3]).toBe('  [ ] run shell');
    expect(countOccurrences(allLines(oracle).join('\n'), 'write files')).toBe(1);
    expect(countOccurrences(allLines(oracle).join('\n'), 'Pick a tool:')).toBe(1);
  });
});

describe('特征 4：备屏切换（ALT_SCREEN_ROUNDTRIP）', () => {
  it('备屏激活期间全屏应用可见', () => {
    const mid = renderTuiStream(ALT_SCREEN_ROUNDTRIP.frames.slice(0, 2));
    expect(mid.altScreen).toBe(true);
    expect(screenText(mid)).toContain('ALT-SCREEN APP v9');
    expect(screenText(mid)).not.toContain('primary log');
  });

  it('退出备屏后主屏内容原样恢复', () => {
    const final = renderTuiStream(ALT_SCREEN_ROUNDTRIP.frames);
    expect(final.altScreen).toBe(false);
    const lines = screenLines(final);
    expect(lines[0]).toBe('primary log alpha');
    expect(lines[1]).toBe('primary log beta');
    expect(allLines(final).join('\n')).not.toContain('ALT-SCREEN');
  });
});

describe('特征 5：长输出滚入 scrollback（LONG_OUTPUT_SCROLLBACK）', () => {
  const oracle = renderTuiStream(LONG_OUTPUT_SCROLLBACK.frames);

  it('行数守恒：61 行内容 = 38 行 scrollback + 23 行视口（尾部 \r\n 多滚一行）', () => {
    expect(allLines(oracle).filter((l) => l.trim() !== '')).toHaveLength(61);
    expect(oracle.scrollback.filter((l) => l !== '')).toHaveLength(61 - ORACLE_ROWS + 1);
    expect(screenLines(oracle).filter((l) => l !== '')).toHaveLength(ORACLE_ROWS - 1);
  });

  it('首行滚入历史、尾行留在屏幕（历史正确滚动、尾部不丢）', () => {
    expect(oracle.scrollback[0]).toBe('scrollback probe line 001');
    const lines = screenLines(oracle);
    expect(lines).toContain('tail status OK');
    expect(lines).toContain('scrollback probe line 060');
    expect(lines).not.toContain('scrollback probe line 001');
  });
});
