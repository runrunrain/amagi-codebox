<template>
  <div class="sub-items-panel">
    <!-- Segmented control for sub-item types -->
    <div class="sub-tabs">
      <button
        v-for="tab in availableTabs"
        :key="tab.value"
        :class="['sub-tab-btn', { active: activeTab === tab.value }]"
        @click="setActiveTab(tab.value)"
      >
        {{ tab.label }}
        <span v-if="tab.count > 0" class="tab-count">{{ tab.count }}</span>
      </button>
    </div>

    <!-- Sub-items list -->
    <div v-if="activeTab !== 'all'" class="sub-items-list">
      <div v-if="items.length === 0" class="empty-sub-items">
        <EmptyState
          icon="○"
          :title="emptyTitle"
          :description="emptyDesc"
        />
      </div>
      <div v-else class="sub-items">
        <div
          v-for="item in items"
          :key="getItemKey(item)"
          :class="['sub-item-row', { disabled: isItemDisabled(item) }]"
        >
          <div class="sir-main">
            <div class="sir-name">{{ getItemName(item) }}</div>
            <div v-if="getItemDesc(item)" class="sir-desc">{{ getItemDesc(item) }}</div>
            <div v-if="getItemMeta(item)" class="sir-meta">{{ getItemMeta(item) }}</div>
          </div>
          <Switch
            :model-value="!isItemDisabled(item)"
            @update:model-value="(val) => handleToggleItem(item, val)"
          />
        </div>
      </div>
    </div>
    <!-- Fallback empty state when 'all' tab is active (no content available) -->
    <div v-else class="sub-items-list">
      <div class="empty-sub-items">
        <EmptyState
          icon="○"
          title="暂无内容"
          description="此插件未提供任何可管理的子项"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import Switch from '../ui/Switch.vue';
import EmptyState from '../ui/EmptyState.vue';

// 子项类型：与后端 internal/plugin/types.go SubItemType 单数保持一致
// （skill / agent / command / hook / mcp；claude 为 baseline，不在面板展示）
type SubItemType = 'skill' | 'agent' | 'command' | 'hook' | 'mcp';

interface Props {
  engine?: 'claude' | 'codex';
  pluginDetail?: any;
  disabledSubItems?: string[];
}

const props = withDefaults(defineProps<Props>(), {
  engine: 'claude',
  pluginDetail: null,
  disabledSubItems: () => [],
});

const emit = defineEmits<{
  (e: 'toggleSubItem', itemType: string, itemId: string, enabled: boolean): void;
}>();

const activeTab = ref<string>('all');

