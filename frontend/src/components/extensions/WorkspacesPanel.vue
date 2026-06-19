<template>
  <div class="workspaces-panel">
    <!-- Toolbar -->
    <div class="ws-toolbar">
      <div class="ws-zone-label">
        工作区 <span class="zn-sep">·</span> 插件部署与冲突检测 <span class="zn-count">· {{ workspaceCount }} 个工作区</span>
      </div>
      <div class="ws-toolbar-actions">
        <AppButton variant="ghost" size="small" @click="handleRefresh" :disabled="busy">
          刷新
        </AppButton>
        <AppButton variant="ghost" size="small" @click="openGlobalDialog">
          全局启用
        </AppButton>
        <AppButton variant="primary" size="small" @click="handleCreateNew">
          新建工作区
        </AppButton>
      </div>
    </div>

    <!-- Main content: list + editor split -->
    <div class="ws-content" v-if="workspaces.length > 0">
      <!-- Left: workspace list -->
      <div class="ws-list">
        <div
          v-for="ws in workspaces"
          :key="ws.id"
          class="ws-item"
          :class="{ active: activeWorkspaceId === ws.id }"
          @click="selectWorkspace(ws.id)"
        >
          <div class="ws-name">
            {{ ws.name }}
            <Badge type="scope" :text="`${ws.plugins.length} 插件`" />
          </div>
          <div class="ws-path">{{ ws.path }}</div>
          <div class="ws-tools">
            <span
              v-for="tool in allToolTypes"
              :key="tool"
              class="tool-tag"
              :class="{ on: ws.tools.includes(tool) }"
            >
              {{ toolLabel(tool) }}
            </span>
          </div>
        </div>
      </div>

      <!-- Right: editor form -->
      <div class="ws-editor" v-if="activeWorkspace">
        <div class="ex-form">
          <h3>编辑工作区</h3>

          <!-- Workspace name -->
          <div class="form-row">
            <span class="form-label">工作区名称</span>
            <input
              v-model="editForm.name"
              type="text"
              class="form-input"
              placeholder="输入工作区名称"
            />
          </div>

          <!-- Workspace directory -->
          <div class="form-row">
            <span class="form-label">工作目录</span>
            <div class="form-input-group">
              <input
                v-model="editForm.path"
                type="text"
                class="form-input"
                placeholder="选择工作目录"
                readonly
              />
              <AppButton variant="ghost" size="small" @click="handleBrowseDirectory">
                浏览
              </AppButton>
            </div>
          </div>

          <!-- Deployment targets (tool tags) -->
          <div class="form-row">
            <span class="form-label">部署目标</span>
            <div class="tool-tags">
              <span
                v-for="tool in allToolTypes"
                :key="tool"
                class="tool-tag"
                :class="{ on: editForm.tools.includes(tool) }"
                @click="toggleTool(tool)"
              >
                {{ toolLabel(tool) }}
              </span>
            </div>
          </div>

          <!-- Actions -->
          <div class="form-row">
            <span class="form-label">操作</span>
            <div class="form-actions">
              <AppButton variant="ghost" size="small" @click="handleBuild" :disabled="busy">
                部署
              </AppButton>
              <AppButton variant="ghost" size="small" @click="handleSync" :disabled="busy">
                同步
              </AppButton>
              <AppButton variant="ghost" size="small" @click="handleClean" :disabled="busy">
                清理
              </AppButton>
              <AppButton variant="primary" size="small" @click="handleSave" :disabled="busy">
                保存
              </AppButton>
            </div>
          </div>
        </div>

        <!-- Execution result card -->
        <div class="result-card" v-if="lastAction">
          <h3>{{ lastAction.label }}结果</h3>
          <p class="section-hint" v-if="lastAction.loading">执行中...</p>

          <template v-if="!lastAction.loading && lastAction.result">
            <!-- Metrics grid -->
            <div class="metrics" v-if="'deployed' in lastAction.result">
              <div class="metric">
                <span>部署条目</span>
                <strong>{{ lastAction.result.deployed.length }}</strong>
              </div>
              <div class="metric">
                <span>移除条目</span>
                <strong>{{ lastAction.result.removed.length }}</strong>
              </div>
              <div class="metric">
                <span>警告</span>
                <strong :class="{ warning: lastAction.result.warnings.length > 0 }">
                  {{ lastAction.result.warnings.length }}
                </strong>
              </div>
              <div class="metric danger" v-if="'conflicts' in lastAction.result && lastAction.result.conflicts.length > 0">
                <span>冲突</span>
                <strong>{{ lastAction.result.conflicts.length }}</strong>
              </div>
            </div>

            <!-- Conflict details -->
            <div class="result-section" v-if="'conflicts' in lastAction.result && lastAction.result.conflicts.length > 0">
              <h4>冲突详情</h4>
              <div
                v-for="conflict in lastAction.result.conflicts"
                :key="`${conflict.type}-${conflict.targetPath || conflict.message}`"
                class="conflict-item"
              >
                <div class="conflict-title">{{ wsConflictLabel(conflict.type) }}</div>
                <div class="section-hint" v-if="conflict.targetPath">{{ conflict.targetPath }}</div>
                <div class="section-hint">{{ conflict.message }}</div>
              </div>
            </div>

            <!-- Warning details -->
            <div class="result-section" v-if="lastAction.result.warnings.length > 0">
              <h4>警告详情</h4>
              <div v-for="(warning, idx) in lastAction.result.warnings" :key="idx" class="section-hint">
                {{ warning }}
              </div>
            </div>
          </template>
        </div>
      </div>

      <!-- Empty state for editor -->
      <div class="ws-editor-empty" v-else>
        <EmptyState icon="◫" title="选择工作区" description="从左侧列表选择一个工作区进行编辑" />
      </div>
    </div>

    <!-- Empty state for no workspaces -->
    <div class="ws-empty" v-else>
      <EmptyState icon="◫" title="暂无工作区" description="创建一个工作区来管理插件部署">
        <template #action>
          <AppButton variant="primary" @click="handleCreateNew">新建工作区</AppButton>
        </template>
      </EmptyState>
    </div>

    <!-- Delete confirmation dialog -->
    <Dialog v-model:open="showDeleteDialog" title="删除工作区" description="删除前会先尝试清理当前工作区的托管文件，此操作不可恢复。">
      <template #footer>
        <AppButton variant="ghost" @click="showDeleteDialog = false" :disabled="busy">
          取消
        </AppButton>
        <AppButton variant="danger" @click="confirmDelete" :disabled="busy">
          {{ busy ? '处理中...' : '确认删除' }}
        </AppButton>
      </template>
    </Dialog>

    <!-- Global enabled dialog -->
    <GlobalEnabledDialog
      v-if="showGlobalDialog"
      :installed-plugins="installedPlugins"
      :entries="globalEnabled"
      @close="showGlobalDialog = false"
      @saved="handleGlobalSaved"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { BrowseDirectory } from '../../../wailsjs/go/main/App';
