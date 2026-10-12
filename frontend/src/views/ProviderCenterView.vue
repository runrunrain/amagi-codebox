<template>
  <!--
    Provider Center - 多级导航容器（对照 demo + 交接说明 §5.3）。
    一级 Segmented(pill): 「服务提供商」|「预设」|「CLI 独立配置」
    服务提供商 tab：网格 + 详情模式切换
    预设 tab / CLI 独立配置 tab（provider-center-layout-redesign）：
    统一左栏控制格式、CLI 模式与分区，右侧单显已选分区。
    CLI 独立配置组件全部 defineAsyncComponent 懒加载，退出入口 chunk。
  -->
  <section class="view-provider" :class="{ 'pc-workspace-view': isWorkspace }">
    <PageHead title="Provider Center" description="管理服务提供商、公共预设与各 CLI 独立配置" />

    <!--
      RC4-2：远程模式下 providers tab 消费远端数据（legacy）；顶部轻提示过渡语义。
      预设 / CLI 独立配置仍为本机内容（mode=local 横幅）。
    -->
    <template v-if="rcStore.isRemoteMode">
      <StatusBanner
        v-if="mainTab === 'providers'"
        type="warning"
        :message="`远程配置管理经 legacy 接口提供，需在主机设置中配置访问令牌（当前主机：${rcStore.currentHostName}）`"
        :action-text="rcStore.currentHasLegacyToken ? '' : '配置令牌'"
        @action="tokenDialogOpen = true"
      />
      <RemoteScopeBanner v-else subject="预设与 CLI 独立配置" mode="local" />
    </template>

    <!-- 详情模式：仅本机模式（远程详情由 RemoteProviderPanel 自管） -->
    <PiLoginProviderDetail
      v-if="!rcStore.isRemoteMode && store.activeLoginProvider"
      :provider="store.activeLoginProvider"
      @back="store.closeProvider"
    />
    <ProviderDetailView
      v-else-if="!rcStore.isRemoteMode && store.activeProviderId && store.activeProvider"
      @back="store.closeProvider"
      @saved="store.loadProviders"
    />

    <!-- 列表模式 -->
    <ConfigCard v-else class="pc-card" :class="{ 'pc-card-workspace': isWorkspace }">
      <!-- 一级 pill 导航 + center 级导出/导入（作用于整个 config，两个 tab 下均可见）-->
      <div class="pc-head">
        <Segmented
          v-model="mainTab"
          :options="MAIN_TABS"
          variant="pill"
          class="pc-main-tabs"
        />
        <div class="pc-head-actions">
          <!-- 本机模式专属操作：导出/导入/配置档均作用于本机配置，远程模式下隐藏 -->
          <template v-if="!rcStore.isRemoteMode">
            <AgentProfileQuickSwitch @applied="onProfileApplied" />
            <AppButton variant="ghost" size="small" :disabled="transferring" @click="requestExport">
              {{ transferAction === 'export' ? '导出中...' : '导出完整配置' }}
            </AppButton>
            <AppButton variant="ghost" size="small" :disabled="transferring" @click="requestImport">
              {{ transferAction === 'import' ? '导入中...' : '导入完整配置' }}
            </AppButton>
          </template>
        </div>
      </div>

      <!-- 服务提供商区：远程模式 → 远端数据源（RC4）；本机 → 本地网格 -->
      <div v-if="mainTab === 'providers'" class="pc-panel pc-panel-providers">
        <RemoteProviderPanel
          v-if="rcStore.isRemoteMode"
          @configure-token="tokenDialogOpen = true"
        />
        <ProviderGrid
          v-else
          @add="handleAdd"
        />
      </div>

      <div v-else class="pc-workspace">
        <SectionNav :groups="navGroups" :current-id="currentNavId" :branch-id="activePageId"
          controls-id="pc-editor-detail" :label="mainTab === 'presets' ? '预设格式' : 'CLI 配置导航'"
          :notice="navigationNotice" @select="selectNavigation" />
        <div id="pc-editor-detail" class="pc-detail-content" :key="activePageId">
          <template v-if="mainTab === 'presets'">
            <p class="pc-format-caption">{{ formatConsumers }}</p>
            <PresetList v-if="store.presetEngine === 'anthropic'" format="anthropic" />
            <PresetList v-else-if="store.presetEngine === 'openai'" format="openai" />
          </template>
          <template v-else-if="store.presetEngine === 'pi'">
            <PiAmagiConfig v-if="piMode === 'agents'" :key="presetLoadKey" v-bind="editorNavigation" @navigation-change="onNavigationChange" />
            <ProviderRegistryEditor v-else-if="piMode === 'registry'" :key="presetLoadKey" engine="pi" v-bind="editorNavigation" @navigation-change="onNavigationChange" />
            <AuthConfigEditor v-else :key="presetLoadKey" v-bind="editorNavigation" @navigation-change="onNavigationChange" />
          </template>
          <template v-else-if="store.presetEngine === 'omp'">
            <OmpGlobalConfig v-if="ompMode === 'agents'" :key="presetLoadKey" v-bind="editorNavigation" @navigation-change="onNavigationChange" />
            <ProviderRegistryEditor v-else :key="presetLoadKey" engine="omp" v-bind="editorNavigation" @navigation-change="onNavigationChange" />
          </template>
          <template v-else-if="store.presetEngine === 'opencode'">
            <OpenCodePresets v-if="openCodeMode === 'presets'" :key="presetLoadKey" />
            <OpenCodeGlobalConfig v-else :key="presetLoadKey" v-bind="editorNavigation" @navigation-change="onNavigationChange" />
          </template>
        </div>
      </div>
    </ConfigCard>

    <ConfirmDialog
      v-model:open="showExportConfirm"
      title="导出完整配置"
      message="完整配置包含 API Key、环境变量及其他敏感设置，并将以明文写入导出文件。请妥善保管导出文件。"
      confirm-text="选择保存位置"
      cancel-text="取消"
      @confirm="handleExport"
    />

    <ConfirmDialog
      v-model:open="showImportConfirm"
      :danger="true"
      title="导入完整配置"
      message="导入完整配置会替换当前设备上的服务商、预设、密钥及应用设置。导入成功后需要重启应用。"
      confirm-text="选择配置文件"
      cancel-text="取消"
      @confirm="handleImport"
    />

    <!-- RC4-2：legacy 访问令牌配置（远程模式） -->
    <LegacyTokenDialog
      v-model:open="tokenDialogOpen"
      :host-name="rcStore.currentHostName"
      :has-token="rcStore.currentHasLegacyToken"
      @changed="onTokenChanged"
    />
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, defineAsyncComponent, defineComponent, h, type Component } from 'vue';
import PageHead from '../components/ui/PageHead.vue';
import SectionNav from '../components/ui/SectionNav.vue';
import { CONFIG_PAGES, FORMAT_ITEMS, staticSections, pageSectionId, type ConfigPageId, type EditorNavigationSnapshot, type SectionNavGroup } from '../components/provider/configNavigation';
import RemoteScopeBanner from '../components/remote/RemoteScopeBanner.vue';
import StatusBanner from '../components/ui/StatusBanner.vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import Segmented from '../components/ui/Segmented.vue';
import AppButton from '../components/ui/AppButton.vue';
import ConfirmDialog from '../components/ui/ConfirmDialog.vue';
import LoadingState from '../components/ui/LoadingState.vue';
import ErrorState from '../components/ui/ErrorState.vue';
import ProviderGrid from '../components/provider/ProviderGrid.vue';
import PresetList from '../components/provider/PresetList.vue';
import AgentProfileQuickSwitch from '../components/provider/AgentProfileQuickSwitch.vue';
import ProviderDetailView from './ProviderDetailView.vue';
import PiLoginProviderDetail from '../components/provider/PiLoginProviderDetail.vue';
import RemoteProviderPanel from '../components/remote/RemoteProviderPanel.vue';
import LegacyTokenDialog from '../components/remote/LegacyTokenDialog.vue';
import { useProviderStore, type PresetEngine } from '../stores/provider';
import { useRemoteClientStore } from '../stores/remoteClient';
import { ExportConfigToFile, ImportConfigFromFile } from '../../wailsjs/go/main/App';
import { useToast } from '../composables/useToast';

