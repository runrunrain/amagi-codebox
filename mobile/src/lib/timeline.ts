/**
 * lib/timeline.ts — PG-03 内容转化（M2-C，§4.3.1 七类组件映射）
 * ---------------------------------------------------------------------------
 * 权威依据：Task Contract M2-C（§4.3.1 内容转化内联条款 + CHG-20260801-05：
 * 内容转化是唯一主形态，原始终端仅为按需诊断视图）。P5《视觉交互设计 v1.2》
 * 原文档缺失（M2-B 已确认），以下检测启发式为本任务定义并如实声明：
 *
 *   · prompt-action 权限确认：问题行（?/？结尾或含 Do you want to / 是否）+
 *     编号选项（Yes/No 语义）→ 按钮组；单行 (y/n)/[Y/n] → 是/否按钮。
 *   · option 选项菜单：≥2 个连续编号行（1. / 2. …）且不构成权限确认 →
 *     单选即答（点按发送 "<n>\r"）。
 *   · tool 工具调用：复用 parser/matchers/toolMatcher 的 isToolBlock 检测与
 *     buildToolBlock 清洗（不重写匹配规则，避免漂移）。
 *   · error 错误：行首 ✗/×/Error 或含 failed/Traceback → danger 左边条 +
 *     原因 + 下一步（按内容关键词给可执行指引）。
 *   · progress 进度：单行 spinner（braille 字符）/百分比/"Running…" →
 *     文字化进度；控制者"停止运行"按钮由组件层按控制权渲染。
 *   · fold 长输出：连续普通文本 ≥ FOLD_MIN_LINES（12）行 → 折叠 + 摘要行 +
 *     展开等宽全文 + 复制。
 *   · mono 等宽兜底：其余短文本 → 等宽呈现 + 诊断视图指引（组件层）。
 *
 * 本模块为纯函数：无副作用、无网络、无 store 依赖；输入为已按行切分的
 * 终端文本（可含 ANSI，内部负责剥离），输出为可渲染的内容块数组。
 * ---------------------------------------------------------------------------
 */

import { hasAnsi, stripAnsi } from '../utils/stripAnsi';
import { buildToolBlock, isToolBlock } from '../parser/matchers/toolMatcher';
import type { AppType } from '../types/terminal';
import type { SessionState } from './contract';

// ---------------------------------------------------------------------------
// 渲染条目类型（七类内容组件 + 用户指令 + 边界/缺口标记）
// ---------------------------------------------------------------------------

export type TimelineItemKind =
  | 'user'
  | 'prompt-action'
  | 'option'
  | 'fold'
  | 'tool'
  | 'error'
  | 'progress'
  | 'mono'
  | 'boundary'
  | 'gap';

export interface TimelineItemBase {
  id: string;
  kind: TimelineItemKind;
}

/** 可答选项：label 展示；input 为发送给会话的完整载荷（编号选项含回车，y/n 为单按键）。 */
export interface AnswerOption {
  key: string;
  label: string;
  input: string;
}

export interface PromptActionItem extends TimelineItemBase {
  kind: 'prompt-action';
  question: string;
  options: AnswerOption[];
}

export interface OptionItem extends TimelineItemBase {
  kind: 'option';
  title: string | null;
  options: AnswerOption[];
}

export interface FoldItem extends TimelineItemBase {
  kind: 'fold';
  summary: string;
  lineCount: number;
  fullText: string;
}

export interface ToolItem extends TimelineItemBase {
  kind: 'tool';
  toolName: string;
  title: string;
  detail: string | null;
}

export interface ErrorItem extends TimelineItemBase {
  kind: 'error';
  reason: string;
  detail: string | null;
  nextStep: string;
}

export interface ProgressItem extends TimelineItemBase {
  kind: 'progress';
  text: string;
}

export interface MonoItem extends TimelineItemBase {
  kind: 'mono';
  text: string;
  /** 原文含 ANSI 控制序列：提示可用诊断视图查看终端级细节。 */
  hadControlChars: boolean;
}

/** 七类内容块（transformOutputLines 的输出）。 */
export type ContentItem =
  | PromptActionItem
  | OptionItem
  | FoldItem
  | ToolItem
  | ErrorItem
  | ProgressItem
  | MonoItem;

/** 用户指令（coral 左边条；draft 确认发送后才出现）。 */
export interface UserItem extends TimelineItemBase {
  kind: 'user';
  text: string;
}

/** 重启边界（PR-05：原位渲染，不占内容）。 */
export interface BoundaryItem extends TimelineItemBase {
  kind: 'boundary';
  seq: number;
  state: SessionState;
  occurredAt: string;
}

/** 历史缺口（GapMarker：原位标记；fillable=可尝试补齐；exhausted=已确认不可补齐）。 */
export interface GapItem extends TimelineItemBase {
  kind: 'gap';
  fromSeq: number;
  toSeq: number;
  fillable: boolean;
  exhausted: boolean;
}

export type TimelineItem = ContentItem | UserItem | BoundaryItem | GapItem;

