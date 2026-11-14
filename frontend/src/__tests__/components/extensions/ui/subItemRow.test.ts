/**
 * SubItemRow 单测（契约 §8）：
 *  - toggleable=false（默认）无 switch；
 *  - expose toggleWith(v) → onToggle 命中。
 */
import { describe, expect, it, vi } from 'vitest';
import SubItemRow from '../../../../components/extensions/ui/SubItemRow.vue';
import { instanceOf, renderWithCapture } from './testUtils';

describe('SubItemRow 渲染', () => {
  it('类型徽章、名称、单行描述渲染', async () => {
    const { html } = await renderWithCapture(SubItemRow, {
      name: 'build-agent',
      description: '构建代理子项',
      typeBadge: '代理',
    });
    expect(html).toContain('build-agent');
    expect(html).toContain('构建代理子项');
    expect(html).toContain('type-badge');
  });

  it('toggleable=false（默认）时无 switch', async () => {
    const { html } = await renderWithCapture(SubItemRow, {
      name: 'readonly-item',
      typeBadge: '技能',
    });
    expect(html).not.toContain('role="switch"');
  });

  it('toggleable=true 时渲染行尾开关', async () => {
    const { html } = await renderWithCapture(SubItemRow, {
      name: 'toggleable-item',
      typeBadge: 'mcp',
      enabled: true,
      toggleable: true,
    });
    expect(html).toContain('role="switch"');
  });
});

describe('SubItemRow 交互（expose 触发器）', () => {
  it('expose toggleWith(v) → onToggle 命中且参数透传', async () => {
    const onToggle = vi.fn();
    const { instances } = await renderWithCapture(SubItemRow, {
      name: 'toggle-item',
      typeBadge: 'skill',
      enabled: false,
      toggleable: true,
      onToggle,
    });
    instanceOf(instances, SubItemRow).exposed?.toggleWith(true);
    expect(onToggle).toHaveBeenCalledWith(true);
  });
});
