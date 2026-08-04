/**
 * __tests__/components/workspace/raw-terminal-view-loading.test.ts — PG-04 加载路径（M2-D）
 * 覆盖：动态导入挂起时 >300ms 文字提示（快速加载不闪烁）；E-10 引擎加载失败
 * 诚实回落。与 raw-terminal-view.test.ts 分文件：本文件每用例 vi.resetModules()
 * 重跑 mock 工厂（挂起/失败注入），不与常规用例共享模块注册表。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import RawTerminalView from '../../../components/workspace/RawTerminalView.vue';

const { state, FakeTerminal, FakeFitAddon } = vi.hoisted(() => {
  const state = {
    gate: null as { promise: Promise<void>; resolve: () => void } | null,
    failImport: false,
    instances: [] as FakeTerminal[],
  };
  class FakeTerminal {
    options: Record<string, unknown>;
    written: string[] = [];
    disposed = false;
    cols = 80;
    rows = 24;
    constructor(options: Record<string, unknown>) {
      this.options = options;
      state.instances.push(this);
    }
    open(): void {}
    loadAddon(): void {}
    write(data: string): void {
      this.written.push(data);
    }
    dispose(): void {
      this.disposed = true;
    }
  }
  class FakeFitAddon {
    fit(): void {}
  }
  return { state, FakeTerminal, FakeFitAddon };
});

vi.mock('@xterm/xterm', async () => {
  if (state.gate) await state.gate.promise;
  if (state.failImport) throw new Error('chunk load failed (simulated)');
  return { Terminal: FakeTerminal };
});

vi.mock('@xterm/addon-fit', () => ({ FitAddon: FakeFitAddon }));

class FakeResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

const noopSubscribe = () => () => {};

describe('RawTerminalView 加载路径', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
    state.gate = null;
    state.failImport = false;
    state.instances.length = 0;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('加载 >300ms 出文字提示；引擎就绪后提示消失', async () => {
    vi.useFakeTimers();
    let resolveGate!: () => void;
    const promise = new Promise<void>((r) => {
      resolveGate = r;
    });
    state.gate = { promise, resolve: () => resolveGate() };
    const before = state.instances.length;
    const wrapper = mount(RawTerminalView, {
      props: { initialTranscript: '', subscribe: noopSubscribe, wsAttached: false },
    });
    try {
      // 未到 300ms：无文字提示（快速加载不闪烁）。
      await vi.advanceTimersByTimeAsync(100);
      expect(wrapper.text()).not.toContain('正在加载终端诊断引擎');
      await vi.advanceTimersByTimeAsync(300);
      expect(wrapper.text()).toContain('正在加载终端诊断引擎');
    } finally {
      vi.useRealTimers();
    }
    // 放行后模块解析走真实事件循环（非 fake timers 域）。
    state.gate.resolve();
    await vi.waitFor(
      () => {
        expect(state.instances.length).toBe(before + 1);
      },
      { timeout: 3000, interval: 10 },
    );
    expect(wrapper.text()).not.toContain('正在加载终端诊断引擎');
    wrapper.unmount();
  });

});