// ---------------------------------------------------------------------------
// 检测常量（启发式，如实声明）
// ---------------------------------------------------------------------------

/** 长输出折叠阈值（行）。 */
export const FOLD_MIN_LINES = 12;

/** 编号选项行：「1. Yes」「2) No」「❯ 1. Allow」。 */
const NUMBERED_OPTION_RE = /^\s*❯?\s*(\d{1,2})[.)]\s+(.+?)\s*$/;

/** 权限确认问题行。 */
const QUESTION_RE = /[?？]\s*$|^\s*(?:do you want to|are you sure|是否|确认)/i;

/** Yes/No 语义选项。 */
const YES_LIKE_RE = /^(yes|y|allow|ok|confirm|proceed|是|允许|确认)/i;
const NO_LIKE_RE = /^(no|n|deny|cancel|skip|否|拒绝|取消)/i;

/** 单行 y/n 确认。 */
const YN_RE = /[\[(]\s*(y\s*\/\s*n|yes\s*\/\s*no|n\s*\/\s*y)\s*[\])]/i;

/** 错误行。 */
const ERROR_LINE_RE = /^\s*(?:✗|×|✘|error\b|Error\b|ERROR\b|fatal\b|FAIL(?:ED)?\b|exception\b|Traceback\b)/;

/** 错误延续行（缩进/树形/堆栈）。 */
const ERROR_CONT_RE = /^\s+(?:\S)|^\s*(?:at\s|│|┃|⎿|↳|\|)/;

/** 进度行（剥离前判 braille spinner；剥离后判百分比/动作词省略号）。 */
const SPINNER_CHAR_RE =/[⠀-⣿◐◓◑◒]/u;
const PROGRESS_TEXT_RE = /^\s*(?:\d{1,3}\s*%|\S.*\b(?:running|working|thinking|processing|loading|executing|building|installing|compiling|waiting)\b\s*[…·.]{0,3}\s*)$/i;

/** 工具延续行。 */
const TOOL_CONT_RE = /^\s*(?:⎿|↳|│|┃|└|├|\s+\S)/;

/** 编号选项可能的最大行数（权限确认/选项菜单）。 */
const MAX_OPTION_LINES = 9;

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

interface ScanLine {
  raw: string;
  clean: string; // stripAnsi 后
}

function toScanLines(lines: string[]): ScanLine[] {
  return lines.map((raw) => ({ raw, clean: stripAnsi(raw) }));
}

function isBlank(l: ScanLine): boolean {
  return l.clean.trim().length === 0;
}

function parseNumberedOptions(lines: ScanLine[], start: number): { n: number; label: string }[] {
  const out: { n: number; label: string }[] = [];
  for (let i = start; i < lines.length && out.length < MAX_OPTION_LINES; i++) {
    const m = lines[i].clean.match(NUMBERED_OPTION_RE);
    if (!m) break;
    out.push({ n: Number(m[1]), label: m[2].trim() });
  }
  // 编号必须连续（1,2,3… 或从任意起点的 +1 连续），否则视为普通文本。
  for (let i = 1; i < out.length; i++) {
    if (out[i].n !== out[i - 1].n + 1) return [];
  }
  return out;
}

function looksYesNo(options: { label: string }[]): boolean {
  if (options.length === 0) return false;
  return YES_LIKE_RE.test(options[0].label) && (options.length === 1 || options.some((o) => NO_LIKE_RE.test(o.label)));
}

function nextStepForError(text: string): string {
  if (/\b(?:api[\s_-]?key|token|unauthorized|401|forbidden|403|credential)\b/i.test(text)) {
    return '下一步：请回桌面端检查该 CLI 的服务配置与凭据（设置 › 远程访问），然后重试。';
  }
  if (/\b(?:econn|etimedout|network|timeout|timed out|unreachable|dns)\b/i.test(text)) {
    return '下一步：请确认这台设备与桌面端连接正常，然后重试。';
  }
  if (/\b(?:eacces|permission denied|eperm)\b/i.test(text)) {
    return '下一步：请在桌面端检查相关工作目录的读写权限，然后重试。';
  }
  if (/\b(?:enoent|not found|no such file)\b/i.test(text)) {
    return '下一步：请确认引用的文件或命令在桌面端存在，然后重试。';
  }
  return '下一步：可展开查看完整输出；需要终端级细节时，从右上角菜单打开诊断视图。';
}

// ---------------------------------------------------------------------------
// 主转换：行数组 → 内容块数组（纯函数）
// ---------------------------------------------------------------------------

export interface TransformOptions {
  /** 块 id 前缀（同一会话内不同 segment 需不同前缀防冲突）。 */
  idPrefix: string;
  /** toolMatcher 所需的宿主 CLI 类型（未知用 generic）。 */
  appType?: AppType;
}

