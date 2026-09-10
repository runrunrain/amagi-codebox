/**
 * tui-frames.ts — 合成全屏 TUI 帧样本（SYNTHETIC，非真实录制）+ 测试用迷你屏幕 oracle
 * ---------------------------------------------------------------------------
 * 【样本来源声明】本文件全部帧样本均为 **手工构造的合成样本（synthetic）**，
 * 不是从真实 pi/omp 进程录制的 PTY 字节。构造依据是 ink 类全屏 TUI 的公开
 * 输出特征（素材F §5：光标寻址全屏重绘 / 擦行局部重绘 / 行内 \r 覆写 / 备屏）：
 *   · 清屏 + 光标绝对寻址整屏重绘（ED2 + CUP，curses 风格）；
 *   · 光标相对寻址 + 擦行局部重绘（CUU + EL，ink 风格 diff 重画）；
 *   · 行首 \r 覆写（spinner / 进度单行刷新）；
 *   · 备屏切换（DECSET/DECRST ?1049）；
 *   · 长输出滚动进入 scrollback。
 * 消费用途：
 *   · vitest：经迷你屏幕 oracle（renderTuiStream）对「组件写入 xterm 的原始
 *     流」做语义验证 —— 重绘帧不重复堆积、\r 覆写无残留、scrollback 正确滚动；
 *   · e2e（e2e/workspace-tui.spec.ts）：同一批帧冒充 server output 流，
 *     断言真 xterm DOM 的可见结果。
 * oracle 是终端仿真语义的**测试替身**（80×24 视口 + scrollback 模型），只解释
 * 上列特征子集；SGR/光标显隐等装饰序列忽略，未知序列安全跳过——不追求完备
 * ANSI 兼容。帧内容约束：单行 ≤ 40 列（兼容 320px 视口不换行）、整屏重绘帧
 * ≤ 8 行（任何 e2e 视口内完整可见）、进入全屏寻址前总行数 ≤ 24（oracle 视口）。
 * ---------------------------------------------------------------------------
 */

/** oracle 视口几何（经典 80×24 字符栅格）。 */
export const ORACLE_COLS = 80;
export const ORACLE_ROWS = 24;

/** 合成样本元数据：显式声明来源，防止误当真实录制帧。 */
export interface SyntheticTuiFixture {
  name: string;
  /** 恒为 true —— 显式标注合成样本（非真实 PTY 录制）。 */
  synthetic: true;
  /** 构造依据（对应上方特征清单条目）。 */
  basis: string;
  /** 按 v1 output 帧发送顺序排列的原始 PTY 文本（未 base64）。 */
  frames: string[];
}

// ---------------------------------------------------------------------------
// 迷你屏幕 oracle：把帧流解释成「scrollback + 可见屏幕」
// ---------------------------------------------------------------------------

interface AltSnapshot {
  screen: string[][];
  scrollback: string[];
  row: number;
  col: number;
}

export interface ScreenOracle {
  /** 滚出视口顶部的历史行（按滚动先后；行尾空白已裁剪）。 */
  scrollback: readonly string[];
  /** 可见屏幕（固定 ORACLE_ROWS 行；含尾部空白填充）。 */
  screen: readonly string[];
  /** 备屏是否激活。 */
  altScreen: boolean;
}

const BLANK_ROW = (): string[] => new Array<string>(ORACLE_COLS).fill(' ');

/** 终端状态机：解析帧流并维护 80×24 屏幕 + scrollback。 */
class MiniTerminal {
  scrollback: string[] = [];
  rows: string[][] = Array.from({ length: ORACLE_ROWS }, BLANK_ROW);
  row = 0;
  col = 0;
  private altSaved: AltSnapshot | null = null;

  /** 状态机：normal / esc / csi / osc / osc-esc / esc-intermediate。 */
  private mode: 'normal' | 'esc' | 'csi' | 'osc' | 'osc-esc' | 'esc-int' = 'normal';
  private csiParams = '';

  feed(text: string): void {
    for (const ch of text) this.step(ch);
  }

  private step(ch: string): void {
    switch (this.mode) {
      case 'normal':
        this.stepNormal(ch);
        return;
      case 'esc':
        if (ch === '[') {
          this.mode = 'csi';
          this.csiParams = '';
        } else if (ch === ']') {
          this.mode = 'osc';
        } else if (ch === '(' || ch === ')' || ch === '*' || ch === '+') {
          this.mode = 'esc-int';
        } else {
          this.mode = 'normal'; // 其余单字节 ESC 序列（7/8/M/D/=/>…）忽略
        }
        return;
      case 'esc-int':
        this.mode = 'normal'; // 字符集指定（如 ESC ( B）整体忽略
        return;
      case 'csi':
        if (ch >= '@' && ch <= '~') {
          this.dispatchCsi(this.csiParams, ch);
          this.mode = 'normal';
        } else {
          this.csiParams += ch; // 参数区 0-9 ; ? < = >
        }
        return;
      case 'osc':
        if (ch === '\x07') this.mode = 'normal';
        else if (ch === '\x1b') this.mode = 'osc-esc';
        return;
      case 'osc-esc':
        this.mode = 'normal'; // ESC \（ST）结束；其他情形按规约不出现，安全复位
        return;
    }
  }

