<!-- 契约 §4 #1：资源计数标签行（胶囊标签 + 计数徽章，行尾搜索框）。
     选中态胶囊=白底浮起（同 ui/Segmented pill 视觉），点击后不残留 WebView
     默认焦点描边（:focus 重置，键盘 :focus-visible 保留软强调环）；hidden 标签不渲染。
     可测试性契约：expose select(key) / search(v)，内部走与模板一致的 emit 路径。 -->
<template>
  <div class="count-tabs">
    <div class="ct-tabs" role="tablist">
      <button
        v-for="tab in visibleTabs"
        :key="tab.key"
        type="button"
        role="tab"
        :class="['ct-tab', { active: tab.key === modelValue }]"
        :aria-selected="tab.key === modelValue"
        @click="select(tab.key)"
      >
        <span class="ct-label">{{ tab.label }}</span>
        <span class="ct-count">{{ tab.count }}</span>
      </button>
    </div>
    <div v-if="searchPlaceholder" class="ct-search">
      <TextInput
        :model-value="searchValue"
        :placeholder="searchPlaceholder"
        @update:model-value="search"
      >
        <template #prefix><span class="ct-search-icon">⌕</span></template>
      </TextInput>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../../ui/TextInput.vue';
import type { CountTabItem } from '../pluginViewTypes';

interface Props {
  tabs: CountTabItem[];
  modelValue: string;
  searchPlaceholder?: string;
  searchValue?: string;
}

const props = withDefaults(defineProps<Props>(), {
  searchPlaceholder: '',
  searchValue: '',
});

const emit = defineEmits<{
  'update:modelValue': [key: string];
  'update:searchValue': [value: string];
}>();

const visibleTabs = computed(() => props.tabs.filter((tab) => !tab.hidden));

function select(key: string) {
  emit('update:modelValue', key);
}

function search(value: string) {
  emit('update:searchValue', value);
}

defineExpose({ select, search });
</script>

<style scoped>
.count-tabs {
  display: flex;
  align-items: center;
  gap: 12px;
}

.ct-tabs {
  display: flex;
  gap: 2px;
  background: var(--control);
  border-radius: 9px;
  padding: 3px;
}

.ct-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  border: none;
  background: transparent;
  border-radius: 7px;
  cursor: pointer;
  font-size: 13px;
  color: var(--secondary);
  font-family: inherit;
  transition: all 0.12s;
}

.ct-tab:hover {
  color: var(--label);
}

/* 焦点环修正：点击选中后不残留 WebView 默认深色描边圆角框；
   键盘可达性保留 :focus-visible 软强调环 */
.ct-tab:focus {
  outline: none;
}

.ct-tab:focus-visible {
  outline: 2px solid rgba(0, 122, 255, 0.35);
  outline-offset: 1px;
}

.ct-tab.active {
  background: var(--card);
  color: var(--label);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}

.ct-count {
  font-size: 10px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 1px 6px;
  line-height: 1.4;
}

.ct-search {
  margin-left: auto;
  width: 230px;
}

.ct-search :deep(.text-input) {
  width: 100%;
}

.ct-search-icon {
  color: var(--tertiary);
  font-size: 13px;
}
</style>
