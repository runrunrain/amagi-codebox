<!-- OpenCode 插件管理面板（plugin-ui-redesign 契约 §6.3 / R2 重写）。
     四段式：DiagnosticBanner → CountTabs（插件 N + 搜索）→ ListToolbar（已安装 N +
     刷新 + 安装插件）→ 内容区（列表态 ⇄ 详情态，面板本地 ref；不分组、无开关——
     OpenCode 无启停 API）。门控为 store 派生（loading/installed/loadError），
     首屏 Loading/Error 与「有数据后刷新失败」横幅双形态由此自然区分。 -->
<template>
  <div class="opencode-plugin-panel">
    <LoadingState v-if="showInitialLoading" message="加载 OpenCode 插件中..." />
    <ErrorState v-else-if="showFatalError" :message="loadError" :on-retry="handleRefresh" />

    <template v-else>
      <DiagnosticBanner :errors="diagnosticErrors" :warnings="warnings" />

      <CountTabs
        v-model="activeTab"
        v-model:search-value="searchQuery"
        :tabs="countTabs"
        search-placeholder="搜索插件名称或描述"
      />

      <ListToolbar title="已安装" :count="filteredPlugins.length" :loading="loading">
        <template #actions>
          <AppButton variant="ghost" size="small" :disabled="loading" @click="handleRefresh">
            {{ loading ? '刷新中…' : '刷新' }}
          </AppButton>
          <AppButton variant="primary" size="small" @click="openInstallDialog">
            安装插件
          </AppButton>
        </template>
      </ListToolbar>

      <!-- 列表态 -->
      <div v-if="view === 'list'" class="list-pane">
        <div v-if="filteredPlugins.length" class="plugin-list">
          <PluginRow
            v-for="plugin in filteredPlugins"
            :key="plugin.spec"
            :name="plugin.name"
            :description="plugin.description"
            :badges="buildBadges(plugin)"
            :active="plugin.spec === activeSpec"
            @click="openDetail(plugin.spec)"
          />
        </div>

        <EmptyStateCard
          v-else
          title="暂无 OpenCode 全局插件"
          description="点击『安装插件』，输入 npm、GitHub 或本地模块地址"
        >
          <template #primary>
            <AppButton variant="primary" size="small" @click="openInstallDialog">
              安装插件
            </AppButton>
          </template>
        </EmptyStateCard>
      </div>

      <!-- 详情态：居中窄单列二级详情页（← 返回列表 + 引擎名面包屑 + DetailHeader +
           描述 + 资源汇总徽章行 + 只读子项 + MetaGrid），替换内容区 -->
      <div v-else-if="activePlugin" class="detail-page">
        <div class="detail-column">
          <button type="button" class="detail-back" @click="backToList">← 返回列表</button>
          <DetailHeader
            :breadcrumb="detailBreadcrumb"
            :title="activePlugin.name"
            :badges="detailBadges"
            @back="backToList"
          >
            <template #actions>
              <AppButton
                variant="primary"
                size="small"
                :disabled="mutating || activePlugin.source === 'file'"
                @click="handleUpdate"
              >
                {{ activePlugin.source === 'file' ? '本地直载' : (mutating ? '处理中…' : '检查更新') }}
              </AppButton>
              <AppButton
                variant="danger"
                size="small"
                :disabled="mutating"
                @click="showUninstallDialog = true"
              >
                卸载
              </AppButton>
            </template>
          </DetailHeader>

          <p v-if="activePlugin.description" class="detail-description">
            {{ activePlugin.description }}
          </p>

          <div v-if="loadingDetail" class="detail-loading">正在读取插件资源…</div>
          <template v-else-if="pluginDetail">
            <div v-if="resourceSummaryBadges.length" class="resource-summary">
              <!-- type='tag' 为白字无底色（浅色背景上不可见），计数汇总改用 scope（--control 底 + --secondary 字） -->
              <Badge
                v-for="item in resourceSummaryBadges"
                :key="item"
                type="scope"
                :text="item"
                variant="muted"
              />
            </div>

            <div v-if="subItems.length" class="sub-item-list">
              <SubItemRow
                v-for="item in subItems"
                :key="`${item.typeBadge}:${item.name}`"
                :name="item.name"
                :type-badge="item.typeBadge"
              />
            </div>
          </template>

          <MetaGrid :items="metaItems" />
        </div>
      </div>
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
        <p>
          支持 npm 包、GitHub spec 和 file:// 本地插件；正式安装推荐固定不可变版本。
          更新时会发现最新稳定版本、切换到不可变 tag 或精确 npm 版本，再通过官方 CLI 安装并校验。
        </p>
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
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import ListToolbar from './ui/ListToolbar.vue';
import MetaGrid from './ui/MetaGrid.vue';
import PluginRow from './ui/PluginRow.vue';
import SubItemRow from './ui/SubItemRow.vue';
import type {
  BreadcrumbSegment,
  CountTabItem,
  PluginBadgeDescriptor,
} from './pluginViewTypes';
import { useToast } from '../../composables/useToast';

