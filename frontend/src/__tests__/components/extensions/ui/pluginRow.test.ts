/**
 * PluginRow 单测（契约 §8）：
 *  - switchVisible=false 时输出无 switch 标记；
 *  - badges 文本出现；
 *  - 描述行截断类名存在；
 *  - expose toggleWith(v) → onToggle 命中且参数透传。
 */
import { describe, expect, it, vi } from 'vitest';
import PluginRow from '../../../../components/extensions/ui/PluginRow.vue';
import { instanceOf, renderWithCapture } from './testUtils';

const BASE_PROPS = {
  name: 'demo-plugin',
  description: '一个用于验证行式条目的演示插件',
  badges: [
    { text: '集成', type: 'type', color: 'integration' },
    { text: 'v1.2.0', type: 'ver' },
    { text: 'official-market', type: 'source', variant: 'muted' },
  ],
};

describe('PluginRow 渲染', () => {
  it('名称、徽章文本、描述渲染', async () => {
    const { html } = await renderWithCapture(PluginRow, BASE_PROPS);
    expect(html).toContain('demo-plugin');
    expect(html).toContain('集成');
    expect(html).toContain('v1.2.0');
    expect(html).toContain('official-market');
    expect(html).toContain('一个用于验证行式条目的演示插件');
  });

  it('描述行截断类名存在', async () => {
    const { html } = await renderWithCapture(PluginRow, BASE_PROPS);
    expect(html).toContain('plugin-row-desc');
  });

  it('switchVisible=false（默认）时输出无 switch 标记', async () => {
    const { html } = await renderWithCapture(PluginRow, BASE_PROPS);
    expect(html).not.toContain('class="switch');
    expect(html).not.toContain('plugin-row-switch');
  });

  it('switchVisible=true 时渲染行尾开关', async () => {
    const { html } = await renderWithCapture(PluginRow, {
      ...BASE_PROPS,
      enabled: true,
      switchVisible: true,
    });
    expect(html).toContain('plugin-row-switch');
    expect(html).toContain('role="switch"');
  });

  it('active=true 时行带 active 类名', async () => {
    const { html } = await renderWithCapture(PluginRow, { ...BASE_PROPS, active: true });
    expect(html).toContain('plugin-row active');
  });

  it('PluginIcon 以 sm 尺寸渲染', async () => {
    const { html } = await renderWithCapture(PluginRow, BASE_PROPS);
    expect(html).toContain('plugin-icon-sm');
  });
});

describe('PluginRow 交互（expose 触发器）', () => {
  it('expose toggleWith(v) → onToggle 命中且参数透传', async () => {
    const onToggle = vi.fn();
    const { instances } = await renderWithCapture(PluginRow, {
      ...BASE_PROPS,
      enabled: false,
      switchVisible: true,
      onToggle,
    });
    instanceOf(instances, PluginRow).exposed?.toggleWith(true);
    expect(onToggle).toHaveBeenCalledWith(true);
    instanceOf(instances, PluginRow).exposed?.toggleWith(false);
    expect(onToggle).toHaveBeenCalledWith(false);
  });
});
