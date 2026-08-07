<template>
  <div class="omp-plugin-panel">
    <LoadingState v-if="initialLoading" message="加载 OMP 插件中..." />
    <ErrorState
      v-else-if="store.loadError"
      :message="store.loadError"
      :on-retry="loadPackages"
    />

    <template v-else>
      <div v-if="warnings.length" class="status-banner warning">
        <span>{{ warnings.join('；') }}</span>
        <AppButton variant="ghost" size="small" @click="loadPackages">重试</AppButton>
      </div>

      <div class="scope-note">
        <div>
          <strong>OMP 插件 / OMP Plugins</strong>
          <p>OMP 插件通过官方 omp CLI 管理（npm 包 / GitHub 源 / marketplace 引用 / 本地路径）。变更后需重启 omp 会话或执行 /reload-plugins 生效。/ Managed via the official omp CLI (npm packages / GitHub sources / marketplace references / local paths). Restart the omp session or run /reload-plugins to apply changes.</p>
        </div>
        <div class="header-actions">
          <AppButton variant="ghost" size="small" :disabled="loading" @click="loadPackages">
            {{ loading ? '刷新中…' : '刷新' }}
          </AppButton>
          <AppButton variant="primary" size="small" @click="openInstallDialog">
            安装插件
          </AppButton>
        </div>
      </div>

      <div class="panel-header">
        <div class="header-title">
          <h2>已安装</h2>
          <span class="count-badge">{{ installed.length }}</span>
        </div>
      </div>

      <div v-if="installed.length" class="plugin-list">
        <div v-for="plugin in installed" :key="plugin.id" class="plugin-item">
          <div class="plugin-main">
            <div class="plugin-name-row">
              <span class="plugin-name">{{ plugin.name }}</span>
              <div class="plugin-badges">
                <Badge type="source" :text="kindLabel(plugin.kind)" variant="muted" />
                <Badge v-if="plugin.version" type="ver" :text="`v${plugin.version}`" />
                <Badge v-if="plugin.scope" type="tag" :text="plugin.scope" variant="muted" />
              </div>
            </div>
            <p v-if="plugin.description" class="plugin-description">
              {{ truncate(plugin.description, 120) }}
            </p>
            <p v-if="plugin.installPath" class="plugin-path">{{ plugin.installPath }}</p>
          </div>
          <div class="plugin-actions">
            <AppButton
              variant="ghost"
              size="small"
              :disabled="mutating"
              @click="handleToggleEnabled(plugin)"
            >
              {{ mutating ? '处理中…' : (plugin.enabled ? '禁用' : '启用') }}
            </AppButton>
            <AppButton variant="ghost" size="small" :disabled="mutating" @click="handleUpgrade(plugin)">
              {{ mutating ? '处理中…' : '升级' }}
            </AppButton>
            <AppButton
              variant="danger"
              size="small"
              :disabled="mutating"
              @click="openUninstallDialog(plugin)"
            >
              卸载
            </AppButton>
          </div>
        </div>
      </div>

      <EmptyState
        v-else
        icon="⊘"
        title="暂无 OMP 插件"
        description="点击「安装插件」，输入 npm 包名、GitHub 源、marketplace 引用或本地路径"
      />
    </template>

    <Dialog
      v-model:open="showInstallDialog"
      title="安装 OMP 插件"
      description="使用 omp 官方插件安装命令（npm 包 / GitHub 源 / marketplace 引用 / 本地路径）"
    >
      <div class="install-form">
        <label for="omp-plugin-spec">插件引用 Plugin reference</label>
        <input
          id="omp-plugin-spec"
          v-model="installSpec"
          class="module-input"
          placeholder="例：omp.nvim、github:owner/repo、name@catalog 或 /path/to/plugin"
          @keydown.enter="handleInstall"
        />
        <p>支持 npm 包名（如 omp.nvim）、GitHub 源（github:owner/repo）、marketplace 引用（name@catalog）与本地路径；安装后需重启 omp 会话或执行 /reload-plugins 生效。/ Accepts npm package names (e.g. omp.nvim), GitHub sources (github:owner/repo), marketplace references (name@catalog) and local paths; restart the omp session or run /reload-plugins to apply.</p>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showInstallDialog = false">取消</AppButton>
        <AppButton
          variant="primary"
          :disabled="mutating || !installSpec.trim()"
          @click="handleInstall"
        >
          {{ mutating ? '处理中…' : '安装' }}
        </AppButton>
      </template>
    </Dialog>

    <ConfirmDialog
      v-model:open="showUninstallDialog"
      title="卸载 OMP 插件"
      :message="uninstallTarget ? `将从 omp 移除该插件「${uninstallTarget.name}」。此操作不可恢复。` : ''"
      danger
      confirm-text="卸载"
      @confirm="handleUninstall"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { useOmpPluginStore } from '../../stores/ompPlugin';