export function transformOutputLines(lines: string[], options: TransformOptions): ContentItem[] {
  const { idPrefix, appType = 'generic' } = options;
  const scan = toScanLines(lines);
  const items: ContentItem[] = [];
  let seq = 0;
  const nextId = (tag: string) => `${idPrefix}-${tag}-${seq++}`;

  let i = 0;
  let plainRun: ScanLine[] = [];

  const flushPlain = () => {
    if (plainRun.length === 0) return;
    const text = plainRun.map((l) => l.clean.trimEnd()).join('\n');
    const hadControl = plainRun.some((l) => hasAnsi(l.raw));
    if (plainRun.length >= FOLD_MIN_LINES) {
      const summary = plainRun[0].clean.trim() || `（${plainRun.length} 行输出）`;
      items.push({ id: nextId('fold'), kind: 'fold', summary, lineCount: plainRun.length, fullText: text });
    } else {
      items.push({ id: nextId('mono'), kind: 'mono', text, hadControlChars: hadControl });
    }
    plainRun = [];
  };

  while (i < scan.length) {
    const line = scan[i];
    if (isBlank(line)) {
      flushPlain();
      i++;
      continue;
    }

    // --- 单行 y/n 权限确认 ---
    if (YN_RE.test(line.clean) && /[?？]/.test(line.clean)) {
      flushPlain();
      items.push({
        id: nextId('prompt'),
        kind: 'prompt-action',
        question: line.clean.trim(),
        options: [
          { key: 'yes', label: '是（y）', input: 'y' },
          { key: 'no', label: '否（n）', input: 'n' },
        ],
      });
      i++;
      continue;
    }

    // --- 编号选项（权限确认 or 选项菜单）---
    const numbered = parseNumberedOptions(scan, i);
    if (numbered.length >= 1) {
      // 向前找最近一条非空普通行作为问题/标题（须紧邻：上一非空行）。
      let q: string | null = null;
      let j = i - 1;
      while (j >= 0 && isBlank(scan[j])) j--;
      if (j >= 0 && !NUMBERED_OPTION_RE.test(scan[j].clean)) {
        q = scan[j].clean.trim();
      }
      if (numbered.length >= 1 && q !== null && QUESTION_RE.test(q) && looksYesNo(numbered)) {
        // 权限确认：问题行已被并入 plainRun，需移除其最后一行。
        flushPlainExceptLast();
        items.push({
          id: nextId('prompt'),
          kind: 'prompt-action',
          question: q,
          options: numbered.map((o) => ({ key: `opt-${o.n}`, label: o.label, input: `${o.n}\r` })),
        });
        i += numbered.length;
        continue;
      }
      if (numbered.length >= 2) {
        flushPlainExceptLast(q !== null && !QUESTION_RE.test(q) ? true : false);
        items.push({
          id: nextId('option'),
          kind: 'option',
          title: q !== null && !QUESTION_RE.test(q) ? q : null,
          options: numbered.map((o) => ({ key: `opt-${o.n}`, label: o.label, input: `${o.n}\r` })),
        });
        i += numbered.length;
        continue;
      }
      // 单个编号行且非权限确认 → 落入普通文本。
    }

    // --- 工具调用（复用 toolMatcher）---
    if (isToolBlock([line.clean])) {
      flushPlain();
      const blockLines: string[] = [line.clean];
      let k = i + 1;
      while (k < scan.length && !isBlank(scan[k]) && TOOL_CONT_RE.test(scan[k].clean) && !isToolBlock([scan[k].clean])) {
        blockLines.push(scan[k].clean);
        k++;
      }
      const built = buildToolBlock({ appType, lines: blockLines, raw: blockLines.join('\n'), createdAt: seq });
      items.push({
        id: nextId('tool'),
        kind: 'tool',
        toolName: built.toolName,
        title: built.title,
        detail: built.summary ?? null,
      });
      i = k;
      continue;
    }

    // --- 错误 ---
    if (ERROR_LINE_RE.test(line.clean)) {
      flushPlain();
      const errLines: string[] = [line.clean.trim()];
      let k = i + 1;
      while (k < scan.length && !isBlank(scan[k]) && ERROR_CONT_RE.test(scan[k].clean) && !ERROR_LINE_RE.test(scan[k].clean)) {
        errLines.push(scan[k].clean.trimEnd());
        k++;
      }
      const full = errLines.join('\n');
      items.push({
        id: nextId('error'),
        kind: 'error',
        reason: errLines[0],
        detail: errLines.length > 1 ? errLines.slice(1).join('\n') : null,
        nextStep: nextStepForError(full),
      });
      i = k;
      continue;
    }

    // --- 进度（单行；spinner 字符需在剥离前判断）---
    if (SPINNER_CHAR_RE.test(line.raw) || PROGRESS_TEXT_RE.test(line.clean)) {
      flushPlain();
      const text = line.clean.replace(SPINNER_CHAR_RE, '').trim() || '正在运行';
      items.push({ id: nextId('progress'), kind: 'progress', text });
      i++;
      continue;
    }

    plainRun.push(line);
    i++;
  }
  flushPlain();

  return items;

  /** flush plainRun 但保留最后一行（被权限确认/选项菜单吸收为问题/标题）。 */
  function flushPlainExceptLast(absorb: boolean = true) {
    if (plainRun.length === 0) return;
    if (absorb) plainRun = plainRun.slice(0, -1);
    flushPlain();
  }
}