const store = useProviderStore();
const rcStore = useRemoteClientStore();
const { showSuccess, showError, showInfo } = useToast();

const MAIN_TABS = [
  { value: 'providers', label: '服务提供商' },
  { value: 'presets', label: '预设' },
  { value: 'cliconfigs', label: 'CLI 独立配置' },
];

const mainTab = ref<'providers' | 'presets' | 'cliconfigs'>('providers');
const openCodeMode = ref<'presets' | 'global'>('global');
const piMode = ref<'agents' | 'registry' | 'auth'>('agents');
const ompMode = ref<'agents' | 'registry'>('agents');
const showExportConfirm = ref(false);
const showImportConfirm = ref(false);
const transferAction = ref<'export' | 'import' | null>(null);
const transferring = computed(() => transferAction.value !== null);
// RC4-2：legacy 令牌配置弹窗（远程模式）
const tokenDialogOpen = ref(false);

// 进入远程模式时关闭本地详情（本地详情数据源不适用于远端）
watch(
  () => rcStore.isRemoteMode,
  (remote) => {
    if (remote) store.closeProvider();
  },
  { immediate: true },
);

/** 令牌配置变化后：providers/settings 面板随 hasLegacyToken 变化重拉。 */
function onTokenChanged() {
  if (!rcStore.isRemoteMode) return;
  if (mainTab.value === 'providers') {
    void rcStore.loadRemoteProviders();
    // 详情打开时同步重拉（否则详情停留在 401/needs-token 态）
    if (rcStore.remoteProviderName) void rcStore.openRemoteProvider(rcStore.remoteProviderName);
  }
}

