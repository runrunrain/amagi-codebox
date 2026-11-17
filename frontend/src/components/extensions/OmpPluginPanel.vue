<template>
  <div class="omp-plugin-panel">
    <!-- 首屏门控为 store 派生（契约 §6.5：loading/loadError/installed 均在 store，SSR 可断言） -->
    <LoadingState v-if="showInitialLoading" message="加载 OMP 插件中..." />
    <ErrorState
      v-else-if="showFatalError"
      :message="store.loadError"
      :on-retry="handleRefresh"
    />

    <template v-else>
      <!-- ① 诊断横幅：warnings 恒走横幅；有数据后的 refresh 失败（loadError）并入 errors -->
      <DiagnosticBanner :errors="diagnosticErrors" :warnings="warnings" />

      <!-- ② 资源计数标签行：OMP 无市场/子项数据，仅「插件」单标签 + 本地搜索 -->
      <CountTabs
        v-model="activeTab"
        v-model:search-value="searchQuery"
        :tabs="tabs"
        search-placeholder="搜索插件名称或描述"
      />

      <!-- ③ 工具栏 -->
      <ListToolbar title="已安装" :count="filtered.length" :loading="loading">
        <template #actions>
          <AppButton variant="ghost" size="small" :disabled="loading" @click="handleRefresh">
            {{ loading ? '刷新中…' : '刷新' }}
          </AppButton>
          <AppButton variant="primary" size="small" @click="openInstallDialog">
            安装插件
          </AppButton>
        </template>
      </ListToolbar>

      <!-- ④ 内容区：list ⇄ detail（面板本地状态，契约 §2） -->
      <template v-if="view === 'list'">
        <div v-if="filtered.length" class="omp-rows">
          <PluginRow
            v-for="plugin in filtered"
            :key="plugin.id"
            :name="plugin.name"
            :description="plugin.description"
            :badges="rowBadges(plugin)"
            :enabled="plugin.enabled"
            switch-visible
            :switch-disabled="isRowMutating(plugin.id)"
            :active="activeId === plugin.id"
            @click="openDetail(plugin)"
            @toggle="(value) => handleRowToggle(plugin, value)"
          />
        </div>
        <EmptyStateCard
          v-else
          title="暂无 OMP 插件"
          description="点击『安装插件』，输入 npm 包名、GitHub 源、marketplace 引用或本地路径"
        >
          <template #primary>
            <AppButton variant="primary" size="small" @click="openInstallDialog">安装插件</AppButton>
          </template>
        </EmptyStateCard>
      </template>

      <!-- 详情态：居中窄单列二级详情页（← 返回列表 + 「OMP › 名称」面包屑）。
           OMP 无二级详情接口，点行即用列表数据渲染（契约 §6.5） -->
      <div v-else-if="activePlugin" class="detail-page">
        <div class="detail-column">
          <button type="button" class="detail-back" @click="closeDetail">← 返回列表</button>
          <DetailHeader
            :breadcrumb="[{ label: 'OMP', clickable: true }, { label: activePlugin.name }]"
            :title="activePlugin.name"
            :badges="detailBadges"
            @back="closeDetail"
          >
            <template #actions>
              <AppButton
                v-if="!activePlugin.enabled"
                variant="primary"
                size="small"
                :disabled="mutating"
                @click="handleEnable(activePlugin)"
              >
                {{ mutating ? '处理中…' : '启用' }}
              </AppButton>
              <AppButton
                v-else
                variant="primary"
                size="small"
                :disabled="mutating"
                @click="handleUpgrade(activePlugin)"
              >
                {{ mutating ? '处理中…' : '升级' }}
              </AppButton>
              <AppButton
                variant="danger"
                size="small"
                :disabled="mutating"
                @click="openUninstallDialog(activePlugin)"
              >
                卸载
              </AppButton>
            </template>
          </DetailHeader>
          <p v-if="activePlugin.description" class="omp-detail-desc">
            {{ activePlugin.description }}
          </p>
          <MetaGrid :items="metaItems" />
        </div>
      </div>
    </template>

    <!-- 安装对话框（契约 §6.5：保留） -->
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
import { computed, onMounted, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { useOmpPluginStore } from '../../stores/ompPlugin';
import type { OmpPlugin } from '../../api/ompPlugin';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import ListToolbar from './ui/ListToolbar.vue';
import MetaGrid from './ui/MetaGrid.vue';
import PluginRow from './ui/PluginRow.vue';
import type { CountTabItem, PluginBadgeDescriptor } from './pluginViewTypes';
import { useToast } from '../../composables/useToast';

const store = useOmpPluginStore();
const { installed, warnings, loading, loadError } = storeToRefs(store);
const { showSuccess, showError } = useToast();

// ---- 面板本地视图状态（契约 §2：OMP 仅 list ⇄ detail，不进 plugin store） ----
type PanelView = 'list' | 'detail';
const view = ref<PanelView>('list');
const activeId = ref<string | null>(null);
const activeTab = ref('plugins');
const searchQuery = ref('');

// ---- 首屏门控（store 派生，契约 §6.5 双形态） ----
const showInitialLoading = computed(
  () => loading.value && installed.value.length === 0 && !loadError.value,
);
const showFatalError = computed(() => !!loadError.value && installed.value.length === 0);
// 首次加载失败（空列表）由整页 ErrorState 承担；有数据后的刷新失败走诊断横幅
const diagnosticErrors = computed<string[]>(() =>
  installed.value.length > 0 && loadError.value ? [loadError.value] : [],
);

const tabs = computed<CountTabItem[]>(() => [
  { key: 'plugins', label: '插件', count: installed.value.length },
]);

// 搜索为面板本地过滤（契约 §7：filteredBySearch 不进 store），作用于名称与描述
const filtered = computed(() => {
  const keyword = searchQuery.value.trim().toLowerCase();
  if (!keyword) return installed.value;
  return installed.value.filter(
    (plugin) =>
      plugin.name.toLowerCase().includes(keyword) ||
      (plugin.description ?? '').toLowerCase().includes(keyword),
  );
});

function kindLabel(kind: OmpPlugin['kind']): string {
  return kind === 'marketplace' ? 'marketplace' : 'npm';
}

function rowBadges(plugin: OmpPlugin): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [
    { text: kindLabel(plugin.kind), type: 'source', variant: 'muted' },
  ];
  if (plugin.version) badges.push({ text: `v${plugin.version}`, type: 'ver' });
  return badges;
}