import { useWorkspaceStore } from '../../stores/workspace';
import { usePluginStore } from '../../stores/plugin';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import EmptyState from '../ui/EmptyState.vue';
import Dialog from '../ui/Dialog.vue';
import GlobalEnabledDialog from './GlobalEnabledDialog.vue';

const workspaceStore = useWorkspaceStore();
const pluginStore = usePluginStore();

const {
  workspaces,
  activeWorkspaceId,
  activeWorkspace,
  globalEnabled,
  loading,
  lastAction,
  workspaceCount,
  busy,
} = storeToRefs(workspaceStore);

// Extract functions from store directly
const { conflictLabel: wsConflictLabel } = workspaceStore;

const { ccInstalled } = storeToRefs(pluginStore);

// All available tool types
const allToolTypes = ['claude', 'opencode', 'cursor', 'vscode'] as const;

// Edit form state
const editForm = ref({
  name: '',
  path: '',
  tools: [] as string[],
});

// Dialog states
const showDeleteDialog = ref(false);
const showGlobalDialog = ref(false);
const workspaceToDelete = ref<string | null>(null);

// Installed plugins for global dialog
const installedPlugins = computed(() => ccInstalled.value);

// Tool label mapping
function toolLabel(tool: string): string {
  const labels: Record<string, string> = {
    claude: 'Claude',
    opencode: 'OpenCode',
    cursor: 'Cursor',
    vscode: 'VS Code',
  };
  return labels[tool] || tool;
}

