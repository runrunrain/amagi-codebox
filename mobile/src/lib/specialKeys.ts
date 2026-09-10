/**
 * specialKeys.ts — 终端特殊按键与快捷键序列定义
 * ---------------------------------------------------------------------------
 * 来源与对齐：
 *   · 基础按键序列移植自 mobile/src/views/TerminalPage.vue:865-909 (sendSpecialKey)；
 *   · ⌥/Alt 组合键对齐 desktop frontend/src/composables/useTerminalEngine.ts:919-933
 *     （pi/amagi ⌥W 看板, ⌥T 任务, ⌥R 评审；ESC 前缀序列 \x1b<key>）；
 *   · 供 KeyTray 触屏托盘与终端输入通路 (sendRaw) 消费。
 * ---------------------------------------------------------------------------
 */

export interface SpecialKeyDef {
  /** 唯一标识符（测试与按键识别用）。 */
  id: string;
  /** 界面展示文案（如 "^C", "Tab", "Esc", "⌥W"）。 */
  label: string;
  /** 无障碍辅助说明。 */
  ariaLabel: string;
  /** 按键类别（影响排版样式与视觉重点）。 */
  kind: 'control' | 'action' | 'arrow' | 'alt' | 'modifier';
  /** 按键发送的 ANSI 字节序列（modifier 类按键可为 null）。 */
  sequence: string | null;
}

/** 核心特殊按键映射表。 */
export const SPECIAL_KEY_MAP: Record<string, string> = {
  Enter: '\r',
  Tab: '\t',
  Esc: '\x1b',
  'Ctrl+C': '\x03',
  'Ctrl+D': '\x04',
  'Ctrl+L': '\x0c',
  'Ctrl+Z': '\x1a',
  Up: '\x1b[A',
  Down: '\x1b[B',
  Left: '\x1b[D',
  Right: '\x1b[C',
  // pi/amagi 专属面板快捷键（⌥W 看板, ⌥T 任务, ⌥R 评审）
  'Alt+W': '\x1bw',
  'Alt+T': '\x1bt',
  'Alt+R': '\x1br',
};

/**
 * 托盘展示的快捷键清单（按触屏常用频度排序）：
 * Esc, Tab, ^C, ^D, ^L, Enter, 四向箭头, Alt 切换开关, ⌥W, ⌥T, ⌥R
 */
export const TERMINAL_KEY_DEFS: SpecialKeyDef[] = [
  { id: 'esc', label: 'Esc', ariaLabel: 'Escape 键', kind: 'action', sequence: '\x1b' },
  { id: 'tab', label: 'Tab', ariaLabel: 'Tab 制表键', kind: 'action', sequence: '\t' },
  { id: 'ctrl-c', label: '^C', ariaLabel: 'Ctrl+C 中断', kind: 'control', sequence: '\x03' },
  { id: 'ctrl-d', label: '^D', ariaLabel: 'Ctrl+D 结束输入', kind: 'control', sequence: '\x04' },
  { id: 'ctrl-l', label: '^L', ariaLabel: 'Ctrl+L 清屏重绘', kind: 'control', sequence: '\x0c' },
  { id: 'enter', label: 'Enter', ariaLabel: '回车键', kind: 'action', sequence: '\r' },
  { id: 'up', label: '↑', ariaLabel: '方向键上', kind: 'arrow', sequence: '\x1b[A' },
  { id: 'down', label: '↓', ariaLabel: '方向键下', kind: 'arrow', sequence: '\x1b[B' },
  { id: 'left', label: '←', ariaLabel: '方向键左', kind: 'arrow', sequence: '\x1b[D' },
  { id: 'right', label: '→', ariaLabel: '方向键右', kind: 'arrow', sequence: '\x1b[C' },
  { id: 'alt-modifier', label: 'Alt', ariaLabel: 'Alt 修饰键开关', kind: 'modifier', sequence: null },
  { id: 'alt-w', label: '⌥W', ariaLabel: 'Alt+W 看板', kind: 'alt', sequence: '\x1bw' },
  { id: 'alt-t', label: '⌥T', ariaLabel: 'Alt+T 任务', kind: 'alt', sequence: '\x1bt' },
  { id: 'alt-r', label: '⌥R', ariaLabel: 'Alt+R 评审', kind: 'alt', sequence: '\x1br' },
];

/**
 * 将按键名称或组合键解析为 ANSI 字节序列。
 * 若开启 altMode 且传入单字符，自动补 \x1b 前缀。
 */
export function resolveSpecialKey(key: string, altMode = false): string | null {
  if (SPECIAL_KEY_MAP[key]) {
    return SPECIAL_KEY_MAP[key];
  }
  if (key.startsWith('Alt+') && key.length === 5) {
    return `\x1b${key[4].toLowerCase()}`;
  }
  if (key.startsWith('Option+') && key.length === 8) {
    return `\x1b${key[7].toLowerCase()}`;
  }
  if (altMode && key.length === 1) {
    return `\x1b${key.toLowerCase()}`;
  }
  return null;
}
