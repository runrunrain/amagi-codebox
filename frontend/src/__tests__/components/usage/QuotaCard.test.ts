import { describe, expect, it } from 'vitest'

// QuotaCard 六态渲染（窗口型双条 / 余额型数字 / 各灰卡文案）的决策逻辑
// 全部在 quotaModel.ts；node 环境无 DOM，这里覆盖驱动模板的全部分支。
// 夹具经 wailsjs createFrom 构造，与真实绑定返回的类实例同构。
import { config } from '../../../../wailsjs/go/models'
import {
  codexCreditsBadge,
  formatBalanceAmount,
  formatResetCountdown,
  grayCardText,
  isStaleOk,
  primaryWindowOf,
  quotaStripSummary,
  secondaryWindowOf,
  windowLabel,
} from '../../../components/usage/quotaModel'

type Entry = config.ProviderQuotaEntry;

function entry(overrides: Record<string, unknown> = {}): Entry {
  return config.ProviderQuotaEntry.createFrom({
    provider: 'glm',
    family: 'glm-bigmodel',
    status: 'ok',
    source: 'glm-api',
    probed_at: new Date().toISOString(),
    ...overrides,
  });
}

function win(overrides: Record<string, unknown> = {}): config.QuotaWindow {
  return config.QuotaWindow.createFrom({ kind: 'primary', used_percent: 62, window_minutes: 300, ...overrides });
}

function balance(overrides: Record<string, unknown> = {}): config.QuotaBalance {
  return config.QuotaBalance.createFrom({ currency: 'CNY', total: 128.5, ...overrides });
}

function windowEntry(overrides: Record<string, unknown> = {}): Entry {
  return entry({
    windows: [
      win({ kind: 'primary', used_percent: 62, window_minutes: 300, resets_at: Math.floor(Date.now() / 1000) + 8040 }),
      win({ kind: 'secondary', used_percent: 31, window_minutes: 10080, resets_at: Math.floor(Date.now() / 1000) + 400_000 }),
    ],
    ...overrides,
  });
}

describe('QuotaCard 窗口型：primary/secondary 双条选取', () => {
  it('primary 与 secondary 各就各位，标签为 5h / 周窗口', () => {
    const e = windowEntry();
    expect(primaryWindowOf(e)?.kind).toBe('primary');
    expect(secondaryWindowOf(e)?.kind).toBe('secondary');
    expect(windowLabel(primaryWindowOf(e))).toBe('5h 窗口');
    expect(windowLabel(secondaryWindowOf(e))).toBe('周窗口');
  });

  it('GLM 的 time_limit 归 primary 槽位', () => {
    const e = windowEntry({ windows: [win({ kind: 'time_limit', used_percent: 55 })] });
    expect(primaryWindowOf(e)?.kind).toBe('time_limit');
    expect(windowLabel(primaryWindowOf(e))).toBe('5h 窗口');
    expect(secondaryWindowOf(e)).toBeNull();
  });

  it('无 windows 的条目两个槽位都为空（回落余额/灰卡渲染）', () => {
    const e = windowEntry({ windows: undefined });
    expect(primaryWindowOf(e)).toBeNull();
    expect(secondaryWindowOf(e)).toBeNull();
  });
});

describe('QuotaCard 余额型：¥ 数字排版（原币种零换算）', () => {
  it('CNY 用 ¥ 前缀，两位小数', () => {
    expect(formatBalanceAmount(balance())).toBe('¥128.50');
  });

  it('非 CNY 保留币种码；缺失/非法回落 —', () => {
    expect(formatBalanceAmount(balance({ currency: 'USD', total: 3 }))).toBe('USD 3.00');
    expect(formatBalanceAmount(null)).toBe('—');
    expect(formatBalanceAmount(balance({ total: Number.NaN }))).toBe('—');
  });
});

describe('QuotaCard 灰卡文案矩阵', () => {
  it('no_plan / no_key / unsupported / error 四态文案', () => {
    expect(grayCardText(entry({ status: 'no_plan' }))).toBe('该 Key 未开通对应套餐');
    expect(grayCardText(entry({ status: 'no_key' }))).toBe('未配置 API Key');
    expect(grayCardText(entry({ status: 'unsupported' }))).toBe('该服务暂不支持额度查询');
    expect(grayCardText(entry({ status: 'error', message: '401 Unauthorized' }))).toBe('401 Unauthorized');
    expect(grayCardText(entry({ status: 'error' }))).toBe('额度获取失败');
  });

  it('无条目回落占位文案', () => {
    expect(grayCardText(null)).toBe('暂无额度数据');
  });
});

