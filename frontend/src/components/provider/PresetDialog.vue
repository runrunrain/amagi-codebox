<!--
  PresetDialog - 添加/编辑终端预设弹窗（对照旧 ProviderCenter + §8.2 项15）。
  props.preset: 已存在预设时为编辑模式，空则为新增模式。
  表单：预设名称、Provider(Dropdown)、模型、参数字段（temperature/top_p/max_tokens/stream/thinking等）。
  保存 → SaveTerminalPreset → 刷新 PresetList。
-->
<template>
  <Dialog :open="open" :title="title" @update:open="handleClose">
    <div class="preset-form">
      <!-- 预设名称（仅新增时显示） -->
      <div v-if="!isEditing" class="form-group">
        <label class="form-label">预设名称</label>
        <input
          v-model="form.name"
          type="text"
          class="form-input"
          placeholder="例如: default, coding"
        />
      </div>

      <!-- 关联 Provider -->
      <div class="form-group">
        <label class="form-label">关联 Provider</label>
        <select v-model="form.provider" class="form-input">
          <option value="">（选择 Provider）</option>
          <option v-for="name in providerNames" :key="name" :value="name">{{ name }}</option>
        </select>
        <p v-if="providerNames.length === 0" class="form-warning">
          当前无可用的 Provider
        </p>
      </div>

      <!-- 模型（留空使用 Provider 默认值） -->
      <div class="form-group">
        <label class="form-label">模型（留空使用 Provider 默认值）</label>
        <input
          v-model="form.model"
          type="text"
          class="form-input mono"
          placeholder="例如: claude-sonnet-4-6"
        />
      </div>

      <!-- Claude Code 模型档位（可选，留空表示不覆盖该档） -->
      <div v-if="engine === 'claude'" class="form-section">
        <div class="form-section-title">Claude Code 模型档位（可选）</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Haiku</label>
            <input
              v-model="form.modelHaiku"
              type="text"
              class="form-input mono"
              placeholder="例如: glm-5-turbo"
            />
          </div>
          <div class="form-group">
            <label class="form-label">Sonnet</label>
            <input
              v-model="form.modelSonnet"
              type="text"
              class="form-input mono"
              placeholder="例如: glm-5.2"
            />
          </div>
          <div class="form-group">
            <label class="form-label">Opus</label>
            <input
              v-model="form.modelOpus"
              type="text"
              class="form-input mono"
              placeholder="例如: glm-5.2"
            />
          </div>
        </div>
      </div>

      <!-- 参数设置 -->
      <div class="form-section">
        <div class="form-section-title">参数设置（留空使用默认值）</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Temperature</label>
            <input
              v-model.number="form.temperature"
              type="number"
              class="form-input"
              step="0.1"
              min="0"
              max="1"
              placeholder="默认"
            />
          </div>
          <div class="form-group">
            <label class="form-label">Top P</label>
            <input
              v-model.number="form.topP"
              type="number"
              class="form-input"
              step="0.1"
              min="0"
              max="1"
              placeholder="默认"
            />
          </div>
          <div class="form-group">
            <label class="form-label">Max Tokens</label>
            <input
              v-model.number="form.maxTokens"
              type="number"
              class="form-input"
              step="1"
              min="1"
              placeholder="默认"
            />
          </div>
          <div class="form-group">
            <label class="form-label">Stream</label>
            <select v-model="form.streamValue" class="form-input">
              <option value="">默认</option>
              <option value="true">启用</option>
              <option value="false">禁用</option>
            </select>
          </div>
        </div>
      </div>

      <!-- Thinking 模式 -->
      <div v-if="engine === 'claude'" class="form-section">
        <div class="form-section-title">Thinking 模式</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Thinking 模式</label>
            <select v-model="form.thinkingType" class="form-input">
              <option value="">默认</option>
              <option value="disabled">禁用</option>
              <option value="enabled">启用</option>
            </select>
          </div>
          <div v-if="form.thinkingType === 'enabled'" class="form-group">
            <label class="form-label">Budget Tokens</label>
            <input
              v-model.number="form.thinkingBudget"
              type="number"
              class="form-input"
              step="1"
              min="1024"
              placeholder="16384"
            />
          </div>
        </div>
      </div>

      <!-- 推理强度（Codex） -->
      <div v-if="engine === 'codex'" class="form-section">
        <div class="form-section-title">推理强度</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">推理强度</label>
            <select v-model="form.reasoningEffort" class="form-input">
              <option value="">默认</option>
              <option value="low">low — 最快</option>
              <option value="medium">medium — 平衡</option>
              <option value="high">high — 深度</option>
              <option value="xhigh">xhigh — 更深</option>
              <option value="max">max — 最深</option>
            </select>
          </div>
        </div>
      </div>

      <!-- Context Window（通用） -->
      <div class="form-section">
        <div class="form-section-title">Context Window</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Context Window</label>
            <input
              v-model.number="form.contextWindow"
              type="number"
              class="form-input"
              step="1"
              min="1"
              placeholder="默认"
            />
          </div>
          <div class="form-group">
            <label class="form-label">Auto Compact Threshold</label>
            <input
              v-model.number="form.compactLimit"
              type="number"
              class="form-input"
              step="1"
              min="1"
              placeholder="默认"
            />
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="dialog-actions">
        <AppButton variant="ghost" @click="handleClose" :disabled="loading">取消</AppButton>
        <AppButton
          variant="primary"
          @click="handleSave"
          :disabled="!canSave || loading"
        >{{ loading ? '保存中...' : '保存' }}</AppButton>
      </div>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { SaveTerminalPreset } from '../../../wailsjs/go/main/App';
