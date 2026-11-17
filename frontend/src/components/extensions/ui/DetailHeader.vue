<!-- 契约 §4 #9：详情头部。面包屑（clickable 段 accent 可点，尾段不可点）+
     大图标 + 名称 + badges + actions 插槽（主操作按钮组）。
     可测试性契约：expose goBack()（emit back，与面包屑点击同一 emit 路径）。 -->
<template>
  <div class="detail-header">
    <nav class="dh-breadcrumb" aria-label="面包屑">
      <template v-for="(segment, index) in breadcrumb" :key="index">
        <span v-if="index > 0" class="dh-sep" aria-hidden="true">›</span>
        <button
          v-if="isClickable(index)"
          type="button"
          class="dh-crumb dh-crumb-link"
          @click="goBack"
        >
          {{ segment.label }}
        </button>
        <span v-else class="dh-crumb dh-crumb-tail">{{ segment.label }}</span>
      </template>
    </nav>
    <div class="dh-main">
      <PluginIcon :name="title" size="lg" />
      <div class="dh-titles">
        <h2 class="dh-title">{{ title }}</h2>
        <div v-if="badges.length" class="dh-badges">
          <Badge
            v-for="(badge, index) in badges"
            :key="index"
            :type="badge.type"
            :text="badge.text"
            :color="badge.color"
            :variant="badge.variant"
          />
        </div>
      </div>
      <div class="dh-actions">
        <slot name="actions" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import PluginIcon from './PluginIcon.vue';
import Badge from '../../ui/Badge.vue';
import type { BreadcrumbSegment, PluginBadgeDescriptor } from '../pluginViewTypes';

interface Props {
  breadcrumb: BreadcrumbSegment[];
  title: string;
  badges?: PluginBadgeDescriptor[];
}

const props = withDefaults(defineProps<Props>(), {
  badges: () => [],
});

const emit = defineEmits<{
  back: [];
}>();

// 尾段恒不可点（表示当前页）；其余段按 clickable 标记
function isClickable(index: number): boolean {
  return !!props.breadcrumb[index]?.clickable && index < props.breadcrumb.length - 1;
}

function goBack() {
  emit('back');
}

defineExpose({ goBack });
</script>

<style scoped>
.detail-header {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.dh-breadcrumb {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  min-width: 0;
  flex-wrap: wrap;
}

.dh-sep {
  color: var(--tertiary);
}

.dh-crumb {
  font-size: 12px;
}

.dh-crumb-tail {
  color: var(--secondary);
}

.dh-crumb-link {
  border: none;
  background: none;
  padding: 0;
  cursor: pointer;
  color: var(--accent);
  font-family: inherit;
  font-size: 12px;
}

.dh-crumb-link:hover {
  text-decoration: underline;
}

.dh-main {
  display: flex;
  align-items: center;
  gap: 14px;
}

.dh-titles {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.dh-title {
  font-size: 20px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.dh-badges {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

.dh-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