// Sync edit form with active workspace
watch(activeWorkspace, (ws) => {
  if (ws) {
    editForm.value = {
      name: ws.name,
      path: ws.path,
      tools: [...ws.tools],
    };
  }
}, { immediate: true });

// Load data on mount
onMounted(async () => {
  try {
    await Promise.all([
      workspaceStore.loadWorkspaces(),
      workspaceStore.loadGlobalEnabled(),
    ]);
  } catch (error) {
    console.error('[WorkspacesPanel] Failed to load workspaces:', error);
  }
});

// Actions
function handleRefresh() {
  workspaceStore.loadWorkspaces();
}

function handleCreateNew() {
  // Reset form for new workspace
  activeWorkspaceId.value = null;
  editForm.value = {
    name: '',
    path: '',
    tools: ['claude'],
  };
}

function selectWorkspace(id: string) {
  workspaceStore.selectWorkspace(id);
}

async function handleBrowseDirectory() {
  try {
    const path = await BrowseDirectory();
    if (path) {
      editForm.value.path = path;
    }
  } catch (error) {
    console.error('[WorkspacesPanel] Failed to browse directory:', error);
  }
}

function toggleTool(tool: string) {
  const idx = editForm.value.tools.indexOf(tool);
  if (idx === -1) {
    editForm.value.tools.push(tool);
  } else {
    editForm.value.tools.splice(idx, 1);
  }
}

async function handleSave() {
  if (!editForm.value.name.trim() || !editForm.value.path.trim()) {
    return;
  }

  try {
    if (activeWorkspace.value) {
      // Update existing
      await workspaceStore.updateWorkspace({
        id: activeWorkspace.value.id,
        name: editForm.value.name,
        path: editForm.value.path,
        tools: editForm.value.tools,
      });
    } else {
      // Create new
      const result = await workspaceStore.createWorkspace({
        name: editForm.value.name,
        path: editForm.value.path,
        tools: editForm.value.tools,
      });
      activeWorkspaceId.value = result.id;
    }
  } catch (error) {
    console.error('[WorkspacesPanel] Failed to save workspace:', error);
  }
}

async function handleBuild() {
  if (!activeWorkspaceId.value) return;
  try {
    await workspaceStore.buildScaffold(activeWorkspaceId.value);
  } catch (error) {
    console.error('[WorkspacesPanel] Failed to build scaffold:', error);
  }
}

async function handleSync() {
  if (!activeWorkspaceId.value) return;
  try {
    await workspaceStore.syncWorkspace(activeWorkspaceId.value);
  } catch (error) {
    console.error('[WorkspacesPanel] Failed to sync workspace:', error);
  }
}

async function handleClean() {
  if (!activeWorkspaceId.value) return;
  try {
    await workspaceStore.cleanWorkspace(activeWorkspaceId.value);
  } catch (error) {
    console.error('[WorkspacesPanel] Failed to clean workspace:', error);
  }
}

function openGlobalDialog() {
  showGlobalDialog.value = true;
}

function handleGlobalSaved(result: any) {
  showGlobalDialog.value = false;
  // Reload global enabled state
  workspaceStore.loadGlobalEnabled();
  // Show result if needed
  if (result) {
    console.log('[WorkspacesPanel] Global enabled result:', result);
  }
}

function confirmDelete() {
  if (workspaceToDelete.value) {
    workspaceStore.deleteWorkspace(workspaceToDelete.value)
      .then(() => {
        showDeleteDialog.value = false;
        workspaceToDelete.value = null;
      })
      .catch((error) => {
        console.error('[WorkspacesPanel] Failed to delete workspace:', error);
      });
  }
}

