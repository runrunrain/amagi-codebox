import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import KeyTray from '../../../components/workspace/KeyTray.vue';

describe('KeyTray', () => {
  it('渲染全部终端快捷键并在 canWrite=true 时允许点击派发对应序列', async () => {
    const wrapper = mount(KeyTray, {
      props: { canWrite: true },
    });

    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);

    const escBtn = wrapper.find('[data-testid="key-esc"]');
    expect(escBtn.exists()).toBe(true);
    expect(escBtn.attributes('disabled')).toBeUndefined();
    await escBtn.trigger('click');

    expect(wrapper.emitted('sendKey')).toEqual([['\x1b']]);

    // 测试 Ctrl+C
    const ctrlCBtn = wrapper.find('[data-testid="key-ctrl-c"]');
    await ctrlCBtn.trigger('click');
    expect(wrapper.emitted('sendKey')![1]).toEqual(['\x03']);

    // 测试 Tab
    const tabBtn = wrapper.find('[data-testid="key-tab"]');
    await tabBtn.trigger('click');
    expect(wrapper.emitted('sendKey')![2]).toEqual(['\t']);

    // 测试方向键 Up
    const upBtn = wrapper.find('[data-testid="key-up"]');
    await upBtn.trigger('click');
    expect(wrapper.emitted('sendKey')![3]).toEqual(['\x1b[A']);

    // 测试 ⌥W
    const altWBtn = wrapper.find('[data-testid="key-alt-w"]');
    await altWBtn.trigger('click');
    expect(wrapper.emitted('sendKey')![4]).toEqual(['\x1bw']);
  });

  it('canWrite=false 时所有按键禁用且不派发 sendKey 事件', async () => {
    const wrapper = mount(KeyTray, {
      props: { canWrite: false },
    });

    const escBtn = wrapper.find('[data-testid="key-esc"]');
    expect(escBtn.attributes('disabled')).toBeDefined();
    await escBtn.trigger('click');

    expect(wrapper.emitted('sendKey')).toBeUndefined();
  });

  it('Alt 修饰键开关：点击激活高亮，后续按键带 \x1b 前缀并复位', async () => {
    const wrapper = mount(KeyTray, {
      props: { canWrite: true },
    });

    const altBtn = wrapper.find('[data-testid="key-alt-modifier"]');
    expect(altBtn.classes()).not.toContain('key-tray-btn--active');

    // 激活 Alt
    await altBtn.trigger('click');
    expect(altBtn.classes()).toContain('key-tray-btn--active');

    // 点击 Tab -> 应发出 \x1b\t
    const tabBtn = wrapper.find('[data-testid="key-tab"]');
    await tabBtn.trigger('click');
    expect(wrapper.emitted('sendKey')).toEqual([['\x1b\t']]);

    // Alt 自动复位
    expect(altBtn.classes()).not.toContain('key-tray-btn--active');
  });

  // P1-C：P1-A 报告 §4.3 data-testid 全表回归——每个键位逐一存在且派发精确序列。
  it('全按键 testid 契约（P1-A §4.3）：14 键逐一存在且派发精确 ANSI 序列', async () => {
    const wrapper = mount(KeyTray, { props: { canWrite: true } });

    /** §4.3 契约表：testid → 精确 ANSI 序列（modifier 键派发 null，单独断言）。 */
    const CONTRACT: [string, string | null][] = [
      ['key-esc', '\x1b'],
      ['key-tab', '\t'],
      ['key-ctrl-c', '\x03'],
      ['key-ctrl-d', '\x04'],
      ['key-ctrl-l', '\x0c'],
      ['key-enter', '\r'],
      ['key-up', '\x1b[A'],
      ['key-down', '\x1b[B'],
      ['key-left', '\x1b[D'],
      ['key-right', '\x1b[C'],
      ['key-alt-modifier', null],
      ['key-alt-w', '\x1bw'],
      ['key-alt-t', '\x1bt'],
      ['key-alt-r', '\x1br'],
    ];

    expect(wrapper.findAll('[data-testid="terminal-key-tray"] button')).toHaveLength(CONTRACT.length);

    let emittedCount = 0;
    for (const [testid, sequence] of CONTRACT) {
      const btn = wrapper.find(`[data-testid="${testid}"]`);
      expect(btn.exists(), `缺失 §4.3 契约键位: ${testid}`).toBe(true);
      expect(btn.attributes('disabled')).toBeUndefined();
      await btn.trigger('click');
      if (sequence === null) {
        // modifier 键只切换修饰态，不派发序列
        expect(btn.classes()).toContain('key-tray-btn--active');
        expect(wrapper.emitted('sendKey')).toHaveLength(emittedCount);
        // 复位修饰态，避免污染后续键位断言
        await btn.trigger('click');
        expect(btn.classes()).not.toContain('key-tray-btn--active');
      } else {
        emittedCount += 1;
        const emitted = wrapper.emitted('sendKey');
        expect(emitted).toHaveLength(emittedCount);
        expect(emitted![emittedCount - 1]).toEqual([sequence]);
      }
    }
  });

  it('Alt 修饰键不重复前缀：已带 \x1b 的键（⌥W）保持单前缀；Alt+Enter → \x1b\r', async () => {
    const wrapper = mount(KeyTray, { props: { canWrite: true } });
    const altBtn = wrapper.find('[data-testid="key-alt-modifier"]');

    // Alt 激活后点击 ⌥W（序列本身已以 \x1b 开头）→ 不得双重前缀
    await altBtn.trigger('click');
    await wrapper.find('[data-testid="key-alt-w"]').trigger('click');
    expect(wrapper.emitted('sendKey')).toEqual([['\x1bw']]);
    expect(altBtn.classes()).not.toContain('key-tray-btn--active');

    // Alt 激活后点击 Enter（无前缀）→ \x1b\r
    await altBtn.trigger('click');
    await wrapper.find('[data-testid="key-enter"]').trigger('click');
    expect(wrapper.emitted('sendKey')![1]).toEqual(['\x1b\r']);
  });
});
