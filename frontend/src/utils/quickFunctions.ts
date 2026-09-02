/**
 * 快捷功能纯函数（蓝图：终端「输入工作路径」等快捷插入）。
 *
 * 与 UI 解耦的纯文本组装/包裹逻辑，便于单测覆盖：
 * - buildAssociatedPathLines：把选中的工作路径组装为「关联工作路径：」行
 * - wrapBracketedPaste：bracketed paste 模式包裹（\x1b[200~ … \x1b[201~）
 */

/** 每条关联路径行的前缀（与 CLI 侧识别约定一致，勿随意改动）。 */
export const ASSOCIATED_PATH_PREFIX = '关联工作路径：';

/**
 * 把选中的绝对路径列表组装为「关联工作路径：<绝对路径>」逐行文本。
 *
 * - 保持传入顺序（= 用户勾选顺序）
 * - 每行一条，行间以 \n 连接，尾部不带换行
 * - 空列表返回空串
 */
export function buildAssociatedPathLines(paths: string[]): string {
  return paths.map((p) => `${ASSOCIATED_PATH_PREFIX}${p}`).join('\n');
}

/** bracketed paste 起始序列（DECSET 2004 应答侧）。 */
const BRACKETED_PASTE_START = '\x1b[200~';
/** bracketed paste 结束序列。 */
const BRACKETED_PASTE_END = '\x1b[201~';

/**
 * 以 bracketed paste 序列包裹文本。
 *
 * TUI 输入框（如 claudecode/pi 的对话框）在 bracketed paste 模式下把
 * 一次粘贴视作整体而非逐行提交：多行文本必须包裹后写入，否则每行
 * 换行都会被当作 Enter 提前提交。
 */
export function wrapBracketedPaste(text: string): string {
  return `${BRACKETED_PASTE_START}${text}${BRACKETED_PASTE_END}`;
}
