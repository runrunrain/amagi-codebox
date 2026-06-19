<!--
  KeyValueEditor - 键值对编辑器
  用于 Instructions/Plugin/Experimental 等对象型配置编辑
-->
<template>
  <div class="key-value-editor">
    <div v-if="isEmpty" class="kve-empty">
      <EmptyState icon="—" title="未配置" :description="emptyDescription" />
    </div>
    <div v-else class="kve-items">
      <div
        v-for="(value, key) in data"
        :key="String(key)"
        class="kve-item"
      >
        <div class="kve-header">
          <span class="kve-key">{{ String(key) }}</span>
          <button class="kve-remove" @click="removeItem(String(key))" title="删除">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <div class="kve-value">
          <TextInput
            :model-value="formatValue(value)"
            placeholder="值"
            @update:model-value="updateItem(String(key), $event)"
          />
        </div>
      </div>
      <AppButton variant="ghost" size="small" @click="addItem">
        + 添加项
      </AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';

interface Props {
  data: Record<string, any>;
  emptyDescription?: string;
}

const props = withDefaults(defineProps<Props>(), {
  emptyDescription: '当前无配置项',
});

const emit = defineEmits<{
  update: [data: Record<string, any>];
}>();

const isEmpty = computed(() => Object.keys(props.data).length === 0);

function formatValue(value: any): string {
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

function updateItem(key: string, value: string) {
  const updated = { ...props.data };
  updated[key] = value;
  emit('update', updated);
}

function addItem() {
  const keys = Object.keys(props.data);
  const newKey = `key_${keys.length + 1}`;
  const updated = { ...props.data };
  updated[newKey] = '';
  emit('update', updated);
}

function removeItem(key: string) {
  const updated = { ...props.data };
  delete updated[key];
  emit('update', updated);
}
</script>

<style scoped>
.key-value-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.kve-empty {
  padding: 10px 0;
}

.kve-items {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.kve-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
}

.kve-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.kve-key {
  font-size: 12px;
  font-weight: 500;
  color: var(--tertiary);
  font-family: var(--mono);
}

.kve-remove {
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  color: var(--danger);
  cursor: pointer;
  border-radius: 4px;
  transition: background 0.15s;
}

.kve-remove:hover {
  background: rgba(255, 59, 48, 0.1);
}

.kve-remove svg {
  width: 14px;
  height: 14px;
}

.kve-value {
  display: flex;
  flex-direction: column;
}
</style>
