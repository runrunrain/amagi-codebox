<!--
  ProviderListEditor - Provider 配置列表编辑器
  支持数组形式的 provider 配置编辑
-->
<template>
  <div class="provider-list-editor">
    <div v-if="providers.length === 0" class="ple-empty">
      <EmptyState icon="—" title="未配置 Provider" description="当前无 provider 配置项" />
    </div>
    <div v-else class="ple-items">
      <div
        v-for="(provider, index) in providers"
        :key="index"
        class="ple-item"
      >
        <div class="ple-header">
          <span class="ple-index">#{{ index + 1 }}</span>
          <button class="ple-remove" @click="removeProvider(index)" title="删除">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <div class="ple-fields">
          <TextInput
            :model-value="provider.name || ''"
            placeholder="Provider 名称"
            @update:model-value="updateProvider(index, 'name', $event)"
          />
          <TextInput
            :model-value="provider.base_url || provider.baseUrl || ''"
            placeholder="Base URL"
            @update:model-value="updateProvider(index, 'base_url', $event)"
          />
          <TextInput
            :model-value="provider.api_key || provider.apiKey || ''"
            placeholder="API Key"
            type="password"
            @update:model-value="updateProvider(index, 'api_key', $event)"
          />
        </div>
      </div>
      <AppButton variant="ghost" size="small" @click="addProvider">
        + 添加 Provider
      </AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';

interface Provider {
  name?: string;
  base_url?: string;
  baseUrl?: string;
  api_key?: string;
  apiKey?: string;
}

interface Props {
  providers: Provider[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  update: [providers: Provider[]];
}>();

const localProviders = computed<Provider[]>(() => [...props.providers]);

function updateProvider(index: number, key: string, value: string) {
  const updated = [...localProviders.value];
  if (!updated[index]) updated[index] = {};
  updated[index] = { ...updated[index], [key]: value };
  emit('update', updated);
}

function addProvider() {
  emit('update', [...localProviders.value, {}]);
}

function removeProvider(index: number) {
  const updated = localProviders.value.filter((_, i) => i !== index);
  emit('update', updated);
}
</script>

<style scoped>
.provider-list-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ple-empty {
  padding: 10px 0;
}

.ple-items {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ple-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
}

.ple-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.ple-index {
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
}

.ple-remove {
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

.ple-remove:hover {
  background: rgba(255, 59, 48, 0.1);
}

.ple-remove svg {
  width: 14px;
  height: 14px;
}

.ple-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>