// ---- 详情态（列表数据渲染，无二级接口） ----
const activePlugin = computed(
  () => installed.value.find((plugin) => plugin.id === activeId.value) ?? null,
);

const detailBadges = computed<PluginBadgeDescriptor[]>(() => {
  const plugin = activePlugin.value;
  if (!plugin) return [];
  const badges: PluginBadgeDescriptor[] = [
    { text: kindLabel(plugin.kind), type: 'source', variant: 'muted' },
  ];
  if (plugin.version) badges.push({ text: `v${plugin.version}`, type: 'ver' });
  if (plugin.scope) badges.push({ text: plugin.scope, type: 'scope' });
  return badges;
});

interface MetaViewItem {
  label: string;
  value: string;
  mono?: boolean;
}

const metaItems = computed<MetaViewItem[]>(() => {
  const plugin = activePlugin.value;
  if (!plugin) return [];
  const items: MetaViewItem[] = [
    { label: '引用 ID', value: plugin.id, mono: true },
    { label: '类型', value: kindLabel(plugin.kind) },
  ];
  if (plugin.version) items.push({ label: '版本', value: plugin.version });
  if (plugin.scope) items.push({ label: '作用域', value: plugin.scope });
  if (plugin.installPath) items.push({ label: '安装路径', value: plugin.installPath });
  if (plugin.enabledFeatures?.length) {
    items.push({ label: '启用特性', value: plugin.enabledFeatures.join('、') });
  }
  return items;
});

