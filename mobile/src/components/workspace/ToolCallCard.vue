<script setup lang="ts">
/**
 * ToolCallCard — 工具调用（§4.3.1 / §6 ToolCallCard 组件契约）
 * ---------------------------------------------------------------------------
 * 类型图标 + 语义标题；有可展开摘要时提供展开（≥44px toggle）。
 * 图标+文字双通道，不只靠颜色。
 * ---------------------------------------------------------------------------
 */
import { computed, ref } from 'vue';
import type { ToolItem } from '../../lib/timeline';

const props = defineProps<{ item: ToolItem }>();

const expanded = ref(false);

/** 工具类型 → 语义图标（SVG path）。 */
const TOOL_ICONS: Record<string, string> = {
  bash: 'M4 17l6-6-6-6M12 19h8',
  read: 'M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2zM22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z',
  write: 'M12 20h9M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4z',
  edit: 'M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4z',
  search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.35-4.35',
  task: 'M9 11l3 3L22 4M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11',
  file: 'M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9zM13 2v7h7',
  default: 'M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z',
};

const icon = computed(() => {
  const name = props.item.toolName.toLowerCase();
  if (name.includes('bash') || name.includes('command') || name.includes('run')) return TOOL_ICONS.bash;
  if (name.includes('read') || name.includes('open')) return TOOL_ICONS.read;
  if (name.includes('write') || name.includes('create')) return TOOL_ICONS.write;
  if (name.includes('edit') || name.includes('patch')) return TOOL_ICONS.edit;
  if (name.includes('grep') || name.includes('search') || name.includes('glob') || name.includes('find') || name.includes('list')) return TOOL_ICONS.search;
  if (name.includes('task')) return TOOL_ICONS.task;
  if (name.includes('file') || name.includes('delete')) return TOOL_ICONS.file;
  return TOOL_ICONS.default;
});
</script>

<template>
  <section class="tool-card">
    <button
      v-if="item.detail"
      type="button"
      class="tool-head tool-head--toggle"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <svg class="tool-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path :d="icon" />
      </svg>
      <span class="tool-title">{{ item.title }}</span>
      <svg class="tool-chevron" :class="{ 'chevron--down': expanded }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <polyline points="9 18 15 12 9 6" />
      </svg>
    </button>
    <div v-else class="tool-head">
      <svg class="tool-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path :d="icon" />
      </svg>
      <span class="tool-title">{{ item.title }}</span>
    </div>
    <p v-if="expanded && item.detail" class="tool-detail">{{ item.detail }}</p>
  </section>
</template>

<style scoped>
.tool-card {
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-control);
  border-radius: 10px;
  overflow: hidden;
}

.tool-head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 44px;
  padding: 6px 12px;
  border: none;
  background: transparent;
  color: var(--VT-text);
  font-size: 13px;
  text-align: left;
}

.tool-head--toggle {
  cursor: pointer;
}

.tool-head--toggle:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.tool-icon {
  flex-shrink: 0;
  color: var(--VT-control);
}

.tool-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}

.tool-chevron {
  flex-shrink: 0;
  color: var(--VT-text-secondary);
}

.chevron--down {
  transform: rotate(90deg);
}

.tool-detail {
  margin: 0;
  padding: 8px 12px 12px;
  border-top: 1px solid var(--VT-border);
  color: var(--VT-text-secondary);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

@media (prefers-reduced-motion: reduce) {
  .chevron--down {
    transition: none;
  }
}
</style>
