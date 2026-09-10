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

  // 优先检测权限拒绝（例如绑定受保护端口产生的 bind: permission denied）
  if (t.includes('permission denied') || t.includes('operation not permitted') || t.includes('access denied')) {
    return {
      category: 'permission',
      message: '系统权限不足：无法在该地址/端口上监听，请更换端口或以合适权限运行。',
      detail: raw,
    };
  }
  if (t.includes('address already in use') || t.includes('bind:') || t.includes('port')) {
    return {
      category: 'port-conflict',
      message: '端口被占用或不可用：请更换监听端口后重试。',
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

/**
 * 判断给定的主机地址是否为回环地址（127.0.0.1 / localhost 等）。
 * 回环监听导致局域网内的移动设备无法访问。
 */
export function isLoopbackHost(host: string | undefined): boolean {
  if (!host) return false;
  const h = host.trim().toLowerCase();
  return (
    h === '127.0.0.1' ||
    h === 'localhost' ||
    h === '::1' ||
    h === '[::1]' ||
    h.startsWith('127.')
  );
}

/**
 * 判断给定的主机地址是否为通配地址（0.0.0.0 / :: 等）。
 */
export function isWildcardHost(host: string | undefined): boolean {
  if (!host) return true;
  const h = host.trim();
  return h === '' || h === '0.0.0.0' || h === '::' || h === '[::]';
}

/**
 * 校验用户输入的局域网 IP 或主机名格式是否合法。
 * 允许 IPv4、标准主机名，以及带括号的 IPv6 地址；拒绝带路径、查询参数、端口或非法字符的输入。
 */
export function isValidIpOrHost(input: string | undefined): boolean {
  if (!input) return false;
  const s = input.trim();
  if (!s) return false;
  if (s.includes('/') || s.includes('?') || s.includes('#') || s.includes('@') || s.includes(' ')) {
    return false;
  }
  // 避免用户将端口粘入 host 输入框（例如 192.168.1.5:8680）
  if (s.includes(':') && !s.startsWith('[')) {
    const colons = s.split(':').length - 1;
    if (colons < 2) {
      return false;
    }
  }

  // 若字符串仅由纯数字与点组成，必须是完整的 4 段式 IPv4，不得误判为域名
  if (/^[\d.]+$/.test(s)) {
    const ipv4Regex = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
    const match = ipv4Regex.exec(s);
    if (!match) return false;
    const octets = [Number(match[1]), Number(match[2]), Number(match[3]), Number(match[4])];
    return octets.every((o) => o >= 0 && o <= 255);
  }

  // 标准主机名/域名校验 (RFC 1123)
  const hostRegex = /^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))*$/;
  if (hostRegex.test(s)) {
    // 顶级域或末尾标签不得为纯数字（避免 192.168.1 等残缺 IP 漏过）
    const parts = s.split('.');
    const lastPart = parts[parts.length - 1];
    if (/^\d+$/.test(lastPart)) {
      return false;
    }
    return true;
  }

  // 简易 IPv6 括号校验
  if (s.startsWith('[') && s.endsWith(']') && s.includes(':')) {
    return true;
  }

  return false;
}

/**
 * 宿主可用性探测（GET /api/remote/v1/host/summary）结果态。
 * P2-B 降级契约：探测失败时服务端返回保守 200 —— serverVersion === "unknown"
 * 且全 CLI Available:false；读面（会话列表）不受影响。
 */
export type HostSummaryProbeState = 'normal' | 'degraded' | 'unreachable';

/**
 * 从远程状态绑定透出的 hostSummaryDegraded（P3-B R2：v1 HostSummary 缓存
 * 最近错误态，GetRemoteWebUIStatus / GetRemoteStatus 响应字段）映射为自检
 * 卡探测态，替代桌面无法直读的 /host/summary fetch 探测（原方案在桌面
 * WebView 中恒为 unreachable：无设备 Cookie 且 Origin 不在 v1 允许表）。
 *
 * 映射规则：服务未运行 → unreachable（卡片按「未运行」渲染）；状态绑定拉取
 * 失败/缺位（status 为 null/undefined）→ unreachable（无法直读，诚实降级
 * 不伪造）；hostSummaryDegraded=true → degraded；其余 → normal。
 */
export function hostSummaryStateFromStatus(
  running: boolean,
  status: { hostSummaryDegraded?: boolean } | null | undefined,
): HostSummaryProbeState {
  if (!running) return 'unreachable';
  if (!status) return 'unreachable';
  return status.hostSummaryDegraded ? 'degraded' : 'normal';
}

/**
 * 合成移动端配对 Web 完整访问 URL。
 * URL 规范对齐 mobile/src/views/ConnectPage 与 PairingCard 原有规范：
 * http://<host>:<port>/#/connect?code=...&expiresAt=...
 */
export function formatPairingUrl(
  host: string,
  port: number,
  code: string,
  expiresAt: string,
): string {
  const cleanHost = (host || '').trim();
  const hostPart =
    cleanHost.includes(':') && !cleanHost.startsWith('[') ? `[${cleanHost}]` : cleanHost;
  const baseUrl = `http://${hostPart}:${port}`;
  const params = new URLSearchParams({ code, expiresAt });
  return `${baseUrl}/#/connect?${params.toString()}`;
}

/**
 * 读取配对卡持久化的手动局域网地址（自检卡展示访问基准地址时优先采用）。
 * 无记录或记录非法时返回空串。
 */
export function readCustomLanHost(): string {
  try {
    const v = localStorage.getItem(CUSTOM_LAN_HOST_STORAGE_KEY);
    return v && isValidIpOrHost(v) ? v.trim() : '';
  } catch {
    return '';
  }
}

/**
 * 一键复制纯文本到剪贴板，支持标准 navigator.clipboard 与回退 textarea 方案。
 */
export async function copyTextToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 降级使用传统 document.execCommand 方案
  }
  try {
    const el = document.createElement('textarea');
    el.value = text;
    el.setAttribute('readonly', '');
    el.style.position = 'fixed';
    el.style.left = '-9999px';
    el.style.top = '-9999px';
    document.body.appendChild(el);
    el.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(el);
    return ok;
  } catch {
    return false;
  }
}

/** 配对卡手动局域网地址在 localStorage 的持久化键（自检卡共用读取） */
export const CUSTOM_LAN_HOST_STORAGE_KEY = 'amagi.remote.customLanHost';

/** 自检卡项严重度级别 */
export type SelfCheckSeverity = 'ok' | 'warning' | 'danger' | 'info' | 'stopped';

/** 自检项数据结构 */
export interface SelfCheckItem {
  id: string;
  label: string;
  valueText: string;
  status: SelfCheckSeverity;
  detail: string;
  actionText?: string;
  actionType?: 'goto-service' | 'goto-lan' | 'set-host-wildcard' | 'refresh';
}

