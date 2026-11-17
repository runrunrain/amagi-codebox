<template>
  <div class="common-mcp-panel">
    <!-- 首次加载 -->
    <LoadingState v-if="initialLoading" message="加载公共 MCP 中..." />

    <!-- 首次加载失败 -->
    <ErrorState v-else-if="initialError" :message="initialError" :on-retry="handleRetry" />

    <template v-else>
      <!-- 顶部操作行 -->
      <div class="cc-toolbar">
        <div class="cc-toolbar-title">
          <h2>公共 MCP</h2>
          <span class="count-badge">{{ rows.length }}</span>
        </div>
        <div class="cc-toolbar-actions">
          <AppButton variant="primary" size="small" @click="importDialog?.openDialog()">导入 JSON</AppButton>
          <AppButton variant="ghost" size="small" :disabled="store.loading" @click="refresh">刷新</AppButton>
        </div>
      </div>
      <p class="cc-hint">导入的 MCP server 可勾选分发到各 CLI 的 MCP 配置写入点；启用即分发、停用即回收。</p>

      <!-- server 列表 -->
      <div v-if="rows.length > 0" class="cc-list">
        <div v-for="row in rows" :key="row.key" class="cc-item">
          <div class="cc-item-main">
            <div class="cc-name-row">
              <span class="cc-name">{{ row.name }}</span>
              <Switch
                :model-value="row.enabled"
                :disabled="store.loading"
                @update:model-value="(val) => handleToggleEnabled(row.name, val)"
              />
            </div>
            <p v-if="row.summary" class="cc-desc mono">{{ truncate(row.summary, 90) }}</p>

            <!-- 分发目标勾选 -->
            <div class="cc-targets">
              <span class="cc-targets-label">分发目标:</span>
              <button
                v-for="engine in engines"
                :key="engine"
                type="button"
                :class="['target-chip', { active: row.targets.includes(engine) }]"
                :disabled="store.loading"
                @click="handleToggleTarget(row.name, engine)"
              >
                {{ ENGINE_LABELS[engine] }}
              </button>
            </div>
          </div>

          <div class="cc-item-actions">
            <AppButton variant="danger" size="small" :disabled="deleting" @click="requestDelete(row)">
              删除
            </AppButton>
          </div>
        </div>
      </div>

      <!-- 空态 -->
      <EmptyState
        v-else
        icon="⊘"
        title="暂无公共 MCP"
        description="粘贴 JSON 文本或选择 .json 文件导入 MCP server"
      />
    </template>

    <!-- 导入对话框（粘贴 / 选文件 → 解析预览 → 导入） -->
    <MCPImportDialog ref="importDialog" />

    <!-- 删除确认 -->
    <ConfirmDialog
      v-model:open="showDeleteDialog"
      title="确认删除"
      :message="deleteTarget ? `确定要删除 MCP server「${deleteTarget.name}」吗？将同时从各目标 CLI 回收。` : ''"
      danger
      confirm-text="删除"
      @confirm="confirmDelete"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import AppButton from '../../ui/AppButton.vue';
import Switch from '../../ui/Switch.vue';
import EmptyState from '../../ui/EmptyState.vue';
import LoadingState from '../../ui/LoadingState.vue';
import ErrorState from '../../ui/ErrorState.vue';
import ConfirmDialog from '../../ui/ConfirmDialog.vue';
import { truncate } from '../../../utils/format';
import { ENGINE_LABELS } from './commonCenterModel';
import { useCommonMCPPanel } from './useCommonMCPPanel';
import { useMCPImportDialog } from './useMCPImportDialog';
import MCPImportDialog from './MCPImportDialog.vue';

const {
  rows,
  store,
  initialLoading,
  initialError,
  engines,
  deleting,
  initialLoad,
  handleRetry,
  refresh,
  handleToggleEnabled,
  handleToggleTarget,
  requestDelete,
  confirmDelete,
  deleteTarget,
  showDeleteDialog,
} = useCommonMCPPanel();

// 导入对话框自带完整状态机（预览→导入→skipped 提示）；面板仅负责打开
const importDialog = ref<InstanceType<typeof MCPImportDialog> | null>(null);

onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.common-mcp-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.cc-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.cc-toolbar-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.cc-toolbar-title h2 {
  font-size: 15px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.count-badge {
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  border-radius: 10px;
  background: var(--control);
  color: var(--secondary);
  font-size: 11px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.cc-toolbar-actions {
  display: flex;
  gap: 8px;
}

.cc-hint {
  font-size: 12px;
  color: var(--secondary);
  margin: -6px 0 0;
}

.cc-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.cc-item {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 11px 13px;
  display: flex;
  gap: 12px;
  align-items: flex-start;
}

.cc-item-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.cc-name-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.cc-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cc-desc {
  font-size: 12px;
  color: var(--secondary);
  margin: 0;
}

.cc-desc.mono {
  font-family: var(--mono);
}

.cc-targets {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.cc-targets-label {
  font-size: 11px;
  color: var(--secondary);
}

.target-chip {
  font-size: 11px;
  padding: 3px 10px;
  border-radius: 999px;
  border: 1px solid var(--separator);
  background: var(--card);
  color: var(--secondary);
  cursor: pointer;
  transition: all 0.12s;
}

.target-chip.active {
  background: rgba(0, 122, 255, 0.1);
  border-color: var(--accent);
  color: var(--accent);
}

.target-chip:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.cc-item-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}
</style>
