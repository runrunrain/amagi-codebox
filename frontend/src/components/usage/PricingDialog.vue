<template>
  <Dialog
    :open="open"
    :title="dialogTitle"
    :description="dialogDescription"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <form class="pricing-form" @submit.prevent="handleSave">
      <!-- 基本信息区 / Basic info -->
      <div class="form-section">
        <div class="form-row">
          <label class="form-field">
            <span class="form-label">模型 ID <span class="required">*</span></span>
            <input
              v-model="form.modelPattern"
              type="text"
              class="form-input"
              :class="{ invalid: !!errors.modelPattern }"
              placeholder="例如 claude-sonnet-4-20250514"
              :disabled="saving"
            />
            <span class="form-hint">标准化后用于价格匹配（小写、去 vendor 前缀）</span>
            <span v-if="errors.modelPattern" class="form-error">{{ errors.modelPattern }}</span>
          </label>
          <label class="form-field">
            <span class="form-label">显示名称</span>
            <input
              v-model="form.displayName"
              type="text"
              class="form-input"
              placeholder="例如 Claude Sonnet 4"
              :disabled="saving"
            />
          </label>
        </div>

        <div class="form-row">
          <label class="form-field">
            <span class="form-label">供应商</span>
            <input
              v-model="form.provider"
              type="text"
              class="form-input"
              placeholder="例如 anthropic / openai / glm"
              :disabled="saving"
            />
          </label>
          <label class="form-field">
            <span class="form-label">币种</span>
            <select v-model="form.currencyCode" class="form-select" :disabled="saving">
              <option value="USD">USD ($)</option>
              <option value="CNY">CNY (¥)</option>
            </select>
          </label>
        </div>
      </div>

      <!-- 四维单价区 / Four-dimension unit prices -->
      <div class="form-section">
        <div class="section-title">
          四维单价（每百万 token 的原生币种）
          <span class="currency-hint">当前币种：{{ form.currencyCode }}</span>
        </div>
        <div class="price-grid">
          <label class="form-field">
            <span class="form-label">Input / 输入</span>
            <input
              v-model.number="form.inputPerMillion"
              type="number"
              min="0"
              step="0.01"
              class="form-input mono"
              :disabled="saving"
            />
          </label>
          <label class="form-field">
            <span class="form-label">Output / 输出</span>
            <input
              v-model.number="form.outputPerMillion"
              type="number"
              min="0"
              step="0.01"
              class="form-input mono"
              :disabled="saving"
            />
          </label>
          <label class="form-field">
            <span class="form-label">Cache Read / 缓存读</span>
            <input
              v-model.number="form.cacheReadPerMillion"
              type="number"
              min="0"
              step="0.01"
              class="form-input mono"
              :disabled="saving"
            />
          </label>
          <label class="form-field">
            <span class="form-label">Cache Creation / 缓存写</span>
            <input
              v-model.number="form.cacheCreationPerMillion"
              type="number"
              min="0"
              step="0.01"
              class="form-input mono"
              :disabled="saving"
            />
          </label>
        </div>
        <span v-if="errors.prices" class="form-error">{{ errors.prices }}</span>
      </div>

      <!-- 备注 / Notes -->
      <div class="form-section">
        <label class="form-field">
          <span class="form-label">备注（可选）</span>
          <input
            v-model="form.notes"
            type="text"
            class="form-input"
            placeholder="例如 Anthropic 2025 定价"
            :disabled="saving"
          />
        </label>
      </div>

      <div v-if="editing?.isBuiltin" class="builtin-hint">
        内置模型：可调整单价，但无法删除；恢复默认请使用价格表的「恢复内置」。
      </div>

      <div v-if="saveError" class="form-error block-error">{{ saveError }}</div>
    </form>

    <template #footer>
      <AppButton variant="ghost" size="small" :disabled="saving" @click="handleCancel">
        取消
      </AppButton>
      <AppButton variant="primary" size="small" :disabled="saving || !canSave" @click="handleSave">
        {{ saving ? '保存中...' : '保存' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';
import { useUsageStore } from '../../stores/usage';
import { useToast } from '../../composables/useToast';
import { usage } from '../../../wailsjs/go/models';
import type { ModelPricing } from '../../api/usage';

interface Props {
  open: boolean;
  /** 编辑现有条目；为 null 表示新增 / Edit existing entry; null for new */
  editing?: ModelPricing | null;
  /** 从未知模型入口预填 modelPattern / Pre-fill modelPattern from unknown-model entry */
  presetPattern?: string;
  /** 预填供应商（可选）/ Pre-fill provider (optional) */
  presetProvider?: string;
}

const props = withDefaults(defineProps<Props>(), {
  editing: null,
  presetPattern: '',
  presetProvider: '',
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'saved', entry: ModelPricing): void;
}>();

const store = useUsageStore();
const { showSuccess, showError } = useToast();

// micro 与原生币种浮点的换算因子 / Divisor between micro-int and float currency.
const MICRO_PER_UNIT = 1_000_000;

interface PricingForm {
  id: string;
  modelPattern: string;
  displayName: string;
  provider: string;
  currencyCode: 'USD' | 'CNY';
  inputPerMillion: number;     // 原生币种浮点 / Float in native currency
  outputPerMillion: number;
  cacheReadPerMillion: number;
  cacheCreationPerMillion: number;
  notes: string;
  isBuiltin: boolean;
}

const emptyForm = (): PricingForm => ({
  id: '',
  modelPattern: '',
  displayName: '',
  provider: '',
  currencyCode: 'USD',
  inputPerMillion: 0,
  outputPerMillion: 0,
  cacheReadPerMillion: 0,
  cacheCreationPerMillion: 0,
  notes: '',
  isBuiltin: false,
});

const form = ref<PricingForm>(emptyForm());
const errors = ref<{ modelPattern?: string; prices?: string }>({});
const saving = ref(false);
const saveError = ref('');

const dialogTitle = computed(() =>
  props.editing ? `编辑价格：${props.editing.displayName || props.editing.modelPattern}` : '新增模型价格',
);
const dialogDescription = computed(() =>
  props.editing
    ? '修改四维单价与币种；保存后立即生效。'
    : '为价格表添加新条目，匹配规则基于模型 ID（精确 + 前缀）。',
);

const canSave = computed(() => {
  return form.value.modelPattern.trim().length > 0 && !errors.value.modelPattern;
});

// 打开对话框时初始化表单 / Initialize form when dialog opens.
watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return;
    saveError.value = '';
    errors.value = {};
    if (props.editing) {
      // 编辑模式：从 ModelPricing 还原表单（micro → float）
      const e = props.editing;
      form.value = {
        id: e.id,
        modelPattern: e.modelPattern,
        displayName: e.displayName,
        provider: e.provider,
        currencyCode: (e.currencyCode as 'USD' | 'CNY') || 'USD',
        inputPerMillion: microToFloat(e.inputPerMillion),
        outputPerMillion: microToFloat(e.outputPerMillion),
        cacheReadPerMillion: microToFloat(e.cacheReadPerMillion),
        cacheCreationPerMillion: microToFloat(e.cacheCreationPerMillion),
        notes: e.notes || '',
        isBuiltin: !!e.isBuiltin,
      };
    } else {
      // 新增模式：从 preset 还原或空表单
      form.value = {
        ...emptyForm(),
        modelPattern: props.presetPattern || '',
        provider: props.presetProvider || '',
        // 默认显示名沿用 modelPattern 的人类可读变体
        displayName: humanizePattern(props.presetPattern),
      };
    }
  },
);

