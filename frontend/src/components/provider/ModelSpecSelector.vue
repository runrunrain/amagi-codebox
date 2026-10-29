<!--
  ModelSpecSelector - `provider/model[:level]` 三级下拉选择器。
  由 useModelCatalog 目录驱动：provider → model（级联过滤）→ thinking level
  （取模型 thinkingLevelMap 键，无元数据时用默认级别集）。
  未知 provider / 无法拆分的 spec 回退为手动输入；目录未收录的模型保持当前值可见。
-->
<template>
  <div class="mss-root">
    <template v-if="manual || !catalog.providers.length || (spec && (!parsed || !providerEntry))">
      <TextInput
        :model-value="modelValue"
        placeholder="provider/model[:thinking]"
        mono
        class="mss-input"
        @update:model-value="$emit('update:model-value', $event)"
      />
      <AppButton v-if="catalog.providers.length && parsed && providerEntry" variant="ghost" size="small" class="mss-mode-btn" @click="manual = false">
        从列表选择
      </AppButton>
    </template>
    <template v-else>
      <div class="mss-row">
        <Dropdown
          :model-value="provider"
          :options="providerOptions"
          placeholder="选择服务商"
          class="mss-dd"
          @update:model-value="onProviderChange"
        />
        <Dropdown
          :model-value="model"
          :options="modelOptions"
          :disabled="!modelOptions.some(o => !o.disabled)"
          placeholder="选择模型"
          class="mss-dd"
          @update:model-value="onModelChange"
        />
        <Dropdown
          :model-value="level"
          :options="levelOptions"
          :disabled="!model"
          class="mss-dd mss-dd-level"
          @update:model-value="onLevelChange"
        />
      </div>
      <p v-if="providerEntry && !providerEntry.models.length && !model" class="mss-empty" role="status">
        暂无模型目录，可切换服务商或手动输入模型。
      </p>
      <AppButton variant="ghost" size="small" class="mss-mode-btn" @click="manual = true">手动输入模型</AppButton>
    </template>
    <code v-if="spec" class="mss-spec">{{ spec }}</code>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import Dropdown, { type DropdownOption } from '../ui/Dropdown.vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import {
  thinkingLevelsFor,
  compatibleThinkingLevel,
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
const manual = ref(false);

const spec = computed(() => props.modelValue || '');
// provider/ 是选择器的可恢复中间态，不等于可保存的完整模型 spec。
const parsed = computed(() => parseModelSpec(spec.value, { allowEmptyModel: true }));

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
    // 标注凭据/来源状态：✓ 已认证；内置目录提供商（OAuth 登录如 openai-codex）
    // 凭据由 CLI 自身管理；未认证的注册表提供商选了也无法调用，帮助用户避坑
    if (p.hasAuth) label += ' ✓';
    else if (p.source === 'builtin') label += '（内置）';
    else label += '（未认证）';
    return { value: p.name, label };
  })
);

const modelOptions = computed<DropdownOption[]>(() => {
  if (!providerEntry.value) return [];
  const options = providerEntry.value.models.map((m) => ({
    value: m.id,
    label: m.name && m.name !== m.id ? `${m.name}（${m.id}）` : m.id,
  }));
  if (model.value && !options.some(o => o.value === model.value)) {
    options.unshift({ value: model.value, label: `${model.value}（当前值，目录未收录）` });
  }
  return options.length ? options : [{ value: '', label: '暂无模型目录', disabled: true }];
});

const levelOptions = computed<DropdownOption[]>(() => {
  const m = providerEntry.value?.models.find((x) => x.id === model.value);
  const levels = thinkingLevelsFor(m);
  const options = [{ value: '', label: '默认（不指定档位）' }, ...levels.map(l => ({ value: l, label: l }))];
  if (level.value && !levels.includes(level.value)) {
    options.push({ value: level.value, label: `${level.value}（当前值，不在支持档位中）` });
  }
  return options;
});

function onProviderChange(p: string) {
  // 切换 provider 后原 model 不再有效，取新 provider 的第一个模型
  const entry = props.catalog.providers.find((x) => x.name === p);
  const first = entry?.models[0]?.id || '';
  if (!first) {
    emit('update:model-value', p + '/');
    return;
  }
  emit('update:model-value', buildModelSpec(p, first, compatibleThinkingLevel(entry?.models[0], level.value)));
}

function onModelChange(m: string) {
  if (!provider.value) return;
  const target = providerEntry.value?.models.find(x => x.id === m);
  emit('update:model-value', buildModelSpec(provider.value, m, compatibleThinkingLevel(target, level.value)));
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

.mss-mode-btn {
  align-self: flex-start;
}

.mss-empty {
  margin: 0;
  color: var(--secondary);
  font-size: 12px;
}

.mss-spec {
  color: var(--secondary);
  font-size: 12px;
  overflow-wrap: anywhere;
}
</style>
