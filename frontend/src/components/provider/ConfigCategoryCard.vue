<!--
  ConfigCategoryCard - 可折叠配置类别卡片
  用于 OpenCode 全局配置的各类配置展示
-->
<template>
  <div class="config-cat-card">
    <button class="ccc-header" @click="$emit('toggle')">
      <div class="ccc-left">
        <svg
          :class="['ccc-icon', { expanded }]"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
        >
          <polyline points="6 9 12 15 18 9" />
        </svg>
        <span class="ccc-title">{{ title }}</span>
        <span v-if="badge !== null" class="ccc-badge">{{ badge }}</span>
      </div>
    </button>

    <div v-if="expanded" class="ccc-content">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
interface Props {
  title: string;
  expanded: boolean;
  badge?: number | null;
}

withDefaults(defineProps<Props>(), {
  badge: null,
});

defineEmits<{
  toggle: [];
}>();
</script>

<style scoped>
.config-cat-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  overflow: hidden;
}

.ccc-header {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: transparent;
  border: none;
  cursor: pointer;
  transition: background 0.15s;
}

.ccc-header:hover {
  background: var(--control);
}

.ccc-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ccc-icon {
  width: 14px;
  height: 14px;
  color: var(--tertiary);
  transition: transform 0.2s ease;
}

.ccc-icon.expanded {
  transform: rotate(180deg);
}

.ccc-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--label);
}

.ccc-badge {
  font-size: 11px;
  font-weight: 600;
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
  border-radius: 10px;
  padding: 2px 8px;
}

.ccc-content {
  padding: 0 16px 16px;
  border-top: 1px solid var(--separator);
  padding-top: 12px;
}
</style>
