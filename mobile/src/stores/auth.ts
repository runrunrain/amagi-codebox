/**
 * stores/auth.ts — 远程端设备信任投影（M1-D1 PG-01）
 * ---------------------------------------------------------------------------
 * 权威模型（P6 §6.1/TD-01）：HttpOnly SameSite=Strict Cookie 是唯一凭据载体，
 * 前端脚本不可读、不存储。本 store 只持有**非密投影**：DeviceSummary 与
 * HostSummary（契约 DTO，均不含凭据字段）。
 *
 * 状态机（授权层，P3 §6.2）：
 *   unknown（启动未诊断）→ unpaired（未配对）→ paired（已授权）
 *   paired → revoked（E-03 设备被撤销）/ expired（E-04 配对失效）→ 清态踢回 PG-01
 *
 * 持久化：仅 device 投影落 localStorage（非密，供刷新后"这台设备曾配对过"
 * 恢复入口展示）；授权事实永远以服务端 Cookie 校验为准，本地投影不冒充授权。
 * ---------------------------------------------------------------------------
 */

import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { DeviceSummary, HostSummary, PairingCompleteResponse } from '../lib/contract';

const DEVICE_PROJECTION_KEY = 'remote.device.projection';

export type AuthStatus = 'unknown' | 'unpaired' | 'paired' | 'revoked' | 'expired';

function loadDeviceProjection(): DeviceSummary | null {
  try {
    const raw = localStorage.getItem(DEVICE_PROJECTION_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed === 'object' && parsed !== null &&
      typeof (parsed as DeviceSummary).id === 'string' &&
      typeof (parsed as DeviceSummary).name === 'string' &&
      typeof (parsed as DeviceSummary).pairedAt === 'string'
    ) {
      return parsed as DeviceSummary;
    }
    return null;
  } catch {
    return null;
  }
}

export const useAuthStore = defineStore('remote-auth', () => {
  const status = ref<AuthStatus>('unknown');
  const device = ref<DeviceSummary | null>(loadDeviceProjection());
  const host = ref<HostSummary | null>(null);

  const isPaired = computed(() => status.value === 'paired');
  /** 本地存在"曾配对过"的投影（授权与否仍需服务端确认）。 */
  const hasDeviceProjection = computed(() => device.value !== null);

  /** 配对成功：写入非密投影（Cookie 由服务端 Set-Cookie 下发，前端不接触）。 */
  function applyPairing(response: PairingCompleteResponse): void {
    device.value = response.device;
    host.value = response.host;
    status.value = 'paired';
    try {
      localStorage.setItem(DEVICE_PROJECTION_KEY, JSON.stringify(response.device));
    } catch {
      // 存储不可用（隐私模式等）：内存投影仍有效，不阻断配对成功路径。
    }
  }

  /** 诊断确认已授权（Cookie 有效）：登记宿主摘要；设备投影沿用本地或留空。 */
  function applyAuthorized(nextHost: HostSummary): void {
    host.value = nextHost;
    status.value = 'paired';
  }

  /** 诊断确认未配对：清投影以外的运行态，保留"曾配对"入口供恢复诊断。 */
  function applyUnpaired(): void {
    host.value = null;
    status.value = 'unpaired';
  }

  /**
   * 授权失效统一入口（E-03 撤销 / E-04 配对失效）：
   * 清态并返回 true，调用方负责踢回 PG-01（路由层动作，store 不依赖 router）。
   */
  function invalidateAuthorization(reason: 'revoked' | 'expired'): void {
    host.value = null;
    status.value = reason === 'revoked' ? 'revoked' : 'expired';
    if (reason === 'revoked') {
      // 已撤销设备不再是"曾配对"可信投影，一并清除，防止伪装恢复入口。
      device.value = null;
      try {
        localStorage.removeItem(DEVICE_PROJECTION_KEY);
      } catch {
        // 同上：存储不可用不阻断清态。
      }
    }
    // E-04 配对失效：保留 device 投影与本地草稿（PR-10），仅授权层失效。
  }

  /** 本地主动断开：清运行态与投影（Cookie 本身 HttpOnly 不可由 JS 清除，由桌面撤销/过期收束）。 */
  function clearLocal(): void {
    host.value = null;
    device.value = null;
    status.value = 'unpaired';
    try {
      localStorage.removeItem(DEVICE_PROJECTION_KEY);
    } catch {
      // 存储不可用不阻断。
    }
  }

  return {
    status,
    device,
    host,
    isPaired,
    hasDeviceProjection,
    applyPairing,
    applyAuthorized,
    applyUnpaired,
    invalidateAuthorization,
    clearLocal,
  };
});
