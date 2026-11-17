<template>
  <div class="codemode-fields">
    <p>CodeMode 可组合当前已有工具，无需单独授权。模型自行决定是否使用、如何使用，调用仍遵循任务原有权限。</p>
    <FieldLabel text="调用偏好" code="codemode.preference" />
    <Segmented :model-value="typeof codemode?.preference === 'string' ? codemode.preference : 'auto'" :options="[{value:'auto',label:'模型自行选择'},{value:'prefer',label:'适用时优先考虑'}]" @update:model-value="patch({preference:$event})" />
    <div class="budget-grid"><label v-for="f in CODEMODE_BUDGETS" :key="f.key">{{f.label}}<TextInput type="number" :model-value="codemodeBudget[f.key] === undefined ? '' : String(codemodeBudget[f.key])" :placeholder="String(f.default)" @update:model-value="budget(f.key,$event)" /></label></div>
    <p>留空使用默认值。模型可在脚本中声明其他输出或时限选项；这些默认值不是授权上限。</p>
    <StatusBanner v-if="error" type="error" role="alert" :message="error" compact />
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue';
import Segmented from '../ui/Segmented.vue';
import FieldLabel from '../ui/FieldLabel.vue';
import TextInput from '../ui/TextInput.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import { CODEMODE_BUDGETS, validateCodemodeConfig } from './piCodemode';
import { asJsonRecord, isJsonRecord, type JsonRecord } from './jsonTypes';

const props = defineProps<{ value?: unknown }>();
const emit = defineEmits<{ change: [value: JsonRecord] }>();
const codemode = computed<JsonRecord>(() => asJsonRecord(props.value));
const codemodeBudget = computed<JsonRecord>(() => asJsonRecord(codemode.value.budget));
const error = computed(() => validateCodemodeConfig({ codemode: props.value }));
function patch(v: JsonRecord) { emit('change', { ...(isJsonRecord(props.value) ? props.value : {}), ...v }); }
function budget(key: string, value: string) {
  const b: JsonRecord = { ...codemodeBudget.value };
  if (value === '') delete b[key];
  else b[key] = Number(value);
  patch({ budget: Object.keys(b).length ? b : undefined });
}
</script>
<style scoped>
.codemode-fields{display:flex;flex-direction:column;gap:12px}.codemode-fields p{font-size:12px;color:var(--text-secondary);line-height:1.6;margin:0}.budget-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px}.budget-grid label{display:flex;flex-direction:column;gap:6px;font-size:12px}
</style>
