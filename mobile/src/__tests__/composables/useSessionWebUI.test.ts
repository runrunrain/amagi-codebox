/**
 * __tests__/composables/useSessionWebUI.test.ts — useSessionWebUI 单元测试（C2/C4）
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import {
  useSessionWebUI,
  PROBING_INTERVAL_MS,
  MONITORING_INTERVAL_MS,
} from '../../composables/useSessionWebUI';
import * as api from '../../lib/api';

describe('useSessionWebUI composable', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('enabled 为 false 时不发起探测，状态为 unknown', () => {
    const getSessionWebUISpy = vi.spyOn(api, 'getSessionWebUI');
    const sessionId = ref('sess-1');
    const enabled = ref(false);

    const { state, url, isProbing, isAvailable } = useSessionWebUI(sessionId, enabled);

    expect(state.value).toBe('unknown');
    expect(url.value).toBeUndefined();
    expect(isProbing.value).toBe(false);
    expect(isAvailable.value).toBe(false);
    expect(getSessionWebUISpy).not.toHaveBeenCalled();
  });

  it('enabled 为 true 时启动探测并进入 probing 状态', async () => {
    vi.spyOn(api, 'getSessionWebUI').mockResolvedValue({
      state: 'probing',
    });

    const sessionId = ref('sess-1');
    const enabled = ref(true);

    const { state, isProbing } = useSessionWebUI(sessionId, enabled);

    expect(state.value).toBe('probing');
    expect(isProbing.value).toBe(true);

    await vi.advanceTimersByTimeAsync(0);
    expect(api.getSessionWebUI).toHaveBeenCalledWith('sess-1');
  });

  it('probing 态下按 800ms 节奏轮询，直到 available', async () => {
    const getSessionWebUISpy = vi.spyOn(api, 'getSessionWebUI')
      .mockResolvedValueOnce({ state: 'probing' })
      .mockResolvedValueOnce({ state: 'probing' })
      .mockResolvedValueOnce({
        state: 'available',
        url: '/webui/sess-1/#/t=tok123',
      });

    const sessionId = ref('sess-1');
    const enabled = ref(true);

    const { state, url, isAvailable } = useSessionWebUI(sessionId, enabled);

    // 第 1 次请求
    await vi.advanceTimersByTimeAsync(0);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(1);
    expect(state.value).toBe('probing');

    // 前进 800ms，触发第 2 次轮询
    await vi.advanceTimersByTimeAsync(PROBING_INTERVAL_MS);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(2);
    expect(state.value).toBe('probing');

    // 再前进 800ms，触发第 3 次轮询（返回 available）
    await vi.advanceTimersByTimeAsync(PROBING_INTERVAL_MS);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(3);
    expect(state.value).toBe('available');
    expect(isAvailable.value).toBe(true);
    expect(url.value).toBe('/webui/sess-1/#/t=tok123');

    // available 后转为 5000ms 低频监测
    await vi.advanceTimersByTimeAsync(MONITORING_INTERVAL_MS);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(4);
  });

  it('状态变为 unavailable 时停止高频轮询', async () => {
    const getSessionWebUISpy = vi.spyOn(api, 'getSessionWebUI')
      .mockResolvedValueOnce({ state: 'probing' })
      .mockResolvedValueOnce({ state: 'unavailable' });

    const sessionId = ref('sess-1');
    const enabled = ref(true);

    const { state } = useSessionWebUI(sessionId, enabled);

    await vi.advanceTimersByTimeAsync(0);
    expect(state.value).toBe('probing');

    await vi.advanceTimersByTimeAsync(PROBING_INTERVAL_MS);
    expect(state.value).toBe('unavailable');

    // 不再自动高频触发
    await vi.advanceTimersByTimeAsync(PROBING_INTERVAL_MS * 5);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(2);
  });

  it('会话 ID 切换时重置状态并针对新 ID 重新探测', async () => {
    const getSessionWebUISpy = vi.spyOn(api, 'getSessionWebUI')
      .mockImplementation(async (sid) => {
        if (sid === 'sess-1') return { state: 'available', url: '/webui/sess-1/#/t=1' };
        return { state: 'probing' };
      });

    const sessionId = ref('sess-1');
    const enabled = ref(true);

    const { state, url } = useSessionWebUI(sessionId, enabled);

    await vi.advanceTimersByTimeAsync(0);
    expect(state.value).toBe('available');
    expect(url.value).toBe('/webui/sess-1/#/t=1');

    // 切换 sessionId
    sessionId.value = 'sess-2';
    await vi.advanceTimersByTimeAsync(0);

    expect(state.value).toBe('probing');
    expect(getSessionWebUISpy).toHaveBeenCalledWith('sess-2');
  });

  it('refresh 会立即触发一次轮询', async () => {
    const getSessionWebUISpy = vi.spyOn(api, 'getSessionWebUI').mockResolvedValue({
      state: 'unavailable',
    });

    const sessionId = ref('sess-1');
    const enabled = ref(true);

    const { refresh } = useSessionWebUI(sessionId, enabled);

    await vi.advanceTimersByTimeAsync(0);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(1);

    refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(getSessionWebUISpy).toHaveBeenCalledTimes(2);
  });
});