function microToFloat(micro: number): number {
  const n = Number(micro) || 0;
  return Math.round((n / MICRO_PER_UNIT) * 1e6) / 1e6;
}

function floatToMicro(f: number): number {
  if (!Number.isFinite(f) || f <= 0) return 0;
  return Math.round(f * MICRO_PER_UNIT);
}

function humanizePattern(p: string): string {
  if (!p) return '';
  // 把 "claude-sonnet-4" 还原成 "Claude Sonnet 4"
  return p
    .split('-')
    .map((s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s))
    .join(' ');
}

function validate(): boolean {
  const e: { modelPattern?: string; prices?: string } = {};
  const pattern = form.value.modelPattern.trim();
  if (!pattern) {
    e.modelPattern = '模型 ID 必填';
  } else if (!/^[a-z0-9._:\-]+$/i.test(pattern)) {
    e.modelPattern = '仅允许字母、数字、点、连字符、冒号、下划线';
  }
  const prices = [
    form.value.inputPerMillion,
    form.value.outputPerMillion,
    form.value.cacheReadPerMillion,
    form.value.cacheCreationPerMillion,
  ];
  if (prices.some((p) => Number.isFinite(p) && p < 0)) {
    e.prices = '单价不能为负';
  }
  errors.value = e;
  return Object.keys(e).length === 0;
}

