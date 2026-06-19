<!--
  RuleDialog - 新建/编辑注入规则弹窗（§8.2 项26）
  新增/编辑共用，表单：规则名称、优先级、关键词、注入提示词、启用开关
-->
<template>
  <Dialog
    :open="open"
    :title="isEditing ? '编辑规则' : '新建规则'"
    @update:open="handleClose"
  >
    <div class="rule-form">
      <div class="form-group">
        <label>规则名称</label>
        <input
          v-model="form.name"
          type="text"
          class="form-input"
          placeholder="输入规则名称"
        />
      </div>

      <div class="form-group">
        <label>优先级（数字越大优先级越高）</label>
        <input
          v-model.number="form.priority"
          type="number"
          class="form-input"
          placeholder="0"
        />
      </div>

      <div class="form-group">
        <label>触发关键词（输入后按回车添加）</label>
        <div class="keyword-input-group">
          <div class="keyword-chips">
            <span
              v-for="(kw, idx) in form.keywords"
              :key="kw"
              class="keyword-chip"
            >
              {{ kw }}
              <span class="remove-btn" @click="removeKeyword(idx)">×</span>
            </span>
          </div>
          <input
            v-model="newKeyword"
            type="text"
            class="keyword-input"
            placeholder="输入关键词..."
            @keydown.enter.prevent="addKeyword"
          />
        </div>
      </div>

      <div class="form-group">
        <label>注入内容（Prompt）</label>
        <textarea
          v-model="form.prompt"
          class="form-textarea"
          rows="5"
          placeholder="输入要注入的提示词内容..."
        />
      </div>

      <div class="form-group checkbox-group">
        <label class="checkbox-label">
          <input type="checkbox" v-model="form.enabled" />
          启用此规则
        </label>
      </div>
    </div>
    <template #footer>
      <AppButton variant="ghost" @click="handleClose" :disabled="loading">
        取消
      </AppButton>
      <AppButton
        variant="primary"
        :disabled="!form.name.trim() || !form.prompt.trim() || loading"
        @click="handleSubmit"
      >
        {{ loading ? '保存中...' : '保存' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';
import { AddRule, UpdateRule } from '../../../wailsjs/go/proxy/ProxyService';
import { proxy } from '../../../wailsjs/go/models';

interface Props {
  open?: boolean;
  rule?: proxy.InjectionRule | null;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  rule: null,
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'success'): void;
}>();

const loading = ref(false);
const newKeyword = ref('');

const isEditing = computed(() => !!props.rule);

const form = ref<proxy.InjectionRule>(new proxy.InjectionRule({
  id: '',
  name: '',
  keywords: [],
  prompt: '',
  enabled: true,
  priority: 0,
}));

// Reset form when dialog opens/closes or rule changes
watch(() => props.open, (isOpen) => {
  if (isOpen && props.rule) {
    // Editing: populate form
    form.value = new proxy.InjectionRule({
      id: props.rule.id,
      name: props.rule.name || '',
      keywords: [...(props.rule.keywords || [])],
      prompt: props.rule.prompt || '',
      enabled: props.rule.enabled ?? true,
      priority: props.rule.priority || 0,
    });
  } else if (isOpen) {
    // New: reset to empty
    form.value = new proxy.InjectionRule({
      id: crypto.randomUUID(),
      name: '',
      keywords: [],
      prompt: '',
      enabled: true,
      priority: 0,
    });
  }
  newKeyword.value = '';
});

function addKeyword() {
  const kw = newKeyword.value.trim();
  if (kw && !form.value.keywords?.includes(kw)) {
    form.value.keywords.push(kw);
  }
  newKeyword.value = '';
}

function removeKeyword(index: number) {
  form.value.keywords?.splice(index, 1);
}

async function handleSubmit() {
  if (!form.value.name?.trim() || !form.value.prompt?.trim()) return;

  loading.value = true;
  try {
    if (isEditing.value) {
      await UpdateRule(form.value);
    } else {
      await AddRule(form.value);
    }
    emit('success');
    handleClose();
  } catch (error) {
    console.error('[RuleDialog] Save failed:', error);
  } finally {
    loading.value = false;
  }
}

function handleClose() {
  emit('update:open', false);
}
</script>

<style scoped>
.rule-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input,
.form-textarea {
  padding: 8px 12px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
  font-family: inherit;
}

.form-input:focus,
.form-textarea:focus {
  outline: none;
  border-color: var(--accent);
}

.form-textarea {
  resize: vertical;
  min-height: 100px;
}

.keyword-input-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
  border: 1px solid var(--separator);
}

.keyword-input-group:focus-within {
  border-color: var(--accent);
}

.keyword-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.keyword-chip {
  font-size: 11px;
  background: var(--window);
  color: var(--label);
  padding: 3px 8px;
  border-radius: 4px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.keyword-chip .remove-btn {
  cursor: pointer;
  color: var(--tertiary);
  font-weight: bold;
}

.keyword-chip .remove-btn:hover {
  color: var(--danger);
}

.keyword-input {
  width: 100%;
  padding: 6px 8px;
  font-size: 12px;
  border: none;
  background: transparent;
  color: var(--label);
  font-family: inherit;
}

.keyword-input:focus {
  outline: none;
}

.checkbox-group {
  flex-direction: row;
  align-items: center;
}

.checkbox-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: var(--label);
  cursor: pointer;
}

.checkbox-label input[type="checkbox"] {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}
</style>
