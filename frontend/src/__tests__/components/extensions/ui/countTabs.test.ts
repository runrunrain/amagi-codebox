/**
 * CountTabs 单测（契约 §8）：
 *  - 计数徽章显示真实数字；
 *  - hidden 标签不在输出；
 *  - 选中标签高亮类名；
 *  - expose select(key) → onUpdate:modelValue 命中、search(v) → onUpdate:searchValue 命中。
 */
import { describe, expect, it, vi } from 'vitest';
import CountTabs from '../../../../components/extensions/ui/CountTabs.vue';
import type { CountTabItem } from '../../../../components/extensions/pluginViewTypes';
import { instanceOf, renderWithCapture } from './testUtils';

const TABS: CountTabItem[] = [
  { key: 'plugins', label: '插件', count: 12 },
  { key: 'mcp', label: 'MCP', count: 3 },
  { key: 'market', label: '市场', count: 5 },
];

describe('CountTabs 渲染', () => {
  it('各标签与真实计数徽章渲染', async () => {
    const { html } = await renderWithCapture(CountTabs, {
      tabs: TABS,
      modelValue: 'plugins',
    });
    expect(html).toContain('插件');
    expect(html).toContain('MCP');
    expect(html).toContain('市场');
    expect(html).toContain('>12</span>');
    expect(html).toContain('>3</span>');
    expect(html).toContain('>5</span>');
  });

  it('hidden 标签不渲染', async () => {
    const { html } = await renderWithCapture(CountTabs, {
      tabs: [
        { key: 'plugins', label: '插件', count: 2 },
        { key: 'skills', label: '技能', count: 0, hidden: true },
      ],
      modelValue: 'plugins',
    });
    expect(html).not.toContain('技能');
    expect(html).toContain('插件');
  });

  it('选中标签带 active 类名，未选中不带', async () => {
    const { html } = await renderWithCapture(CountTabs, {
      tabs: TABS,
      modelValue: 'mcp',
    });
    expect(html).toContain('ct-tab active');
  });

  it('searchPlaceholder 提供时渲染搜索框 placeholder，未提供时不渲染', async () => {
    const withSearch = await renderWithCapture(CountTabs, {
      tabs: TABS,
      modelValue: 'plugins',
      searchPlaceholder: '搜索插件名称或描述',
    });
    expect(withSearch.html).toContain('搜索插件名称或描述');
    expect(withSearch.html).toContain('ct-search');

    const withoutSearch = await renderWithCapture(CountTabs, {
      tabs: TABS,
      modelValue: 'plugins',
    });
    expect(withoutSearch.html).not.toContain('ct-search');
  });
});

describe('CountTabs 交互（expose 触发器）', () => {
  it('expose select(key) → onUpdate:modelValue 命中', async () => {
    const onUpdate = vi.fn();
    const { instances } = await renderWithCapture(CountTabs, {
      tabs: TABS,
      modelValue: 'plugins',
      'onUpdate:modelValue': onUpdate,
    });
    instanceOf(instances, CountTabs).exposed?.select('market');
    expect(onUpdate).toHaveBeenCalledWith('market');
  });

  it('expose search(v) → onUpdate:searchValue 命中', async () => {
    const onSearch = vi.fn();
    const { instances } = await renderWithCapture(CountTabs, {
      tabs: TABS,
      modelValue: 'plugins',
      searchPlaceholder: '搜索插件名称或描述',
      'onUpdate:searchValue': onSearch,
    });
    instanceOf(instances, CountTabs).exposed?.search('foo');
    expect(onSearch).toHaveBeenCalledWith('foo');
  });
});
