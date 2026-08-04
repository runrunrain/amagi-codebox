/**
 * lib/rawTerminal.ts — PG-04 终端诊断视图纯逻辑（M2-D）
 * ---------------------------------------------------------------------------
 * 权威依据：P5 v1.2 §PG-04 / §9（xterm 仅在诊断视图动态导入、长输出分批）、
 * P4 v2.2 §6.3（ANSI 调色 = VT 令牌冻结 spec 值，禁止硬编码色值）。
 * 本模块不含任何 xterm 依赖（动态 import 只出现在 RawTerminalView.vue 字面
 * import()，保证代码分割）；全部为可单测的纯函数/类：
 *   · RawTranscript：有界滚动原始输出缓冲（诊断视图打开时回放 + 直播续写）；
 *   · createBatchedWriter：xterm.write 分批器（长输出回放/突发输出不阻塞主线程）；
 *   · buildXtermTheme / readVtThemeTokens：VT 令牌 → xterm ITheme 映射。
 * ---------------------------------------------------------------------------
 */

/** xterm ITheme 的最小结构（避免静态 import 类型进主 chunk 依赖图——仅结构类型）。 */
export interface XtermThemeLike {
  background: string;
  foreground: string;
  cursor: string;
  cursorAccent: string;
  selectionBackground: string;
  black: string;
  red: string;
  green: string;
  yellow: string;
  blue: string;
  magenta: string;
  cyan: string;
  white: string;
  brightBlack: string;
  brightRed: string;
  brightGreen: string;
  brightYellow: string;
  brightBlue: string;
  brightMagenta: string;
  brightCyan: string;
  brightWhite: string;
}

/** VT 令牌槽 → xterm theme 键（P4 v2.2 §6.3：ANSI 调色即 VT 冻结值）。 */
const TOKEN_TO_THEME: ReadonlyArray<readonly [keyof XtermThemeLike, string]> = [
  ['background', '--VT-surface-dark'],
  ['foreground', '--VT-ansi-foreground'],
  ['cursor', '--VT-accent'],
  ['cursorAccent', '--VT-surface-dark'],
  ['selectionBackground', '--VT-ansi-blue'],
  ['black', '--VT-ansi-black'],
  ['red', '--VT-ansi-red'],
  ['green', '--VT-ansi-green'],
  ['yellow', '--VT-ansi-yellow'],
  ['blue', '--VT-ansi-blue'],
  ['magenta', '--VT-ansi-magenta'],
  ['cyan', '--VT-ansi-cyan'],
  ['white', '--VT-ansi-foreground'],
  ['brightBlack', '--VT-ansi-black'],
  ['brightRed', '--VT-ansi-red'],
  ['brightGreen', '--VT-ansi-green'],
  ['brightYellow', '--VT-ansi-yellow'],
  ['brightBlue', '--VT-ansi-blue'],
  ['brightMagenta', '--VT-ansi-magenta'],
  ['brightCyan', '--VT-ansi-cyan'],
  ['brightWhite', '--VT-ansi-bright-foreground'],
];

/** 由令牌表构造 xterm theme（纯函数；缺槽位时回退 foreground/background 已有值）。 */
export function buildXtermTheme(tokens: Readonly<Record<string, string>>): XtermThemeLike {
  const theme = {} as Record<keyof XtermThemeLike, string>;
  for (const [key, token] of TOKEN_TO_THEME) {
    theme[key] = tokens[token] ?? '';
  }
  // 防呆：任何槽位缺失（空串）时回退到前景/背景，保证不产生非法色值。
  for (const [key] of TOKEN_TO_THEME) {
    if (!theme[key]) theme[key] = key === 'background' || key === 'cursorAccent' ? tokens['--VT-surface-dark'] ?? '#1F1E1B' : tokens['--VT-ansi-foreground'] ?? '#FAF9F5';
  }
  return theme as XtermThemeLike;
}