import type { OmpPlugin } from '../../api/ompPlugin';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import { useToast } from '../../composables/useToast';
import { truncate } from '../../utils/format';

const store = useOmpPluginStore();
const { installed, warnings, loading } = storeToRefs(store);
const { showSuccess, showError } = useToast();

const initialLoading = ref(true);
const mutating = ref(false);
const showInstallDialog = ref(false);
const showUninstallDialog = ref(false);
const installSpec = ref('');
const uninstallTarget = ref<OmpPlugin | null>(null);

function kindLabel(kind: OmpPlugin['kind']) {
  return kind === 'marketplace' ? 'marketplace' : 'npm';
}

async function loadPackages() {
  // 首次挂载与错误重试统一走 Loading 态，避免 loadError 清空瞬间闪回主区
  initialLoading.value = true;
  try {
    await store.refresh(true);
  } finally {
    initialLoading.value = false;
  }
}

function openInstallDialog() {
  installSpec.value = '';
  showInstallDialog.value = true;
}

async function handleInstall() {
  const spec = installSpec.value.trim();
  if (!spec || mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.install(spec);
    showInstallDialog.value = false;
    showSuccess(result.output || 'OMP 插件安装成功');
  } catch (error) {
    showError(`安装失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleToggleEnabled(plugin: OmpPlugin) {
  if (mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.setEnabled(plugin.id, !plugin.enabled);
    showSuccess(result.output || (plugin.enabled ? 'OMP 插件已禁用' : 'OMP 插件已启用'));
  } catch (error) {
    showError(`${plugin.enabled ? '禁用' : '启用'}失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleUpgrade(plugin: OmpPlugin) {
  if (mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.upgrade(plugin.id);
    showSuccess(result.output || 'OMP 插件已升级');
  } catch (error) {
    showError(`升级失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

function openUninstallDialog(plugin: OmpPlugin) {
  uninstallTarget.value = plugin;
  showUninstallDialog.value = true;
}

async function handleUninstall() {
  const target = uninstallTarget.value;
  if (!target || mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.uninstall(target.id);
    showUninstallDialog.value = false;
    uninstallTarget.value = null;
    showSuccess(result.output || 'OMP 插件已卸载');
  } catch (error) {
    showError(`卸载失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

onMounted(loadPackages);
</script>

<style scoped>
.omp-plugin-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.status-banner,
.scope-note {
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 14px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.status-banner.warning {
  color: var(--warning);
  background: color-mix(in srgb, var(--warning) 8%, var(--card));
}

.scope-note {
  background: var(--card);
}

.scope-note strong {
  font-size: 14px;
  color: var(--label);
}

.scope-note p,
.install-form p {
  margin: 5px 0 0;
  font-size: 12px;
  color: var(--secondary);
}

.header-actions,
.plugin-actions,
.plugin-badges {
  display: flex;
  align-items: center;
  gap: 8px;
}

.panel-header,
.header-title {
  display: flex;
  align-items: center;
}

.panel-header {
  justify-content: space-between;
}

.header-title {
  justify-content: flex-start;
  gap: 8px;
}

.header-title h2 {
  margin: 0;
}

.count-badge {
  min-width: 22px;
  text-align: center;
  border-radius: 999px;
  padding: 2px 7px;
  font-size: 11px;
  background: var(--control);
  color: var(--secondary);
}

.plugin-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.plugin-item {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 14px 16px;
}

.plugin-main {
  min-width: 0;
  flex: 1;
}

.plugin-name-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.plugin-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
}

.plugin-badges {
  flex-wrap: wrap;
}

.plugin-description,
.plugin-path {
  margin: 7px 0 0;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.5;
}

.plugin-path {
  font-size: 11px;
  color: var(--tertiary);
  font-family: var(--mono);
  overflow-wrap: anywhere;
}

.plugin-actions {
  flex-shrink: 0;
  padding-top: 2px;
}

.install-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.install-form label {
  font-size: 13px;
  font-weight: 600;
}

.module-input {
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  padding: 9px 11px;
}

.module-input:focus {
  outline: none;
  border-color: var(--accent);
}

@media (max-width: 900px) {
  .plugin-item {
    flex-direction: column;
  }

  .plugin-actions {
    width: 100%;
  }
}
</style>
