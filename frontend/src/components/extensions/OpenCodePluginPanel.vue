<template>
  <div class="opencode-plugin-panel">
    <LoadingState v-if="initialLoading" message="加载 OpenCode 插件中..." />
    <ErrorState
      v-else-if="initialError"
      :message="initialError"
      :on-retry="loadPlugins"
    />

    <template v-else>
      <div v-if="warnings.length" class="status-banner warning">
        <span>{{ warnings.join('；') }}</span>
        <AppButton variant="ghost" size="small" @click="loadPlugins">重试</AppButton>
      </div>

      <div class="scope-note">
        <div>
          <strong>OpenCode 全局插件</strong>
          <p>更新时会发现最新稳定版本、切换到不可变 tag/精确 npm 版本，再通过官方 CLI 安装并校验。</p>
        </div>
        <div class="header-actions">
          <AppButton variant="ghost" size="small" :disabled="loading" @click="loadPlugins">
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

      <div v-if="installed.length" class="plugin-split">
        <div class="plugin-list">
          <button
            v-for="plugin in installed"
            :key="plugin.spec"
            :class="['plugin-item', { active: activeSpec === plugin.spec }]"
            @click="select(plugin.spec)"
          >
            <div class="plugin-name-row">
              <span class="plugin-name">{{ plugin.name }}</span>
              <Badge type="scope" text="全局" />
            </div>
            <div class="plugin-badges">
              <Badge type="source" :text="sourceLabel(plugin.source)" variant="muted" />
              <Badge v-if="plugin.version" type="ver" :text="`v${plugin.version}`" />
              <Badge
                v-for="target in plugin.targets || []"
                :key="target"
                type="type"
                :text="target"
                :color="target === 'tui' ? 'command' : 'plugin'"
              />
            </div>
            <p class="plugin-spec">{{ plugin.spec }}</p>
            <p v-if="plugin.description" class="plugin-description">
              {{ truncate(plugin.description, 90) }}
            </p>
          </button>
        </div>

        <div v-if="activePlugin" class="plugin-detail">
          <div class="detail-header">
            <div>
              <h3>{{ activePlugin.name }}</h3>
              <div class="plugin-badges">
                <Badge type="source" :text="sourceLabel(activePlugin.source)" variant="muted" />
                <Badge v-if="activePlugin.version" type="ver" :text="`v${activePlugin.version}`" />
                <Badge
                  v-for="target in activePlugin.targets || []"
                  :key="target"
                  type="type"
                  :text="target"
                  :color="target === 'tui' ? 'command' : 'plugin'"
                />
              </div>
            </div>
            <div class="detail-actions">
              <AppButton
                variant="ghost"
                size="small"
                :disabled="mutating || activePlugin.source === 'file'"
                @click="handleUpdate"
              >
                {{ activePlugin.source === 'file' ? '本地直载' : (mutating ? '处理中…' : '检查更新') }}
              </AppButton>
              <AppButton variant="danger" size="small" :disabled="mutating" @click="showUninstallDialog = true">
                卸载
              </AppButton>
            </div>
          </div>

          <p v-if="activePlugin.description" class="detail-description">
            {{ activePlugin.description }}
          </p>

          <div class="meta-list">
            <div class="meta-row">
              <span>模块 Spec</span>
              <code>{{ activePlugin.spec }}</code>
            </div>
            <div class="meta-row">
              <span>安装路径</span>
              <code>{{ activePlugin.installPath || '未找到缓存包' }}</code>
            </div>
            <div v-if="activePlugin.author" class="meta-row">
              <span>作者</span>
              <span>{{ activePlugin.author }}</span>
            </div>
            <div v-if="activePlugin.repository" class="meta-row">
              <span>仓库</span>
              <span class="break-text">{{ activePlugin.repository }}</span>
            </div>
            <div v-if="activePlugin.lastUpdated" class="meta-row">
              <span>缓存更新时间</span>
              <span>{{ formatDate(activePlugin.lastUpdated) }}</span>
            </div>
          </div>

          <div v-if="loadingDetail" class="detail-loading">正在读取插件资源…</div>
          <template v-else-if="isDetail(activePlugin)">
            <div class="resource-summary">
              <div class="resource-card">
                <span class="resource-value">{{ activePlugin.skills.length }}</span>
                <span>Skills</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ activePlugin.agents.length }}</span>
                <span>Agents</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ activePlugin.commands.length }}</span>
                <span>Commands</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ activePlugin.hooks.length }}</span>
                <span>Hooks</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ activePlugin.hasMcp ? 1 : 0 }}</span>
                <span>MCP</span>
              </div>
            </div>

            <div v-if="resourceNames.length" class="resource-list">
              <h4>包含资源</h4>
              <div class="resource-chips">
                <code v-for="item in resourceNames" :key="item">{{ item }}</code>
              </div>
            </div>
          </template>
        </div>

        <div v-else class="detail-empty">
          <EmptyState icon="◇" title="选择插件" description="点击左侧插件查看包信息与资源" />
        </div>
      </div>

      <EmptyState
        v-else
        icon="⊘"
        title="暂无 OpenCode 全局插件"
        description="点击“安装插件”，输入 npm、GitHub 或本地模块地址"
      />
    </template>

    <Dialog
      v-model:open="showInstallDialog"
      title="安装 OpenCode 插件"
      description="使用 OpenCode 官方全局插件安装命令"
    >
      <div class="install-form">
        <label for="opencode-plugin-spec">插件模块</label>
        <input
          id="opencode-plugin-spec"
          v-model="installSpec"
          class="module-input"
          placeholder="例：github:owner/repo#v1.2.3 或 package-name@1.2.3"
          @keydown.enter="handleInstall"
        />
        <p>支持 npm 包、GitHub spec 和 file:// 本地插件；正式安装推荐固定不可变版本。</p>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showInstallDialog = false">取消</AppButton>
        <AppButton
          variant="primary"
          :disabled="mutating || !installSpec.trim()"
          @click="handleInstall"
        >
          {{ mutating ? '安装中…' : '安装' }}
        </AppButton>
      </template>
    </Dialog>

    <ConfirmDialog
      v-model:open="showUninstallDialog"
      title="卸载 OpenCode 插件"
      :message="activePlugin ? `将从 OpenCode 全局配置移除「${activePlugin.name}」。缓存包会保留，便于后续重新安装。` : ''"
      danger
      confirm-text="卸载"
      @confirm="handleUninstall"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { useOpenCodePluginStore } from '../../stores/opencodePlugin';
