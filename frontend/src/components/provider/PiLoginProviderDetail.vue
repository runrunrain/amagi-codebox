<template>
  <ConfigCard>
    <AppButton variant="ghost" @click="$emit('back')">返回列表</AppButton>
    <h2>{{ provider.provider }} · 登录认证</h2>
    <p>来源：{{ provider.source }}。仅用于 Pi，凭据由 Pi 原生管理，CodeBox 不读取或复制 token。</p>
    <p>预设保存模型、思考等级、可选上下文窗口与输出上限（额度经 ~/.pi/agent/models.json 的 modelOverrides 生效，对 Pi 会话与 amagi 子 agent 透明），不参与 API 服务商同步或媒体模型导出。</p>
    <AppButton variant="ghost" @click="refresh">刷新登录状态</AppButton>
    <ErrorState v-if="error" :message="error" :on-retry="refresh" />
    <div v-for="[key, preset] in entries" :key="key" class="login-preset">
      <span>{{ preset.name }} · {{ preset.model }} · {{ preset.thinking || '默认思考等级' }} · {{ preset.context_window ? `${preset.context_window} tokens` : '内置窗口' }} · {{ preset.max_tokens ? `输出 ${preset.max_tokens} tokens` : '目录默认输出' }}</span>
      <AppButton variant="ghost" size="small" @click="edit(key, preset)">编辑</AppButton>
      <AppButton variant="ghost" size="small" @click="remove(key)">删除</AppButton>
    </div>
    <p v-if="entries.length === 0">暂无登录预设。保存后可在「会话设置 → Pi」中选择并启动。</p>
    <form class="login-form" @submit.prevent="save">
      <label>预设名称<TextInput v-model="name" :readonly="!!editingKey" placeholder="例如：日常编码" /></label>
      <label>模型 ID
        <input v-model="model" list="pi-login-models" class="model-input" placeholder="输入 Pi 原生模型 ID" required />
        <datalist id="pi-login-models"><option v-for="m in provider.models" :key="m.id" :value="m.id">{{ m.name || m.id }}</option></datalist>
      </label>
      <p v-if="provider.models.length === 0">尚无本地模型目录；仍可手动填写 Pi 支持的模型 ID。</p>
      <label>思考等级
        <select v-model="thinking"><option value="">Pi 默认</option><option v-for="level in levels" :key="level" :value="level">{{ level }}</option></select>
      </label>
      <label>上下文窗口（tokens，选填）
        <input v-model="contextWindow" type="number" min="0" step="1000" class="model-input" placeholder="留空或 0 = Pi 内置目录窗口" />
      </label>
      <label>输出上限（tokens，选填）
        <input v-model="maxTokens" type="number" min="0" step="1" class="model-input" placeholder="留空或 0 = Pi 目录默认输出上限" />
      </label>
      <div>
        <AppButton variant="ghost" type="button" :disabled="busy || !outputMaximum" @click="maxTokens = outputMaximum">填入模型最大值</AppButton>
        <p v-if="outputMaximum">模型目录最大输出：{{ outputMaximum.toLocaleString() }} tokens。此按钮仅填入，保存后生效。</p>
        <p v-else>所选模型暂无已知最大输出额度；可刷新目录或按供应商文档手动填写。</p>
      </div>
      <p>窗口与输出上限按供应商和模型共享，保存时会同步同模型的其他预设。目录值不保证供应商实际接受；输出仍受上下文余量与推理预算限制。已有会话需重新加载模型或重启后生效。输出上限仅同步本机 Pi 配置，不分发至 WSL。</p>
      <div><AppButton variant="primary" :disabled="busy || !name.trim() || !model.trim()" type="submit">保存预设</AppButton>
        <AppButton v-if="editingKey" variant="ghost" type="button" @click="reset">取消编辑</AppButton></div>
    </form>
  </ConfigCard>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import ConfigCard from '../ui/ConfigCard.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import ErrorState from '../ui/ErrorState.vue';
import { useProviderStore } from '../../stores/provider';
import { getPiLoginPresets, savePiLoginPreset, deletePiLoginPreset, type PiLoginProvider, type PiLoginPreset } from '../../api/piLogin';
import { normalizeContextWindowInput } from './piLoginContextWindow';
import { modelOutputMaximum, normalizeOutputLimitInput } from './piLoginOutputLimit';
import { config } from '../../../wailsjs/go/models';
import { useToast } from '../../composables/useToast';

const props = defineProps<{ provider: PiLoginProvider }>();
defineEmits<{ back: [] }>();
const store = useProviderStore();
const { showSuccess } = useToast();
const presets = ref<Record<string, PiLoginPreset>>({});
const entries = computed(() => Object.entries(presets.value).filter(([, p]) => p.provider === props.provider.provider));
const name = ref(''); const model = ref(''); const thinking = ref(''); const contextWindow = ref<string | number>(''); const editingKey = ref('');
const maxTokens = ref<string | number>('');
const outputMaximum = computed(() => modelOutputMaximum(props.provider.models, model.value));
const busy = ref(false); const error = ref('');
const levels = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];

function reset() { editingKey.value = ''; name.value = ''; model.value = ''; thinking.value = ''; contextWindow.value = ''; maxTokens.value = ''; }
function edit(key: string, preset: PiLoginPreset) { editingKey.value = key; name.value = preset.name; model.value = preset.model; thinking.value = preset.thinking || ''; contextWindow.value = preset.context_window ? String(preset.context_window) : ''; maxTokens.value = preset.max_tokens ? String(preset.max_tokens) : ''; }
async function refresh() {
  error.value = '';
  try { await store.loadLoginProviders(); presets.value = await getPiLoginPresets() || {}; }
  catch (err) { error.value = String(err); }
}
async function save() {
  busy.value = true; error.value = '';
  try {
    // number input 下 v-model 可能是 number（Vue castToNumber）——归一化函数统一处理。
    const contextWindowValue = normalizeContextWindowInput(contextWindow.value);
    await savePiLoginPreset(new config.PiLoginPreset({ name: name.value, provider: props.provider.provider, model: model.value, thinking: thinking.value, context_window: contextWindowValue, max_tokens: normalizeOutputLimitInput(maxTokens.value) }));
    await refresh(); reset(); showSuccess('Pi 登录预设已保存');
  } catch (err) { error.value = String(err); } finally { busy.value = false; }
}
async function remove(key: string) {
  error.value = '';
  try { await deletePiLoginPreset(key); if (editingKey.value === key) reset(); await refresh(); }
  catch (err) { error.value = String(err); }
}
onMounted(refresh);
watch(() => props.provider.id, () => { reset(); void refresh(); });
</script>

<style scoped>
.login-preset { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 10px 0; border-bottom: 1px solid var(--separator); }
.login-form { display: flex; flex-direction: column; gap: 14px; max-width: 520px; }
.login-form label { display: flex; flex-direction: column; gap: 8px; }
.model-input, select { font: inherit; color: var(--label); background: var(--control); border: 1px solid var(--separator); border-radius: 8px; padding: 8px; }
p { color: var(--secondary); font-size: 13px; }
</style>
