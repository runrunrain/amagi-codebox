<!--
  ConfigCategoryCard - 可折叠配置类别卡片（mac Finder 略缩图风格）

  - 默认收起（由父组件 expanded prop 控制）
  - 点击 header 切换展开/收起
  - 收起态：左侧大号语义图标 + 标题 + 计数徽章 + 右侧展开箭头
  - 展开态：在 header 下渲染 slot 内容
  - 苹果HIG 克制：图标作为视觉锚点，留白克制，无过度装饰
-->
<template>
  <div :class="['config-cat-card', { collapsed: !expanded }]">
    <button
      type="button"
      class="ccc-header"
      :aria-expanded="expanded"
      @click="$emit('toggle')"
    >
      <div class="ccc-thumb" :style="thumbStyle">
        <span class="ccc-thumb-icon" v-html="categoryIcon" />
      </div>

      <div class="ccc-meta">
        <span class="ccc-title">{{ title }}</span>
        <span v-if="badge !== null && badge !== undefined" class="ccc-badge">
          {{ badge }}
        </span>
      </div>

      <svg
        class="ccc-chevron"
        :class="{ expanded }"
        viewBox="0 0 12 12"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <polyline points="3 4.5 6 7.5 9 4.5" />
      </svg>
    </button>

    <div v-if="expanded" class="ccc-content">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { ICONS, ACCENTS } from './icons';

interface Props {
  title: string;
  expanded: boolean;
  badge?: number | null;
  category?: string;
}

const props = withDefaults(defineProps<Props>(), {
  badge: null,
  category: 'unknown',
});

defineEmits<{
  toggle: [];
}>();

const categoryIcon = computed(() => ICONS[props.category] ?? ICONS.unknown);

const thumbStyle = computed(() => {
  const color = ACCENTS[props.category] ?? ACCENTS.unknown;
  // 用 rgba 派生极浅背景，与强调色协调
  const hex = color.replace('#', '');
  const r = parseInt(hex.slice(0, 2), 16);
  const g = parseInt(hex.slice(2, 4), 16);
  const b = parseInt(hex.slice(4, 6), 16);
  return {
    color,
    background: `rgba(${r}, ${g}, ${b}, 0.10)`,
  };
});
</script>

<style scoped>
.config-cat-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  overflow: hidden;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.config-cat-card.collapsed {
  /* 收起态更扁平，贴近 Finder 略缩图卡片 */
  box-shadow: none;
}

.config-cat-card:hover {
  border-color: rgba(0, 122, 255, 0.25);
}

/* Header：略缩图条带 */
.ccc-header {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  background: transparent;
  border: none;
  cursor: pointer;
  text-align: left;
  font: inherit;
  transition: background 0.15s ease;
}

.ccc-header:hover {
  background: var(--control);
}

.ccc-header:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
  border-radius: 12px;
}

/* 略缩图：左边的代表图标方块（mac Finder 风格） */
.ccc-thumb {
  flex: 0 0 36px;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: transform 0.18s ease;
}

.ccc-header:hover .ccc-thumb {
  transform: translateY(-1px);
}

.ccc-thumb-icon {
  display: inline-flex;
  width: 22px;
  height: 22px;
}

.ccc-thumb-icon :deep(svg) {
  width: 22px;
  height: 22px;
  display: block;
}

/* 标题 + 计数 */
.ccc-meta {
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.ccc-title {
  font-size: 13.5px;
  font-weight: 560;
  color: var(--label);
  letter-spacing: -0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ccc-badge {
  flex: 0 0 auto;
  min-width: 20px;
  padding: 1px 7px;
  font-size: 11px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  text-align: center;
  line-height: 1.5;
}

/* 展开箭头 */
.ccc-chevron {
  flex: 0 0 14px;
  width: 14px;
  height: 14px;
  color: var(--tertiary);
  transform: rotate(-90deg);
  transition: transform 0.18s ease, color 0.15s ease;
}

.ccc-chevron.expanded {
  transform: rotate(0deg);
  color: var(--accent);
}

.ccc-header:hover .ccc-chevron {
  color: var(--secondary);
}

.ccc-header:hover .ccc-chevron.expanded {
  color: var(--accentHover);
}

/* 展开内容区 */
.ccc-content {
  padding: 4px 14px 14px;
  border-top: 1px solid var(--separator);
  margin-top: 0;
}

/* 收起态略微减小内边距，让略缩图更紧凑 */
.config-cat-card.collapsed .ccc-header {
  padding: 8px 14px;
}
</style>
