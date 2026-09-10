/**
 * __tests__/components/workspace/raw-terminal-view-tui.test.ts — P1-C 全屏 TUI 帧样本 × RawTerminalView
 * ---------------------------------------------------------------------------
 * 消费 mobile/src/__tests__/fixtures/tui-frames.ts（SYNTHETIC 合成帧 + 迷你屏幕
 * oracle），在组件写入 xterm 的数据接缝上验证终端仿真面契约：
 *   · 帧保真：合成 TUI 帧原样（含全部 ANSI 控制序列）按序写入 xterm，oracle
 *     对「组件写入流」的渲染与对「原始帧流」的渲染逐字节等价；
 *   · 重绘不堆积：整屏重绘帧 ×3 后屏幕只含最后一帧（旧帧 0 残留）；
 *   · \r 覆写无残留：spinner 单行刷新终态干净；
 *   · scrollback：长输出帧全量送达（分批写入不截断）；
 *   · 交互输入：xterm onData（含控制序列）→ emit('data')，readonly 拦截；
 *   · readonly 动态降级/恢复：横幅出现/消失 + stdin 开关随 props.readonly 切换。
 * xterm 本体不进 jsdom（结构替身记录写入），oracle 是终端语义的测试替身。
 * ---------------------------------------------------------------------------
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import RawTerminalView from '../../../components/workspace/RawTerminalView.vue';
import {
  ALT_SCREEN_ROUNDTRIP,
  LONG_OUTPUT_SCROLLBACK,
  PARTIAL_REDRAW_MENU,
  PI_FULLSCREEN_REDRAW,
  PI_SPINNER_CR,
  allLines,
  countOccurrences,
  renderTuiStream,
  screenLines,
  screenText,
} from '../../fixtures/tui-frames';

// --- vi.hoisted：mock 工厂与测试体共享替身 ---
const { state, FakeTerminal, FakeFitAddon } = vi.hoisted(() => {
  const state = {
    failImport: false,
    instances: [] as FakeTerminal[],
  };
  class FakeTerminal {
    options: Record<string, unknown>;
    written: string[] = [];
    disposed = false;
    addons: unknown[] = [];
    cols = 80;
    rows = 24;
    dataListeners: ((data: string) => void)[] = [];
    constructor(options: Record<string, unknown>) {
      this.options = options;
      state.instances.push(this);
    }
    open(): void {}
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
    fit(): void {}
  }
  return { state, FakeTerminal, FakeFitAddon };
});

vi.mock('@xterm/xterm', () => ({ Terminal: FakeTerminal }));
vi.mock('@xterm/addon-fit', () => ({ FitAddon: FakeFitAddon }));

class FakeResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

/** 可回放订阅源（模拟 store.subscribeRawOutput 的 P1-D 契约）：登记时同步
 * 回放 initial 缓冲，其后 emit 直播续写。 */
