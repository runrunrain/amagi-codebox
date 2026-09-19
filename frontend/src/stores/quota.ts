/**
 * Quota Store (Pinia setup style)
 * 额度查询 store：entries 缓存 + 单飞探测 + ensure 冷却（设计《额度查询与常显》§7 数据流）。
 *
 * 仿 stores/usage.ts / stores/appearance.ts：
 * - loadAll：读 GetProviderQuotas 本地缓存（廉价，挂载即读）；
 * - probe(name)：单飞（in-flight Map 防重入），probing map 暴露 spinner 态；
 * - probeAll：额度页「全部刷新」；
 * - ensure(name)：Web 平面按钮首挂载用——缓存缺失或 probed_at>10min 且
 *   非 in-flight 时后台探测一次（失败静默 warn，不刷屏 toast）；
 * - quotaFor(name)：直接查 entries；无条目且 provider 名非 codex →
 *   返回 {status:'unsupported'} 占位（codex 为固定键，未探测时返回 null
 *   表示「待探测」而非「不可查」，由调用方走 ensure 路径）。
 */

import { defineStore } from 'pinia';
import { ref } from 'vue';
import { config } from '../../wailsjs/go/models';
import * as quotaApi from '../api/quota';
import type { ProviderQuotaEntry } from '../api/quota';

/** Codex 订阅条目的固定 provider 键（与后端 codexQuotaProviderName 一致） */
export const CODEX_QUOTA_KEY = 'codex';

/** ensure 冷却：距上次探测 <10min 不重复后台探测（设计 §3 刷新策略） */
export const ENSURE_COOLDOWN_MS = 10 * 60 * 1000;

export const useQuotaStore = defineStore('quota', () => {
  // === State ===
  const entries = ref<Record<string, ProviderQuotaEntry>>({});
  const loading = ref(false);
  const error = ref('');
  const probingAll = ref(false);
  /** 单卡 spinner 态（provider -> 是否探测中） */
  const probing = ref<Record<string, boolean>>({});

  // 单飞 map：非响应式，仅并发去重用（复刻 modality probe 单飞语义）
  const inFlight = new Map<string, Promise<ProviderQuotaEntry>>();

  // === Actions ===

  /** 加载全量缓存快照；silent 后台刷新失败保留旧数据 + warn */
  async function loadAll(opts: { silent?: boolean } = {}): Promise<void> {
    const silent = opts.silent === true;
    if (!silent) {
      loading.value = true;
      error.value = '';
    }
    try {
      const result = await quotaApi.getProviderQuotas();
      entries.value = result ?? {};
    } catch (err) {
      if (!silent || Object.keys(entries.value).length === 0) {
        error.value = errToString(err);
      } else {
        console.warn('[quota.loadAll] background refresh failed:', err);
      }
    } finally {
      if (!silent) loading.value = false;
    }
  }

  /** 单 provider 探测（单飞）：并发调用共享同一 Promise，结果写回 entries */
  function probe(name: string): Promise<ProviderQuotaEntry> {
    const existing = inFlight.get(name);
    if (existing) return existing;
    const task = (async () => {
      probing.value = { ...probing.value, [name]: true };
      try {
        const entry = await quotaApi.probeProviderQuota(name);
        if (entry) entries.value = { ...entries.value, [name]: entry };
        return entry;
      } finally {
        probing.value = { ...probing.value, [name]: false };
      }
    })();
    inFlight.set(name, task);
    void task.catch(() => undefined).finally(() => inFlight.delete(name));
    return task;
  }

  /** 全量并发探测（额度页「全部刷新」）；错误条目以 status=error 返回，不 reject */
  async function probeAll(): Promise<Record<string, ProviderQuotaEntry>> {
    probingAll.value = true;
    try {
      const result = await quotaApi.probeAllProviderQuotas();
      entries.value = result ?? {};
      return result ?? {};
    } finally {
      probingAll.value = false;
    }
  }

  /**
   * 背景探测（Web 平面 strip 首挂载 / 会话启动）：缓存缺失或距上次探测
   * 超 10min 且当前非 in-flight 时探测一次；失败静默（保留旧数据）。
   */
  async function ensure(name: string): Promise<void> {
    if (!name) return;
    if (inFlight.has(name) || probingAll.value) return;
    const cached = entries.value[name];
    if (cached?.probed_at) {
      const probedMs = Date.parse(cached.probed_at);
      // probed_at 可解析且新鲜 → 跳过；解析失败按缺失处理（探测一次）
      if (Number.isFinite(probedMs) && Date.now() - probedMs < ENSURE_COOLDOWN_MS) return;
    }
    try {
      await probe(name);
    } catch (err) {
      console.warn('[quota.ensure] probe failed:', err);
    }
  }

  /**
   * 查询 helper：直接查 entries。
   * 探测进行中且尚无缓存条目 → null（调用方可显示「查询中…」，修复 diting
   * Minor-1：unsupported 占位会短路首探中的 spinner 分支）；
   * 无条目且 provider 名非 codex → unsupported 占位（灰化，避免误报「加载中」）；
   * 名为 codex（固定键）且无条目 → null（待探测，调用方可走 ensure）。
   */
  function quotaFor(providerName: string): ProviderQuotaEntry | null {
    if (!providerName) return null;
    const direct = entries.value[providerName];
    if (direct) return direct;
    if (probing.value[providerName] === true || probingAll.value) return null;
    if (providerName === CODEX_QUOTA_KEY) return null;
    // wailsjs 生成的类带 convertValues 方法，占位条目须经 createFrom 构造
    return config.ProviderQuotaEntry.createFrom({
      provider: providerName,
      family: '',
      status: 'unsupported',
      source: '',
      probed_at: '',
    });
  }

  function $reset(): void {
    entries.value = {};
    loading.value = false;
    error.value = '';
    probingAll.value = false;
    probing.value = {};
    inFlight.clear();
  }

  return {
    entries,
    loading,
    error,
    probingAll,
    probing,
    loadAll,
    probe,
    probeAll,
    ensure,
    quotaFor,
    $reset,
  };
});

function errToString(err: unknown): string {
  if (!err) return '未知错误';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  if (Array.isArray(err)) return err.map(String).join('; ');
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
}
