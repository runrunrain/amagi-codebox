/**
 * Quota display model — 额度展示纯逻辑（设计文档《额度查询与常显》§6/§7）
 *
 * 为什么独立成纯 TS 模块：frontend Vitest 是 node 环境、无 DOM/vue 插件
 * （见 vitest.config.ts 注释），QuotaBar/QuotaCard/Strip 的渲染决策
 * （阈值分档、倒计时文案、六态文案、摘要映射）集中在这里保持可测。
 * 组件层只做模板绑定，不内嵌分支逻辑。
 */

import { config } from '../../../wailsjs/go/models';

export type ProviderQuotaEntry = config.ProviderQuotaEntry;
export type QuotaWindow = config.QuotaWindow;
export type QuotaBalance = config.QuotaBalance;

// === 家族常量 / Family constants ===
export const QUOTA_FAMILY_GLM_BIGMODEL = 'glm-bigmodel';
export const QUOTA_FAMILY_GLM_ZAI = 'glm-zai';
export const QUOTA_FAMILY_DEEPSEEK = 'deepseek';
export const QUOTA_FAMILY_CODEX_SUB = 'codex-sub';
export const QUOTA_FAMILY_OPENCODE_ZEN = 'opencode-zen';
export const QUOTA_FAMILY_OPENROUTER = 'openrouter';

/** 支持额度查询的三家家族（「其他提供商」聚合卡排除判定用） */
export const SUPPORTED_QUOTA_FAMILIES = new Set([
  QUOTA_FAMILY_GLM_BIGMODEL,
  QUOTA_FAMILY_GLM_ZAI,
  QUOTA_FAMILY_DEEPSEEK,
  QUOTA_FAMILY_CODEX_SUB,
  QUOTA_FAMILY_OPENCODE_ZEN,
  QUOTA_FAMILY_OPENROUTER,
]);

/** 额度页卡片头家族显示名（设计 §6 线框） */
export function familyDisplayName(family: string): string {
  switch (family) {
    case QUOTA_FAMILY_GLM_BIGMODEL:
      return 'GLM Coding Plan';
    case QUOTA_FAMILY_GLM_ZAI:
      return 'GLM Coding Plan';
    case QUOTA_FAMILY_DEEPSEEK:
      return 'DeepSeek';
    case QUOTA_FAMILY_CODEX_SUB:
      return 'Codex 订阅';
    case QUOTA_FAMILY_OPENCODE_ZEN:
      return 'OpenCode Zen';
    case QUOTA_FAMILY_OPENROUTER:
      return 'OpenRouter';
    default:
      return '额度';
  }
}

/** GLM 双通道副标（bigmodel / z.ai，区分同族两卡） */
export function familySubLabel(family: string): string {
  if (family === QUOTA_FAMILY_GLM_BIGMODEL) return 'bigmodel';
  if (family === QUOTA_FAMILY_GLM_ZAI) return 'z.ai';
  return '';
}

/** Web 平面 strip 迷你摘要家族短名（设计 §7：glm-*→GLM） */
export function familyShortName(family: string): string {
  if (family === QUOTA_FAMILY_GLM_BIGMODEL || family === QUOTA_FAMILY_GLM_ZAI) return 'GLM';
  if (family === QUOTA_FAMILY_DEEPSEEK) return 'DeepSeek';
  if (family === QUOTA_FAMILY_CODEX_SUB) return 'Codex';
  if (family === QUOTA_FAMILY_OPENCODE_ZEN) return 'Zen';
  if (family === QUOTA_FAMILY_OPENROUTER) return 'OpenRouter';
  return family || '—';
}

/** 来源文案 map：glm-api→API、deepseek-api→API、codex-session-file→最近会话 */
const SOURCE_LABELS: Record<string, string> = {
  'glm-api': 'API',
  'deepseek-api': 'API',
  'codex-session-file': '最近会话',
  'opencode-zen-api': 'API',
  'openrouter-api': 'API',
};

/**
 * 会话 → 额度查询的 provider 名解析（v1.3.80 修复：pi 会话额度获取失败）。
 *
 * 字段语义（reserveLaunchSession 实测）：pi/omp/codex 会话的
 * SessionInfo.Provider 存的是 AppType 字面量（"pi"/"omp"/"codex"），
 * 真实 CodeBox provider 名落在 Preset 字段；claudecode/opencode 会话
 * Provider 字段即 provider 名。用错字段拿 "pi" 去探测会命中后端
 * 「provider 不存在」 error（不落盘不留日志），条上常显「额度获取失败」。
 *
 * codex 会话未选 provider（preset 空）时回落 "codex" 固定键——
 * 订阅额度与具体 provider 无关，语义正交。
 */