import { useProviderStore } from '../../stores/provider';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';

interface Props {
  open?: boolean;
  engine: 'claude' | 'codex';
  preset?: config.MergedTerminalPreset | null;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  preset: null,
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'saved'): void;
}>();

const store = useProviderStore();
const loading = ref(false);

// terminalType 映射
const terminalType = computed(() => props.engine === 'claude' ? 'claude_code' : 'codex');

// 可用的 Provider 名称
const providerNames = computed(() => Object.keys(store.providers));

// 是否编辑模式
const isEditing = computed(() => !!props.preset);

// 标题
const title = computed(() => (isEditing.value ? '编辑' : '添加') + ' ' + (props.engine === 'claude' ? 'Claude Code' : 'Codex') + ' 预设');

const form = reactive({
  name: '',
  label: '',
  provider: '',
  model: '',
  modelHaiku: '',
  modelSonnet: '',
  modelOpus: '',
  temperature: undefined as number | undefined,
  topP: undefined as number | undefined,
  maxTokens: undefined as number | undefined,
  streamValue: '',
  thinkingType: '',
  thinkingBudget: undefined as number | undefined,
  reasoningEffort: '',
  contextWindow: undefined as number | undefined,
  compactLimit: undefined as number | undefined,
});

const canSave = computed(() => {
  return (form.name || form.label) && form.provider;
});

// 初始化表单（编辑时完整读取 MergedTerminalPreset 扩展字段）
// MergedTerminalPreset 已含 model_haiku/sonnet/opus + parameters（temperature/top_p/max_tokens/
// thinking/context_window/reasoning_effort/stream），编辑时必须完整回填，避免保存后擦除用户配置。
function initForm() {
  if (props.preset) {
    const p = props.preset;
    const params = p.parameters;
    const ctx = params?.context_window;
    form.name = p.key || '';
    form.label = p.label || '';
    form.provider = p.provider || '';
    form.model = p.model || '';
    // 模型档位（Claude Code）
    form.modelHaiku = p.model_haiku || '';
    form.modelSonnet = p.model_sonnet || '';
    form.modelOpus = p.model_opus || '';
    // 基础参数
    form.temperature = params?.temperature;
    form.topP = params?.top_p;
    form.maxTokens = params?.max_tokens;
    form.streamValue = params?.stream === undefined ? '' : (params.stream ? 'true' : 'false');
    // Thinking（Claude Code）
    form.thinkingType = params?.thinking?.type || '';
    form.thinkingBudget = params?.thinking?.budgetTokens;
    // 推理强度（Codex）
    form.reasoningEffort = params?.reasoning_effort || '';
    // Context Window / Auto Compact
    form.contextWindow = ctx?.model_context_window;
    form.compactLimit = ctx?.model_auto_compact_token_limit;
  } else {
    resetForm();
  }
}

