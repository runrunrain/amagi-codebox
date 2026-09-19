/**
 * Quota API
 * 额度查询三绑定包装（设计《额度查询与常显》§3：App 挂载，模式同 api/provider.ts）。
 *
 * 后端契约（P1 已落地，绑定已再生成进 wailsjs）：
 * - GetProviderQuotas：全量缓存快照（models.json provider_quota）
 * - ProbeProviderQuota(name)：单 provider 手动探测并落盘（返回最新条目）
 * - ProbeAllProviderQuotas()：并发全探测（返回探测后的全量快照）
 * 注意：探测错误不 reject，而是以 status=error 的条目返回（app_quota_probe.go）。
 */

import {
  GetProviderQuotas,
  ProbeProviderQuota,
  ProbeAllProviderQuotas,
} from '../../wailsjs/go/main/App';
import { config } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// === 类型别名（wailsjs 真相源 re-export）===
export type ProviderQuotaEntry = config.ProviderQuotaEntry;
export type QuotaWindow = config.QuotaWindow;
export type QuotaBalance = config.QuotaBalance;

/** 全量额度缓存快照（页面挂载加载） */
export function getProviderQuotas(): Promise<Record<string, ProviderQuotaEntry>> {
  return callApi('[api.quota.getProviderQuotas]', () => GetProviderQuotas());
}

/** 单 provider 探测（返回最新条目；error 态不 reject） */
export function probeProviderQuota(name: string): Promise<ProviderQuotaEntry> {
  return callApi('[api.quota.probeProviderQuota]', () => ProbeProviderQuota(name));
}

/** 并发全探测（额度页「全部刷新」按钮） */
export function probeAllProviderQuotas(): Promise<Record<string, ProviderQuotaEntry>> {
  return callApi('[api.quota.probeAllProviderQuotas]', () => ProbeAllProviderQuotas());
}