export function sessionQuotaProvider(s: {
  appType?: string;
  provider?: string;
  preset?: string;
}): string {
  const appType = (s.appType ?? '').toLowerCase();
  if (appType === 'pi' || appType === 'omp') {
    return (s.preset ?? '').trim();
  }
  if (appType === 'codex') {
    const name = (s.preset ?? '').trim();
    return name || 'codex';
  }
  return (s.provider ?? '').trim();
}

export function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] ?? source;
}

// === 阈值分档 / Threshold tones（设计 §6：<70 正常 / 70–90 warn / >90 danger）===

export type QuotaTone = 'normal' | 'warn' | 'danger';

/** 70 与 90 都落在 warn 档（含边界），>90 才进 danger */
export function quotaTone(usedPercent: number): QuotaTone {
  if (!Number.isFinite(usedPercent)) return 'normal';
  if (usedPercent > 90) return 'danger';
  if (usedPercent >= 70) return 'warn';
  return 'normal';
}

/** 进度条宽度取值：负数/NaN 归 0，超 100 封顶 */
export function clampPercent(usedPercent: number): number {
  if (!Number.isFinite(usedPercent) || usedPercent < 0) return 0;
  return Math.min(100, usedPercent);
}

// === 窗口选取 / Window slots ===

/** 主窗口：primary；GLM 的 time_limit 归主窗口（任务 §5） */
export function primaryWindowOf(entry: ProviderQuotaEntry | null | undefined): QuotaWindow | null {
  const windows = entry?.windows ?? [];
  return windows.find((w) => w?.kind === 'primary' || w?.kind === 'time_limit') ?? null;
}

/** 次窗口：secondary（周窗口） */
export function secondaryWindowOf(entry: ProviderQuotaEntry | null | undefined): QuotaWindow | null {
  const windows = entry?.windows ?? [];
  return windows.find((w) => w?.kind === 'secondary') ?? null;
}

/** 第三窗口：tertiary（Zen 月窗口，v1.3.86 起新增） */
export function tertiaryWindowOf(entry: ProviderQuotaEntry | null | undefined): QuotaWindow | null {
  const windows = entry?.windows ?? [];
  return windows.find((w) => w?.kind === 'tertiary') ?? null;
}

/** 窗口显示名：secondary 统称「周窗口」，主窗口按 window_minutes 推导（300→5h） */
export function windowLabel(w: QuotaWindow | null | undefined): string {
  if (!w) return '';
  // v1.3.81：后端实证推导的语义标签优先（"5h"/"月"/"MCP·月"）；
  // secondary 恒标「周」的旧假设在无周额度的 max 套餐上错位。
  if (w.label) return `${w.label} 窗口`;
  if (w.kind === 'secondary') return '周窗口';
  const minutes = w.window_minutes ?? 300;
  if (minutes >= 1440 && minutes % 1440 === 0) return `${minutes / 1440}d 窗口`;
  return `${Math.max(1, Math.round(minutes / 60))}h 窗口`;
}

/** strip 迷用短标签：300→5h、10080→周，其余按小时折算 */
export function windowShortLabel(w: QuotaWindow | null | undefined): string {
  if (!w) return '';
  // v1.3.81：GLM 后端实证推导的语义标签优先（"5h"/"月"/"MCP·月"），
  // 修复旧版 primary=5h/secondary=周硬编码在无周额度的 max 套餐上错位。
  if (w.label) return w.label;
  if (w.window_minutes === 10080) return '周';
  const minutes = w.window_minutes ?? 300;
  return `${Math.max(1, Math.round(minutes / 60))}h`;
}