function resetForm() {
  form.name = '';
  form.label = '';
  form.provider = '';
  form.model = '';
  form.modelHaiku = '';
  form.modelSonnet = '';
  form.modelOpus = '';
  form.temperature = undefined;
  form.topP = undefined;
  form.maxTokens = undefined;
  form.streamValue = '';
  form.thinkingType = '';
  form.thinkingBudget = undefined;
  form.reasoningEffort = '';
  form.contextWindow = undefined;
  form.compactLimit = undefined;
}

// 监听 preset 变化
watch(() => props.preset, initForm, { immediate: true });

function handleClose() {
  resetForm();
  emit('update:open', false);
}

async function handleSave() {
  if (!canSave.value) return;

  loading.value = true;
  try {
    const presetName = form.name || form.label;
    const terminalPreset: any = new config.TerminalPreset();

    // 基本信息
    terminalPreset.name = presetName;

    // Provider 和模型
    terminalPreset.provider = form.provider;
    if (form.model) terminalPreset.model = form.model;

    // Claude Code 模型档位（使用下划线命名，与旧代码一致）
    if (props.engine === 'claude') {
      if (form.modelHaiku) terminalPreset.model_haiku = form.modelHaiku;
      if (form.modelSonnet) terminalPreset.model_sonnet = form.modelSonnet;
      if (form.modelOpus) terminalPreset.model_opus = form.modelOpus;
    }

    // 参数设置
    const parameters: any = {};
    if (form.temperature !== undefined) parameters.temperature = form.temperature;
    if (form.topP !== undefined) parameters.top_p = form.topP;
    if (form.maxTokens !== undefined) parameters.max_tokens = form.maxTokens;
    if (form.streamValue !== '') {
      parameters.stream = form.streamValue === 'true';
    }
    if (props.engine === 'claude' && form.thinkingType) {
      parameters.thinking = {
        type: form.thinkingType,
        budgetTokens: form.thinkingBudget,
      };
    }
    if (props.engine === 'codex' && form.reasoningEffort) {
      parameters.reasoning_effort = form.reasoningEffort;
    }
    if (form.contextWindow !== undefined || form.compactLimit !== undefined) {
      parameters.context_window = {};
      if (form.contextWindow !== undefined) {
        parameters.context_window.model_context_window = form.contextWindow;
      }
      if (form.compactLimit !== undefined) {
        parameters.context_window.model_auto_compact_token_limit = form.compactLimit;
      }
    }
    if (Object.keys(parameters).length > 0) {
      terminalPreset.parameters = parameters;
    }

    // 保存
    const key = presetName;
    await SaveTerminalPreset(terminalType.value, key, terminalPreset);

    emit('saved');
    handleClose();
  } catch (err) {
    console.error('[PresetDialog] 保存失败:', err);
  } finally {
    loading.value = false;
  }
}
</script>

<style scoped>
.preset-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 10px;
  min-width: 0;
}

.form-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px;
  background: var(--sidebar);
  border-radius: 10px;
}

.form-section-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--tertiary);
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  height: 34px;
  padding: 0 10px;
  font-size: 13px;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
  transition: border-color 0.15s ease;
  font-family: inherit;
}

.form-input:focus {
  border-color: var(--accent);
}

.form-input.mono {
  font-family: var(--mono);
}

.form-warning {
  margin: 0;
  font-size: 12px;
  color: #FF3B30;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
