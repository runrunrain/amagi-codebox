<!--
  Agent 配置档（设置 > Agent 配置档）
  保存多份 CLI 模型配置（家/公司两套）并一键切换。
  数据契约：wailsjs/go/agentprofile/Service（见 src/api/agentProfile.ts）。
  live 文件：pi 侧 ~/.pi/agent/amagi.json（JSON），omp 侧 ~/.omp/agent/config.yml（YAML）。
-->
<template>
  <div class="ap-settings">
    <!-- live 文件路径说明 -->
    <ConfigCard>
      <div class="ap-block">
        <div class="ap-block-title">管理的 live 配置文件</div>
        <p class="ap-block-desc">
          pi 侧：<code class="ap-path">~/.pi/agent/amagi.json</code>（JSON）<br>
          omp 侧：<code class="ap-path">~/.omp/agent/config.yml</code>（YAML，留空表示不管理该侧）
        </p>
        <p class="ap-block-desc">应用配置档会覆盖以上文件；覆盖前已有的 live 文件会自动备份为 <code class="ap-path">.bak-时间戳</code>。</p>
      </div>
    </ConfigCard>

    <!-- 从当前配置快照为档 -->
    <ConfigCard>
      <div class="ap-block">
        <div class="ap-block-title">从当前配置快照为档</div>
        <p class="ap-block-desc">把当前两份 live 文件的内容保存为一个命名配置档。</p>
        <div class="ap-capture-row">
          <TextInput
            v-model="captureName"
            placeholder="配置档名称，如：公司 / 家里"
            :disabled="busy"
          />
          <AppButton variant="primary" :disabled="busy || !captureName.trim()" @click="onCapture">
            快照为档
          </AppButton>
        </div>
        <p v-if="captureError" class="ap-error" role="alert">{{ captureError }}</p>
      </div>
    </ConfigCard>

    <!-- 档列表 -->
    <ConfigCard>
      <div class="ap-block">
        <div class="ap-block-title">已保存的配置档</div>
        <div v-if="listLoading" class="ap-loading" aria-live="polite">正在读取配置档…</div>
        <p v-else-if="listError" class="ap-error" role="alert">
          {{ listError }}
          <button type="button" class="ap-link" @click="loadList">重试</button>
        </p>
        <EmptyState
          v-else-if="profileNames.length === 0"
          icon="📁"
          title="暂无配置档"
          description="使用上方「快照为档」保存当前配置，或点击下方「新建配置档」手动编辑内容。"
        >
          <template #action>
            <AppButton variant="primary" size="small" @click="openEditor('')">新建配置档</AppButton>
          </template>
        </EmptyState>
        <template v-else>
          <div class="ap-list">
            <div v-for="name in profileNames" :key="name" class="ap-row">
              <div class="ap-row-info">
                <span class="ap-row-name">
                  {{ name }}
                  <span v-if="name === store.lastApplied" class="ap-applied-badge">当前已应用</span>
                </span>
                <span class="ap-row-time">更新于 {{ formatTime(store.profiles[name].updatedAt) }}</span>
              </div>
              <div class="ap-row-actions">
                <AppButton size="small" variant="primary" :disabled="busy" @click="askApply(name)">应用</AppButton>
                <AppButton size="small" :disabled="busy" @click="openEditor(name)">编辑</AppButton>
                <AppButton size="small" variant="danger" :disabled="busy" @click="askDelete(name)">删除</AppButton>
              </div>
            </div>
          </div>
          <div class="ap-list-footer">
            <AppButton size="small" :disabled="busy" @click="openEditor('')">新建配置档</AppButton>
          </div>
        </template>
        <p v-if="actionError" class="ap-error" role="alert">{{ actionError }}</p>
        <p v-if="actionSuccess" class="ap-success" role="status">{{ actionSuccess }}</p>
      </div>
    </ConfigCard>

    <!-- 编辑/新建档弹窗 -->
    <Dialog
      :open="editorOpen"
      :title="editorExisting ? `编辑配置档：${editorName}` : '新建配置档'"
      @update:open="closeEditor"
    >
      <div class="ap-editor">
        <label class="ap-field">
          <span class="ap-field-label">配置档名称</span>
          <TextInput v-model="editorName" placeholder="如：公司 / 家里" :disabled="busy || editorExisting" />
        </label>
        <label class="ap-field">
          <span class="ap-field-label">pi 内容（JSON，~/.pi/agent/amagi.json 全文）</span>
          <textarea
            v-model="editorPi"
            class="ap-textarea"
            rows="8"
            spellcheck="false"
            placeholder='{ "agents": { ... } }'
            :disabled="busy"
          />
        </label>
        <label class="ap-field">
          <span class="ap-field-label">omp 内容（YAML，~/.omp/agent/config.yml 全文；可留空表示不管理该侧）</span>
          <textarea
            v-model="editorOmp"
            class="ap-textarea"
            rows="8"
            spellcheck="false"
            placeholder="modelRoles: ..."
            :disabled="busy"
          />
        </label>
        <p v-if="editorError" class="ap-error" role="alert">{{ editorError }}</p>
      </div>
      <template #footer>
        <div class="ap-editor-actions">
          <AppButton variant="ghost" :disabled="busy" @click="closeEditor">取消</AppButton>
          <AppButton variant="primary" :disabled="busy || !editorName.trim()" @click="onSaveEditor">
            {{ busy ? '保存中…' : '保存' }}
          </AppButton>
        </div>
      </template>
    </Dialog>

    <!-- 覆盖同名档确认（快照时） -->
    <ConfirmDialog
      v-model:open="captureOverwriteOpen"
      title="覆盖同名配置档"
      :message="`已存在名为「${captureName.trim()}」的配置档，快照将覆盖其内容。确定继续吗？`"
      confirm-text="覆盖"
      danger
      @confirm="doCapture"
    />

    <!-- 应用确认 -->
    <ConfirmDialog
      v-model:open="applyConfirmOpen"
      title="应用配置档"
      :message="`将把「${pendingName}」应用到 live 配置：覆盖 ~/.pi/agent/amagi.json 与 ~/.omp/agent/config.yml（已有的 live 文件会自动备份为 .bak）。确定切换吗？`"
      confirm-text="应用"
      danger
      @confirm="doApply"
    />

    <!-- 删除确认 -->
    <ConfirmDialog
      v-model:open="deleteConfirmOpen"
      title="删除配置档"
      :message="`确定删除配置档「${pendingName}」吗？此操作不会影响当前 live 配置，但档内容无法恢复。`"
      confirm-text="删除"
      danger
      @confirm="doDelete"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import ConfigCard from '../../components/ui/ConfigCard.vue';