// 二级 engine 双向绑定（写入 store + 触发按需加载）。
const FORMAT_ENGINES: readonly PresetEngine[] = ['anthropic', 'openai'];
const CLI_ENGINES: readonly PresetEngine[] = ['pi', 'omp', 'opencode'];

const formatModel = computed<string>({
  get: () => (FORMAT_ENGINES.includes(store.presetEngine) ? store.presetEngine : 'anthropic'),
  set: (v: string) => {
    if (v) store.setPresetEngine(v as PresetEngine);
  },
});

const cliModel = computed<string>({
  get: () => (CLI_ENGINES.includes(store.presetEngine) ? store.presetEngine : 'pi'),
  set: (v: string) => {
    if (v) store.setPresetEngine(v as PresetEngine);
  },
});

// 预设页 subnav 右侧说明：对谁有用（Claude Code 消费 Anthropic 格式；Codex / Pi / OMP 消费 OpenAI 格式）
const formatConsumers = computed(() =>
  store.presetEngine === 'openai' ? '用于 Codex · Pi · OMP' : '用于 Claude Code',
);

// CLI 独立配置组件懒加载（AppShell.vue:37-43 模式）：合计约 4,100 行且非默认视图，
// 异步加载使其退出入口 chunk；delay 避免快速切换时 loading 闪烁。
// 更换 presetLoadKey 会重新挂载异步组件并重新执行 loader（动态 import 可重试）。
const presetLoadKey = ref(0);
const isWorkspace = computed(() => mainTab.value !== 'providers' && !store.activeLoginProvider && !store.activeProviderId);
const activePageId = computed<ConfigPageId | 'format:anthropic' | 'format:openai'>(() => {
  if (mainTab.value === 'presets') return `format:${formatModel.value}` as 'format:anthropic' | 'format:openai';
  if (cliModel.value === 'omp') return `omp:${ompMode.value}`;
  if (cliModel.value === 'opencode') return `opencode:${openCodeMode.value}`;
  return `pi:${piMode.value}`;
});
const sectionMemory = ref<Record<string, string>>({});
const navSnapshot = ref<EditorNavigationSnapshot | null>(null);
const navOwner = ref('');
let mountSequence = 0;
watch([activePageId, presetLoadKey], () => {
  navOwner.value = `${activePageId.value}:${presetLoadKey.value}:${++mountSequence}`;
  navSnapshot.value = null;
}, { immediate: true, flush: 'sync' });
const desiredSection = computed(() => sectionMemory.value[activePageId.value] ?? CONFIG_PAGES.find(p => p.id === activePageId.value)?.defaultId ?? '');
const editorNavigation = computed(() => ({ navigation: 'external' as const, sectionId: desiredSection.value, navOwner: navOwner.value }));
function onNavigationChange(snapshot: EditorNavigationSnapshot) {
  if (snapshot.owner !== navOwner.value) return;
  navSnapshot.value = snapshot;
  if (snapshot.phase === 'ready' && snapshot.mode === 'visual' && snapshot.activeId !== desiredSection.value) {
    sectionMemory.value[activePageId.value] = snapshot.activeId;
  }
}
const navGroups = computed<SectionNavGroup[]>(() => {
  if (mainTab.value === 'presets') return [{ id: 'formats', label: '格式', items: FORMAT_ITEMS }];
  return ['Pi', 'OMP', 'OpenCode'].map(cli => ({ id: cli, label: cli, items: CONFIG_PAGES.filter(p => p.cli === cli).map(p => {
    const active = p.id === activePageId.value;
    const snapshot = active ? navSnapshot.value : null;
    const sections = active ? (snapshot?.items ?? staticSections(p.id)) : [];
    return { id: p.id, label: p.label, ariaLabel: `${cli} · ${p.label}`, scrollChildren: p.dynamic,
      children: sections.map(s => ({ ...s, id: pageSectionId(p.id, s.id), ariaLabel: `${cli} · ${p.label} · ${s.label}`,
        disabled: snapshot?.mode === 'source' || snapshot?.phase === 'error' || (!!p.dynamic && snapshot?.phase !== 'ready') })) };
  }) }));
});
const navigationTargets = computed(() => new Map(navGroups.value.flatMap(g => g.items.flatMap(p => (p.children || []).map(s => [s.id, { page: p.id, section: (navSnapshot.value?.items ?? staticSections(p.id as ConfigPageId)).find(i => pageSectionId(p.id, i.id) === s.id)?.id || '' }] as const)))));
const currentNavId = computed(() => {
  if (mainTab.value === 'presets') return activePageId.value;
  if (navSnapshot.value && (navSnapshot.value.mode === 'source' || navSnapshot.value.phase !== 'ready')) return activePageId.value;
  const id = pageSectionId(activePageId.value, desiredSection.value);
  return navigationTargets.value.has(id) ? id : activePageId.value;
});
const navigationNotice = computed(() => {
  if (mainTab.value !== 'cliconfigs') return '';
  if (navSnapshot.value?.mode === 'source') return '源码模式：返回可视化后切换分区';
  if (navSnapshot.value?.phase === 'error') return '分区不可用';
  if (CONFIG_PAGES.find(p => p.id === activePageId.value)?.dynamic && navSnapshot.value?.phase !== 'ready') return '加载分区中…';
  return '';
});
function selectNavigation(id: string) {
  const target = navigationTargets.value.get(id);
  if (target) { sectionMemory.value[target.page] = target.section; return; }
  if (id.startsWith('format:')) { if (id !== activePageId.value) formatModel.value = id.slice(7); return; }
  const page = CONFIG_PAGES.find(p => p.id === id);
  if (!page || id === activePageId.value) return;
  // One synchronous callback; section navigation never touches this engine setter.
  const [engine, mode] = page.id.split(':');
  if (engine === 'pi') piMode.value = mode as typeof piMode.value;
  else if (engine === 'omp') ompMode.value = mode as typeof ompMode.value;
  else openCodeMode.value = mode as typeof openCodeMode.value;
  if (engine !== cliModel.value) cliModel.value = engine;
}

