/**
 * provider 配置编辑器共享工具函数与常量：
 * - `genId`：为动态列表项生成唯一非空 ID
 * - `hexToRgb`：解析 3 位或 6 位十六进制颜色字符串
 * - `EDITOR_MODE_OPTIONS`：可视化 / JSON 双模式切换选项
 * - `copyConfigPath`：复制配置文件路径到剪贴板并触发 toast 反馈
 */

export interface RgbColor {
  r: number;
  g: number;
  b: number;
}

export interface EditorModeOption {
  value: string;
  label: string;
}

export interface ConfigPathToast {
  showSuccess: (msg: string) => void;
  showError: (msg: string) => void;
}

/** 共享的「可视化 / JSON」模式切换选项（供 mode 为 'visual' | 'json' 的编辑器复用）。 */
export const EDITOR_MODE_OPTIONS: EditorModeOption[] = [
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
];

let fallbackIdSeq = 0;

/**
 * 生成组件内部使用的唯一非空 ID（用于 v-for `:key` 等场景）。
 * 优先使用 `crypto.randomUUID()`，不可用时回退至递增序号 + 随机串 + 时间戳。
 */
export function genId(prefix?: string): string {
  const hasRandomUuid =
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function';
  const core = hasRandomUuid
    ? crypto.randomUUID()
    : `${++fallbackIdSeq}-${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`;
  if (prefix) {
    return `${prefix}-${core}`;
  }
  return hasRandomUuid ? core : `id-${core}`;
}

/**
 * 解析 `#rgb` 或 `#rrggbb` 格式颜色为 RGB 分量，不合法输入返回 `null`。
 */
export function hexToRgb(hex: string): RgbColor | null {
  if (typeof hex !== 'string') return null;
  const match = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec(hex);
  if (!match) return null;
  const digits = match[1];
  const full =
    digits.length === 3
      ? digits[0] + digits[0] + digits[1] + digits[1] + digits[2] + digits[2]
      : digits;
  return {
    r: parseInt(full.slice(0, 2), 16),
    g: parseInt(full.slice(2, 4), 16),
    b: parseInt(full.slice(4, 6), 16),
  };
}

/**
 * 复制配置文件路径到剪贴板并给出 toast 反馈。
 * 路径为空时静默返回；优先使用 `navigator.clipboard.writeText`，失败时在浏览器环境回退至 textarea + execCommand。
 */
export async function copyConfigPath(
  path: string | undefined,
  toast: ConfigPathToast,
): Promise<void> {
  if (!path || !path.trim()) return;
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(path);
      toast.showSuccess('路径已复制到剪贴板');
      return;
    }
  } catch {
    // Clipboard API 不可用或被拒绝时尝试传统 DOM 复制回退
  }

  if (typeof document !== 'undefined' && document.body) {
    let textarea: HTMLTextAreaElement | null = null;
    try {
      textarea = document.createElement('textarea');
      textarea.value = path;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.left = '-9999px';
      textarea.style.top = '-9999px';
      document.body.appendChild(textarea);
      textarea.select();
      if (document.execCommand('copy')) {
        toast.showSuccess('路径已复制到剪贴板');
        return;
      }
    } catch {
      // 回退复制失败，走下方统一错误提示
    } finally {
      if (textarea && textarea.parentNode) {
        textarea.parentNode.removeChild(textarea);
      }
    }
  }

  toast.showError('复制失败');
}