import AppButton from '../../components/ui/AppButton.vue';
import TextInput from '../../components/ui/TextInput.vue';
import EmptyState from '../../components/ui/EmptyState.vue';
import Dialog from '../../components/ui/Dialog.vue';
import ConfirmDialog from '../../components/ui/ConfirmDialog.vue';
import {
  listAgentProfiles,
  getAgentProfile,
  captureAgentProfile,
  saveAgentProfile,
  applyAgentProfile,
  deleteAgentProfile,
  type AgentProfileStore,
} from '../../api/agentProfile';

const store = ref<AgentProfileStore>({ version: 1, profiles: {}, lastApplied: '' });
const listLoading = ref(true);
const listError = ref('');
const busy = ref(false);

const captureName = ref('');
const captureError = ref('');
const captureOverwriteOpen = ref(false);

const actionError = ref('');
const actionSuccess = ref('');

const applyConfirmOpen = ref(false);
const deleteConfirmOpen = ref(false);
const pendingName = ref('');

const editorOpen = ref(false);
const editorExisting = ref(false);
const editorName = ref('');
const editorPi = ref('');
const editorOmp = ref('');
const editorError = ref('');

const profileNames = computed(() => Object.keys(store.value.profiles).sort((a, b) => a.localeCompare(b)));

function errMsg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function formatTime(ts: number): string {
  if (!ts) return '—';
  return new Date(ts).toLocaleString();
}

function clearFeedback() {
  actionError.value = '';
  actionSuccess.value = '';
}

async function loadList() {
  listLoading.value = true;
  try {
    store.value = await listAgentProfiles();
    listError.value = '';
  } catch (err) {
    listError.value = `读取配置档失败：${errMsg(err)}`;
  } finally {
    listLoading.value = false;
  }
}

/** 快照入口：同名档先弹覆盖确认，否则直接快照。 */
function onCapture() {
  captureError.value = '';
  const name = captureName.value.trim();
  if (!name) return;
  if (store.value.profiles[name]) {
    captureOverwriteOpen.value = true;
    return;
  }
  void doCapture();
}

async function doCapture() {
  const name = captureName.value.trim();
  if (!name) return;
  busy.value = true;
  clearFeedback();
  try {
    await captureAgentProfile(name);
    captureName.value = '';
    captureError.value = '';
    actionSuccess.value = `已快照为配置档「${name}」`;
    await loadList();
  } catch (err) {
    captureError.value = `快照失败：${errMsg(err)}`;
  } finally {
    busy.value = false;
  }
}