function makeSubscribe(initial = ''): {
  subscribe: (cb: (t: string) => void) => () => void;
  emit: (t: string) => void;
  unsubscribed: () => boolean;
} {
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

/** 真实时钟小睡：等分批器 setTimeout(0) flush。 */
function sleep(ms = 10): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

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

async function waitWritten(term: InstanceType<typeof FakeTerminal>, expected: string): Promise<void> {
  await vi.waitFor(
    () => {
      expect(term.written.join('')).toBe(expected);
    },
    { timeout: 3000, interval: 10 },
  );
}

describe('RawTerminalView × 合成全屏 TUI 帧（P1-C）', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
    state.failImport = false;
    state.instances.length = 0;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('整屏重绘帧 ×3 原样写入且不堆积：oracle 终屏只含最后一帧', async () => {
    const frames = PI_FULLSCREEN_REDRAW.frames;
    const sub = makeSubscribe(frames.join(''));
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    await waitWritten(term, frames.join(''));

    // 帧保真：写入流与原始帧流经 oracle 渲染逐字节等价（无截断/改写/重复）。
    const fromComponent = renderTuiStream(term.written);
    const fromFixture = renderTuiStream(frames);
    expect(screenLines(fromComponent)).toEqual(screenLines(fromFixture));
    expect(fromComponent.scrollback).toEqual(fromFixture.scrollback);

    // 重绘语义：终屏只有 frame 3，旧帧在屏幕与 scrollback 中零残留。
    const screen = screenText(fromComponent);
    expect(countOccurrences(screen, '[pi-tui] frame 3 ready')).toBe(1);
    expect(screen).toContain('status: idle');
    expect(countOccurrences(allLines(fromComponent).join('\n'), 'frame 1 ready')).toBe(0);
    expect(countOccurrences(allLines(fromComponent).join('\n'), 'frame 2 ready')).toBe(0);
    wrapper.unmount();
  });

  it('\\r spinner 覆写帧写入后终态单行、旧帧零残留', async () => {
    const frames = PI_SPINNER_CR.frames;
    const sub = makeSubscribe(frames.join(''));
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    await waitWritten(term, frames.join(''));

    const oracle = renderTuiStream(term.written);
    expect(screenLines(oracle)[0]).toBe('✓ done in 1.2s');
    const everything = allLines(oracle).join('\n');
    expect(everything).not.toContain('generating');
    expect(countOccurrences(everything, '⠋')).toBe(0);
    expect(countOccurrences(everything, '⠹')).toBe(0);
    wrapper.unmount();
  });

  it('ink 局部重绘帧（CUU+EL）保真：改写目标行、未变更行不重复', async () => {
    const frames = PARTIAL_REDRAW_MENU.frames;
    const sub = makeSubscribe(frames.join(''));
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    await waitWritten(term, frames.join(''));

    const lines = screenLines(renderTuiStream(term.written));
    expect(lines[0]).toBe('Pick a tool:');
    expect(lines[1]).toBe('  [ ] read files');
    expect(lines[2]).toBe('  [x] write files');
    expect(lines[3]).toBe('  [ ] run shell');
    expect(countOccurrences(allLines(renderTuiStream(term.written)).join('\n'), 'write files')).toBe(1);
    wrapper.unmount();
  });

  it('备屏切换帧保真：进/出 ?1049 后主屏内容恢复', async () => {
    const frames = ALT_SCREEN_ROUNDTRIP.frames;
    const sub = makeSubscribe(frames.join(''));
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    await waitWritten(term, frames.join(''));

    const oracle = renderTuiStream(term.written);
    expect(oracle.altScreen).toBe(false);
    const lines = screenLines(oracle);
    expect(lines[0]).toBe('primary log alpha');
    expect(lines[1]).toBe('primary log beta');
    expect(allLines(oracle).join('\n')).not.toContain('ALT-SCREEN');
    wrapper.unmount();
  });

  it('scrollback 长输出帧（61 行）全量送达：分批写入不截断、历史滚入正确', async () => {
    const frames = LONG_OUTPUT_SCROLLBACK.frames;
    const sub = makeSubscribe(frames.join(''));
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    await waitWritten(term, frames.join(''));

    expect(term.written.join('')).toBe(frames.join(''));
    const oracle = renderTuiStream(term.written);
    expect(allLines(oracle).filter((l) => l.trim() !== '')).toHaveLength(61);
    expect(oracle.scrollback[0]).toBe('scrollback probe line 001');
    expect(screenLines(oracle)).toContain('tail status OK');
    wrapper.unmount();
  });

  it('直播续写合成帧：attach 后新帧经 subscribe 追加写入且语义连续', async () => {
    const sub = makeSubscribe(PI_FULLSCREEN_REDRAW.frames[0] + PI_FULLSCREEN_REDRAW.frames[1]);
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    await waitWritten(term, PI_FULLSCREEN_REDRAW.frames[0] + PI_FULLSCREEN_REDRAW.frames[1]);

    // 第 3 帧直播到达 → 追加写入，重绘语义仍然成立。
    sub.emit(PI_FULLSCREEN_REDRAW.frames[2]);
    await waitWritten(term, PI_FULLSCREEN_REDRAW.frames.join(''));
    const oracle = renderTuiStream(term.written);
    expect(countOccurrences(screenText(oracle), '[pi-tui] frame 3 ready')).toBe(1);
    expect(countOccurrences(allLines(oracle).join('\n'), 'frame 1 ready')).toBe(0);
    wrapper.unmount();
    expect(sub.unsubscribed()).toBe(true);
  });

  it('可交互输入：onData 派发控制序列与普通字符（→ store.sendRaw 通路）', async () => {
    const sub = makeSubscribe(PI_FULLSCREEN_REDRAW.frames.join(''));
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
      readonly: false,
    });
    await waitWritten(term, PI_FULLSCREEN_REDRAW.frames.join(''));

    // pi TUI 全套关键输入：Ctrl+C 中断、方向键、Esc、Alt 组合、普通文本。
    term.emitData('\x03');
    term.emitData('\x1b[A');
    term.emitData('\x1b[B');
    term.emitData('\x1b');
    term.emitData('\x1bw');
    term.emitData('q');
    expect(wrapper.emitted('data')).toEqual([['\x03'], ['\x1b[A'], ['\x1b[B'], ['\x1b'], ['\x1bw'], ['q']]);
    wrapper.unmount();
  });

  it('readonly 动态降级/恢复：横幅出现/消失、stdin 开关、data 事件随之启停', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
      readonly: false,
    });
    expect(wrapper.find('[data-testid="terminal-readonly-indicator"]').exists()).toBe(false);

    // 控制权被夺 → 只读横幅出现 + stdin 禁用 + 输入被拦截。
    await wrapper.setProps({ readonly: true, readonlyReason: '桌面端正在控制，你可观察但无法输入' });
    await vi.waitFor(() => {
      const banner = wrapper.find('[data-testid="terminal-readonly-indicator"]');
      expect(banner.exists()).toBe(true);
      expect(banner.text()).toContain('桌面端正在控制');
    });
    expect(term.options.disableStdin).toBe(true);
    expect(term.options.cursorStyle).toBe('bar');
    term.emitData('\x03');
    expect(wrapper.emitted('data')).toBeUndefined();

    // 控制权恢复 → 横幅消失 + stdin 重开 + 输入恢复派发。
    await wrapper.setProps({ readonly: false, readonlyReason: null });
    await vi.waitFor(() => {
      expect(wrapper.find('[data-testid="terminal-readonly-indicator"]').exists()).toBe(false);
    });
    expect(term.options.disableStdin).toBe(false);
    expect(term.options.cursorStyle).toBe('block');
    term.emitData('\x1b[A');
    expect(wrapper.emitted('data')).toEqual([['\x1b[A']]);
    wrapper.unmount();
  });

  it('卸载后排空：直播尾部合成帧不丢', async () => {
    const sub = makeSubscribe();
    const { wrapper, term } = await mountAndWaitInstance({
      subscribe: sub.subscribe,
      wsAttached: false,
    });
    sub.emit(PI_SPINNER_CR.frames[3]);
    wrapper.unmount();
    expect(term.written.join('')).toBe(PI_SPINNER_CR.frames[3]);
    await sleep();
    expect(term.written.join('')).toBe(PI_SPINNER_CR.frames[3]);
  });
});