const PresetLoadError = defineComponent({
  name: 'PresetLoadError',
  setup() {
    const owner = navOwner.value;
    onMounted(() => {
      if (owner !== navOwner.value) return;
      navSnapshot.value = { owner, phase: 'error', mode: 'visual', activeId: desiredSection.value, items: staticSections(activePageId.value as ConfigPageId) };
    });
    return () =>
      h(ErrorState, {
        title: '配置页加载失败',
        message: '配置界面资源加载失败，请重试。',
        onRetry: () => {
          // Chromium 对失败的模块 URL 有负缓存（module map 记住 rejected promise）：
          // remount 重跑 loader 也不会重新请求同一 URL。可靠恢复是整页重载——
          // module map 随文档重建，新部署的 index 会指向新 chunk。会话级标记防循环
          // （App.vue 启动时清除，下次故障仍可走重载恢复）。
          const CHUNK_RELOAD_FLAG = 'amagi-codebox:chunk-reload';
          try {
            if (!window.sessionStorage.getItem(CHUNK_RELOAD_FLAG)) {
              window.sessionStorage.setItem(CHUNK_RELOAD_FLAG, '1');
              window.location.reload();
              return;
            }
          } catch {
            // sessionStorage 不可用（极端 WebView 沙箱）则退回 remount 重试
          }
          presetLoadKey.value++;
        },
      });
  },
});

function lazyPreset<T extends Component>(loader: () => Promise<T>) {
  return defineAsyncComponent({
    loader,
    loadingComponent: LoadingState,
    errorComponent: PresetLoadError,
    delay: 120,
  });
}

