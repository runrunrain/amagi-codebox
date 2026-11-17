import { isJsonRecord, type JsonRecord } from './jsonTypes';

export const CODEMODE_BUDGETS = [
  {key:'maxOutputTokensPerScript',label:'默认输出 tokens',default:2000,max:1000000},
  {key:'maxTimeoutMsPerScript',label:'默认时限（毫秒）',default:60000,max:2147483647},
];
export function validateCodemodeConfig(config: JsonRecord): string {
  const c = config.codemode;
  if (c === undefined) return '';
  if (!isJsonRecord(c)) return 'codemode 必须是对象';
  if (c.preference !== undefined && c.preference !== 'auto' && c.preference !== 'prefer') return 'codemode.preference 只允许 auto/prefer';
  if (c.budget !== undefined) {
    if (!isJsonRecord(c.budget)) return 'codemode.budget 必须是对象';
    for (const f of CODEMODE_BUDGETS) {
      const n = c.budget[f.key];
      if (n !== undefined && (typeof n !== 'number' || !Number.isSafeInteger(n) || n < 1 || n > f.max)) return `codemode.budget.${f.key} 必须是 1–${f.max} 的整数`;
    }
  }
  return '';
}
