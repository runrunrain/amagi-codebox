/**
 * __tests__/components/workspace/raw-terminal-view-e10.test.ts — PG-04 E-10 回落（M2-D）
 * 覆盖：引擎（xterm chunk）加载失败 → 明示诊断视图不可用 + 原因 + 回落指引，
 * 不假装可用。独立文件：vi.mock 工厂在模块注册表内只跑一次（failImport 注入），
 * 与其他加载用例互不干扰。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import RawTerminalView from '../../../components/workspace/RawTerminalView.vue';

vi.mock('@xterm/xterm', async () => {
  throw new Error('chunk load failed (simulated)');
});

vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit(): void {} } }));

class FakeResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

const noopSubscribe = () => () => {};

describe('RawTerminalView E-10 回落', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('引擎加载失败 → 明示不可用与回落指引，不假装可用', async () => {
    const wrapper = mount(RawTerminalView, {
      props: { initialTranscript: '', subscribe: noopSubscribe, wsAttached: false },
    });
    await vi.waitFor(
      () => {
        expect(wrapper.text()).toContain('诊断视图不可用');
      },
      { timeout: 3000, interval: 10 },
    );
    expect(wrapper.text()).toContain('终端引擎未能加载');
    expect(wrapper.text()).toContain('MonoBlock');
    expect(wrapper.find('[role="alert"]').exists()).toBe(true);
    wrapper.unmount();
  });
});