// 归一化的子项列表：把任意形态的 pluginDetail 统一成 SubItem[] 形态
// - Claude 引擎：plugin.PluginDetail 自带 subItems（后端聚合好，含 enabled 标记）→ 直接用
// - Codex 引擎：CodexPluginDetail 没有 subItems 字段 → 从 skills/agents/commands/hooks/mcpServers 合成
// - 兜底：若 subItems 为空数组且 detail 没有原始字段，返回空
// 合成时为每个条目补充 type 字段（单数），与后端 SubItemType 一致
const normalizedSubItems = computed(() => {
  const detail = props.pluginDetail;
  if (!detail) return [];

  // 优先用后端聚合好的 subItems（Claude 路径）
  if (Array.isArray(detail.subItems) && detail.subItems.length > 0) {
    return detail.subItems;
  }

  // 降级合成：从原始 skills/agents/commands/hooks/mcpServers 构建（Codex 路径）
  const items: any[] = [];
  (detail.skills || []).forEach((s: any) => {
    items.push({
      type: 'skill',
      name: s.name,
      description: s.description || '',
      filePath: s.filePath,
      enabled: true,
    });
  });
  (detail.agents || []).forEach((s: any) => {
    items.push({
      type: 'agent',
      name: s.name,
      description: s.description || '',
      filePath: s.filePath,
      enabled: true,
    });
  });
  (detail.commands || []).forEach((s: any) => {
    items.push({
      type: 'command',
      name: s.name,
      description: s.description || '',
      filePath: s.filePath,
      enabled: true,
    });
  });
  (detail.hooks || []).forEach((s: any) => {
    items.push({
      type: 'hook',
      name: s.name || s.event,
      description: s.description || '',
      event: s.event,
      enabled: true,
    });
  });
  if (detail.hasMcp) {
    // 多个 MCP server 各成一项（顺带解决 G5）；mcpServers 为空时回退单项展示
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

// 按 type 分组：{ skill: [], agent: [], command: [], hook: [], mcp: [] }
const groupedSubItems = computed(() => {
  const groups: Record<SubItemType, any[]> = {
    skill: [],
    agent: [],
    command: [],
    hook: [],
    mcp: [],
  };
  normalizedSubItems.value.forEach((item: any) => {
    const t = item.type as SubItemType;
    if (groups[t]) groups[t].push(item);
  });
  return groups;
});

// 子项类型展示顺序与标签（tab 值统一为单数，与后端 SubItemType 一致）
const TAB_ORDER: { value: SubItemType; label: string }[] = [
  { value: 'skill', label: 'Skills' },
  { value: 'agent', label: 'Agents' },
  { value: 'command', label: 'Commands' },
  { value: 'hook', label: 'Hooks' },
  { value: 'mcp', label: 'MCP' },
];

// Available tabs：双引擎统一构建（去掉 engine === 'codex' 守卫）
// - 从 groupedSubItems 按类型顺序聚合，仅展示有内容的 tab
const availableTabs = computed(() => {
  const tabs: { value: string; label: string; count: number }[] = [
    { value: 'all', label: '全部', count: 0 },
  ];
  const groups = groupedSubItems.value;
  TAB_ORDER.forEach(({ value, label }) => {
    const count = groups[value].length;
    if (count > 0) {
      tabs.push({ value, label, count });
    }
  });
  return tabs;
});

// 默认选中第一个有内容的 tab（非 'all'）。
// - 切换插件 / pluginDetail 变化时自动重算
// - 若所有 tab 都没有内容，保持 'all'（交给下方 empty 区块提示）
watch(
  () => availableTabs.value,
  (tabs) => {
    const firstWithContent = tabs.find((t) => t.value !== 'all' && t.count > 0);
    if (firstWithContent) {
      activeTab.value = firstWithContent.value;
    } else {
      activeTab.value = 'all';
    }
  },
  { immediate: true }
);

// Items for current tab：直接从分组结果取
const items = computed(() => {
  if (activeTab.value === 'all') return [];
  const t = activeTab.value as SubItemType;
  return groupedSubItems.value[t] || [];
});

const emptyTitle = computed(() => {
  switch (activeTab.value) {
    case 'skill':
      return '暂无 Skills';
    case 'agent':
      return '暂无 Agents';
    case 'command':
      return '暂无 Commands';
    case 'hook':
      return '暂无 Hooks';
    case 'mcp':
      return '未配置 MCP';
    default:
      return '暂无内容';
  }
});

const emptyDesc = computed(() => {
  switch (activeTab.value) {
    case 'skill':
      return '此插件未提供 Skills';
    case 'agent':
      return '此插件未提供 Agents';
    case 'command':
      return '此插件未提供 Commands';
    case 'hook':
      return '此插件未提供 Hooks';
    case 'mcp':
      return '此插件未配置 MCP 服务器';
    default:
      return '';
  }
});

function setActiveTab(tab: string) {
  activeTab.value = tab;
}

function getItemKey(item: any): string {
  // type+name 唯一标识；MCP 多 server 场景 name 即 server 名
  return `${item.type}:${item.name || item.filePath || Math.random().toString(36)}`;
}

function getItemName(item: any): string {
  return item.name || 'Unknown';
}

function getItemDesc(item: any): string {
  return item.description || '';
}

function getItemMeta(item: any): string {
  if (item.event) return `Event: ${item.event}`;
  if (item.type) return `Type: ${item.type}`;
  return '';
}

// 判断子项是否被禁用：disabledSubItems 是 "type/name" 字符串数组（type 单数）
function isItemDisabled(item: any): boolean {
  const key = `${item.type}/${item.name}`;
  return props.disabledSubItems?.includes(key) || false;
}

// 开关切换：emit 的 itemType 用 item.type（单数），与后端 SubItemType 一致
function handleToggleItem(item: any, value: boolean) {
  emit('toggleSubItem', item.type, item.name, value);
}

// Expose handler for parent to call directly
defineExpose({
  handleToggleItem
});
</script>

<style scoped>
.sub-items-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.sub-tabs {
  display: flex;
  gap: 6px;
  background: var(--control);
  padding: 4px;
  border-radius: 10px;
  width: fit-content;
}

.sub-tab-btn {
  font-size: 12px;
  padding: 5px 12px;
  border: none;
  background: transparent;
  color: var(--secondary);
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.15s;
  display: flex;
  align-items: center;
  gap: 4px;
}

.sub-tab-btn:hover {
  color: var(--label);
}

.sub-tab-btn.active {
  background: var(--label);
  color: #fff;
}

.tab-count {
  font-size: 10px;
  opacity: 0.7;
}

.sub-items-list {
  min-height: 120px;
}

.empty-sub-items {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 120px;
}

.sub-items {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.sub-item-row {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 10px 12px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
  transition: border-color 0.15s, opacity 0.15s;
}

.sub-item-row:hover {
  border-color: #c5c5cc;
}

.sub-item-row.disabled {
  opacity: 0.6;
}

.sir-main {
  flex: 1;
  min-width: 0;
}

.sir-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  margin-bottom: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sir-desc {
  font-size: 11px;
  color: var(--secondary);
  line-height: 1.4;
  margin-bottom: 2px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.sir-meta {
  font-size: 10px;
  color: var(--tertiary);
  background: var(--control);
  padding: 2px 6px;
  border-radius: 4px;
  display: inline-block;
}
</style>
