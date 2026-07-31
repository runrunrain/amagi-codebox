<template>
  <div class="pi-plugin-panel">
    <LoadingState v-if="initialLoading" message="加载 Pi 包中..." />
    <ErrorState
      v-else-if="initialError"
      :message="initialError"
      :on-retry="loadPackages"
    />

    <template v-else>
      <div v-if="warnings.length" class="status-banner warning">
        <span>{{ warnings.join('；') }}</span>
        <AppButton variant="ghost" size="small" @click="loadPackages">重试</AppButton>
      </div>

      <div class="scope-note">
        <div>
          <strong>Pi 全局包 / Pi Packages</strong>
          <p>通过官方 pi CLI 管理 settings.json 中登记的包，支持 npm、git 与本地路径源。/ Manage packages registered in settings.json via the official pi CLI.</p>
        </div>
        <div class="header-actions">
          <AppButton variant="ghost" size="small" :disabled="loading" @click="loadPackages">
            {{ loading ? '刷新中…' : '刷新' }}
          </AppButton>
          <AppButton variant="primary" size="small" @click="openInstallDialog">
            安装包
          </AppButton>
        </div>
      </div>

      <div class="panel-header">
        <div class="header-title">
          <h2>已安装</h2>
          <span class="count-badge">{{ installed.length }}</span>
        </div>
      </div>

      <div v-if="installed.length" class="package-split">
        <div class="package-list">
          <button
            v-for="pkg in installed"
            :key="pkg.source"
            :class="['package-item', { active: activeSource === pkg.source }]"
            @click="select(pkg.source)"
          >
            <div class="package-name-row">
              <span class="package-name">{{ pkg.name }}</span>
              <Badge type="scope" text="用户级" />
            </div>
            <div class="package-badges">
              <Badge type="source" :text="sourceLabel(pkg.sourceType)" variant="muted" />
              <Badge v-if="pkg.version" type="ver" :text="`v${pkg.version}`" />
              <Badge v-if="pkg.pinned" type="tag" text="pinned" variant="muted" />
            </div>
            <p class="package-source">{{ pkg.source }}</p>
            <p v-if="pkg.description" class="package-description">
              {{ truncate(pkg.description, 90) }}
            </p>
          </button>
        </div>

        <div v-if="activePackage" class="package-detail">
          <div class="detail-header">
            <div>
              <h3>{{ activePackage.name }}</h3>
              <div class="package-badges">
                <Badge type="source" :text="sourceLabel(activePackage.sourceType)" variant="muted" />
                <Badge v-if="activePackage.version" type="ver" :text="`v${activePackage.version}`" />
                <Badge v-if="activePackage.pinned" type="tag" text="pinned" variant="muted" />
              </div>
            </div>
            <div class="detail-actions">
              <AppButton
                variant="ghost"
                size="small"
                :disabled="mutating || activePackage.sourceType === 'local'"
                @click="handleUpdate"
              >
                {{ activePackage.sourceType === 'local' ? '本地直载' : (mutating ? '处理中…' : '更新') }}
              </AppButton>
              <AppButton variant="danger" size="small" :disabled="mutating" @click="showRemoveDialog = true">
                移除
              </AppButton>
            </div>
          </div>

          <p v-if="activePackage.description" class="detail-description">
            {{ activePackage.description }}
          </p>

          <div class="meta-list">
            <div class="meta-row">
              <span>包源 Source</span>
              <code>{{ activePackage.source }}</code>
            </div>
            <div class="meta-row">
              <span>安装路径</span>
              <code>{{ activePackage.installPath || '未找到实体目录' }}</code>
            </div>
            <div v-if="activePackage.author" class="meta-row">
              <span>作者</span>
              <span>{{ activePackage.author }}</span>
            </div>
            <div v-if="activePackage.repository" class="meta-row">
              <span>仓库</span>
              <span class="break-text">{{ activePackage.repository }}</span>
            </div>
            <div v-if="activePackage.lastUpdated" class="meta-row">
              <span>更新时间</span>
              <span>{{ formatDate(activePackage.lastUpdated) }}</span>
            </div>
            <div v-if="isDetail(activePackage)" class="meta-row">
              <span>Manifest</span>
              <span>{{ activePackage.manifestDeclared ? '已声明 Declared' : '未声明（按目录扫描）' }}</span>
            </div>
          </div>

          <div v-if="loadingDetail" class="detail-loading">正在读取包资源…</div>
          <template v-else-if="isDetail(activePackage)">
            <div class="resource-summary">
              <div class="resource-card">
                <span class="resource-value">{{ resourceCount('extension') }}</span>
                <span>Extensions</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ resourceCount('skill') }}</span>
                <span>Skills</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ resourceCount('prompt') }}</span>
                <span>Prompts</span>
              </div>
              <div class="resource-card">
                <span class="resource-value">{{ resourceCount('theme') }}</span>
                <span>Themes</span>
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
          <EmptyState icon="◇" title="选择包" description="点击左侧包查看元数据与 extensions/skills 资源" />
        </div>
      </div>

      <EmptyState
        v-else
        icon="⊘"
        title="暂无 Pi 全局包"
        description="点击“安装包”，输入 npm 包名、git 地址或本地路径"
      />
    </template>

    <Dialog
      v-model:open="showInstallDialog"
      title="安装 Pi 包"
      description="使用 pi 官方包安装命令（写入 settings.json 并拉取实体）"
    >
      <div class="install-form">
        <label for="pi-package-source">包源 Package source</label>
        <input
          id="pi-package-source"
          v-model="installSource"
          class="module-input"
          placeholder="例：npm:@earendil-works/pi-web-access、git:github.com/owner/repo 或 /path/to/pkg"
          @keydown.enter="handleInstall"
        />
        <p>npm 包需加 npm: 前缀（如 npm:@earendil-works/pi-web-access）；git 源用 git:&lt;url&gt;；本地源直接给绝对路径。/ npm packages need the npm: prefix (e.g. npm:@earendil-works/pi-web-access); git sources use git:&lt;url&gt;; local sources are absolute paths.</p>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showInstallDialog = false">取消</AppButton>
        <AppButton
          variant="primary"
          :disabled="mutating || !installSource.trim()"
          @click="handleInstall"
        >
          {{ mutating ? '安装中…' : '安装' }}
        </AppButton>
      </template>
    </Dialog>

    <ConfirmDialog
      v-model:open="showRemoveDialog"
      title="移除 Pi 包"
      :message="activePackage ? `将从 Pi settings.json 移除「${activePackage.name}」。实体目录会保留，便于后续重新安装。` : ''"
      danger
      confirm-text="移除"
      @confirm="handleRemove"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { usePiPluginStore } from '../../stores/piPlugin';