describe('QuotaCard Codex credits 徽标（message 编码解析）', () => {
  it('有/无/无限三形态；仅 codex-sub 家族解析', () => {
    expect(codexCreditsBadge(entry({ family: 'codex-sub', message: 'credits: 有' })))
      .toEqual({ text: '含 credits', has: true });
    expect(codexCreditsBadge(entry({ family: 'codex-sub', message: 'credits: 无' })))
      .toEqual({ text: '无 credits', has: false });
    expect(codexCreditsBadge(entry({ family: 'codex-sub', message: 'credits: 无限' })))
      .toEqual({ text: '无限 credits', has: true });
    expect(codexCreditsBadge(entry({ message: 'credits: 有' }))).toBeNull();
    expect(codexCreditsBadge(entry({ family: 'codex-sub' }))).toBeNull();
  });
});

describe('QuotaCard 陈旧标注（ok 且 probed_at >30min 黄点）', () => {
  it('31 分钟前的 ok 条目标陈旧；5 分钟前 / 非 ok 不标', () => {
    const staleIso = new Date(Date.now() - 31 * 60_000).toISOString();
    const freshIso = new Date(Date.now() - 5 * 60_000).toISOString();
    expect(isStaleOk(entry({ probed_at: staleIso }))).toBe(true);
    expect(isStaleOk(entry({ probed_at: freshIso }))).toBe(false);
    expect(isStaleOk(entry({ status: 'error', probed_at: staleIso }))).toBe(false);
    expect(isStaleOk(entry({ probed_at: '' }))).toBe(false);
  });
});

describe('重置时刻文案', () => {
  it('绝对时刻优先：今日/明日时刻、7 天内周几、更远日期；过期诚实陈述', () => {
    const now = Date.now();
    const baseSec = Math.ceil(now / 1000);
    // 过期（Codex 本地会话数据常见）：不再误导性「即将重置」
    expect(formatResetCountdown(Math.floor(now / 1000) - 10, now)).toBe('重置时刻已过');
    expect(formatResetCountdown(undefined, now)).toBe('');
    // 未来时刻按自然日分档：今日/明日/周几/日期（用固定 now 避免跨日边界抖动）
    const fixed = new Date(2026, 8, 19, 10, 0, 0, 0).getTime(); // 2026-09-19 10:00
    const at = (dayPlus: number, h: number, m: number) =>
      Math.floor(new Date(2026, 8, 19 + dayPlus, h, m, 0, 0).getTime() / 1000);
    expect(formatResetCountdown(at(0, 22, 35), fixed)).toBe('今日 22:35 重置');
    expect(formatResetCountdown(at(1, 3, 10), fixed)).toBe('明日 03:10 重置');
    expect(formatResetCountdown(at(3, 14, 0), fixed)).toMatch(/^周[一二三四五六日] 14:00 重置$/);
    expect(formatResetCountdown(at(12, 9, 5), fixed)).toBe('10月1日 09:05 重置');
  });
});

describe('Web 平面 strip 摘要映射（quotaStripSummary）', () => {
  it('窗口型 ok：家族短名 + 剩余 % + 窗口短标 + 主窗重置短时刻', () => {
    const summary = quotaStripSummary(windowEntry());
    // fixture 主窗 resets_at = now+8040s（~2.2h 后，今日内）→ 摘要附「今日 HH:mm」
    expect(summary.text).toMatch(/^GLM · 38% 5h · (今日|明日) \d{2}:\d{2}$/);
    expect(summary.tone).toBe('data');
    expect(summary.disabled).toBe(false);
  });

  it('窗口型 ok 无 resets_at：无重置后缀（向后兼容）', () => {
    const e = windowEntry({
      windows: [win({ kind: 'primary', used_percent: 62, window_minutes: 300 }), win({ kind: 'secondary', used_percent: 31, window_minutes: 10080 })],
    });
    expect(quotaStripSummary(e).text).toBe('GLM · 38% 5h');
  });

  it('余额型 ok：家族短名 + ¥ 值', () => {
    const e = entry({ provider: 'deepseek', family: 'deepseek', windows: undefined, balance: balance() });
    expect(quotaStripSummary(e).text).toBe('DeepSeek · ¥128.50');
  });

  it('error：额度获取失败（点击重试语义）；unsupported：禁用灰字', () => {
    const error = quotaStripSummary(entry({ status: 'error', message: 'x' }));
    expect(error.text).toBe('额度获取失败');
    expect(error.tone).toBe('error');
    expect(error.disabled).toBe(false);

    const muted = quotaStripSummary(entry({ status: 'unsupported' }));
    expect(muted.text).toBe('额度不可查');
    expect(muted.disabled).toBe(true);
    expect(muted.title).toBe('该服务暂不支持额度查询');

    expect(quotaStripSummary(null).disabled).toBe(true);
  });
});
