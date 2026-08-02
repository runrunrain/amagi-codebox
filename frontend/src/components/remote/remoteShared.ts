/**
 * 远程控制中心共享工具：错误分类文案（禁笼统失败，§5 错误状态规则）与时间格式化。
 */

export interface ClassifiedError {
  /** 分类名（用于测试断言与日志） */
  category:
    | 'port-conflict'
    | 'permission'
    | 'security-unavailable'
    | 'not-running'
    | 'storage'
    | 'invalid-input'
    | 'network'
    | 'unknown';
  /** 面向用户的分类文案 + 可执行动作 */
  message: string;
  /** 原始错误文本（供排查，一行） */
  detail: string;
}

function errText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === 'string') return err;
  try {
    return String(err);
  } catch {
    return '未知错误';
  }
}

/**
 * 把后端/绑定层错误分类为可执行文案。分类依据来自 internal/remote 与 app.go 的
 * 真实错误字符串（security state unavailable / durable sink / start remote server 等）。
 */
export function classifyRemoteError(err: unknown): ClassifiedError {
  const raw = errText(err);
  const t = raw.toLowerCase();

  if (t.includes('address already in use') || t.includes('bind:') || t.includes('port')) {
    return {
      category: 'port-conflict',
      message: '端口被占用或不可用：请更换监听端口后重试。',
      detail: raw,
    };
  }
  if (t.includes('permission denied') || t.includes('operation not permitted') || t.includes('access')) {
    return {
      category: 'permission',
      message: '系统权限不足：无法在该地址/端口上监听，请更换端口或以合适权限运行。',
      detail: raw,
    };
  }
  if (t.includes('security state unavailable')) {
    return {
      category: 'security-unavailable',
      message: '安全子系统未就绪：请确认远程服务已开启；若持续出现请重启应用。',
      detail: raw,
    };
  }
  if (t.includes('not running') || t.includes('server stopped')) {
    return {
      category: 'not-running',
      message: '远程服务未运行：请先在上方开启服务。',
      detail: raw,
    };
  }
  if (t.includes('durable sink') || t.includes('store:') || t.includes('disk')) {
    return {
      category: 'storage',
      message: '本地记录存储异常：事件可能未持久化，请检查配置目录与磁盘权限。',
      detail: raw,
    };
  }
  if (t.includes('invalid') || t.includes('required') || t.includes('out of valid range')) {
    return {
      category: 'invalid-input',
      message: '输入不合法：请检查地址、端口或设备标识后重试。',
      detail: raw,
    };
  }
  if (t.includes('timeout') || t.includes('deadline') || t.includes('connection')) {
    return {
      category: 'network',
      message: '网络/连接异常：请确认本机网络状态后重试。',
      detail: raw,
    };
  }
  return {
    category: 'unknown',
    message: '操作未完成（原因见下）：请按提示修正后重试。',
    detail: raw,
  };
}

/** 配对时间等完整时间戳：YYYY-MM-DD HH:mm */
export function formatDateTime(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** 事件记录时间：今天只显示 HH:mm:ss，跨天显示 MM-DD HH:mm */
export function formatEventTime(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  const p = (n: number) => String(n).padStart(2, '0');
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  if (sameDay) return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** 倒计时：MM:SS（等宽字体配合 tabular-nums 使用） */
export function formatCountdown(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}
