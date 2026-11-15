<!-- 契约 §5.3/§6.1：详情页子项列表。沿用既有归一化逻辑（Claude subItems 优先，
     Codex 由 skills/agents/commands/hooks/mcpServers 合成），改用共享 SubItemRow 渲染。
     开关诚实：Codex 子项禁用是后端 no-op（docs/user/plugins.md:104）→ 一律只读。 -->
<template>
  <div v-if="normalizedSubItems.length > 0" class="sub-items-panel">
    <GroupHeader title="子项" :count="normalizedSubItems.length" />
    <div class="sub-items-list">
      <SubItemRow
        v-for="item in normalizedSubItems"
        :key="getItemKey(item)"
        :name="item.name || 'Unknown'"
        :description="item.description"
        :type-badge="item.type"
        :enabled="item.enabled !== false"
        :toggleable="engine === 'claude'"
        @toggle="(value) => handleToggleItem(item, value)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import GroupHeader from './ui/GroupHeader.vue';
import SubItemRow from './ui/SubItemRow.vue';
import type {
  PluginDetailInput,
  PluginSubItemView,
} from './pluginViewTypes';

interface Props {
  engine?: 'claude' | 'codex';
  pluginDetail?: PluginDetailInput | null;
}

const props = withDefaults(defineProps<Props>(), {
  engine: 'claude',
  pluginDetail: null,
});

const emit = defineEmits<{
  (e: 'toggleSubItem', itemType: string, itemId: string, enabled: boolean): void;
}>();

// 归一化的子项列表：把任意形态的 pluginDetail 统一成 PluginSubItemView[] 形态
// - Claude 引擎：plugin.PluginDetail 自带 subItems（后端聚合好，含 enabled 标记）→ 直接用
// - Codex 引擎：CodexPluginDetail 没有 subItems 字段 → 从 skills/agents/commands/hooks/mcpServers 合成
// 合成时为每个条目补充 type 字段（单数），与后端 SubItemType 一致
const normalizedSubItems = computed<PluginSubItemView[]>(() => {
  const detail = props.pluginDetail;
  if (!detail) return [];

  // 优先用后端聚合好的 subItems（Claude 路径）
  if (Array.isArray(detail.subItems) && detail.subItems.length > 0) {
    return detail.subItems;
  }

  // 降级合成：从原始 skills/agents/commands/hooks/mcpServers 构建（Codex 路径）
  const items: PluginSubItemView[] = [];
  (detail.skills || []).forEach((s) => {
    items.push({
      type: 'skill',
      name: s.name || '',
      description: s.description || '',
      filePath: s.filePath,
      enabled: true,
    });
  });
  (detail.agents || []).forEach((s) => {
    items.push({
      type: 'agent',
      name: s.name || '',
      description: s.description || '',
      filePath: s.filePath,
      enabled: true,
    });
  });
  (detail.commands || []).forEach((s) => {
    items.push({
      type: 'command',
      name: s.name || '',
      description: s.description || '',
      filePath: s.filePath,
      enabled: true,
    });
  });
  (detail.hooks || []).forEach((s) => {
    items.push({
      type: 'hook',
      name: s.name || s.event || '',
      description: s.description || '',
      event: s.event,
      enabled: true,
    });
  });
  if (detail.hasMcp) {
    // 多个 MCP server 各成一项；mcpServers 为空时回退单项展示。
    // mcpServers 的 value 结构未知，不解析、不虚构连接状态（契约 §6.1.1）
    const servers = detail.mcpServers || {};
    const serverNames = Object.keys(servers);
    if (serverNames.length > 0) {
      serverNames.forEach((name) => {
        items.push({
          type: 'mcp',
          name,
          description: 'Model Context Protocol 服务器',
          enabled: true,
        });
      });
    } else {
      items.push({
        type: 'mcp',
        name: 'MCP Servers',
        description: 'Model Context Protocol 服务器',
        enabled: true,
      });
    }
  }
  return items;
});

function getItemKey(item: PluginSubItemView): string {
  // type+name 唯一标识；MCP 多 server 场景 name 即 server 名
  return `${item.type}:${item.name || item.filePath || ''}`;
}

// 开关切换：emit 的 itemType 用 item.type（单数），与后端 SubItemType 一致。
// 仅 Claude 可达（Codex 行 toggleable=false 不渲染 Switch）
function handleToggleItem(item: PluginSubItemView, value: boolean) {
  emit('toggleSubItem', item.type, item.name, value);
}

// Expose handler for parent/tests to call directly（先例：契约 §8）
defineExpose({
  handleToggleItem
});
</script>

<style scoped>
/* 详情页内全宽使用（列表↔详情分页化）：列表区卡片化，与各面板行式列表一致 */
.sub-items-panel {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.sub-items-list {
  display: flex;
  flex-direction: column;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: var(--radius-card);
  padding: 6px;
}

.sub-items-list :deep(.sub-item-row) + :deep(.sub-item-row) {
  border-top: 1px solid var(--separator);
}
</style>
