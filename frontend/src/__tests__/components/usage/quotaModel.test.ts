import { describe, expect, it } from 'vitest'

// v1.3.86 新家族（OpenCode Zen / OpenRouter）的展示纯逻辑：家族映射、
// 第三窗口选取、余额 headline/明细（含 remaining=0 存在性）、strip 摘要口径。
// 夹具经 wailsjs createFrom 构造，与真实绑定返回的类实例同构。
import { config } from '../../../../wailsjs/go/models'
import {
  QUOTA_FAMILY_OPENCODE_ZEN,
  QUOTA_FAMILY_OPENROUTER,
  SUPPORTED_QUOTA_FAMILIES,
  familyDisplayName,
  familyShortName,
  formatBalanceDetail,
  formatBalanceHeadline,
  quotaStripSummary,
  sourceLabel,
  tertiaryWindowOf,
  windowLabel,
  windowShortLabel,
} from '../../../components/usage/quotaModel'

type Entry = config.ProviderQuotaEntry;

function entry(overrides: Record<string, unknown> = {}): Entry {
  return config.ProviderQuotaEntry.createFrom({
    provider: 'p',
    family: QUOTA_FAMILY_OPENCODE_ZEN,
    status: 'ok',
    source: 'opencode-zen-api',
    probed_at: new Date().toISOString(),
    ...overrides,
  });
}

function balance(overrides: Record<string, unknown> = {}): config.QuotaBalance {
  return config.QuotaBalance.createFrom({ currency: 'USD', ...overrides });
}

describe('新家族映射', () => {
  it('SUPPORTED 集合 / 显示名 / 短名 / 来源文案', () => {
    expect(SUPPORTED_QUOTA_FAMILIES.has(QUOTA_FAMILY_OPENCODE_ZEN)).toBe(true);
    expect(SUPPORTED_QUOTA_FAMILIES.has(QUOTA_FAMILY_OPENROUTER)).toBe(true);
    expect(familyDisplayName(QUOTA_FAMILY_OPENCODE_ZEN)).toBe('OpenCode Zen');
    expect(familyDisplayName(QUOTA_FAMILY_OPENROUTER)).toBe('OpenRouter');
    expect(familyShortName(QUOTA_FAMILY_OPENCODE_ZEN)).toBe('Zen');
    expect(familyShortName(QUOTA_FAMILY_OPENROUTER)).toBe('OpenRouter');
    expect(sourceLabel('opencode-zen-api')).toBe('API');
    expect(sourceLabel('openrouter-api')).toBe('API');
  });
});

describe('Zen 三窗口', () => {
  const windows = [
    config.QuotaWindow.createFrom({ kind: 'primary', label: '滚动', used_percent: 42 }),
    config.QuotaWindow.createFrom({ kind: 'secondary', label: '周', used_percent: 7 }),
    config.QuotaWindow.createFrom({ kind: 'tertiary', label: '月', used_percent: 100 }),
  ];

  it('tertiaryWindowOf 取第三窗口，Label 优先显示', () => {
    const e = entry({ windows });
    const tertiary = tertiaryWindowOf(e);
    expect(tertiary?.label).toBe('月');
    expect(windowLabel(tertiary)).toBe('月 窗口');
    expect(windowShortLabel(tertiary)).toBe('月');
  });

  it('strip 摘要走主窗口口径（剩余% + 滚动短标签）', () => {
    const summary = quotaStripSummary(entry({ windows }));
    expect(summary.text).toBe('Zen · 58% 滚动');
    expect(summary.tone).toBe('data');
  });
});

describe('OpenRouter 余额排版', () => {
  it('headline=剩余 + 明细「已用 · 共」', () => {
    const b = balance({ total: 100.5, used: 25.75, remaining: 74.75 });
    expect(formatBalanceHeadline(b)).toBe('USD 74.75');
    expect(formatBalanceDetail(b)).toBe('已用 USD 25.75 · 共 USD 100.50');
  });

  it('remaining=0 仍显示剩余（指针存在性，不回落 total）', () => {
    const b = balance({ total: 25.75, used: 25.75, remaining: 0 });
    expect(formatBalanceHeadline(b)).toBe('USD 0.00');
    expect(formatBalanceDetail(b)).toBe('已用 USD 25.75 · 共 USD 25.75');
  });

  it('DeepSeek 旧口径不受影响（total 大数字、无明细）', () => {
    const b = config.QuotaBalance.createFrom({ currency: 'CNY', total: 128.5 });
    expect(formatBalanceHeadline(b)).toBe('¥128.50');
    expect(formatBalanceDetail(b)).toBe('');
  });

  it('降级口径（仅 used 无上限）：headline 留空由明细承接，不硬造总额', () => {
    const b = balance({ used: 30 });
    expect(formatBalanceHeadline(b)).toBe('—');
    expect(formatBalanceDetail(b)).toBe('已用 USD 30.00');
  });

  it('strip 降级口径改拼「已用 X」（diting F-2 回归）', () => {
    const e = entry({
      family: QUOTA_FAMILY_OPENROUTER,
      source: 'openrouter-api',
      balance: balance({ used: 30 }),
    });
    expect(quotaStripSummary(e).text).toBe('OpenRouter · 已用 USD 30.00');
  });

  it('strip 摘要走余额口径（headline=剩余）', () => {
    const e = entry({
      family: QUOTA_FAMILY_OPENROUTER,
      source: 'openrouter-api',
      balance: balance({ total: 100.5, used: 25.75, remaining: 74.75 }),
    });
    const summary = quotaStripSummary(e);
    expect(summary.text).toBe('OpenRouter · USD 74.75');
    expect(summary.tone).toBe('data');
  });
});