import type { OpenCodePlugin, OpenCodePluginDetail } from '../../api/opencodePlugin';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import { useToast } from '../../composables/useToast';
import { truncate } from '../../utils/format';

const store = useOpenCodePluginStore();
const {
  installed,
  warnings,
  activeSpec,
  activePlugin,
  loading,
  loadingDetail,
} = storeToRefs(store);
const { showSuccess, showError } = useToast();

const initialLoading = ref(true);
const initialError = ref('');
const mutating = ref(false);
const showInstallDialog = ref(false);
const showUninstallDialog = ref(false);
const installSpec = ref('');

const resourceNames = computed(() => {
  const plugin = activePlugin.value;
  if (!plugin || !isDetail(plugin)) return [];
  return [
    ...plugin.skills.map(item => `skill:${item.name}`),
    ...plugin.agents.map(item => `agent:${item.name}`),
    ...plugin.commands.map(item => `command:${item.name}`),
    ...plugin.hooks.map(item => `hook:${item.name}`),
    ...(plugin.hasMcp ? ['mcp'] : []),
  ].slice(0, 30);
});

function isDetail(plugin: OpenCodePlugin | OpenCodePluginDetail): plugin is OpenCodePluginDetail {
  return Array.isArray((plugin as OpenCodePluginDetail).skills);
}

function sourceLabel(source: string) {
  if (source === 'github') return 'GitHub';
  if (source === 'file') return '本地';
  return 'npm';
}

function formatDate(value: string) {
  return new Date(value).toLocaleString('zh-CN');
}

async function loadPlugins() {
  initialError.value = '';
  try {
    await store.refresh(true);
  } catch (error) {
    initialError.value = error instanceof Error ? error.message : String(error);
  } finally {
    initialLoading.value = false;
  }
}

