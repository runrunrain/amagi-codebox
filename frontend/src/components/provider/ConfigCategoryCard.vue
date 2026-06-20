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

/**
 * 内联 SVG 图标库（禁 emoji）
 * 每个图标为 20x20 viewBox 的描边图标，stroke=currentColor 便于继承色相
 * 图标语义：
 * - model: 芯片（顶层模型字段）
 * - provider: 云（服务提供方）
 * - agent: 机器人头（智能体）
 * - mcp: 插块拼装（MCP servers）
 * - permission: 盾牌+锁（权限）
 * - instructions: 文档列表（说明文件）
 * - plugin: 积木拼块（插件）
 * - experimental: 烧瓶（实验性）
 * - unknown: 三点堆叠（兜底）
 */
const ICONS: Record<string, string> = {
  model: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <rect x="7" y="7" width="10" height="10" rx="1.5"/>
      <rect x="10" y="10" width="4" height="4" rx="0.5" fill="currentColor" stroke="none"/>
      <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.5 5.5l1.4 1.4M17.1 17.1l1.4 1.4M5.5 18.5l1.4-1.4M17.1 6.9l1.4-1.4"/>
    </svg>`,
  provider: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M6 15a4 4 0 0 1 .8-7.9 5.5 5.5 0 0 1 10.6 1.4A3.75 3.75 0 0 1 17.5 15"/>
      <path d="M9 13.5l3-3 3 3"/>
      <path d="M12 10.5V18"/>
    </svg>`,
  agent: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <rect x="5" y="8" width="14" height="11" rx="3"/>
      <path d="M12 4v4"/>
      <circle cx="12" cy="3.5" r="1" fill="currentColor" stroke="none"/>
      <circle cx="9.5" cy="13" r="1.1" fill="currentColor" stroke="none"/>
      <circle cx="14.5" cy="13" r="1.1" fill="currentColor" stroke="none"/>
      <path d="M9.8 16h4.4"/>
    </svg>`,
  mcp: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 4v3.5L5 9.5V14"/>
      <path d="M15 4v3.5l4 2V14"/>
      <path d="M9 20v-3.5l-4-2"/>
      <path d="M15 20v-3.5l4-2"/>
      <rect x="9" y="3" width="6" height="2.4" rx="0.6"/>
      <rect x="3.6" y="8.4" width="2.8" height="2.4" rx="0.6" transform="rotate(-90 5 9.6)"/>
      <rect x="17.6" y="8.4" width="2.8" height="2.4" rx="0.6" transform="rotate(-90 19 9.6)"/>
    </svg>`,
  permission: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M12 3l7 3v5c0 4.2-2.8 7.8-7 9-4.2-1.2-7-4.8-7-9V6l7-3z"/>
      <rect x="9.5" y="10.5" width="5" height="3.6" rx="0.6"/>
      <path d="M10.4 10.5V9.2a1.6 1.6 0 0 1 3.2 0v1.3"/>
    </svg>`,
  instructions: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M6 3h9l3 3v15a0 0 0 0 1 0 0H6a0 0 0 0 1 0 0V3z"/>
      <path d="M14.5 3v3.5H18"/>
      <path d="M8.5 12h7M8.5 15h7M8.5 18h4"/>
    </svg>`,
  plugin: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 4.5h6v4a3 3 0 0 1-6 0v-4z"/>
      <path d="M12 4.5V2.5"/>
      <path d="M9 11v9a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1v-9"/>
      <path d="M7 14H9M15 14h2"/>
    </svg>`,
  experimental: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 3h6"/>
      <path d="M10 3v5.5L5.5 17a2 2 0 0 0 1.8 3h9.4a2 2 0 0 0 1.8-3L14 8.5V3"/>
      <path d="M7.5 14h9"/>
      <circle cx="10" cy="17" r="0.8" fill="currentColor" stroke="none"/>
    </svg>`,
  unknown: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="6" cy="12" r="1.4"/>
      <circle cx="12" cy="12" r="1.4"/>
      <circle cx="18" cy="12" r="1.4"/>
    </svg>`,
};

/**
 * 各类的语义强调色（HIG 风格低饱和点缀色）
 * 用于略缩图背景的弱色调，前景仍是白色卡片
 */
const ACCENTS: Record<string, string> = {
  model: '#5856D6',        // indigo
  provider: '#007AFF',     // systemBlue
  agent: '#34C759',        // systemGreen
  mcp: '#FF9500',          // systemOrange
  permission: '#FF3B30',   // systemRed
  instructions: '#AF52DE', // systemPurple
  plugin: '#00C7BE',       // systemTeal
  experimental: '#FF2D55', // systemPink
  unknown: '#8E8E93',      // systemGray
};

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
