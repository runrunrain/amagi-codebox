/**
 * stores.ompPlugin 单测（契约 §7 归一化 / §8 store 断言范式）：
 *  - enabledCount 计数来自 store 数据表达式；
 *  - refresh 写入 installed/warnings/loadError（面板门控与诊断横幅数据源）；
 *  - TTL 内非强制 refresh 复用缓存，动作签名保持兼容。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const { refreshOmpPlugins } = vi.hoisted(() => ({
  refreshOmpPlugins: vi.fn(),
}));

vi.mock('../../api/ompPlugin', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/ompPlugin')>();
  return { ...actual, refreshOmpPlugins };
});

import { useOmpPluginStore } from '../../stores/ompPlugin';
import type { OmpPlugin } from '../../api/ompPlugin';

function plugin(overrides: Partial<OmpPlugin> = {}): OmpPlugin {
  return {
    id: 'omp-a',
    name: 'omp.nvim',
    kind: 'npm',
    enabled: true,
    ...overrides,
  };
}

describe('stores.ompPlugin（契约 §7）', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    refreshOmpPlugins.mockReset();
  });

  it('enabledCount 统计已启用插件数', () => {
    const store = useOmpPluginStore();
    expect(store.enabledCount).toBe(0);
    store.installed = [
      plugin(),
      plugin({ id: 'omp-b', enabled: false }),
      plugin({ id: 'omp-c' }),
    ];
    expect(store.enabledCount).toBe(2);
  });

  it('refresh(true) 成功写入 installed/warnings 并清空 loadError', async () => {
    const store = useOmpPluginStore();
    store.loadError = '旧错误';
    refreshOmpPlugins.mockResolvedValueOnce({
      installed: [plugin()],
      warnings: ['omp CLI 未安装'],
    });
    await store.refresh(true);
    expect(store.installed).toHaveLength(1);
    expect(store.warnings).toEqual(['omp CLI 未安装']);
    expect(store.loadError).toBe('');
    expect(store.loading).toBe(false);
  });

  it('refresh 失败写入 loadError 且不抛出（诊断横幅/整页 ErrorState 数据源）', async () => {
    const store = useOmpPluginStore();
    refreshOmpPlugins.mockRejectedValueOnce(new Error('omp 未安装'));
    await expect(store.refresh(true)).resolves.toBeUndefined();
    expect(store.loadError).toBe('omp 未安装');
    expect(store.loading).toBe(false);
  });

  it('TTL 内非强制 refresh 复用缓存（不发请求），force=true 越过缓存', async () => {
    const store = useOmpPluginStore();
    store.loadedAt = Date.now();
    await store.refresh();
    expect(refreshOmpPlugins).not.toHaveBeenCalled();

    refreshOmpPlugins.mockResolvedValueOnce({ installed: [], warnings: [] });
    await store.refresh(true);
    expect(refreshOmpPlugins).toHaveBeenCalledTimes(1);
  });
});
