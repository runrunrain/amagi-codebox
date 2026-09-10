import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import ComposerBar from '../../../components/workspace/ComposerBar.vue';

describe('ComposerBar (Terminal Mode)', () => {
  const baseProps = {
    draft: '',
    sending: false,
    stopping: false,
    canWrite: true,
    canControl: true,
    blockReason: null,
    history: [],
    terminalMode: true,
  };

  it('terminalMode=true 时渲染终端托盘及切换按钮', () => {
    const wrapper = mount(ComposerBar, { props: baseProps });

    expect(wrapper.find('[data-testid="toggle-terminal-keys"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);
  });

  it('terminalMode=false 时不渲染终端托盘及切换按钮（其余 CLI 保持不变）', () => {
    const wrapper = mount(ComposerBar, {
      props: { ...baseProps, terminalMode: false },
    });

    expect(wrapper.find('[data-testid="toggle-terminal-keys"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(false);
  });

  it('点击切换按钮可折叠与展开 KeyTray', async () => {
    const wrapper = mount(ComposerBar, { props: baseProps });

    const toggleBtn = wrapper.find('[data-testid="toggle-terminal-keys"]');
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);

    // 点击收起
    await toggleBtn.trigger('click');
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(false);

    // 点击再次展开
    await toggleBtn.trigger('click');
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);
  });

  it('点击 KeyTray 内按键会向上传递 specialKey 事件', async () => {
    const wrapper = mount(ComposerBar, { props: baseProps });

    const escBtn = wrapper.find('[data-testid="key-esc"]');
    await escBtn.trigger('click');

    expect(wrapper.emitted('specialKey')).toEqual([['\x1b']]);
  });
});
