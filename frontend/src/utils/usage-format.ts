/**
 * Usage formatting utilities
 * 格式化使用统计的成本与 token 数值。
 *
 * Truth source: agent-outputs/architect/usage-stats-design.md §6.3
 *   - 后端成本字段单位为 int64 micro-native-currency（1e-6 原生币种）。
 *   - 价格表 *_per_million 字段单位为 micro-native-currency per 1M tokens。
 *   - 前端展示统一 / 1e6 得到原生币种浮点值，再按 currencyCode 选符号。
 */

/** 后端 micro 单位与小数之间的换算因子 / Divisor between micro-int and float currency. */
const MICRO_PER_UNIT = 1_000_000;

/**
 * 各币种对 USD 的固定折算率（设计 §6.5：第一期固定，未来从设置项读取）。
 * Fixed native→USD exchange rates (design §6.5: fixed in v1, pluggable later).
 * 用途：图表跨模型/跨供应商对比时把原生 micro 成本换算到统一 USD 口径。
 */
const CURRENCY_TO_USD: Record<string, number> = {
  USD: 1,
  CNY: 0.14,
};

/**
 * 把原生币种的 micro 成本换算为 USD 的 micro 成本（图表对比用）。
 * Convert a micro-native-currency cost to micro-USD for chart comparability.
 * 未知币种按 1:1 处理（假设已是 USD）。前端仅用于可视化，汇总数字仍以后端 Summary.totalCostUSD 为准。
 */
export function nativeMicroToUsdMicro(micro: number, currencyCode: string): number {
  if (!Number.isFinite(micro) || micro === 0) return 0;
  const rate = CURRENCY_TO_USD[currencyCode] ?? 1;
  return Math.round(micro * rate);
}

/** 币种符号映射（用于展示）/ Currency symbol map for display. */
const CURRENCY_SYMBOL: Record<string, string> = {
  USD: '$',
  CNY: '¥',
};

/**
 * 取币种展示符号，未知币种回退为 ISO 代码前缀。
 * Get the display symbol for a currency code; unknown codes fall back to the code itself.
 */
export function currencySymbol(currencyCode: string): string {
  return CURRENCY_SYMBOL[currencyCode || 'USD'] || `${currencyCode} `;
}

/**
 * 把后端 micro 整数换算为浮点币种值。
 * Convert a backend micro-currency int to a float currency value.
 */
export function microToCurrency(micro: number): number {
  if (!Number.isFinite(micro) || micro === 0) return 0;
  return micro / MICRO_PER_UNIT;
}

/**
 * 格式化成本：micro 整数 + 币种 → "$12.34" / "¥56.78"。
 * 小数位数按绝对值自适应：>= 1 用 2 位（货币惯例），< 1 用 4 位（小额调试可见精度）。
 *
 * Format a micro-int cost with currency symbol. Decimal precision adapts to magnitude:
 * >= 1 uses 2 digits (currency convention), < 1 uses 4 digits (visible precision for small amounts).
 */
export function formatCost(micro: number, currencyCode = 'USD'): string {
  const value = microToCurrency(micro);
  const symbol = currencySymbol(currencyCode);
  const abs = Math.abs(value);
  const digits = abs === 0 ? 2 : abs < 1 ? 4 : 2;
  const formatted = value.toLocaleString('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
  return `${symbol}${formatted}`;
}

/**
 * 把价格表 *_per_million（micro/M token）换算为 "X.XX / M" 形式的人类可读价。
 * Convert a micro-per-million price to human-readable "X.XX / M" form.
 */
export function formatPerMillion(microPerMillion: number, currencyCode = 'USD'): string {
  const perMillion = microToCurrency(microPerMillion);
  const symbol = currencySymbol(currencyCode);
  return `${symbol}${perMillion.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  })} / M`;
}

/**
 * 格式化 token 数值：小于 1000 显示原值；否则使用 K/M/G 后缀并保留 1-2 位小数。
 * Format a token count: raw under 1k, otherwise K/M/G suffix with adaptive precision.
 */
export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '0';
  const abs = Math.abs(n);
  if (abs < 1000) return String(n);
  if (abs < 1_000_000) {
    // < 1M：用 K；整数倍不带小数 / Use K suffix; drop decimals on exact multiples.
    const v = n / 1000;
    return v % 1 === 0 ? `${v}K` : `${v.toFixed(1)}K`;
  }
  if (abs < 1_000_000_000) {
    const v = n / 1_000_000;
    return v % 1 === 0 ? `${v}M` : `${v.toFixed(2)}M`;
  }
  const v = n / 1_000_000_000;
  return v % 1 === 0 ? `${v}G` : `${v.toFixed(2)}G`;
}

/**
 * 千分位格式化整数（请求计数等）。
 * Locale-grouped integer formatting (request counts, etc.).
 */
export function formatCount(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '0';
  return Math.trunc(n).toLocaleString('en-US');
}

/**
 * 把后端 any 类型的时间字段安全转为字符串（Go time.Time 序列化为 ISO 8601 字符串）。
 * Safely render a backend `any` time field (Go time.Time → ISO 8601 string) as a localized string.
 */
export function formatTimeValue(value: unknown, locale = 'zh-CN'): string {
  if (!value) return '';
  if (typeof value === 'string') {
    const d = new Date(value);
    if (!Number.isNaN(d.getTime())) {
      return d.toLocaleString(locale, {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      });
    }
    return value;
  }
  if (typeof value === 'number') {
    // 10 位秒 / 13 位毫秒 / 16 位微秒 / 19 位纳秒
    let ms = value;
    if (value < 1e12) ms = value * 1000;
    else if (value > 1e15) ms = value / 1e6;
    const d = new Date(ms);
    if (!Number.isNaN(d.getTime())) {
      return d.toLocaleString(locale, {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      });
    }
  }
  return String(value);
}