import type { PiPackage, PiPackageDetail } from '../../api/piPlugin';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import { useToast } from '../../composables/useToast';
import { truncate } from '../../utils/format';

const store = usePiPluginStore();
const {
  installed,
  warnings,
  activeSource,
  activePackage,
  loading,
  loadingDetail,
} = storeToRefs(store);
const { showSuccess, showError } = useToast();

const initialLoading = ref(true);
const initialError = ref('');
const mutating = ref(false);
const showInstallDialog = ref(false);
const showRemoveDialog = ref(false);
const installSource = ref('');

const resourceNames = computed(() => {
  const pkg = activePackage.value;
  if (!pkg || !isDetail(pkg)) return [];
  return pkg.resources
    .map(item => `${item.type}:${item.name}`)
    .slice(0, 30);
});

function isDetail(pkg: PiPackage | PiPackageDetail): pkg is PiPackageDetail {
  return Array.isArray((pkg as PiPackageDetail).resources);
}

function resourceCount(type: PiPackageDetail['resources'][number]['type']) {
  const pkg = activePackage.value;
  if (!pkg || !isDetail(pkg)) return 0;
  return pkg.resources.filter(item => item.type === type).length;
}

function sourceLabel(sourceType: string) {
  if (sourceType === 'git') return 'Git';
  if (sourceType === 'local') return '本地';
  return 'npm';
}

function formatDate(value: string) {
  return new Date(value).toLocaleString('zh-CN');
}

async function loadPackages() {
  initialError.value = '';
  try {
    await store.refresh(true);
  } catch (error) {
    initialError.value = error instanceof Error ? error.message : String(error);
  } finally {
    initialLoading.value = false;
  }
}

async function select(source: string) {
  try {
    await store.selectPackage(source);
  } catch (error) {
    showError(`读取包详情失败：${error instanceof Error ? error.message : String(error)}`);
  }
}

function openInstallDialog() {
  installSource.value = '';
  showInstallDialog.value = true;
}

async function handleInstall() {
  const source = installSource.value.trim();
  if (!source || mutating.value) return;
  mutating.value = true;
  try {
    await store.install(source);
    showInstallDialog.value = false;
    showSuccess('Pi 包安装成功');
  } catch (error) {
    showError(`安装失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleUpdate() {
  if (!activePackage.value || mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.update(activePackage.value.source);
    showSuccess(result.output || 'Pi 包已更新');
  } catch (error) {
    showError(`更新失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleRemove() {
  if (!activePackage.value || mutating.value) return;
  const source = activePackage.value.source;
  mutating.value = true;
  try {
    await store.remove(source);
    showSuccess('Pi 包已移除');
  } catch (error) {
    showError(`移除失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

onMounted(loadPackages);
</script>

<style scoped>
.pi-plugin-panel {
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
.package-badges {
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

.package-split {
  display: grid;
  grid-template-columns: minmax(280px, 36%) 1fr;
  min-height: 470px;
  border: 1px solid var(--separator);
  border-radius: 14px;
  overflow: hidden;
  background: var(--card);
}

.package-list {
  border-right: 1px solid var(--separator);
  overflow-y: auto;
  max-height: 620px;
}

.package-item {
  width: 100%;
  text-align: left;
  border: 0;
  border-bottom: 1px solid var(--separator);
  background: transparent;
  padding: 15px 16px;
  color: var(--label);
  cursor: pointer;
}

.package-item:hover,
.package-item.active {
  background: var(--control);
}

.package-item.active {
  box-shadow: inset 3px 0 var(--accent);
}

.package-name-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.package-name {
  font-size: 14px;
  font-weight: 600;
}

.package-badges {
  margin-top: 8px;
  flex-wrap: wrap;
}

.package-source,
.package-description {
  margin: 8px 0 0;
  font-size: 11px;
  color: var(--tertiary);
  overflow-wrap: anywhere;
}

.package-description {
  color: var(--secondary);
  line-height: 1.5;
}

.package-detail {
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
  grid-template-columns: repeat(4, minmax(70px, 1fr));
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
  .package-split {
    grid-template-columns: 1fr;
  }

  .package-list {
    border-right: 0;
    border-bottom: 1px solid var(--separator);
    max-height: 280px;
  }

  .resource-summary {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
