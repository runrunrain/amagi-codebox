<!--
  AddMarketDialog - 添加市场弹窗（§8.2 项19）
  支持 Claude/Codex 引擎，输入市场源 URL
-->
<template>
  <Dialog
    :open="open"
    title="添加市场"
    :description="engine === 'claude' ? '输入 Claude 插件市场源 URL' : '输入 Codex 插件市场源 URL'"
    @update:open="handleClose"
  >
    <div class="add-market-form">
      <div class="form-group">
        <label>市场源 URL</label>
        <input
          v-model="marketUrl"
          type="text"
          class="form-input"
          placeholder="https://example.com/plugins.json"
          @keydown.enter="handleSubmit"
        />
        <p class="input-hint">
          {{ engine === 'claude' ? '示例：https://raw.githubusercontent.com/xxx/claude-plugins/main/plugins.json' : '示例：https://raw.githubusercontent.com/xxx/codex-plugins/main/plugins.json' }}
        </p>
      </div>
    </div>
    <template #footer>
      <AppButton variant="ghost" @click="handleClose">
        取消
      </AppButton>
      <AppButton
        variant="primary"
        :disabled="!marketUrl.trim() || submitting"
        @click="handleSubmit"
      >
        {{ submitting ? '添加中...' : '添加' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';
import { AddMarketplace } from '../../../wailsjs/go/plugin/Service';
import { AddMarketplace as AddCodexMarketplace } from '../../../wailsjs/go/codexplugin/Service';

interface Props {
  open?: boolean;
  engine?: 'claude' | 'codex';
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  engine: 'claude',
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'success'): void;
}>();

const marketUrl = ref('');
const submitting = ref(false);

async function handleSubmit() {
  const url = marketUrl.value.trim();
  if (!url) return;

  submitting.value = true;
  try {
    let result;
    if (props.engine === 'claude') {
      result = await AddMarketplace(url);
    } else {
      result = await AddCodexMarketplace({ source: url });
    }

    if (result && !result.success) {
      console.error('[AddMarketDialog] Failed:', result.error);
    } else {
      marketUrl.value = '';
      emit('success');
      handleClose();
    }
  } catch (error) {
    console.error('[AddMarketDialog] Error:', error);
  } finally {
    submitting.value = false;
  }
}

function handleClose() {
  emit('update:open', false);
}
</script>

<style scoped>
.add-market-form {
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

.form-input {
  padding: 8px 12px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
  font-family: inherit;
}

.form-input:focus {
  outline: none;
  border-color: var(--accent);
}

.input-hint {
  font-size: 11px;
  color: var(--tertiary);
  margin: 0;
  line-height: 1.5;
}
</style>