async function handleSave() {
  if (!validate()) return;
  if (saving.value) return; // 重复提交防护 / Duplicate-submit guard.
  saving.value = true;
  saveError.value = '';
  try {
    const payload = new usage.ModelPricing({
      id: form.value.id || generateId(form.value.modelPattern),
      modelPattern: form.value.modelPattern.trim(),
      displayName: form.value.displayName.trim() || form.value.modelPattern.trim(),
      provider: form.value.provider.trim(),
      currencyCode: form.value.currencyCode,
      inputPerMillion: floatToMicro(form.value.inputPerMillion),
      outputPerMillion: floatToMicro(form.value.outputPerMillion),
      cacheReadPerMillion: floatToMicro(form.value.cacheReadPerMillion),
      cacheCreationPerMillion: floatToMicro(form.value.cacheCreationPerMillion),
      isBuiltin: form.value.isBuiltin,
      notes: form.value.notes.trim(),
      updatedAt: new Date().toISOString(),
    });
    await store.savePricing(payload);
    showSuccess(`价格已保存：${payload.displayName}`);
    emit('saved', payload);
    emit('update:open', false);
  } catch (err) {
    saveError.value = errToString(err);
    showError('保存失败：' + saveError.value);
  } finally {
    saving.value = false;
  }
}

function handleCancel() {
  if (saving.value) return;
  emit('update:open', false);
}

function generateId(pattern: string): string {
  // 优先用加密 UUID，回退到 pattern+时间戳 / Prefer crypto UUID, fallback to pattern+ts.
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `${pattern || 'pricing'}-${Date.now()}`;
}

function errToString(err: unknown): string {
  if (!err) return '未知错误';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  if (Array.isArray(err)) return err.map(String).join('; ');
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
}
</script>

<style scoped>
.pricing-form {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.form-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.form-label {
  font-size: 12px;
  color: var(--secondary);
  font-weight: 500;
}

.required {
  color: var(--danger);
  margin-left: 2px;
}

.form-input,
.form-select {
  appearance: none;
  -webkit-appearance: none;
  background: var(--control);
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 7px 10px;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  outline: none;
  transition: box-shadow 0.12s, background-color 0.12s;
  width: 100%;
}

.form-input.mono {
  font-family: var(--mono);
}

.form-input:focus,
.form-select:focus {
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.25);
  background: var(--card);
}

.form-input.invalid {
  border-color: var(--danger);
}

.form-input:disabled,
.form-select:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.form-hint {
  font-size: 11px;
  color: var(--tertiary);
}

.form-error {
  font-size: 12px;
  color: var(--danger);
}

.block-error {
  padding: 8px 10px;
  background: rgba(255, 59, 48, 0.08);
  border-radius: 7px;
}

.section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.currency-hint {
  font-size: 11px;
  color: var(--tertiary);
  font-weight: 400;
}

.price-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.builtin-hint {
  font-size: 12px;
  color: var(--warning-strong);
  background: rgba(255, 149, 0, 0.1);
  padding: 8px 10px;
  border-radius: 7px;
  line-height: 1.5;
}

@media (max-width: 540px) {
  .form-row,
  .price-grid {
    grid-template-columns: 1fr;
  }
}
</style>
