/**
 * ActionMenu 单测（契约 §8）：
 *  - 折叠态菜单项不在输出中（含 ctx.teleports）；
 *  - expose choose(key) → onSelect 命中。
 */
import { describe, expect, it, vi } from 'vitest';
import ActionMenu from '../../../../components/extensions/ui/ActionMenu.vue';
import type { ActionMenuItem } from '../../../../components/extensions/pluginViewTypes';
import { instanceOf, renderWithCapture } from './testUtils';

const ITEMS: ActionMenuItem[] = [
  { key: 'add-market', label: '添加市场' },
  { key: 'manual-install', label: '手动安装' },
];

describe('ActionMenu 渲染', () => {
  it('触发器文字渲染，菜单项不在折叠态输出（含 teleports）', async () => {
    const { html, teleports } = await renderWithCapture(ActionMenu, {
      triggerText: '添加',
      items: ITEMS,
    });
    expect(html).toContain('添加');
    expect(html).toContain('▾');
    // 折叠态：菜单项既不在主输出，也不在 teleport 弹层
    expect(html).not.toContain('添加市场');
    expect(html).not.toContain('手动安装');
    expect(teleports.body ?? '').not.toContain('添加市场');
  });

  it('disabled 项渲染但带禁用标记', async () => {
    // open 只能经客户端点击置真，SSR 直渲折叠态；disabled 项以 items 数据断言
    const onDisabled = vi.fn();
    const { instances } = await renderWithCapture(ActionMenu, {
      triggerText: '添加',
      items: [{ key: 'noop', label: '禁用项', disabled: true }],
      onSelect: onDisabled,
    });
    instanceOf(instances, ActionMenu).exposed?.choose('noop');
    expect(onDisabled).not.toHaveBeenCalled();
  });
});

describe('ActionMenu 交互（expose 触发器）', () => {
  it('expose choose(key) → onSelect 命中', async () => {
    const onSelect = vi.fn();
    const { instances } = await renderWithCapture(ActionMenu, {
      triggerText: '添加',
      items: ITEMS,
      onSelect,
    });
    instanceOf(instances, ActionMenu).exposed?.choose('add-market');
    expect(onSelect).toHaveBeenCalledWith('add-market');
    instanceOf(instances, ActionMenu).exposed?.choose('manual-install');
    expect(onSelect).toHaveBeenCalledWith('manual-install');
  });
});