// === 时间格式化 / Time formatting ===

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** RFC3339 → 本地 HH:mm（卡脚「[↻] HH:mm · 来源」与 stale 标注用） */
export function formatClock(iso: string): string {
  if (!iso) return '';
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return '';
  const d = new Date(ms);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

/**
 * 重置时刻文案（v1.3.82 重写：绝对时刻优先，主上报「即将重置」不准）：
 * - 过期（resets_at <= now）：「重置时刻已过」——Codex 本地会话数据的 5h/周窗
 *   经常已过期，旧文案「即将重置」误导；诚实陈述而非臆造新时刻。
 * - 未来：今日/明日给时刻，7 天内给周几，更远给日期。
 * 挂载时计算一次，不轮询。
 */
export function formatResetCountdown(resetsAtSec?: number, nowMs: number = Date.now()): string {
  if (!resetsAtSec || resetsAtSec <= 0) return '';
  const target = resetsAtSec * 1000;
  if (target <= nowMs) return '重置时刻已过';
  const d = new Date(target);
  const hm = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
  const now = new Date(nowMs);
  const dayDiff = dayDiffDays(now, d);
  if (dayDiff === 0) return `今日 ${hm} 重置`;
  if (dayDiff === 1) return `明日 ${hm} 重置`;
  if (dayDiff < 7) {
    const weekday = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][d.getDay()] ?? '';
    return `${weekday} ${hm} 重置`;
  }
  return `${d.getMonth() + 1}月${d.getDate()}日 ${hm} 重置`;
}

/** 当地时区下「目标日 - 今日」的日差（按自然日切分，非 24h 段）。 */
function dayDiffDays(from: Date, to: Date): number {
  const f = new Date(from.getFullYear(), from.getMonth(), from.getDate()).getTime();
  const t = new Date(to.getFullYear(), to.getMonth(), to.getDate()).getTime();
  return Math.round((t - f) / 86400000);
}

/** 重置时刻短形式（strip 迷你摘要用）：今日 HH:mm / 明日 HH:mm / 周四 / M月d日。 */
export function formatResetShort(resetsAtSec?: number, nowMs: number = Date.now()): string {
  if (!resetsAtSec || resetsAtSec <= 0) return '';
  const target = resetsAtSec * 1000;
  if (target <= nowMs) return '已过';
  const d = new Date(target);
  const hm = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
  const now = new Date(nowMs);
  const dayDiff = dayDiffDays(now, d);
  if (dayDiff === 0) return `今日 ${hm}`;
  if (dayDiff === 1) return `明日 ${hm}`;
  if (dayDiff < 7) {
    return ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][d.getDay()] ?? '';
  }
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}

/** probed_at 距今毫秒；解析失败返回 NaN */
export function probedAgeMs(entry: ProviderQuotaEntry | null | undefined, nowMs: number = Date.now()): number {
  if (!entry?.probed_at) return NaN;
  const ms = Date.parse(entry.probed_at);
  if (Number.isNaN(ms)) return NaN;
  return nowMs - ms;
}

/** ok 态数据陈旧标注（>30min 黄点「数据来自 HH:mm」） */
export function isStaleOk(entry: ProviderQuotaEntry | null | undefined, staleAfterMin = 30): boolean {
  if (!entry || entry.status !== 'ok') return false;
  const age = probedAgeMs(entry);
  if (Number.isNaN(age)) return false;
  return age > staleAfterMin * 60 * 1000;
}

// === 余额排版 / Balance formatting ===

/** 原币种显示（设计 §8：不引入汇率换算）：CNY→¥ 前缀，其余原样带币种码 */
export function formatBalanceAmount(balance: QuotaBalance | null | undefined): string {
  if (!balance || !Number.isFinite(balance.total)) return '—';
  const amount = balance.total.toFixed(2);
  return balance.currency === 'CNY' ? `¥${amount}` : `${balance.currency} ${amount}`;
}

/**
 * 余额 headline（v1.3.86）：OpenRouter 口径（remaining 字段存在，含 0）显示
 * 剩余金额；否则显示 total（DeepSeek 语义不变）。
 */
export function formatBalanceHeadline(balance: QuotaBalance | null | undefined): string {
  if (!balance) return '—';
  if (typeof balance.remaining === 'number') {
    return formatBalanceAmount({ ...balance, total: balance.remaining });
  }
  // 降级口径（仅有 used、无上限分母）：不硬造总额，headline 留空由明细承接
  if (typeof balance.used === 'number' && !(balance.total > 0)) return '—';
  return formatBalanceAmount(balance);
}

