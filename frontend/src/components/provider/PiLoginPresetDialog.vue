<template>
  <Dialog
    :open="open"
    :title="preset ? '编辑 Pi 登录预设' : '添加 Pi 登录预设'"
    description="使用 Pi 原生登录，无需填写 API Key。该预设仅能用于 Pi。"
    @update:open="close"
  >
    <form id="pi-login-preset-form" class="preset-form" @submit.prevent="save">
      <label class="form-group">预设名称
        <input v-model="form.name" class="form-input" placeholder="例如：日常编码" :readonly="!!preset" :disabled="saving" required />
      </label>
      <label class="form-group">登录来源
        <input v-if="preset" :value="form.provider" class="form-input" readonly />
        <select v-else v-model="form.provider" class="form-input" :disabled="saving" required @change="changeProvider">
          <option value="" disabled>选择已登录的提供商</option>
          <option v-for="source in providers" :key="source.id" :value="source.provider">{{ source.provider }}</option>
        </select>
      </label>
      <p v-if="preset" class="form-hint">预设名称与登录来源用于关联已保存的会话配置，创建后不可修改。</p>
      <label class="form-group">模型 ID
        <input :value="form.model" list="pi-login-preset-models" class="form-input mono" placeholder="输入或选择 Pi 原生模型 ID" :disabled="saving" required @input="changeModel" />
      </label>
      <datalist id="pi-login-preset-models">
        <option v-for="model in models" :key="model.id" :value="model.id">{{ model.name || model.id }}</option>
      </datalist>
      <p v-if="form.provider && !models.length" class="form-hint">暂无本地模型目录，可手动填写 Pi 支持的模型 ID。</p>
      <label class="form-group">思考等级
        <select v-model="form.thinking" class="form-input" :disabled="saving">
          <option value="">Pi 默认</option>
          <option v-for="level in levels" :key="level" :value="level">{{ level }}</option>
        </select>
      </label>
      <div class="form-row">
        <label class="form-group">上下文窗口（tokens）
          <input v-model="form.contextWindow" type="number" min="0" step="1" class="form-input" placeholder="目录默认" :disabled="saving" />
        </label>
        <label class="form-group">输出上限（tokens）
          <input v-model="form.maxTokens" type="number" min="0" step="1" class="form-input" placeholder="目录默认" :disabled="saving" />
        </label>
      </div>
      <div v-if="outputMaximum" class="output-row">
        <span class="form-hint">目录最大输出：{{ outputMaximum.toLocaleString() }} tokens</span>
        <AppButton variant="ghost" size="small" type="button" :disabled="saving" @click="form.maxTokens = outputMaximum">填入模型最大值</AppButton>
      </div>
      <p class="form-hint">同一登录来源下，同模型的预设共享上下文窗口和输出上限。留空或填 0 使用 Pi 目录默认值。已有会话需重新加载模型或重启。</p>
      <section class="vision-section" aria-labelledby="pi-login-vision-title">
        <h4 id="pi-login-vision-title">视觉能力</h4>
        <label class="vision-option">
          <input v-model="form.vision" type="checkbox" :disabled="saving" /> 识图（允许图片输入）
        </label>
        <p class="form-hint">只为确实支持图片的模型开启。同一登录来源的同一模型共享此设置；关闭后沿用 Pi 原有配置。Pi 登录不支持视频输入，也不会导出到 API 视觉清单。</p>
      </section>
      <p v-if="!preset && !canAdd" class="form-error" role="status">当前未发现可用的 Pi 登录，请刷新登录状态后重试。</p>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
    </form>

    <template #footer>
      <AppButton type="button" variant="ghost" :disabled="saving" @click="close">取消</AppButton>
      <AppButton type="submit" form="pi-login-preset-form" variant="primary" :disabled="!canSave || saving">
        {{ saving ? '保存中…' : '保存' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { savePiLoginPreset, type PiLoginPreset, type PiLoginProvider } from '../../api/piLogin';
import { useToast } from '../../composables/useToast';
import AppButton from '../ui/AppButton.vue';
import Dialog from '../ui/Dialog.vue';
import { normalizeContextWindowInput } from './piLoginContextWindow';
import { modelOutputMaximum, normalizeOutputLimitInput } from './piLoginOutputLimit';

const props = defineProps<{
  open: boolean;
  preset: PiLoginPreset | null;
  preferredProvider: string;
  providers: PiLoginProvider[];
  canAdd: boolean;
  presets: Record<string, PiLoginPreset>;
}>();
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [provider: string] }>();
const { showSuccess } = useToast();
const error = ref('');
const saving = ref(false);
const levels = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
const form = reactive({
  name: '', provider: '', model: '', thinking: '',
  vision: false,
  contextWindow: '' as string | number,
  maxTokens: '' as string | number,
});
const source = computed(() => props.providers.find(p => p.provider === form.provider));
const models = computed(() => source.value?.models ?? []);
const outputMaximum = computed(() => modelOutputMaximum(models.value, form.model));
const canSave = computed(() => (props.preset || (props.canAdd && source.value)) && !!form.name.trim() && !!form.model.trim());

watch(() => props.open, open => {
  if (!open) return;
  const preset = props.preset;
  const preferred = props.providers.find(p => p.provider === props.preferredProvider);
  Object.assign(form, {
    name: preset?.name ?? '',
    provider: preset?.provider ?? preferred?.provider ?? (props.providers.length === 1 ? props.providers[0].provider : ''),
    model: preset?.model ?? '',
    thinking: preset?.thinking ?? '',
    vision: !!preset?.vision,
    contextWindow: preset?.context_window || '',
    maxTokens: preset?.max_tokens || '',
  });
  error.value = '';
}, { immediate: true });

function changeProvider() {
  form.model = '';
  loadSharedOverrides();
}

function changeModel(event: Event) {
  const model = (event.target as HTMLInputElement).value;
  const changed = model.trim() !== form.model.trim();
  form.model = model;
  if (changed) loadSharedOverrides();
}

function loadSharedOverrides() {
  // Pi stores overrides by provider/model. Starting from existing values avoids
  // resetting the shared overrides when creating another preset for the same model.
  const shared = Object.values(props.presets).find(p => p.provider === form.provider && p.model === form.model.trim());
  form.contextWindow = shared?.context_window || '';
  form.maxTokens = shared?.max_tokens || '';
  form.vision = !!shared?.vision;
}

function close() {
  if (!saving.value) emit('update:open', false);
}

async function save() {
  if (saving.value || !canSave.value) return;
  const name = form.name.trim();
  if (!props.preset && Object.values(props.presets).some(p => p.provider === form.provider && p.name === name)) {
    error.value = '该登录来源下已有同名预设，请换一个名称，或在列表中编辑现有预设。';
    return;
  }

  saving.value = true;
  error.value = '';
  try {
    const preset = new config.PiLoginPreset({
      name, provider: form.provider, model: form.model.trim(), thinking: form.thinking,
      context_window: normalizeContextWindowInput(form.contextWindow),
      max_tokens: normalizeOutputLimitInput(form.maxTokens),
      vision: form.vision,
    });
    await savePiLoginPreset(preset);
    emit('update:open', false);
    emit('saved', preset.provider);
    showSuccess('已保存 Pi 登录预设');
  } catch (err) {
    error.value = String(err);
  } finally {
    saving.value = false;
  }
}
</script>

<style scoped>
.preset-form, .form-group { display: flex; flex-direction: column; gap: 6px; }
.preset-form { gap: 14px; }
.form-group { font-size: 13px; font-weight: 500; min-width: 0; }
.form-row { display: grid; grid-template-columns: repeat(auto-fit, minmax(135px, 1fr)); gap: 10px; }
.form-input { box-sizing: border-box; width: 100%; min-width: 0; height: 34px; padding: 0 10px; font: inherit; color: var(--label); background: var(--control); border: 1px solid var(--separator); border-radius: 8px; }
.form-input:focus-visible { outline: 1px solid var(--accent); }
.form-input[readonly] { color: var(--secondary); }
.form-input.mono { font-family: var(--mono); }
.form-hint, .form-error { margin: 0; color: var(--secondary); font-size: 12px; line-height: 1.5; }
.form-error { color: var(--danger-strong); }
.output-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.vision-section { display: grid; gap: 8px; padding: 12px; background: var(--sidebar); border-radius: 10px; }
.vision-section h4 { margin: 0; font-size: 13px; }
.vision-option { display: flex; align-items: center; gap: 6px; font-size: 13px; }
.vision-option input { accent-color: var(--accent); }
</style>