function openDetail(plugin: OmpPlugin) {
  activeId.value = plugin.id;
  view.value = 'detail';
}

function closeDetail() {
  view.value = 'list';
  activeId.value = null;
}

// ---- 写操作 ----
const mutating = ref(false);
// 行内开关仅禁用当前行（契约 §10.8）；详情按钮保持全局禁用
const rowMutatingIds = ref(new Set<string>());

function isRowMutating(id: string): boolean {
  return rowMutatingIds.value.has(id);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function handleRefresh() {
  // refresh 内部 catch 写 loadError（整页 ErrorState / 诊断横幅），不向上抛
  await store.refresh(true);
}

async function handleRowToggle(plugin: OmpPlugin, value: boolean) {
  if (isRowMutating(plugin.id)) return;
  rowMutatingIds.value.add(plugin.id);
  try {
    const result = await store.setEnabled(plugin.id, value);
    showSuccess(result.output || (value ? 'OMP 插件已启用' : 'OMP 插件已禁用'));
  } catch (error) {
    showError(`${value ? '启用' : '禁用'}失败：${errorMessage(error)}`);
  } finally {
    rowMutatingIds.value.delete(plugin.id);
  }
}

async function handleEnable(plugin: OmpPlugin) {
  if (mutating.value) return;
  mutating.value = true;
  try {
    const result = await store.setEnabled(plugin.id, true);
    showSuccess(result.output || 'OMP 插件已启用');
  } catch (error) {
    showError(`启用失败：${errorMessage(error)}`);
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
    showError(`升级失败：${errorMessage(error)}`);
  } finally {
    mutating.value = false;
  }
}

// ---- 安装 / 卸载对话框 ----
const showInstallDialog = ref(false);
const showUninstallDialog = ref(false);
const installSpec = ref('');
const uninstallTarget = ref<OmpPlugin | null>(null);

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
    showError(`安装失败：${errorMessage(error)}`);
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
    closeDetail();
    showSuccess(result.output || 'OMP 插件已卸载');
  } catch (error) {
    showError(`卸载失败：${errorMessage(error)}`);
  } finally {
    mutating.value = false;
  }
}

onMounted(() => {
  void store.refresh(true);
});

// 契约 §8：面板交互断言走 expose 的 handler（node 环境不派发 DOM 事件）
defineExpose({
  view,
  activeId,
  searchQuery,
  showInstallDialog,
  showUninstallDialog,
  installSpec,
  handleRefresh,
  openInstallDialog,
  handleInstall,
  handleRowToggle,
  openDetail,
  closeDetail,
  handleEnable,
  handleUpgrade,
  openUninstallDialog,
  handleUninstall,
});
</script>

<style scoped>
/* 根间距与 PluginInstalledPanel/PluginMarketPanel 对齐（14px，跨面板一致） */
.omp-plugin-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.omp-rows {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: var(--radius-card);
  padding: 6px;
}

/* 描述完整多行显示（不截断、不加省略号）：ui/PluginRow 不可改，面板 scoped :deep 覆盖 */
.omp-rows :deep(.plugin-row) {
  align-items: flex-start;
}

.omp-rows :deep(.plugin-row-desc) {
  white-space: normal;
  overflow: visible;
  text-overflow: clip;
}

/* 详情页：居中窄单列（约 720px，左右留白） */
.detail-page {
  display: flex;
  flex-direction: column;
}

.detail-column {
  width: 100%;
  max-width: 720px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.detail-back {
  align-self: flex-start;
  border: none;
  background: none;
  padding: 0;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  color: var(--accent);
}

.detail-back:hover {
  text-decoration: underline;
}

.omp-detail-desc {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.6;
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

.install-form p {
  margin: 5px 0 0;
  font-size: 12px;
  color: var(--secondary);
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
</style>
