<template>
  <Dialog
    :open="open"
    title="导入 MCP JSON"
    description="粘贴 JSON 文本或从文件选择，解析预览后确认导入"
    @update:open="(v) => !v && closeDialog()"
  >
    <div class="mcp-import-form">
      <!-- 文本输入 -->
      <div class="form-group">
        <div class="form-group-head">
          <label>JSON 文本</label>
          <AppButton variant="ghost" size="small" :disabled="pickingFile" @click="handlePickFile">
            {{ pickingFile ? '读取中…' : '从文件导入' }}
          </AppButton>
        </div>
        <textarea
          v-model="text"
          class="form-textarea mono"
          rows="8"
          placeholder='{ "context7": { "command": "npx", "args": ["-y", "@context7/mcp"] } }'
          spellcheck="false"
        />
      </div>

      <!-- 就地预览错误（解析失败，不污染全局） -->
      <p v-if="previewError" class="preview-error">{{ previewError }}</p>

      <!-- 解析预览区 -->
      <div v-if="preview.length > 0" class="preview-box">
        <div class="preview-head">解析到 {{ preview.length }} 个 server：</div>
        <div v-for="row in preview" :key="row.name" class="preview-row">
          <span class="preview-name">{{ row.name }}</span>
          <span class="preview-summary mono">{{ row.summary || '（无 command/url）' }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <AppButton variant="ghost" :disabled="importing" @click="closeDialog">取消</AppButton>
      <AppButton variant="ghost" :disabled="!canParse" @click="handleParse">
        {{ parsing ? '解析中…' : '解析预览' }}
      </AppButton>
      <AppButton variant="primary" :disabled="!canImport" @click="handleImport">
        {{ importing ? '导入中…' : '导入' }}
      </AppButton>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import Dialog from '../../ui/Dialog.vue';
import AppButton from '../../ui/AppButton.vue';
import { useMCPImportDialog } from './useMCPImportDialog';

// 状态机全部在 composable（可测内核）；组件仅模板绑定
const {
  open,
  text,
  preview,
  previewError,
  parsing,
  importing,
  pickingFile,
  canParse,
  canImport,
  openDialog,
  closeDialog,
  handlePickFile,
  handleParse,
  handleImport,
} = useMCPImportDialog();

// 父组件通过 ref 打开（CommonMCPPanel 的「导入 JSON」按钮）
defineExpose({ openDialog, closeDialog });
</script>

<style scoped>
.mcp-import-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 420px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.form-group label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-textarea {
  padding: 8px 12px;
  font-size: 12px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  resize: vertical;
  font-family: var(--mono);
  transition: border-color 0.15s;
}

.form-textarea:focus {
  outline: none;
  border-color: var(--accent);
}

.mono {
  font-family: var(--mono);
}

.preview-error {
  font-size: 12px;
  color: #d92d20;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
}

.preview-box {
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--control);
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 180px;
  overflow-y: auto;
}

.preview-head {
  font-size: 12px;
  font-weight: 500;
  color: var(--label);
}

.preview-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 12px;
}

.preview-name {
  color: var(--label);
  font-weight: 500;
  flex-shrink: 0;
}

.preview-summary {
  color: var(--secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