/** MetaGrid items 的结构镜像（MetaGrid.vue 未导出 Props 接口） */
interface MetaItem {
  label: string;
  value: string;
  mono?: boolean;
}

/** 详情子项行视图（§5.3 OpenCode：skills/agents/commands/hooks + hasMcp 汇总行，只读） */
interface OpenCodeSubItemView {
  typeBadge: string;
  name: string;
}

const store = useOpenCodePluginStore();
const {
  installed,
  warnings,
  activeSpec,
  activePlugin,
  loading,
  loadingDetail,
  loadError,
} = storeToRefs(store);
const { showSuccess, showError } = useToast();

// —— 面板本地状态（契约 §2：OpenCode 仅 list ⇄ detail，本地 ref）——
const view = ref<'list' | 'detail'>('list');
const activeTab = ref('plugins');
const searchQuery = ref('');
const mutating = ref(false);
const showInstallDialog = ref(false);
const showUninstallDialog = ref(false);
const installSpec = ref('');

// —— 首屏门控：store 派生（契约 §6.3，SSR 测试可经 store 状态种子驱动分支）——
const showInitialLoading = computed(() =>
  loading.value && installed.value.length === 0 && !loadError.value);
const showFatalError = computed(() => !!loadError.value && installed.value.length === 0);
// 有数据后的刷新失败 → 并入诊断横幅 errors（整页 ErrorState 只在列表为空时出现）
const diagnosticErrors = computed(() =>
  loadError.value && installed.value.length > 0 ? [loadError.value] : []);

// —— 资源计数标签（§3.1 OpenCode：无市场概念，仅单标签）——
const countTabs = computed<CountTabItem[]>(() => [
  { key: 'plugins', label: '插件', count: installed.value.length },
]);

// —— 搜索：作用于当前标签，本地过滤，不发后端请求 ——
const filteredPlugins = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  if (!query) return installed.value;
  return installed.value.filter(plugin =>
    plugin.name.toLowerCase().includes(query)
    || (plugin.description ?? '').toLowerCase().includes(query));
});

// —— 详情（列表数据兜底；资源汇总/子项仅在详情形态可用）——
function isDetail(plugin: OpenCodePlugin | OpenCodePluginDetail): plugin is OpenCodePluginDetail {
  return Array.isArray((plugin as OpenCodePluginDetail).skills);
}

const pluginDetail = computed(() => {
  const plugin = activePlugin.value;
  return plugin && isDetail(plugin) ? plugin : null;
});

function sourceLabel(source: string) {
  if (source === 'github') return 'GitHub';
  if (source === 'file') return '本地';
  return 'npm';
}

function formatDate(value: string) {
  return new Date(value).toLocaleString('zh-CN');
}

/** 行/详情徽章（§5.1/§5.2 OpenCode：来源 + v{version} + targets，无开关） */
function buildBadges(plugin: OpenCodePlugin): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [
    { text: sourceLabel(plugin.source), type: 'source', variant: 'muted' },
  ];
  if (plugin.version) {
    badges.push({ text: `v${plugin.version}`, type: 'ver' });
  }
  for (const target of plugin.targets || []) {
    badges.push({ text: target, type: 'type', color: target === 'tui' ? 'command' : 'plugin' });
  }
  return badges;
}

const detailBadges = computed(() =>
  activePlugin.value ? buildBadges(activePlugin.value) : []);

const detailBreadcrumb = computed<BreadcrumbSegment[]>(() => [
  { label: 'OpenCode', clickable: true },
  { label: activePlugin.value?.name ?? '' },
]);

