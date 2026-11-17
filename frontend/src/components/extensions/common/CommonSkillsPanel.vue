<template>
  <div class="common-skills-panel">
    <!-- 首次加载 -->
    <LoadingState v-if="initialLoading" message="加载公共技能中..." />

    <!-- 首次加载失败（聚合与仓库清单全失败时） -->
    <ErrorState v-else-if="initialError" :message="initialError" :on-retry="handleRetry" />

    <template v-else>
      <!-- 顶部操作行 -->
      <div class="cc-toolbar">
        <div class="cc-toolbar-title">
          <h2>公共技能</h2>
          <span class="count-badge">{{ rows.length }}</span>
        </div>
        <div class="cc-toolbar-actions">
          <AppButton variant="primary" size="small" :disabled="importingZip" @click="handleImportZip">
            {{ importingZip ? '导入中…' : '导入压缩包' }}
          </AppButton>
          <AppButton variant="primary" size="small" :disabled="importingDir" @click="handleImportDir">
            {{ importingDir ? '导入中…' : '导入文件夹' }}
          </AppButton>
          <AppButton variant="ghost" size="small" :disabled="store.loading" @click="refresh(true)">
            刷新
          </AppButton>
        </div>
      </div>
      <p class="cc-hint">手动导入的技能可勾选分发到各 CLI；插件自带技能在插件管理中统一启停与卸载。</p>

      <!-- 聚合列表：manual（可管理） + plugin（只读 + 跳转插件） -->
      <div v-if="rows.length > 0" class="cc-list">
        <div v-for="row in rows" :key="row.key" class="cc-item">
          <div class="cc-item-main">
            <div class="cc-name-row">
              <div class="cc-name-block">
                <span class="cc-name">{{ row.name }}</span>
                <span v-if="!row.managed && !row.enabled" class="cc-off-mark" title="已在插件管理中停用">已停用</span>
              </div>
              <Switch
                v-if="row.canToggle"
                :model-value="row.enabled"
                :disabled="store.loading"
                @update:model-value="(val) => handleToggleEnabled(row.name, val)"
              />
            </div>
            <div class="cc-meta-row">
              <Badge type="type" :text="row.sourceText" :color="row.sourceColor" />
              <Badge v-if="row.engineLabel" type="source" :text="row.engineLabel" variant="muted" />
            </div>
            <p v-if="row.description" class="cc-desc">{{ truncate(row.description, 80) }}</p>

            <!-- 分发目标勾选（仅 manual） -->
            <div v-if="row.canEditTargets" class="cc-targets">
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
            <!-- plugin 条目：所属插件只读展示 -->
            <div v-else class="cc-plugin-line">
              <span class="cc-targets-label">所属插件:</span>
              <span class="cc-plugin-name">{{ row.pluginName || row.pluginId }}</span>
            </div>
          </div>

          <div class="cc-item-actions">
            <template v-if="row.managed">
              <AppButton variant="danger" size="small" :disabled="deleting" @click="requestDelete(row)">
                删除
              </AppButton>
            </template>
            <template v-else>
              <AppButton variant="ghost" size="small" @click="viewPlugin(row)">查看插件</AppButton>
              <AppButton variant="danger" size="small" disabled :title="row.deleteTitle">删除</AppButton>
            </template>
          </div>
        </div>
      </div>

      <!-- 空态 -->
      <EmptyState
        v-else
        icon="⊘"
        title="暂无公共技能"
        description="从压缩包或文件夹导入技能，或安装带技能的插件后在此统一查看"
      />
    </template>

    <!-- 删除确认 -->
    <ConfirmDialog
      v-model:open="showDeleteDialog"
      title="确认删除"
      :message="deleteTarget ? `确定要删除公共技能「${deleteTarget.name}」吗？将同时从各目标 CLI 回收。` : ''"
      danger
      confirm-text="删除"
      @confirm="confirmDelete"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';
import AppButton from '../../ui/AppButton.vue';
import Badge from '../../ui/Badge.vue';
import Switch from '../../ui/Switch.vue';
import EmptyState from '../../ui/EmptyState.vue';
import LoadingState from '../../ui/LoadingState.vue';
import ErrorState from '../../ui/ErrorState.vue';
import ConfirmDialog from '../../ui/ConfirmDialog.vue';
import { truncate } from '../../../utils/format';
import { ENGINE_LABELS } from './commonCenterModel';
import { useCommonSkillsPanel } from './useCommonSkillsPanel';

const {
  rows,
  store,
  initialLoading,
  initialError,
  engines,
  importingZip,
  importingDir,
  deleting,
  initialLoad,
  handleRetry,
  refresh,
  handleImportZip,
  handleImportDir,
  handleToggleEnabled,
  handleToggleTarget,
  requestDelete,
  confirmDelete,
  viewPlugin,
  deleteTarget,
  showDeleteDialog,
} = useCommonSkillsPanel();

onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.common-skills-panel {
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

.cc-name-block {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.cc-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cc-off-mark {
  font-size: 11px;
  color: var(--secondary);
}

.cc-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.cc-desc {
  font-size: 12px;
  color: var(--secondary);
  margin: 0;
}

.cc-targets,
.cc-plugin-line {
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

.cc-plugin-name {
  font-size: 12px;
  color: var(--label);
}

.cc-item-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}
</style>
