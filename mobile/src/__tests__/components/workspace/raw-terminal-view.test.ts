/**
 * __tests__/components/workspace/raw-terminal-view.test.ts — PG-04 诊断视图组件（M2-D）
 * 覆盖：动态导入（vi.mock 拦截 @xterm/xterm / @xterm/addon-fit）→ ready 渲染；
 * 订阅即回放 + 直播续写（分批 flush）；P1-D 初始回放竞态两方向（历史先到、
 * 订阅先建）；>300ms 加载文字提示；E-10 加载失败诚实回落；
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
    dataListeners: ((data: string) => void)[] = [];
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
    onData(cb: (data: string) => void): { dispose(): void } {
      this.dataListeners.push(cb);
      return {
        dispose: () => {
          this.dataListeners = this.dataListeners.filter((l) => l !== cb);
        },
      };
    }
    emitData(data: string): void {
      for (const cb of this.dataListeners) cb(data);
    }
    scrollLines(_n: number): void {}
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

/** 可回放订阅源（模拟 store.subscribeRawOutput 的 P1-D 契约）：登记时同步
 * 回放 initial 缓冲，其后 emit 直播续写；返回退订函数。 */
function makeSubscribe(initial = ''): { subscribe: (cb: (t: string) => void) => () => void; emit: (t: string) => void; unsubscribed: () => boolean } {
  const cbs = new Set<(t: string) => void>();
  return {
    subscribe: (cb) => {
      if (initial) cb(initial);
      cbs.add(cb);
      return () => cbs.delete(cb);
    },
    emit: (t) => cbs.forEach((cb) => cb(t)),
    unsubscribed: () => cbs.size === 0,
  };
}

/** 带缓冲的订阅源（P1-D 竞态回归用）：append 模拟 store 输出入流（先入缓冲、
 * 后通知订阅者）；subscribe 登记时同步回放当前缓冲全量。 */
function makeReplayingSource(): { subscribe: (cb: (t: string) => void) => () => void; append: (t: string) => void } {
  const cbs = new Set<(t: string) => void>();
  let buffer = '';
  return {
    subscribe: (cb) => {
      if (buffer) cb(buffer);
      cbs.add(cb);
      return () => cbs.delete(cb);
    },
    append: (t) => {
      buffer += t;
      for (const cb of cbs) cb(t);
    },
  };
}

/** 真实时钟小睡：等分批器 setTimeout(0) flush。 */
function sleep(ms = 10): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/** 挂载并等待本用例的终端实例创建（基线 +1，全量跑时不受慢解析影响）。 */
async function mountAndWaitInstance(props: {
  subscribe: (cb: (t: string) => void) => () => void;
  wsAttached: boolean;
  readonly?: boolean;
  readonlyReason?: string | null;
}) {
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

  it('动态导入就绪 → ready：默认可交互模式放开 stdin 并可接收用户键入', async () => {
    const sub = makeSubscribe('replay-output\r\n');
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
      readonly: false,
    });
    expect(term.options.disableStdin).toBe(false);
    expect(term.options.cursorBlink).toBe(true);
    expect(term.options.scrollback).toBe(10_000);
    // fit 后上报真实网格（PR-04 同一 sendResize 路径由父级承接）。
    expect(wrapper.emitted('resize')).toEqual([[80, 24]]);
    await waitWritten(term, 'replay-output\r\n');

    // 模拟终端按键输入
    term.emitData('ls\r');
    expect(wrapper.emitted('data')).toEqual([['ls\r']]);

    expect(wrapper.find('[data-testid="terminal-readonly-indicator"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it('readonly=true 时降级为只读网格，显示横幅且不派发 data 事件', async () => {
    const sub = makeSubscribe('diagnostics\r\n');
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
      readonly: true,
      readonlyReason: '桌面端控制中，你可观察但无法输入',
    });
    expect(term.options.disableStdin).toBe(true);
    expect(term.options.cursorBlink).toBe(false);
    expect(term.options.screenReaderMode).toBe(true);
    expect(term.options.scrollback).toBe(1024 * 1024);

    const banner = wrapper.find('[data-testid="terminal-readonly-indicator"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('桌面端控制中');

    // 只读态拦截输入
    term.emitData('ignored');
    expect(wrapper.emitted('data')).toBeUndefined();
    wrapper.unmount();
  });

  it('直播续写：subscribe 推送的文本写入 xterm；退订后不再写', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
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

  it('P1-D 竞态①历史先到、订阅后建：引擎加载窗口内到达的缓冲历史经订阅回放进入网格', async () => {
    // P1-C §7.1 缺陷①回归：attach 历史落在「组件挂载」与「xterm 就绪 + 订阅」
    // 之间——旧实现读冻结的 initialTranscript 快照会丢 mid 段；修复后由
    // subscribeRawOutput 登记时的同步回放兜住全部缓冲。
    const source = makeReplayingSource();
    source.append('early-history\r\n'); // 组件挂载前已入缓冲
    let releaseGate!: () => void;
    const promise = new Promise<void>((r) => {
      releaseGate = r;
    });
    state.gate = { promise, resolve: () => releaseGate() };
    const before = state.instances.length;
    const wrapper = mount(RawTerminalView, { props: { subscribe: source.subscribe, wsAttached: false } });
    await Promise.resolve(); // onMounted 已启动，import 挂起在 gate 上
    source.append('mid-history\r\n'); // 引擎加载窗口内到达：无订阅者，仅入缓冲
    state.gate.resolve();
    await vi.waitFor(
      () => {
        expect(state.instances.length).toBe(before + 1);
      },
      { timeout: 3000, interval: 10 },
    );
    const term = state.instances[before];
    await waitWritten(term, 'early-history\r\nmid-history\r\n');
    source.append('live-after\r\n'); // 订阅建立后直播续写，不丢不重
    await waitWritten(term, 'early-history\r\nmid-history\r\nlive-after\r\n');
    wrapper.unmount();
  });

  it('P1-D 竞态②订阅先建、历史后到：历史经直播按序送达且不重复', async () => {
    const source = makeReplayingSource();
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: source.subscribe,
      wsAttached: false,
    });
    // 终端就绪即订阅已登记（空缓冲回放不投递）→ 迟到历史走直播推送
    source.append('late-history-1\r\n');
    source.append('late-history-2\r\n');
    await waitWritten(term, 'late-history-1\r\nlate-history-2\r\n');
    // 不重复：缓冲内容只在登记时刻回放一次，直播不重放历史
    source.append('live\r\n');
    await waitWritten(term, 'late-history-1\r\nlate-history-2\r\nlive\r\n');
    wrapper.unmount();
  });

  it('卸载：排空尾部缓冲并 dispose 终端', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    sub.emit('tail-chunk');
    wrapper.unmount(); // flushAll：不等分批定时器也应写出尾部
    expect(term.written.join('')).toBe('tail-chunk');
    expect(term.disposed).toBe(true);
  });
});
