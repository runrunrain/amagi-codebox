<template>
  <div class="wsl-plugin-section">
    <div class="section-head">
      <div>
        <strong>WSL 侧插件（原生重放）</strong>
        <p>
          每侧实体各反映自己的状态：WSL 侧经 distro 内原生 pi CLI 重放安装（npm 固定版本、git 固定 ref），
          不复制 Windows 实体；每目标结果独立显示。
          <template v-if="status.distro">当前目标：{{ status.distro }}</template>
        </p>
      </div>
      <AppButton variant="ghost" size="small" :disabled="checking" @click="refresh(true)">
        {{ checking ? '检查中…' : '检查 WSL 侧' }}
      </AppButton>
    </div>

    <div v-if="status.reason" class="wsl-note">{{ status.reason }}</div>

    <template v-if="status.snapshot?.checked">
      <div class="wsl-meta">
        <Badge type="tag" :text="`distro 内 pi：${status.snapshot.piResolve ? '已解析' : '未解析'}`" variant="muted" />
        <Badge type="scope" :text="`本系统管理 ${status.snapshot.managed}`" />
        <Badge v-if="status.snapshot.installed" type="ver" :text="`已安装 ${status.snapshot.installed}`" />
        <Badge v-if="status.snapshot.mismatched" type="tag" :text="`登记/实体不一致 ${status.snapshot.mismatched}`" variant="muted" />
        <Badge v-if="status.snapshot.local" type="tag" :text="`本地项 ${status.snapshot.local}`" variant="muted" />
        <span class="wsl-checked-at">检查于 {{ formatTime(status.snapshot.checkedAt || '') }}</span>
      </div>

      <div v-if="status.snapshot.items.length" class="wsl-list">
        <div v-for="item in status.snapshot.items" :key="item.identity" class="wsl-item">
          <div class="wsl-item-main">
            <span class="wsl-item-source">{{ item.source }}</span>
            <Badge :type="item.managed ? 'scope' : 'tag'" :text="stateLabel(item)" :variant="item.managed ? undefined : 'muted'" />
          </div>
          <p class="wsl-item-detail">
            {{ item.detail }}
            <template v-if="item.pinnedRef">· 锚定 {{ item.refKind === 'git-commit' ? 'commit' : (item.refKind === 'npm-version' ? '版本' : 'ref') }} {{ shortRef(item.pinnedRef) }}</template>
            <template v-if="item.version && item.sourceType === 'npm'">· 实体 v{{ item.version }}</template>
            <template v-if="item.commit && item.sourceType === 'git'">· HEAD {{ shortRef(item.commit) }}</template>
          </p>
        </div>
      </div>
      <p v-else class="wsl-empty">WSL 侧暂无插件登记。</p>
    </template>
    <p v-else class="wsl-empty">尚未检查（读取缓存不唤醒停机发行版；点击「检查 WSL 侧」探测当前目标）。</p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import {
  checkPiPluginWSLStatus,
  getPiPluginWSLStatus,
  type PiPluginWSLStatus,
} from '../../api/piPlugin';

const props = defineProps<{ initial?: PiPluginWSLStatus | null }>();

const status = ref<PiPluginWSLStatus>(
  props.initial ?? { distro: '', available: false, snapshot: { distro: '', available: false, checked: false, items: [], managed: 0, installed: 0, mismatched: 0, local: 0 }, summary: { managed: 0, installed: 0, failed: 0, frozen: 0, missing: 0 } },
);
const checking = ref(false);

async function refresh(explicit = false) {
  if (checking.value) return;
  checking.value = true;
  try {
    status.value = explicit ? await checkPiPluginWSLStatus() : await getPiPluginWSLStatus();
  } finally {
    checking.value = false;
  }
}

function stateLabel(item: PiPluginWSLStatus['snapshot']['items'][number]): string {
  switch (item.state) {
    case 'installed': return '已安装';
    case 'mismatch': return '不一致';
    case 'local-item': return '本地项';
    case 'registered-missing': return '登记漂移';
    case 'frozen': return '已冻结';
    default: return item.state;
  }
}

function shortRef(ref: string): string {
  return ref.length > 12 ? `${ref.slice(0, 8)}…` : ref;
}

function formatTime(value: string): string {
  if (!value) return '未知';
  try {
    return new Date(value).toLocaleString('zh-CN');
  } catch {
    return value;
  }
}

defineExpose({ refresh });

onMounted(() => {
  void refresh(false);
});
</script>

<style scoped>
.wsl-plugin-section {
  border: 1px solid var(--separator);
  border-radius: 12px;
  background: var(--card);
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.section-head strong {
  font-size: 13px;
  color: var(--label);
}

.section-head p {
  margin: 5px 0 0;
  font-size: 11px;
  color: var(--secondary);
  line-height: 1.6;
}

.wsl-note {
  font-size: 12px;
  color: var(--warning);
}

.wsl-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.wsl-checked-at {
  font-size: 11px;
  color: var(--tertiary);
}

.wsl-list {
  display: flex;
  flex-direction: column;
}

.wsl-item {
  padding: 9px 0;
  border-top: 1px solid var(--separator);
}

.wsl-item-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.wsl-item-source {
  font-size: 12px;
  color: var(--label);
  overflow-wrap: anywhere;
}

.wsl-item-detail {
  margin: 4px 0 0;
  font-size: 11px;
  color: var(--secondary);
}

.wsl-empty {
  margin: 0;
  font-size: 12px;
  color: var(--tertiary);
}
</style>