// —— 资源汇总徽章行（详情形态：真实计数；hasMcp=false 不显示假零徽章）——
const resourceSummaryBadges = computed(() => {
  const detail = pluginDetail.value;
  if (!detail) return [];
  const items = [
    `技能 ${detail.skills.length}`,
    `代理 ${detail.agents.length}`,
    `命令 ${detail.commands.length}`,
    `钩子 ${detail.hooks.length}`,
  ];
  if (detail.hasMcp) {
    items.push('含 MCP');
  }
  return items;
});

// —— 只读子项列表（无启停 API，SubItemRow 不传 toggleable）——
const subItems = computed<OpenCodeSubItemView[]>(() => {
  const detail = pluginDetail.value;
  if (!detail) return [];
  return [
    ...detail.skills.map(item => ({ typeBadge: '技能', name: item.name })),
    ...detail.agents.map(item => ({ typeBadge: '代理', name: item.name })),
    ...detail.commands.map(item => ({ typeBadge: '命令', name: item.name })),
    ...detail.hooks.map(item => ({ typeBadge: '钩子', name: item.name })),
    ...(detail.hasMcp ? [{ typeBadge: 'mcp', name: 'MCP 服务器' }] : []),
  ];
});

// —— MetaGrid（§5.2 OpenCode；空 value 项过滤，installedAt 无字段不显示）——
const metaItems = computed<MetaItem[]>(() => {
  const plugin = activePlugin.value;
  if (!plugin) return [];
  const items: MetaItem[] = [
    { label: '模块 Spec', value: plugin.spec, mono: true },
    { label: '来源', value: sourceLabel(plugin.source) },
    { label: '作用域', value: plugin.scope },
    { label: '安装路径', value: plugin.installPath ?? '', mono: true },
    { label: '开发者', value: plugin.author ?? '' },
    { label: '仓库', value: plugin.repository ?? '' },
    { label: '缓存更新时间', value: plugin.lastUpdated ? formatDate(plugin.lastUpdated) : '' },
  ];
  return items.filter(item => item.value);
});

async function handleRefresh() {
  try {
    await store.refresh(true);
  } catch {
    // 失败已由 store.loadError 记录（诊断横幅/整页错误数据源）
  }
}

async function openDetail(spec: string) {
  view.value = 'detail';
  try {
    await store.selectPlugin(spec);
  } catch (error) {
    showError(`读取插件详情失败：${error instanceof Error ? error.message : String(error)}`);
  }
}

function backToList() {
  view.value = 'list';
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
    view.value = 'list';
  } catch (error) {
    showError(`卸载失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

onMounted(() => {
  // 失败已由 store.loadError 记录，此处捕获防未处理拒绝
  store.refresh(true).catch(() => {});
});

// 可测试性契约（§8：行为断言一律走面板 expose 的 handler / ref）：
// openInstallDialog + showInstallDialog/installSpec ref 供 Dialog teleports 与安装流断言。
defineExpose({
  handleRefresh,
  openDetail,
  backToList,
  openInstallDialog,
  handleInstall,
  handleUpdate,
  handleUninstall,
  showInstallDialog,
  installSpec,
});
</script>

<style scoped>
/* 根间距与 PluginInstalledPanel/PluginMarketPanel 对齐（14px，跨面板一致） */
.opencode-plugin-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.plugin-list {
  border: 1px solid var(--separator);
  border-radius: var(--radius-card);
  background: var(--card);
  overflow: hidden;
}

.plugin-list :deep(.plugin-row) + :deep(.plugin-row) {
  border-top: 1px solid var(--separator);
}

/* 描述完整多行显示（不截断、不加省略号）：ui/PluginRow 不可改，面板 scoped :deep 覆盖 */
.plugin-list :deep(.plugin-row) {
  align-items: flex-start;
}

.plugin-list :deep(.plugin-row-desc) {
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
  gap: 18px;
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

.detail-description {
  margin: 0;
  color: var(--secondary);
  font-size: 13px;
  line-height: 1.65;
}

.resource-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.sub-item-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--separator);
  border-radius: var(--radius-card);
  background: var(--card);
  overflow: hidden;
}

.sub-item-list :deep(.sub-item-row) + :deep(.sub-item-row) {
  border-top: 1px solid var(--separator);
}

.detail-loading {
  color: var(--secondary);
  font-size: 12px;
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
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.6;
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
