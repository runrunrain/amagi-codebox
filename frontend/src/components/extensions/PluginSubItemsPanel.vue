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
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import Switch from '../ui/Switch.vue';
import EmptyState from '../ui/EmptyState.vue';

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

// Available tabs based on engine and plugin detail
const availableTabs = computed(() => {
  const tabs = [{ value: 'all', label: '全部', count: 0 }];

  if (props.engine === 'codex' && props.pluginDetail) {
    const detail = props.pluginDetail;
    if (detail.skills?.length > 0) {
      tabs.push({ value: 'skills', label: 'Skills', count: detail.skills.length });
    }
    if (detail.agents?.length > 0) {
      tabs.push({ value: 'agents', label: 'Agents', count: detail.agents.length });
    }
    if (detail.commands?.length > 0) {
      tabs.push({ value: 'commands', label: 'Commands', count: detail.commands.length });
    }
    if (detail.hooks?.length > 0) {
      tabs.push({ value: 'hooks', label: 'Hooks', count: detail.hooks.length });
    }
    if (detail.hasMcp) {
      tabs.push({ value: 'mcp', label: 'MCP', count: 1 });
    }
  }

  return tabs;
});

// Items for current tab
const items = computed(() => {
  if (!props.pluginDetail || activeTab.value === 'all') return [];

  const detail = props.pluginDetail;
  switch (activeTab.value) {
    case 'skills':
      return detail.skills || [];
    case 'agents':
      return detail.agents || [];
    case 'commands':
      return detail.commands || [];
    case 'hooks':
      return detail.hooks || [];
    case 'mcp':
      // MCP is a boolean flag, return a pseudo item for display
      return detail.hasMcp ? [{ name: 'MCP Servers', type: 'mcp', enabled: true }] : [];
    default:
      return [];
  }
});

const emptyTitle = computed(() => {
  switch (activeTab.value) {
    case 'skills':
      return '暂无 Skills';
    case 'agents':
      return '暂无 Agents';
    case 'commands':
      return '暂无 Commands';
    case 'hooks':
      return '暂无 Hooks';
    case 'mcp':
      return '未配置 MCP';
    default:
      return '暂无内容';
  }
});

const emptyDesc = computed(() => {
  switch (activeTab.value) {
    case 'skills':
      return '此插件未提供 Skills';
    case 'agents':
      return '此插件未提供 Agents';
    case 'commands':
      return '此插件未提供 Commands';
    case 'hooks':
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
  if (item.type === 'mcp') return 'mcp';
  return item.name || item.filePath || Math.random().toString(36);
}

function getItemName(item: any): string {
  if (item.type === 'mcp') return 'MCP Servers';
  return item.name || 'Unknown';
}

function getItemDesc(item: any): string {
  if (item.type === 'mcp') return 'Model Context Protocol 服务器';
  return item.description || '';
}

function getItemMeta(item: any): string {
  if (item.type === 'mcp') return '';
  if (item.event) return `Event: ${item.event}`;
  if (item.type) return `Type: ${item.type}`;
  return '';
}

function isItemDisabled(item: any): boolean {
  if (item.type === 'mcp') {
    // For MCP, check if plugin-level MCP is disabled
    const key = `mcp`;
    return props.disabledSubItems?.includes(key) || false;
  }
  const key = `${activeTab.value}/${item.name}`;
  return props.disabledSubItems?.includes(key) || false;
}

function handleToggleItem(item: any, value: boolean) {
  if (item.type === 'mcp') {
    emit('toggleSubItem', 'mcp', 'mcp', value);
  } else {
    emit('toggleSubItem', activeTab.value, item.name, value);
  }
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
