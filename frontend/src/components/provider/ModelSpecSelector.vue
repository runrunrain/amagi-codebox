<!--
  ModelSpecSelector - `provider/model[:level]` 三级下拉选择器。
  由 useModelCatalog 目录驱动：provider → model（级联过滤）→ thinking level
  （取模型 thinkingLevelMap 键，无元数据时用默认级别集）。
  目录中不存在的 spec（如手写或目录未同步）回退为手动输入模式。
-->
<template>
  <div class="mss-root">
    <template v-if="spec && !parsed">
      <!-- 无法拆解的 spec：手动输入回退 -->
      <TextInput
        :model-value="modelValue"
        placeholder="provider/model:level"
        mono
        class="mss-input"
        @update:model-value="$emit('update:model-value', $event)"
      />
    </template>
    <template v-else-if="spec && !providerEntry">
      <!-- provider 不在目录中：手动输入回退（保留原值可见） -->
      <TextInput
        :model-value="modelValue"
        placeholder="provider/model:level（当前 provider 不在模型目录中）"
        mono
        class="mss-input"
        @update:model-value="$emit('update:model-value', $event)"
      />
    </template>
    <template v-else>
      <div class="mss-row">
        <Dropdown
          :model-value="provider"
          :options="providerOptions"
          class="mss-dd"
          @update:model-value="onProviderChange"
        />
        <Dropdown
          :model-value="model"
          :options="modelOptions"
          class="mss-dd"
          @update:model-value="onModelChange"
        />
        <Dropdown
          :model-value="level"
          :options="levelOptions"
          class="mss-dd mss-dd-level"
          @update:model-value="onLevelChange"
        />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import Dropdown, { type DropdownOption } from '../ui/Dropdown.vue';
import TextInput from '../ui/TextInput.vue';
import {
  DEFAULT_THINKING_LEVELS,
  parseModelSpec,
  buildModelSpec,
  type ModelCatalog,
} from './useModelCatalog';

const props = defineProps<{
  /** 当前 spec（provider/model[:level]），空字符串表示未设置 */
  modelValue: string;
  catalog: ModelCatalog;
}>();

const emit = defineEmits<{ 'update:model-value': [value: string] }>();

const spec = computed(() => props.modelValue || '');
const parsed = computed(() => parseModelSpec(spec.value));

const providerEntry = computed(() => {
  const p = parsed.value?.provider;
  if (!p) return undefined;
  return props.catalog.providers.find((x) => x.name === p);
});

const provider = computed(() => parsed.value?.provider || '');
const model = computed(() => parsed.value?.model || '');
const level = computed(() => parsed.value?.level || '');

const providerOptions = computed<DropdownOption[]>(() =>
  props.catalog.providers.map((p) => {
    let label = p.api ? `${p.name}（${p.api}）` : p.name;
    // 标注凭据状态：未认证的提供商选了也无法调用，帮助用户避坑
    label += p.hasAuth ? ' ✓' : '（未认证）';
    return { value: p.name, label };
  })
);

const modelOptions = computed<DropdownOption[]>(() => {
  if (!providerEntry.value) return [];
  return providerEntry.value.models.map((m) => ({
    value: m.id,
    label: m.name && m.name !== m.id ? `${m.name}（${m.id}）` : m.id,
  }));
});

const levelOptions = computed<DropdownOption[]>(() => {
  const m = providerEntry.value?.models.find((x) => x.id === model.value);
  const levels = m?.thinkingLevels?.length ? m.thinkingLevels : DEFAULT_THINKING_LEVELS;
  return levels.map((l) => ({ value: l, label: l }));
});

function onProviderChange(p: string) {
  // 切换 provider 后原 model 不再有效，取新 provider 的第一个模型
  const entry = props.catalog.providers.find((x) => x.name === p);
  const first = entry?.models[0]?.id || '';
  if (!first) {
    emit('update:model-value', p + '/');
    return;
  }
  emit('update:model-value', buildModelSpec(p, first, level.value));
}

function onModelChange(m: string) {
  if (!provider.value) return;
  // 换模型后保留 level（若新模型元数据不含该 level，仍由后端/CLI 校验兜底）
  emit('update:model-value', buildModelSpec(provider.value, m, level.value));
}

function onLevelChange(l: string) {
  if (!provider.value || !model.value) return;
  emit('update:model-value', buildModelSpec(provider.value, model.value, l));
}
</script>

<style scoped>
.mss-root {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.mss-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.mss-dd {
  min-width: 140px;
  max-width: 220px;
}

.mss-dd-level {
  min-width: 100px;
  max-width: 140px;
}

.mss-input {
  width: 100%;
}
</style>