  private stepNormal(ch: string): void {
    if (ch === '\x1b') {
      this.mode = 'esc';
    } else if (ch === '\r') {
      this.col = 0;
    } else if (ch === '\n') {
      this.lineFeed();
    } else if (ch === '\b') {
      this.col = Math.max(0, this.col - 1);
    } else if (ch === '\t') {
      this.col = Math.min(ORACLE_COLS - 1, (Math.floor(this.col / 8) + 1) * 8);
    } else if (ch === '\x07') {
      /* BEL 忽略 */
    } else {
      this.putChar(ch);
    }
  }

  private putChar(ch: string): void {
    this.rows[this.row][this.col] = ch;
    this.col += 1;
    if (this.col >= ORACLE_COLS) {
      this.col = 0;
      this.lineFeed();
    }
  }

  private lineFeed(): void {
    if (this.row >= ORACLE_ROWS - 1) this.scroll();
    else this.row += 1;
  }

  private scroll(): void {
    this.scrollback.push(rowText(this.rows[0]));
    this.rows.shift();
    this.rows.push(BLANK_ROW());
  }

  private dispatchCsi(params: string, final: string): void {
    const nums = params.replace('?', '').split(';').map((s) => (s === '' ? 0 : parseInt(s, 10) || 0));
    const n = (i: number): number => (nums[i] > 0 ? nums[i] : 1);
    switch (final) {
      case 'H':
      case 'f': { // CUP：视口内绝对寻址（1-based；与 scrollback 无关）
        this.row = Math.min(nums[0] > 0 ? nums[0] : 1, ORACLE_ROWS) - 1;
        this.col = Math.min(nums[1] > 0 ? nums[1] : 1, ORACLE_COLS) - 1;
        return;
      }
      case 'A':
        this.row = Math.max(0, this.row - n(0));
        return;
      case 'B':
        this.row = Math.min(ORACLE_ROWS - 1, this.row + n(0));
        return;
      case 'C':
        this.col = Math.min(ORACLE_COLS - 1, this.col + n(0));
        return;
      case 'D':
        this.col = Math.max(0, this.col - n(0));
        return;
      case 'J': { // ED
        const m = nums[0] ?? 0;
        if (m === 0) {
          this.eraseInRow(this.row, this.col, ORACLE_COLS - 1);
          for (let r = this.row + 1; r < ORACLE_ROWS; r++) this.rows[r] = BLANK_ROW();
        } else if (m === 1) {
          for (let r = 0; r < this.row; r++) this.rows[r] = BLANK_ROW();
          this.eraseInRow(this.row, 0, this.col);
        } else {
          for (let r = 0; r < ORACLE_ROWS; r++) this.rows[r] = BLANK_ROW();
        }
        return;
      }
      case 'K': { // EL
        const m = nums[0] ?? 0;
        if (m === 0) this.eraseInRow(this.row, this.col, ORACLE_COLS - 1);
        else if (m === 1) this.eraseInRow(this.row, 0, this.col);
        else this.rows[this.row] = BLANK_ROW();
        return;
      }
      case 'h':
      case 'l': { // DECSET/DECRST：仅解释备屏 ?1049，其余（?25 光标显隐等）忽略
        if (params === '?1049') {
          if (final === 'h' && !this.altSaved) {
            this.altSaved = { screen: this.rows, scrollback: this.scrollback, row: this.row, col: this.col };
            this.rows = Array.from({ length: ORACLE_ROWS }, BLANK_ROW);
            this.scrollback = [];
            this.row = 0;
            this.col = 0;
          } else if (final === 'l' && this.altSaved) {
            this.rows = this.altSaved.screen;
            this.scrollback = this.altSaved.scrollback;
            this.row = this.altSaved.row;
            this.col = this.altSaved.col;
            this.altSaved = null;
          }
        }
        return;
      }
      default:
        return; // SGR(m)/光标显隐等装饰序列忽略
    }
  }

  private eraseInRow(r: number, from: number, to: number): void {
    for (let c = from; c <= to; c++) this.rows[r][c] = ' ';
  }

  snapshot(): ScreenOracle {
    return {
      scrollback: this.scrollback,
      screen: this.rows.map(rowText),
      altScreen: this.altSaved !== null,
    };
  }
}

function rowText(row: string[]): string {
  return row.join('').trimEnd();
}

/** 把帧流顺序渲染成终端屏幕状态（纯函数；每次新建 oracle 状态）。 */
export function renderTuiStream(frames: readonly string[]): ScreenOracle {
  const term = new MiniTerminal();
  for (const frame of frames) term.feed(frame);
  return term.snapshot();
}