const OpenCodePresets = lazyPreset(() => import('../components/provider/OpenCodePresets.vue'));
const OpenCodeGlobalConfig = lazyPreset(() => import('../components/provider/OpenCodeGlobalConfig.vue'));
const PiAmagiConfig = lazyPreset(() => import('../components/provider/PiAmagiConfig.vue'));
const OmpGlobalConfig = lazyPreset(() => import('../components/provider/OmpGlobalConfig.vue'));
const ProviderRegistryEditor = lazyPreset(() => import('../components/provider/ProviderRegistryEditor.vue'));
const AuthConfigEditor = lazyPreset(() => import('../components/provider/AuthConfigEditor.vue'));

onMounted(() => {
  store.loadProviders();
  // 预设数据在切换到 presets tab 或切换 engine 时按需加载
});

// 首次进入 presets / cliconfigs tab 时加载相应 engine 数据
watch(mainTab, (tab) => {
  if (tab === 'presets') {
    if (!FORMAT_ENGINES.includes(store.presetEngine)) {
      store.setPresetEngine('anthropic');
    }
    void store.loadPresets(store.presetEngine);
  } else if (tab === 'cliconfigs') {
    if (!CLI_ENGINES.includes(store.presetEngine)) {
      store.setPresetEngine('pi');
    }
  }
});

// 进入详情模式时，强制回到 providers tab（防止详情出现在 presets tab）
watch(
  () => store.activeProviderId,
  (id) => {
    if (id) mainTab.value = 'providers';
  }
);

function requestExport() {
  showExportConfirm.value = true;
}

function onProfileApplied() {
  presetLoadKey.value++;
}

function requestImport() {
  showImportConfirm.value = true;
}

async function handleExport() {
  if (transferring.value) return;
  transferAction.value = 'export';
  try {
    const path = await ExportConfigToFile();
    if (path) showSuccess('配置已导出到: ' + path);
  } catch (err) {
    showError('导出失败: ' + normalizeTransferError(err));
  } finally {
    transferAction.value = null;
  }
}

async function handleImport() {
  if (transferring.value) return;
  transferAction.value = 'import';
  try {
    const result = await ImportConfigFromFile();
    if (result) {
      showSuccess(result);
      await store.loadProviders();
    }
  } catch (err) {
    showError('导入失败: ' + normalizeTransferError(err));
  } finally {
    transferAction.value = null;
  }
}

function normalizeTransferError(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  try {
    return JSON.stringify(error);
  } catch {
    return String(error);
  }
}

// 添加提供商弹窗在 P7 批次实现，本批仅提示
function handleAdd() {
  showInfo('添加提供商功能将在 P7 弹窗批次实现');
}

</script>

<style scoped>
.view-provider {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.pc-card {
  /* 覆盖 ConfigCard 默认 padding，让 Segmented 顶部贴合 */
  padding: 14px 16px 18px;
  /*
   * ConfigCard 默认 gap:16px 会和 .pc-panel 的 margin-top 叠加。设计意图是「一级导航→subnav = 12px」，
   * 所以把卡片 gap 收到 12px、面板 margin 为 0；服务提供商面板用 .pc-panel-providers 补 10px，
   * 保持原来的 22px（那一页不在本次改造范围内，视觉不变）。
   */
  gap: 12px;
}

.pc-main-tabs {
  align-self: flex-start;
  max-width: 480px;
}

/* center 级头部：一级 pill + 导出/导入按钮同行 */
.pc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.pc-head-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

/* override segmented 内部 seg flex:1，让 pill 收缩为内容宽 */
.pc-main-tabs :deep(.segmented) {
  display: inline-flex;
}

.pc-main-tabs :deep(.seg) {
  flex: 0 0 auto;
  padding: 7px 18px;
}

/* 面板容器：服务提供商面板只有一个子元素，gap 不起作用 */
.pc-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.pc-panel-providers {
  margin-top: 10px;
}

.pc-workspace { --section-nav-width:184px; display:grid; grid-template-columns:var(--section-nav-width) minmax(0, 1fr); gap:16px; }
.pc-detail-content { min-width:0; }
.pc-format-caption { margin:0 0 12px; font-size:12px; color:var(--tertiary); }
.pc-card-workspace .pc-head-actions { flex-wrap:wrap; min-width:0; max-width:100%; }
@media (max-width:1199px) {
  .pc-workspace-view { padding-inline:24px; }
  .pc-workspace { --section-nav-width:144px; gap:12px; }
}
@media (max-width:1023px) {
  .pc-workspace-view { padding-inline:16px; }
  .pc-card-workspace { padding-inline:12px; }
  .pc-workspace { --section-nav-width:112px; }
  .pc-main-tabs :deep(.seg) { padding-inline:12px; }
}
</style>
