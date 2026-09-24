<!--
  HostManageDialog（RC1-6 桌面端互联 · 已登记主机管理）
  补齐此前缺失的管理入口：编辑（显示名/地址）、重新配对（预填配对向导）、
  移除（含本机凭据清理）。域语义：
  - 改名：仅本机登记簿字段。
  - 改地址：域层重置配对态并清理旧凭据（UpdateHostPort），之后需重新配对。
  - 重新配对：复用 PairingWizardDialog（UpsertPaired 按 hostPort/deviceID
    回写原条目，显示名保留）。
  - 移除：ForgetHost（凭据删除失败则整体失败，不留孤儿）；对方 CodeBox
    不受影响，对方设备列表旧条目可在对方端撤销。
-->
<template>
  <Dialog
    :open="open"
    title="管理远程主机"
    description="编辑显示名与地址、重新配对或移除已登记主机。"
    @update:open="handleClose"
  >
    <div class="hm-body">
      <StatusBanner v-if="errorCopy" type="error" :message="errorCopy" />

      <EmptyState
        v-if="store.hosts.length === 0"
        icon="⌁"
        title="尚未登记主机"
        description="通过主机切换菜单的「添加主机…」配对第一台远程 CodeBox。"
      />

      <ul v-else class="hm-list">
        <li v-for="h in store.hosts" :key="h.id" class="hm-row">
          <!-- 编辑态：显示名 + 地址行内表单 -->
          <template v-if="editingId === h.id">
            <div class="hm-edit">
              <div class="hm-edit-fields">
                <label class="hm-field">
                  <span class="hm-label">显示名</span>
                  <TextInput v-model="editName" :disabled="busy" @keydown.enter="saveEdit" />
                </label>
                <label class="hm-field">
                  <span class="hm-label">地址</span>
                  <TextInput v-model="editAddr" mono :disabled="busy" @keydown.enter="saveEdit" />
                </label>
              </div>
              <p v-if="addressChanged" class="hm-warn">
                地址变更将重置该主机的配对状态（清理本机保存的旧凭据），保存后需重新配对。
              </p>
              <div class="hm-edit-actions">
                <AppButton size="small" variant="primary" :disabled="busy || !canSave" @click="saveEdit">
                  {{ saving ? '保存中…' : '保存' }}
                </AppButton>
                <AppButton size="small" variant="ghost" :disabled="busy" @click="cancelEdit">取消</AppButton>
              </div>
            </div>
          </template>

          <!-- 展示态：状态灯 + 名称/地址/健康 + 操作 -->
          <template v-else>
            <span class="hm-dot" :class="dotClass(h.health)" aria-hidden="true"></span>
            <div class="hm-main">
              <span class="hm-name" :class="{ revoked: h.health === 'revoked' }">{{ h.displayName }}</span>
              <span class="hm-sub">
                {{ h.hostPort }} · {{ hostHealthLabel(h.health) }}<template v-if="!h.deviceId">（未配对）</template>
              </span>
            </div>
            <div class="hm-actions">
              <AppButton size="small" variant="ghost" :disabled="busy" @click="startEdit(h)">编辑</AppButton>
              <AppButton size="small" variant="ghost" :disabled="busy" @click="emit('repair', h)">重新配对</AppButton>
              <AppButton size="small" variant="danger" :disabled="busy" @click="askRemove(h)">删除</AppButton>
            </div>
          </template>
        </li>
      </ul>

      <p class="hm-note">
        移除主机仅影响本机登记（同时清理本机保存的配对凭据），不会改动对方 CodeBox；
        对方设备列表中的旧条目可在对方端撤销。
      </p>
    </div>

    <ConfirmDialog
      :open="removeTarget !== null"
      title="移除远程主机"
      :consequence="`将移除「${removeTarget?.displayName ?? ''}」并清理本机保存的配对凭据。`"
      irreversible-note="重新添加该主机需再次输入配对码；对方 CodeBox 不受影响，对方设备列表中的旧条目可在对方端撤销。"
      confirm-text="移除"
      :busy="removing"
      busy-text="移除中…"
      @update:open="onRemoveDialogOpenChange"
      @confirm="doRemove"
    />
  </Dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import ConfirmDialog from './ConfirmDialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import { useRemoteClientStore } from '../../stores/remoteClient';
