/**
 * __tests__/components/workspace/raw-terminal-view.test.ts — PG-04 诊断视图组件（M2-D）
 * 覆盖：动态导入（vi.mock 拦截 @xterm/xterm / @xterm/addon-fit）→ ready 渲染；
 * 回放 + 直播续写（分批 flush）；>300ms 加载文字提示；E-10 加载失败诚实回落；
 * 卸载 dispose/退订/排空。xterm 本体不进 jsdom（结构替身记录调用）。
 * 注：beforeEach vi.resetModules()——动态 import 的 mock 工厂每次重跑，
 * 各用例的 gate/fail 开关互不泄漏。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import RawTerminalView from '../../../components/workspace/RawTerminalView.vue';

// --- vi.hoisted：mock 工厂与测试体共享替身与可控 deferred ---
const { state, FakeTerminal, FakeFitAddon } = vi.hoisted(() => {
  const state = {
    /** 非 null 时 @xterm/xterm 模块解析被挂起（测试加载提示用）。 */
    gate: null as { promise: Promise<void>; resolve: () => void } | null,
    failImport: false,
    instances: [] as FakeTerminal[],
  };
  class FakeTerminal {
    options: Record<string, unknown>;
    written: string[] = [];
    disposed = false;
    openedEl: unknown = null;
    addons: unknown[] = [];
    cols = 80;
    rows = 24;
    constructor(options: Record<string, unknown>) {
      this.options = options;
      state.instances.push(this);
    }
    open(el: unknown): void {
      this.openedEl = el;
    }
    loadAddon(addon: unknown): void {
      this.addons.push(addon);
    }
    write(data: string): void {
      this.written.push(data);
    }
    dispose(): void {
      this.disposed = true;
    }
  }
  class FakeFitAddon {
    fitCount = 0;
    fit(): void {
      this.fitCount += 1;
    }
  }
  return { state, FakeTerminal, FakeFitAddon };
});

vi.mock('@xterm/xterm', async () => {
  if (state.gate) await state.gate.promise;
  if (state.failImport) throw new Error('chunk load failed (simulated)');
  return { Terminal: FakeTerminal };
});

vi.mock('@xterm/addon-fit', () => ({ FitAddon: FakeFitAddon }));

// jsdom 无 ResizeObserver：结构替身。
class FakeResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

function makeSubscribe(): { subscribe: (cb: (t: string) => void) => () => void; emit: (t: string) => void; unsubscribed: () => boolean } {
  const cbs = new Set<(t: string) => void>();
  return {
    subscribe: (cb) => {
      cbs.add(cb);
      return () => cbs.delete(cb);
    },
    emit: (t) => cbs.forEach((cb) => cb(t)),
    unsubscribed: () => cbs.size === 0,
  };
}

/** 真实时钟小睡：等分批器 setTimeout(0) flush。 */
function sleep(ms = 10): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/** 挂载并等待本用例的终端实例创建（基线 +1，全量跑时不受慢解析影响）。 */
async function mountAndWaitInstance(props: { initialTranscript: string; subscribe: (cb: (t: string) => void) => () => void; wsAttached: boolean }) {
  const before = state.instances.length;
  const wrapper = mount(RawTerminalView, { props });
  await vi.waitFor(
    () => {
      expect(state.instances.length).toBe(before + 1);
    },
    { timeout: 3000, interval: 10 },
  );
  return { wrapper, term: state.instances[before] };
}

/** 等待分批器把期望文本全部写入。 */
async function waitWritten(term: InstanceType<typeof FakeTerminal>, expected: string): Promise<void> {
  await vi.waitFor(
    () => {
      expect(term.written.join('')).toBe(expected);
    },
    { timeout: 3000, interval: 10 },
  );
}

describe('RawTerminalView', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
    state.gate = null;
    state.failImport = false;
    state.instances.length = 0;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('动态导入就绪 → ready：回放初始 transcript（分批）并 emit 真实网格尺寸', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
      initialTranscript: 'replay-output\r\n',
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    expect(term.options.disableStdin).toBe(true);
    expect(term.options.screenReaderMode).toBe(true);
    expect(term.options.scrollback).toBe(1024 * 1024);
    // fit 后上报真实网格（PR-04 同一 sendResize 路径由父级承接）。
    expect(wrapper.emitted('resize')).toEqual([[80, 24]]);
    await waitWritten(term, 'replay-output\r\n');
    wrapper.unmount();
  });

  it('直播续写：subscribe 推送的文本写入 xterm；退订后不再写', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
      initialTranscript: '',
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    sub.emit('live-chunk-1\r\n');
    await waitWritten(term, 'live-chunk-1\r\n');
    wrapper.unmount();
    expect(sub.unsubscribed()).toBe(true);
    sub.emit('after-unmount');
    await sleep();
    expect(term.written.join('')).toBe('live-chunk-1\r\n');
  });

  it('卸载：排空尾部缓冲并 dispose 终端', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
      initialTranscript: '',
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    sub.emit('tail-chunk');
    wrapper.unmount(); // flushAll：不等分批定时器也应写出尾部
    expect(term.written.join('')).toBe('tail-chunk');
    expect(term.disposed).toBe(true);
  });
});
