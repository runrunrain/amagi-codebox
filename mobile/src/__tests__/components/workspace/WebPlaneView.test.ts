/**
 * __tests__/components/workspace/WebPlaneView.test.ts — WebPlaneView 单元测试（C2/C4）
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import WebPlaneView from '../../../components/workspace/WebPlaneView.vue';

describe('WebPlaneView.vue', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('渲染 iframe 且携带冻结 sandbox 属性与相对 url', () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
      },
    });

    const iframe = wrapper.find('iframe');
    expect(iframe.exists()).toBe(true);
    expect(iframe.attributes('src')).toBe('/webui/sess-1/#/t=token123');
    expect(iframe.attributes('sandbox')).toBe('allow-scripts allow-forms');
    expect(iframe.attributes('title')).toBe('Web 会话平面');
  });

  it('初始处于 loading 态，iframe 触发 load 事件后进入 loaded 态', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
      },
    });

    // 初始 loading 遮罩在位
    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="webplane-error"]').exists()).toBe(false);

    // 触发 iframe load 事件
    await wrapper.find('iframe').trigger('load');

    // loading 遮罩消失
    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="webplane-error"]').exists()).toBe(false);
  });

  it('iframe 触发 error 事件时进入 error 态并发出 error 事件', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
      },
    });

    await wrapper.find('iframe').trigger('error');

    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="webplane-error"]').exists()).toBe(true);
    expect(wrapper.emitted('error')?.[0]).toEqual(['sess-1']);
  });

  it('10 秒看门狗超时未加载完成时进入 error 态', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
      },
    });

    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(true);

    // 推进 10s
    await vi.advanceTimersByTimeAsync(10_000);

    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="webplane-error"]').exists()).toBe(true);
    expect(wrapper.emitted('error')?.[0]).toEqual(['sess-1']);
  });

  it('在 error 态下点击重试触发 retry 事件并重新进入 loading 态', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
      },
    });

    await wrapper.find('iframe').trigger('error');
    expect(wrapper.find('[data-testid="webplane-error"]').exists()).toBe(true);

    // 点击重试
    const retryBtn = wrapper.find('.plane-btn--primary');
    await retryBtn.trigger('click');

    expect(wrapper.emitted('retry')?.[0]).toEqual(['sess-1']);
    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="webplane-error"]').exists()).toBe(false);
  });

  it('点击切回终端发出 switchToTerminal 事件', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
      },
    });

    await wrapper.find('iframe').trigger('error');

    const switchBtn = wrapper.findAll('.plane-btn').find((btn) => btn.text().includes('切回终端'));
    expect(switchBtn?.exists()).toBe(true);
    await switchBtn?.trigger('click');

    expect(wrapper.emitted('switchToTerminal')).toBeTruthy();
  });

  it('ended 为 true 时展示会话已结束栏', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token123',
        sessionId: 'sess-1',
        ended: true,
      },
    });

    const endedBar = wrapper.find('[data-testid="webplane-ended-bar"]');
    expect(endedBar.exists()).toBe(true);
    expect(endedBar.text()).toContain('会话已结束');

    const switchBtn = endedBar.find('button');
    await switchBtn.trigger('click');
    expect(wrapper.emitted('switchToTerminal')).toBeTruthy();
  });

  it('url 变更时重新进入 loading 并更新 iframe src', async () => {
    const wrapper = mount(WebPlaneView, {
      props: {
        url: '/webui/sess-1/#/t=token1',
        sessionId: 'sess-1',
      },
    });

    await wrapper.find('iframe').trigger('load');
    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(false);

    await wrapper.setProps({ url: '/webui/sess-1/#/t=token2' });

    expect(wrapper.find('iframe').attributes('src')).toBe('/webui/sess-1/#/t=token2');
    expect(wrapper.find('[data-testid="webplane-loading"]').exists()).toBe(true);
  });
});
