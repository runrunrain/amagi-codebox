<!--
  ConfigCategoryCard - 可折叠配置类别卡片（mac Finder 略缩图风格）

  - 默认收起（由父组件 expanded prop 控制）
  - 点击 header 切换展开/收起
  - 收起态：左侧大号语义图标 + 标题 + 配置键 code + 计数徽章 + 右侧 meta 标签 / ⓘ / 展开箭头
  - 展开态：在 header 下渲染 slot 内容
  - 苹果HIG 克制：图标作为视觉锚点，留白克制，无过度装饰

  provider-center-layout-redesign（§4.0.6，向后兼容）新增可选 props：
  - code：配置键，等宽小字，紧随标题（P5：键名代码化，不再写成「标题（key）」）
  - meta / metaTone：header 右侧的小标签（如「下次压缩生效」「受管」），折叠时也可见
  - tip / tipItems：卡片级说明，收进可聚焦的 InfoTip（P6）。InfoTip 是 header 按钮的兄弟
    节点（按钮内嵌交互元素不合法），绝对定位到 header 右侧；Tab 顺序先 header 后 tip，
    在 ⓘ 上按 Enter 不会切换折叠。
  不传新 props 时渲染结果与改造前一致。
-->
<template>
  <div :class="['config-cat-card', { collapsed: !expanded, 'has-tip': hasTip }]">
    <div class="ccc-head">
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
          <code v-if="code" class="ccc-code">{{ code }}</code>
          <span v-if="badge !== null && badge !== undefined" class="ccc-badge">
            {{ badge }}
          </span>
        </div>

        <span v-if="meta" :class="['ccc-tag', `tone-${metaTone}`]">{{ meta }}</span>

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
      <InfoTip v-if="hasTip" class="ccc-tip" :label="title" :text="tip" :items="tipItems" />
    </div>

    <div v-if="expanded" class="ccc-content">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { ICONS, ACCENTS } from './icons';
import InfoTip, { type InfoTipItem } from '../ui/InfoTip.vue';

interface Props {
  title: string;
  expanded: boolean;
  badge?: number | null;
  category?: string;
  /** 配置键（等宽小字） */
  code?: string;
  /** header 右侧小标签 */
  meta?: string;
  metaTone?: 'neutral' | 'warning';
  /** 卡片级说明（InfoTip 段落） */
  tip?: string | string[];
  /** 卡片级说明的术语对照 */
  tipItems?: InfoTipItem[];
}

const props = withDefaults(defineProps<Props>(), {
  badge: null,
  category: 'unknown',
  metaTone: 'neutral',
});

defineEmits<{
  toggle: [];
}>();

const categoryIcon = computed(() => ICONS[props.category] ?? ICONS.unknown);

const hasTip = computed(() => {
  const t = props.tip;
  const hasText = Array.isArray(t) ? t.length > 0 : !!t;
  return hasText || !!(props.tipItems && props.tipItems.length);
});

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

/* header 行：按钮 + 绝对定位的 InfoTip 兄弟节点 */
.ccc-head {
  position: relative;
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

/* 标题 + 配置键 + 计数 */
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

.ccc-code {
  flex: 0 0 auto;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--tertiary);
  white-space: nowrap;
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

/* header 右侧 meta 标签：中性 / 警示 */
.ccc-tag {
  flex: 0 0 auto;
  font-size: 11px;
  font-weight: 500;
  border-radius: 999px;
  padding: 1px 7px;
  white-space: nowrap;
}

.ccc-tag.tone-neutral {
  color: var(--secondary);
  background: var(--control);
}

.ccc-tag.tone-warning {
  color: var(--warning-strong);
  background: var(--warning-tint);
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

/* InfoTip：绝对定位在箭头左侧，给它预留 28px 位置 */
.has-tip .ccc-chevron {
  margin-left: 28px;
}

.ccc-tip {
  position: absolute;
  right: 36px;
  top: 50%;
  transform: translateY(-50%);
}

/* 展开内容区：顶部留 10px（原 4px）——删掉卡内说明段落后首个字标签会贴着 header 分隔线 */
.ccc-content {
  padding: 10px 14px 14px;
  border-top: 1px solid var(--separator);
  margin-top: 0;
}

/* 收起态略微减小内边距，让略缩图更紧凑 */
.config-cat-card.collapsed .ccc-header {
  padding: 8px 14px;
}
</style>
