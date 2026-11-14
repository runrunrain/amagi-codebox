/**
 * PluginSubItemsPanel 单测（契约 §5.3/§8）：
 *  - Claude：subItems 直渲 + Switch 可切换，expose handleToggleItem → toggleSubItem(type, name, value)；
 *  - Codex：skills/agents/commands/hooks/mcpServers 合成 + 只读（无 switch 标记）。
 */
import { describe, expect, it, vi } from 'vitest';
import PluginSubItemsPanel from '../../../../components/extensions/PluginSubItemsPanel.vue';
import type { PluginDetailInput, PluginSubItemView } from '../../../../components/extensions/pluginViewTypes';
import { instanceOf, renderWithCapture } from '../ui/testUtils';

describe('Claude 子项（后端聚合 subItems，可切换）', () => {
  it('渲染类型徽章 + 名称 + 开关', async () => {
    const detail: PluginDetailInput = {
      subItems: [
        { type: 'skill', name: 's1', enabled: true, selectable: true },
        { type: 'hook', name: 'h1', enabled: false, selectable: true },
      ],
    };
    const { html } = await renderWithCapture(PluginSubItemsPanel, {
      engine: 'claude',
      pluginDetail: detail,
    });

    expect(html).toContain('s1');
    expect(html).toContain('h1');
    expect(html).toContain('skill');
    expect(html).toContain('hook');
    // Claude 子项行可切换（§5.3：唯一真正落盘路径）
    const switchCount = (html.match(/role="switch"/g) ?? []).length;
    expect(switchCount).toBe(2);
  });

  it('expose handleToggleItem → toggleSubItem(type 单数, name, value)', async () => {
    const onToggleSubItem = vi.fn();
    const { instances } = await renderWithCapture(PluginSubItemsPanel, {
      engine: 'claude',
      pluginDetail: { subItems: [{ type: 'skill', name: 's1', enabled: true }] },
      onToggleSubItem,
    });
    const item: PluginSubItemView = { type: 'skill', name: 's1' };
    instanceOf(instances, PluginSubItemsPanel).exposed?.handleToggleItem(item, false);
    expect(onToggleSubItem).toHaveBeenCalledWith('skill', 's1', false);
  });
});

describe('Codex 子项（合成归一化，只读）', () => {
  it('skills/agents/commands/hooks/mcpServers 合成渲染，行内无开关', async () => {
    const detail: PluginDetailInput = {
      skills: [{ name: 'sk1', description: '技能描述', filePath: '/f' }],
      commands: [{ name: 'cmd1', description: '', filePath: '/c' }],
      hooks: [{ name: 'hk1', event: 'PostToolUse', filePath: '/h' }],
      hasMcp: true,
      mcpServers: { 'srv-1': {} },
    };
    const { html } = await renderWithCapture(PluginSubItemsPanel, {
      engine: 'codex',
      pluginDetail: detail,
    });

    expect(html).toContain('sk1');
    expect(html).toContain('cmd1');
    expect(html).toContain('hk1');
    expect(html).toContain('srv-1');
    expect(html).toContain('Model Context Protocol 服务器');
    // Codex 子项禁用为后端 no-op（docs/user/plugins.md:104）→ 不渲染开关
    expect(html).not.toContain('role="switch"');
  });
});