async function select(spec: string) {
  try {
    await store.selectPlugin(spec);
  } catch (error) {
    showError(`读取插件详情失败：${error instanceof Error ? error.message : String(error)}`);
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
    await store.install(spec);
    showInstallDialog.value = false;
    showSuccess('OpenCode 插件安装成功');
  } catch (error) {
    showError(`安装失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleUpdate() {
  if (!activePlugin.value || mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.update(activePlugin.value.spec);
    showSuccess(result.output || 'OpenCode 插件已更新');
  } catch (error) {
    showError(`更新失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleUninstall() {
  if (!activePlugin.value || mutating.value) return;
  const spec = activePlugin.value.spec;
  mutating.value = true;
  try {
    await store.uninstall(spec);
    showSuccess('OpenCode 插件已卸载');
  } catch (error) {
    showError(`卸载失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

onMounted(loadPlugins);
</script>

<style scoped>
.opencode-plugin-panel {
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
.detail-actions,
.plugin-badges {
  display: flex;
  align-items: center;
  gap: 8px;
}

.panel-header,
.header-title,
.detail-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.header-title {
  justify-content: flex-start;
  gap: 8px;
}

.header-title h2,
.detail-header h3 {
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

.plugin-split {
  display: grid;
  grid-template-columns: minmax(280px, 36%) 1fr;
  min-height: 470px;
  border: 1px solid var(--separator);
  border-radius: 14px;
  overflow: hidden;
  background: var(--card);
}

.plugin-list {
  border-right: 1px solid var(--separator);
  overflow-y: auto;
  max-height: 620px;
}

.plugin-item {
  width: 100%;
  text-align: left;
  border: 0;
  border-bottom: 1px solid var(--separator);
  background: transparent;
  padding: 15px 16px;
  color: var(--label);
  cursor: pointer;
}

.plugin-item:hover,
.plugin-item.active {
  background: var(--control);
}

.plugin-item.active {
  box-shadow: inset 3px 0 var(--accent);
}

.plugin-name-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.plugin-name {
  font-size: 14px;
  font-weight: 600;
}

.plugin-badges {
  margin-top: 8px;
  flex-wrap: wrap;
}

.plugin-spec,
.plugin-description {
  margin: 8px 0 0;
  font-size: 11px;
  color: var(--tertiary);
  overflow-wrap: anywhere;
}

.plugin-description {
  color: var(--secondary);
  line-height: 1.5;
}

.plugin-detail {
  padding: 22px;
  overflow-y: auto;
}

.detail-header {
  align-items: flex-start;
  gap: 16px;
}

.detail-description {
  color: var(--secondary);
  line-height: 1.65;
  font-size: 13px;
}

.meta-list {
  margin-top: 20px;
  border-top: 1px solid var(--separator);
}

.meta-row {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 14px;
  padding: 10px 0;
  border-bottom: 1px solid var(--separator);
  font-size: 12px;
  color: var(--secondary);
}

.meta-row code,
.break-text {
  color: var(--label);
  overflow-wrap: anywhere;
}

.detail-loading {
  padding: 24px 0;
  color: var(--secondary);
  font-size: 12px;
}

.resource-summary {
  display: grid;
  grid-template-columns: repeat(5, minmax(70px, 1fr));
  gap: 8px;
  margin-top: 20px;
}

.resource-card {
  background: var(--control);
  border-radius: 10px;
  padding: 12px 8px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--secondary);
}

.resource-value {
  font-size: 18px;
  font-weight: 650;
  color: var(--label);
}

.resource-list {
  margin-top: 20px;
}

.resource-list h4 {
  margin: 0 0 10px;
}

.resource-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.resource-chips code {
  padding: 4px 7px;
  border-radius: 6px;
  background: var(--control);
  color: var(--secondary);
  font-size: 10px;
}

.detail-empty {
  display: flex;
  align-items: center;
  justify-content: center;
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
  .plugin-split {
    grid-template-columns: 1fr;
  }

  .plugin-list {
    border-right: 0;
    border-bottom: 1px solid var(--separator);
    max-height: 280px;
  }

  .resource-summary {
    grid-template-columns: repeat(3, 1fr);
  }
}
</style>