/** 余额明细行（OpenRouter 口径）：「已用 X · 共 Y」；无 used 字段返回空串 */
export function formatBalanceDetail(balance: QuotaBalance | null | undefined): string {
  if (!balance || typeof balance.used !== 'number') return '';
  const used = formatBalanceAmount({ ...balance, total: balance.used });
  if (Number.isFinite(balance.total) && balance.total > 0) {
    return `已用 ${used} · 共 ${formatBalanceAmount(balance)}`;
  }
  return `已用 ${used}`;
}

// === 灰卡文案矩阵 / Gray-state matrix（设计 §6 状态矩阵）===

export function grayCardText(entry: ProviderQuotaEntry | null | undefined): string {
  if (!entry) return '暂无额度数据';
  switch (entry.status) {
    case 'no_plan':
      return '该 Key 未开通对应套餐';
    case 'no_key':
      return '未配置 API Key';
    case 'unsupported':
      return '该服务暂不支持额度查询';
    case 'error':
      return entry.message || '额度获取失败';
    default:
      return entry.message || '暂无额度数据';
  }
}

// === Codex credits 徽标 ===
// P1 模型无独立 credits 字段：后端把结论编码进 message（quota_probe.go：
// "credits: 无限" / "credits: 有" / "credits: 无"），前端解析呈现。

export interface CreditsBadge {
  text: string;
  has: boolean;
}

export function codexCreditsBadge(entry: ProviderQuotaEntry | null | undefined): CreditsBadge | null {
  if (!entry || entry.family !== QUOTA_FAMILY_CODEX_SUB) return null;
  const m = /^credits:\s*(无限|有|无)/.exec(entry.message ?? '');
  if (!m) return null;
  if (m[1] === '无') return { text: '无 credits', has: false };
  return { text: m[1] === '无限' ? '无限 credits' : '含 credits', has: true };
}

// === Web 平面 strip 摘要 / Strip summary（设计 §7）===

export type QuotaStripTone = 'data' | 'error' | 'muted';

export interface QuotaStripSummary {
  /** 迷你摘要正文（不含「额度」前缀，由组件拼） */
  text: string;
  tone: QuotaStripTone;
  /** unsupported/no_key/no_plan：按钮禁用 + title 说明原因 */
  disabled: boolean;
  title: string;
}

/**
 * strip 常驻态摘要：
 * - 窗口型 ok：「GLM · 62% 5h」（剩余 % = 100-已用）
 * - 余额型 ok：「DeepSeek · ¥128.50」
 * - error：「额度获取失败」（点击重试语义，由组件实现）
 * - unsupported / no_key / no_plan / 无条目：「额度不可查」灰字禁用
 */
export function quotaStripSummary(entry: ProviderQuotaEntry | null | undefined): QuotaStripSummary {
  if (!entry) {
    return { text: '额度不可查', tone: 'muted', disabled: true, title: '暂无额度数据' };
  }
  if (entry.status === 'error') {
    return { text: '额度获取失败', tone: 'error', disabled: false, title: entry.message || '额度获取失败' };
  }
  if (entry.status === 'ok') {
    const primary = primaryWindowOf(entry);
    if (primary) {
      const remain = Math.max(0, Math.round(100 - clampPercent(primary.used_percent)));
      // v1.3.82：常驻摘要附主窗口重置短时刻（主上报 web 视图也要看到重置时间）
      const reset = formatResetShort(primary.resets_at);
      return {
        text: `${familyShortName(entry.family)} · ${remain}% ${windowShortLabel(primary)}${reset ? ` · ${reset}` : ''}`,
        tone: 'data',
        disabled: false,
        title: '',
      };
    }
    if (entry.balance) {
      // diting F-2：降级仅 used 形态 headline='—' 时改拼「已用 X」，不让已有信息丢失
      const headline = formatBalanceHeadline(entry.balance);
      const amount =
        headline === '—' && typeof entry.balance.used === 'number'
          ? `已用 ${formatBalanceAmount({ ...entry.balance, total: entry.balance.used })}`
          : headline;
      return {
        text: `${familyShortName(entry.family)} · ${amount}`,
        tone: 'data',
        disabled: false,
        title: '',
      };
    }
    return { text: familyShortName(entry.family), tone: 'data', disabled: false, title: '' };
  }
  // no_plan / no_key / unsupported：不可查，禁用 + 原因
  return { text: '额度不可查', tone: 'muted', disabled: true, title: grayCardText(entry) };
}