/** 运行时从根元素计算样式读取 VT 令牌（组件挂载后调用；不硬编码任何色值）。 */
export function readVtThemeTokens(root: HTMLElement = document.documentElement): Record<string, string> {
  const style = getComputedStyle(root);
  const out: Record<string, string> = {};
  for (const [, token] of TOKEN_TO_THEME) {
    const value = style.getPropertyValue(token).trim();
    if (value) out[token] = value;
  }
  return out;
}

// ---------------------------------------------------------------------------
// RawTranscript：有界滚动原始输出缓冲
// ---------------------------------------------------------------------------

export interface RawTranscriptOptions {
  /** 缓冲上限（字符数）；超出后从头部裁剪（保留最新）。 */
  maxChars: number;
}

/**
 * 原始输出滚动缓冲：诊断视图打开时一次性回放，其后由订阅续写。
 * 只存解码后文本（含 ANSI 序列原样）；不做任何行处理/转化。
 */
export class RawTranscript {
  private buffer = '';
  private readonly maxChars: number;

  constructor(options: RawTranscriptOptions) {
    this.maxChars = options.maxChars;
  }

  append(text: string): void {
    if (text.length === 0) return;
    this.buffer += text;
    if (this.buffer.length > this.maxChars) {
      // 从头部裁剪；为避免截断 ANSI 转义序列，裁剪点推进到下一个 ESC 之后
      // 不完整序列不保证语义正确——截断发生在远古历史处，诊断语义可接受。
      let cut = this.buffer.length - this.maxChars;
      const nextEsc = this.buffer.indexOf('\x1b', cut);
      if (nextEsc >= 0 && nextEsc < cut + 64) cut = nextEsc;
      this.buffer = this.buffer.slice(cut);
    }
  }

  text(): string {
    return this.buffer;
  }

  get length(): number {
    return this.buffer.length;
  }

  clear(): void {
    this.buffer = '';
  }
}

// ---------------------------------------------------------------------------
// createBatchedWriter：xterm.write 分批器
// ---------------------------------------------------------------------------

export interface BatchedWriterOptions {
  /** 每次 flush 的最大字符数（长回放分批，防止单帧阻塞）。 */
  maxBatchChars: number;
  /** flush 调度器（默认 setTimeout 0；测试可注入 fake）。 */
  schedule?: (cb: () => void) => void;
}

export interface BatchedWriter {
  /** 追加待写文本（可多次调用，合并为批量 flush）。 */
  push(text: string): void;
  /** 立即排空剩余缓冲（卸载前调用，防丢尾部）。 */
  flushAll(): void;
  /** 待写字符数（测试观测用）。 */
  readonly pendingChars: number;
  dispose(): void;
}

/**
 * 分批写出器：把突发/回放文本聚合后按 maxBatchChars 分批送入 write()，
 * 每批之间让出事件循环（schedule），长输出不卡死主线程（性能锚点）。
 */
export function createBatchedWriter(write: (chunk: string) => void, options: BatchedWriterOptions): BatchedWriter {
  let queue = '';
  let scheduled = false;
  let disposed = false;
  const schedule = options.schedule ?? ((cb: () => void) => setTimeout(cb, 0));

  function flushBatch(): void {
    scheduled = false;
    if (disposed || queue.length === 0) return;
    const batch = queue.slice(0, options.maxBatchChars);
    queue = queue.slice(batch.length);
    write(batch);
    if (queue.length > 0) {
      scheduled = true;
      schedule(flushBatch);
    }
  }

  return {
    push(text: string): void {
      if (disposed || text.length === 0) return;
      queue += text;
      if (!scheduled) {
        scheduled = true;
        schedule(flushBatch);
      }
    },
    flushAll(): void {
      while (queue.length > 0) {
        const batch = queue.slice(0, options.maxBatchChars);
        queue = queue.slice(batch.length);
        write(batch);
      }
      scheduled = false;
    },
    get pendingChars(): number {
      return queue.length;
    },
    dispose(): void {
      disposed = true;
      queue = '';
      scheduled = false;
    },
  };
}