// Watch for workspace changes to sync form
watch(() => [editForm.value.name, editForm.value.path, editForm.value.tools], () => {
  // Could add auto-save or dirty state tracking here
}, { deep: true });
</script>

<style scoped>
.workspaces-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ws-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 18px;
  background: var(--card);
  border-radius: 12px;
  border: 1px solid var(--separator);
}

.ws-zone-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  display: flex;
  align-items: center;
  gap: 6px;
}

.zn-sep {
  color: var(--tertiary);
}

.zn-count {
  color: var(--secondary);
  font-weight: 400;
}

.ws-toolbar-actions {
  display: flex;
  gap: 8px;
}

.ws-content {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: 16px;
  min-height: 400px;
}

.ws-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 600px;
  overflow-y: auto;
  padding-right: 4px;
}

.ws-item {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 12px 14px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

.ws-item:hover {
  border-color: var(--tertiary);
}

.ws-item.active {
  border-color: var(--accent);
  background: rgba(0, 122, 255, 0.04);
}

.ws-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
}

.ws-path {
  font-size: 12px;
  color: var(--secondary);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-bottom: 8px;
}

.ws-tools {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}

.ws-editor {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.ws-editor-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 300px;
}

.ex-form {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 0 18px;
}

.ex-form h3 {
  font-size: 12px;
  font-weight: 600;
  color: var(--tertiary);
  padding: 14px 0 8px;
  letter-spacing: 0.2px;
  margin: 0;
}

.form-row {
  padding: 12px 0;
  display: flex;
  align-items: center;
  gap: 16px;
}

.form-label {
  min-width: 80px;
  font-size: 13px;
  color: var(--secondary);
  font-weight: 500;
}

.form-input {
  flex: 1;
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

.form-input::placeholder {
  color: var(--tertiary);
}

.form-input:read-only {
  background: var(--control);
  cursor: pointer;
}

.form-input-group {
  flex: 1;
  display: flex;
  gap: 8px;
}

.tool-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.tool-tag {
  font-size: 11px;
  padding: 4px 8px;
  border-radius: 4px;
  background: var(--control);
  color: var(--secondary);
  cursor: pointer;
  transition: all 0.15s;
  user-select: none;
}

.tool-tag.on {
  background: var(--accent);
  color: #fff;
}

.tool-tag:hover {
  opacity: 0.85;
}

.form-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.result-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 0 18px;
}

.result-card h3 {
  font-size: 12px;
  font-weight: 600;
  color: var(--tertiary);
  padding: 14px 0 8px;
  letter-spacing: 0.2px;
  margin: 0;
}

.section-hint {
  font-size: 12px;
  color: var(--secondary);
  margin: 4px 0;
}

.metrics {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  padding: 12px 0;
}

.metric {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.metric span {
  font-size: 11px;
  color: var(--secondary);
}

.metric strong {
  font-size: 18px;
  font-weight: 600;
  color: var(--success);
}

.metric.warning strong {
  color: var(--warning);
}

.metric.danger strong {
  color: #FF3B30;
}

.result-section {
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.result-section h4 {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  margin: 0 0 8px 0;
}

.conflict-item {
  background: rgba(255, 59, 48, 0.05);
  border: 1px solid rgba(255, 59, 48, 0.15);
  border-radius: 8px;
  padding: 10px 12px;
  margin-bottom: 8px;
}

.conflict-title {
  font-size: 12px;
  font-weight: 600;
  color: #FF3B30;
  margin-bottom: 4px;
}

.ws-empty {
  min-height: 300px;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* Scrollbar styling */
.ws-list::-webkit-scrollbar {
  width: 6px;
}

.ws-list::-webkit-scrollbar-track {
  background: transparent;
}

.ws-list::-webkit-scrollbar-thumb {
  background: var(--separator);
  border-radius: 3px;
}

.ws-list::-webkit-scrollbar-thumb:hover {
  background: var(--tertiary);
}

/* Responsive */
@media (max-width: 960px) {
  .ws-content {
    grid-template-columns: 1fr;
  }

  .ws-list {
    max-height: 300px;
  }

  .metrics {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