/** 可见屏幕行（行尾空白已裁剪）。 */
export function screenLines(oracle: ScreenOracle): string[] {
  return [...oracle.screen];
}

/** scrollback + 可见屏幕全量行（历史滚动断言用）。 */
export function allLines(oracle: ScreenOracle): string[] {
  return [...oracle.scrollback, ...oracle.screen];
}

export function screenText(oracle: ScreenOracle): string {
  return oracle.screen.join('\n');
}

export function countOccurrences(haystack: string, needle: string): number {
  if (needle === '') return 0;
  let count = 0;
  let idx = haystack.indexOf(needle);
  while (idx !== -1) {
    count += 1;
    idx = haystack.indexOf(needle, idx + needle.length);
  }
  return count;
}

// ---------------------------------------------------------------------------
// 合成帧样本（SYNTHETIC）
// ---------------------------------------------------------------------------

/** curses 风格整屏重绘帧：清屏 + 光标归位 + 全画面重画（label/status 逐帧变化）。 */
function fullscreenRedrawFrame(label: string, status: string): string {
  return (
    '\x1b[H\x1b[2J' +
    `[pi-tui] ${label}\r\n` +
    '\r\n' +
    'Model: glm-4.7   Ctx: 42%\r\n' +
    '------------------------------\r\n' +
    '> type your query_\r\n' +
    '\r\n' +
    '[1] plan  [2] code  [3] review\r\n' +
    `status: ${status}\r\n`
  );
}

/** 特征 1：清屏 + 绝对寻址整屏重绘 ×3 —— 重绘帧不得在屏幕/scrollback 堆积。 */
export const PI_FULLSCREEN_REDRAW: SyntheticTuiFixture = {
  name: 'pi-fullscreen-redraw',
  synthetic: true,
  basis: 'ink/curses 类 TUI 整屏重绘特征：每次状态变化 ED2+CUP 后重画全画面',
  frames: [
    fullscreenRedrawFrame('frame 1 ready', 'boot'),
    fullscreenRedrawFrame('frame 2 ready', 'thinking'),
    fullscreenRedrawFrame('frame 3 ready', 'idle'),
  ],
};

/** 特征 3：行首 \r 覆写（spinner 单行刷新）+ 擦行收尾 —— 覆写不得残留旧帧。 */
export const PI_SPINNER_CR: SyntheticTuiFixture = {
  name: 'pi-spinner-cr',
  synthetic: true,
  basis: 'spinner 单行 \r 覆写刷新特征（素材F §5：spinner 用行内 \\r 覆盖刷新）',
  frames: [
    'generating ⠋\r',
    'generating ⠙\r',
    'generating ⠹\r',
    '\r\x1b[2K✓ done in 1.2s\r\n',
  ],
};

/** 特征 2：ink 风格局部重绘（CUU 相对寻址 + EL 擦行后重写选中项）。 */
export const PARTIAL_REDRAW_MENU: SyntheticTuiFixture = {
  name: 'partial-redraw-menu',
  synthetic: true,
  basis: 'ink 风格 diff 重画：光标相对上移 + 擦行重写变更行，未变更行不动',
  frames: [
    '\x1b[?25lPick a tool:\r\n  [ ] read files\r\n  [ ] write files\r\n  [ ] run shell\r\n\x1b[?25h',
    '\x1b[2A\r\x1b[2K  [x] write files',
  ],
};

/** 特征 4：备屏切换（?1049 进/出）——退出备屏后主屏内容必须原样恢复。 */
export const ALT_SCREEN_ROUNDTRIP: SyntheticTuiFixture = {
  name: 'alt-screen-roundtrip',
  synthetic: true,
  basis: 'DECSET/DECRST ?1049 备屏切换特征（全屏应用进入/退出）',
  frames: [
    'primary log alpha\r\nprimary log beta\r\n',
    '\x1b[?1049h\x1b[H\x1b[2JALT-SCREEN APP v9\r\npress q to quit\r\n',
    '\x1b[?1049l',
  ],
};

/** 特征 5：长输出滚动 —— 61 行 > 24 行视口，旧行滚入 scrollback、尾部可见。 */
export const LONG_OUTPUT_SCROLLBACK: SyntheticTuiFixture = {
  name: 'long-output-scrollback',
  synthetic: true,
  basis: '行式长输出滚入 scrollback 特征（验证历史正确滚动、尾部不丢）',
  frames: [
    Array.from({ length: 60 }, (_, i) => `scrollback probe line ${String(i + 1).padStart(3, '0')}\r\n`).join('') +
      'tail status OK\r\n',
  ],
};

/** 全部样本（供自检测试遍历）。 */
export const ALL_SYNTHETIC_TUI_FIXTURES: readonly SyntheticTuiFixture[] = [
  PI_FULLSCREEN_REDRAW,
  PI_SPINNER_CR,
  PARTIAL_REDRAW_MENU,
  ALT_SCREEN_ROUNDTRIP,
  LONG_OUTPUT_SCROLLBACK,
];
