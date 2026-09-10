import { describe, expect, it } from 'vitest';
import {
  SPECIAL_KEY_MAP,
  TERMINAL_KEY_DEFS,
  resolveSpecialKey,
} from '../../lib/specialKeys';

describe('specialKeys', () => {
  it('包含基础终端控制键映射（对齐 TerminalPage sendSpecialKey）', () => {
    expect(SPECIAL_KEY_MAP['Enter']).toBe('\r');
    expect(SPECIAL_KEY_MAP['Tab']).toBe('\t');
    expect(SPECIAL_KEY_MAP['Esc']).toBe('\x1b');
    expect(SPECIAL_KEY_MAP['Ctrl+C']).toBe('\x03');
    expect(SPECIAL_KEY_MAP['Ctrl+D']).toBe('\x04');
    expect(SPECIAL_KEY_MAP['Ctrl+L']).toBe('\x0c');
    expect(SPECIAL_KEY_MAP['Ctrl+Z']).toBe('\x1a');
    expect(SPECIAL_KEY_MAP['Up']).toBe('\x1b[A');
    expect(SPECIAL_KEY_MAP['Down']).toBe('\x1b[B');
    expect(SPECIAL_KEY_MAP['Left']).toBe('\x1b[D');
    expect(SPECIAL_KEY_MAP['Right']).toBe('\x1b[C');
  });

  it('包含 pi/amagi 专属 Alt/Option 面板组合键', () => {
    expect(SPECIAL_KEY_MAP['Alt+W']).toBe('\x1bw');
    expect(SPECIAL_KEY_MAP['Alt+T']).toBe('\x1bt');
    expect(SPECIAL_KEY_MAP['Alt+R']).toBe('\x1br');
  });

  it('TERMINAL_KEY_DEFS 结构完整且包含所有预期快捷键', () => {
    const ids = TERMINAL_KEY_DEFS.map((d) => d.id);
    expect(ids).toContain('esc');
    expect(ids).toContain('tab');
    expect(ids).toContain('ctrl-c');
    expect(ids).toContain('ctrl-d');
    expect(ids).toContain('ctrl-l');
    expect(ids).toContain('enter');
    expect(ids).toContain('up');
    expect(ids).toContain('down');
    expect(ids).toContain('left');
    expect(ids).toContain('right');
    expect(ids).toContain('alt-modifier');
    expect(ids).toContain('alt-w');
    expect(ids).toContain('alt-t');
    expect(ids).toContain('alt-r');

    for (const def of TERMINAL_KEY_DEFS) {
      expect(def.id).toBeTruthy();
      expect(def.label).toBeTruthy();
      expect(def.ariaLabel).toBeTruthy();
      if (def.kind !== 'modifier') {
        expect(def.sequence).toBeTruthy();
      }
    }
  });

  it('resolveSpecialKey 解析标准键名及 Alt 前缀', () => {
    expect(resolveSpecialKey('Enter')).toBe('\r');
    expect(resolveSpecialKey('Esc')).toBe('\x1b');
    expect(resolveSpecialKey('Ctrl+C')).toBe('\x03');
    expect(resolveSpecialKey('Up')).toBe('\x1b[A');
    expect(resolveSpecialKey('Alt+W')).toBe('\x1bw');
    expect(resolveSpecialKey('Alt+A')).toBe('\x1ba');
    expect(resolveSpecialKey('Option+T')).toBe('\x1bt');
    expect(resolveSpecialKey('x', true)).toBe('\x1bx');
    expect(resolveSpecialKey('unknown')).toBeNull();
  });
});
