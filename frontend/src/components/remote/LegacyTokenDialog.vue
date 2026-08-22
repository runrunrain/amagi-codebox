<!--
  LegacyTokenDialog（RC4-2 · legacy 访问令牌配置）
  作用于当前远程 scope 主机：设置/替换/清除 legacy token（仅入 Keychain，
  登记簿只落 hasLegacyToken 布尔）。已配置状态只显示「已配置（隐藏）」，
  不回显令牌本体（掩码纪律：与宿主密钥字段同等待遇）。
-->
<template>
  <Dialog
    :open="open"
    title="配置 legacy 访问令牌"
    :description="`远程配置管理（提供商/设置读写）经宿主 legacy 接口提供，需要访问令牌。当前主机：${hostName}`"
    @update:open="$emit('update:open', $event)"
  >
    <div class="ltd-body">
      <div class="ltd-status">
        <span class="ltd-status-label">令牌状态</span>
        <span class="ltd-status-value">
          <span class="sess-dot" :class="{ on: hasToken }"></span>
          {{ hasToken ? '已配置（隐藏）' : '未配置' }}
        </span>
      </div>
      <p v-if="hasToken" class="ltd-hint">已保存的令牌不会回显；输入新令牌将整体替换旧令牌。</p>
      <TextInput
        v-model="tokenInput"
        type="password"
        mono
        placeholder="粘贴宿主的远程控制访问令牌"
        :disabled="submitting"
      />
      <p class="ltd-hint">
        令牌即宿主「设置 → 远程访问」中的远程控制 Token（RegenerateRemoteToken 生成），仅保存在本机系统凭据库中。
      </p>
    </div>
    <template #footer>
      <AppButton
        v-if="hasToken"
        variant="danger"
        size="small"
        :disabled="submitting || !tokenInput"
        @click="confirmClearOpen = true"
      >清除令牌</AppButton>
      <span class="ltd-spacer"></span>
      <AppButton variant="ghost" size="small" :disabled="submitting" @click="close">取消</AppButton>
      <AppButton
        variant="primary"
        size="small"
        :disabled="submitting || !tokenInput.trim()"
        @click="submit"
      >{{ submitting ? '保存中…' : '保存令牌' }}</AppButton>
    </template>

    <ConfirmDialog
      v-model:open="confirmClearOpen"
      :danger="true"
      title="清除 legacy 访问令牌"
      message="清除后本机将无法读写该主机的远程配置（提供商/设置），直到重新配置令牌。确认清除？"
      confirm-text="清除"
      cancel-text="取消"
      @confirm="doClear"
    />
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import Dialog from '../ui/Dialog.vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import { useRemoteClientStore } from '../../stores/remoteClient';
import { useToast } from '../../composables/useToast';
import { copyForRemoteError } from './remoteClientShared';

interface Props {
  open: boolean;
  hostName: string;
  hasToken: boolean;
}

const props = defineProps<Props>();
const emit = defineEmits<{
  'update:open': [value: boolean];
  changed: [];
}>();

const store = useRemoteClientStore();
const { showSuccess, showError } = useToast();

const tokenInput = ref('');
const submitting = ref(false);
const confirmClearOpen = ref(false);

watch(
  () => props.open,
  (val) => {
    if (val) {
      tokenInput.value = '';
      confirmClearOpen.value = false;
    }
  },
);

function close() {
  emit('update:open', false);
}

async function submit() {
  const token = tokenInput.value.trim();
  if (!token || submitting.value) return;
  submitting.value = true;
  try {
    await store.setRemoteLegacyToken(token);
    showSuccess('访问令牌已保存');
    emit('changed');
    close();
  } catch (err) {
    showError('保存令牌失败: ' + copyForRemoteError(err));
  } finally {
    submitting.value = false;
  }
}

async function doClear() {
  if (submitting.value) return;
  submitting.value = true;
  try {
    await store.clearRemoteLegacyToken();
    showSuccess('访问令牌已清除');
    confirmClearOpen.value = false;
    emit('changed');
    close();
  } catch (err) {
    showError('清除令牌失败: ' + copyForRemoteError(err));
  } finally {
    submitting.value = false;
  }
}
</script>

<style scoped>
.ltd-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ltd-status {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
}

.ltd-status-label {
  color: var(--tertiary);
  font-size: 12px;
  min-width: 64px;
}

.ltd-status-value {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--secondary);
}

.sess-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--tertiary);
}

.sess-dot.on {
  background: var(--success);
}

.ltd-hint {
  margin: 0;
  font-size: 12px;
  color: var(--tertiary);
  line-height: 1.6;
}

.ltd-spacer {
  flex: 1;
}
</style>