import { hostHealthLabel, hostHealthTone, copyForRemoteError } from './remoteClientShared';
import type { HostEntry } from '../../api/remoteClient';

interface Props {
  open?: boolean;
}

withDefaults(defineProps<Props>(), { open: false });

const emit = defineEmits<{
  'update:open': [value: boolean];
  /** 重新配对：父层关闭本对话框并以该主机预填打开配对向导。 */
  repair: [host: HostEntry];
}>();

const store = useRemoteClientStore();

// ---- 编辑态 ----
const editingId = ref('');
const editName = ref('');
const editAddr = ref('');
const saving = ref(false);

const editingHost = computed<HostEntry | null>(
  () => store.hosts.find((h) => h.id === editingId.value) ?? null,
);
const addressChanged = computed(
  () => editingHost.value !== null && editAddr.value.trim() !== editingHost.value.hostPort,
);
const canSave = computed(() => editName.value.trim().length > 0 && editAddr.value.trim().length > 0);
const busy = computed(() => saving.value || removing.value);

function startEdit(h: HostEntry) {
  clearError();
  editingId.value = h.id;
  editName.value = h.displayName;
  editAddr.value = h.hostPort;
}

function cancelEdit() {
  editingId.value = '';
  editName.value = '';
  editAddr.value = '';
}

async function saveEdit() {
  const host = editingHost.value;
  if (!host || saving.value || !canSave.value) return;
  const name = editName.value.trim();
  const addr = editAddr.value.trim();
  saving.value = true;
  clearError();
  try {
    if (addr !== host.hostPort) {
      await store.updateHostAddress(host.id, addr);
    }
    if (name !== host.displayName) {
      await store.renameHost(host.id, name);
    }
    cancelEdit();
  } catch (err) {
    errorCopy.value = copyForRemoteError(err);
  } finally {
    saving.value = false;
  }
}

// ---- 删除态 ----
const removeTarget = ref<HostEntry | null>(null);
const removing = ref(false);

function askRemove(h: HostEntry) {
  clearError();
  removeTarget.value = h;
}

/** ConfirmDialog 关闭（取消/遮罩禁用态）即清空待删目标。 */
function onRemoveDialogOpenChange(value: boolean) {
  if (!value) removeTarget.value = null;
}

async function doRemove() {
  const target = removeTarget.value;
  if (!target || removing.value) return;
  removing.value = true;
  clearError();
  try {
    await store.removeHost(target.id);
    if (editingId.value === target.id) cancelEdit();
  } catch (err) {
    errorCopy.value = copyForRemoteError(err);
  } finally {
    removing.value = false;
    removeTarget.value = null;
  }
}

// ---- 公共 ----
const errorCopy = ref('');

function clearError() {
  errorCopy.value = '';
}

function dotClass(health: string): string {
  switch (hostHealthTone(health)) {
    case 'green':
      return 'dot-green';
    case 'red':
      return 'dot-red';
    default:
      return 'dot-gray';
  }
}

function handleClose(value: boolean) {
  emit('update:open', value);
  if (!value) {
    cancelEdit();
    removeTarget.value = null;
    clearError();
  }
}
</script>

<style scoped>
.hm-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.hm-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}

.hm-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 2px;
  border-bottom: 1px solid var(--separator);
}

.hm-row:last-child {
  border-bottom: none;
}

.hm-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  flex-shrink: 0;
}

.dot-green {
  background: var(--success);
}

.dot-gray {
  background: var(--tertiary);
}

.dot-red {
  background: var(--danger-strong);
}

.hm-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.hm-name {
  font-size: 13px;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hm-name.revoked {
  color: var(--danger-strong);
}

.hm-sub {
  font-size: 11px;
  color: var(--tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hm-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.hm-edit {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.hm-edit-fields {
  display: flex;
  gap: 10px;
}

.hm-field {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.hm-label {
  font-size: 11px;
  color: var(--secondary);
}

.hm-warn {
  margin: 0;
  font-size: 11px;
  color: var(--warning, #b58a2c);
  line-height: 1.5;
}

.hm-edit-actions {
  display: flex;
  gap: 8px;
}

.hm-note {
  margin: 0;
  font-size: 11px;
  color: var(--tertiary);
  line-height: 1.6;
}
</style>