function askApply(name: string) {
  pendingName.value = name;
  applyConfirmOpen.value = true;
}

async function doApply() {
  const name = pendingName.value;
  busy.value = true;
  clearFeedback();
  try {
    await applyAgentProfile(name);
    actionSuccess.value = `已应用配置档「${name}」，live 配置已切换（原文件已备份为 .bak）`;
    await loadList();
  } catch (err) {
    actionError.value = `应用失败：${errMsg(err)}`;
  } finally {
    busy.value = false;
  }
}

function askDelete(name: string) {
  pendingName.value = name;
  deleteConfirmOpen.value = true;
}

async function doDelete() {
  const name = pendingName.value;
  busy.value = true;
  clearFeedback();
  try {
    await deleteAgentProfile(name);
    actionSuccess.value = `已删除配置档「${name}」`;
    await loadList();
  } catch (err) {
    actionError.value = `删除失败：${errMsg(err)}`;
  } finally {
    busy.value = false;
  }
}

/** 打开编辑弹窗：name 为空=新建；否则载入已有档内容。 */
async function openEditor(name: string) {
  editorError.value = '';
  editorExisting.value = !!name;
  editorName.value = name;
  editorPi.value = '';
  editorOmp.value = '';
  editorOpen.value = true;
  if (name) {
    busy.value = true;
    try {
      const p = await getAgentProfile(name);
      editorPi.value = p.pi;
      editorOmp.value = p.omp;
    } catch (err) {
      editorError.value = `读取配置档失败：${errMsg(err)}`;
    } finally {
      busy.value = false;
    }
  }
}

function closeEditor() {
  if (busy.value) return;
  editorOpen.value = false;
}

/** 保存编辑结果：前端先对 pi 侧做 JSON 初校（omp 为 YAML 不校验），最终以后端报错为准。 */
async function onSaveEditor() {
  const name = editorName.value.trim();
  if (!name) return;
  editorError.value = '';
  if (editorPi.value.trim() !== '') {
    try {
      JSON.parse(editorPi.value);
    } catch (err) {
      editorError.value = `pi 内容不是合法 JSON：${errMsg(err)}`;
      return;
    }
  }
  busy.value = true;
  try {
    await saveAgentProfile(name, editorPi.value, editorOmp.value);
    editorOpen.value = false;
    clearFeedback();
    actionSuccess.value = `已保存配置档「${name}」`;
    await loadList();
  } catch (err) {
    editorError.value = `保存失败：${errMsg(err)}`;
  } finally {
    busy.value = false;
  }
}

onMounted(loadList);
</script>

<style scoped>
.ap-settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 760px;
}

.ap-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-bottom: 10px;
}

.ap-block-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--label);
}

.ap-block-desc {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.7;
}

.ap-path {
  font-family: var(--mono);
  font-size: 12px;
  background: var(--control);
  border-radius: 4px;
  padding: 1px 5px;
}

.ap-capture-row {
  display: flex;
  gap: 10px;
  align-items: center;
}

.ap-capture-row :deep(.text-input) {
  flex: 1;
}

.ap-loading {
  padding: 20px 0;
  font-size: 13px;
  color: var(--tertiary);
}

.ap-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ap-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 8px;
  border-radius: 8px;
}

.ap-row:hover {
  background: var(--control);
}

.ap-row-info {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.ap-row-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 500;
  color: var(--label);
  word-break: break-all;
}

.ap-applied-badge {
  flex-shrink: 0;
  font-size: 10px;
  font-weight: 600;
  color: var(--success);
  background: rgba(52, 199, 89, 0.14);
  border-radius: 4px;
  padding: 1px 6px;
}

.ap-row-time {
  font-size: 12px;
  color: var(--tertiary);
}

.ap-row-actions {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.ap-list-footer {
  padding-top: 8px;
}

.ap-error {
  margin: 0;
  font-size: 13px;
  color: #FF3B30;
  line-height: 1.6;
}

.ap-success {
  margin: 0;
  font-size: 13px;
  color: var(--success);
  line-height: 1.6;
}

.ap-link {
  background: none;
  border: none;
  padding: 0;
  font-size: inherit;
  color: var(--accent);
  cursor: pointer;
}

.ap-editor {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.ap-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ap-field-label {
  font-size: 13px;
  color: var(--secondary);
}

.ap-textarea {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  background: var(--control);
  border: none;
  border-radius: 8px;
  padding: 8px 10px;
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.6;
  color: var(--label);
  outline: none;
}

.ap-textarea:focus {
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.2);
}

.ap-textarea::placeholder {
  color: var(--tertiary);
}

.ap-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
