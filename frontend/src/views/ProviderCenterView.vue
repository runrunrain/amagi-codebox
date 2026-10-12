<template>
  <!--
    Provider Center - 多级导航容器（对照 demo + 交接说明 §5.3）。
    一级 Segmented(pill): 「服务提供商」|「预设」|「CLI 独立配置」
    服务提供商 tab：网格 + 详情模式切换
    预设 tab / CLI 独立配置 tab（provider-center-layout-redesign）：
    二级 underline Tab 与带所属 CLI 标签的三级 pill 上下紧贴，构成父子两行 .pc-subnav；
    有内部分区的编辑器在自身内部带分区导航（左栏），页面级不再有任何左栏。
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

      <div v-else class="pc-panel">
        <!--
          CLI 父子导航上下紧贴：二级 underline Tab 在上，所属 CLI 与三级 pill 在下。
          预设页仍为格式页签与紧邻的「用于 …」说明。
          三级留在容器里而不下放进编辑器：异步编辑器加载中 / 加载失败时用户仍可切换。
          有内部分区的编辑器自带分区导航（左栏），页面级没有任何左栏。
        -->
        <div class="pc-subnav" :class="mainTab === 'presets' ? 'pc-subnav--presets' : 'pc-subnav--cli'">
          <template v-if="mainTab === 'presets'">
            <Segmented v-model="formatModel" :options="FORMAT_TABS" variant="underline" class="pc-subnav-tabs" />
            <span class="pc-subnav-caption">{{ formatConsumers }}</span>
          </template>
          <template v-else>
            <Segmented v-model="cliModel" :options="CLI_TABS" variant="underline" class="pc-subnav-tabs" />
            <div class="pc-subnav-context" role="group" :aria-label="currentCliLabel + ' 配置类型'">
              <span class="pc-subnav-owner">{{ currentCliLabel }} 配置</span>
              <Segmented v-if="store.presetEngine === 'pi'" v-model="piMode" :options="PI_MODES" variant="pill" class="pc-subnav-modes" />
              <Segmented v-else-if="store.presetEngine === 'omp'" v-model="ompMode" :options="OMP_MODES" variant="pill" class="pc-subnav-modes" />
              <Segmented v-else-if="store.presetEngine === 'opencode'" v-model="openCodeMode" :options="OPENCODE_MODES" variant="pill" class="pc-subnav-modes" />
            </div>
          </template>
        </div>
        <div class="pc-detail-content" :key="activePageId">
          <template v-if="mainTab === 'presets'">
            <PresetList v-if="store.presetEngine === 'anthropic'" format="anthropic" />
            <PresetList v-else-if="store.presetEngine === 'openai'" format="openai" />
          </template>
          <template v-else-if="store.presetEngine === 'pi'">
            <PiAmagiConfig v-if="piMode === 'agents'" :key="presetLoadKey" />
            <ProviderRegistryEditor v-else-if="piMode === 'registry'" :key="presetLoadKey" engine="pi" />
            <AuthConfigEditor v-else :key="presetLoadKey" />
          </template>
          <template v-else-if="store.presetEngine === 'omp'">
            <OmpGlobalConfig v-if="ompMode === 'agents'" :key="presetLoadKey" />
            <ProviderRegistryEditor v-else :key="presetLoadKey" engine="omp" />
          </template>
          <template v-else-if="store.presetEngine === 'opencode'">
            <OpenCodePresets v-if="openCodeMode === 'presets'" :key="presetLoadKey" />
            <OpenCodeGlobalConfig v-else :key="presetLoadKey" />
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
import { CONFIG_PAGES, FORMAT_ITEMS, type ConfigPageId } from '../components/provider/configNavigation';
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

// 顶侧页签选项（二级：格式 / CLI；三级：各 CLI 内的配置文件模式）。
// 只从纯导航表（configNavigation.ts）派生，文案单一来源，不在此复制；
// 值与 PresetEngine 以及 piMode / ompMode / openCodeMode 的联合类型字面一致。
const FORMAT_TABS = FORMAT_ITEMS.map(i => ({ value: i.id.slice('format:'.length), label: i.label }));
const CLI_TABS = [...new Map(CONFIG_PAGES.map((p): [string, string] => [p.id.split(':')[0], p.cli])).entries()]
  .map(([value, label]) => ({ value, label }));
const modeTabs = (engine: string) => CONFIG_PAGES.filter(p => p.id.startsWith(`${engine}:`))
  .map(p => ({ value: p.id.slice(engine.length + 1), label: p.label }));
const PI_MODES = modeTabs('pi');
const OMP_MODES = modeTabs('omp');
const OPENCODE_MODES = modeTabs('opencode');

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

const currentCliLabel = computed(() => CLI_TABS.find(tab => tab.value === cliModel.value)?.label || '');

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

const PresetLoadError = defineComponent({
  name: 'PresetLoadError',
  setup() {
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

/* CLI 两行共享一个导航容器；下划线只对齐父级行，不随 CLI 选择横移子组。 */
.pc-subnav { display:flex; min-width:0; }
.pc-subnav--cli { flex-direction:column; align-items:stretch; gap:6px; }
.pc-subnav--cli .pc-subnav-tabs.underline-tabs { width:100%; border-bottom:1px solid var(--separator); }
.pc-subnav-tabs :deep(.underline-seg) { font-size:13px; line-height:16px; padding:9px 2px; }
.pc-subnav-context { display:flex; align-items:center; justify-content:flex-start; flex-wrap:wrap; gap:6px 8px; margin-left:12px; border-left:2px solid var(--separator); padding-left:10px; min-width:0; min-height:30px; }
.pc-subnav-owner { font-size:12px; line-height:18px; color:var(--secondary); white-space:nowrap; }
.pc-subnav--presets { flex-direction:row; align-items:flex-end; justify-content:flex-start; gap:8px 12px; flex-wrap:wrap; border-bottom:1px solid var(--separator); }
.pc-subnav--presets .pc-subnav-tabs.underline-tabs { border-bottom:0; }

.pc-subnav-caption {
  font-size: 12px;
  color: var(--tertiary);
  /* 8px：与左侧 13px Tab 文字同基线（实测 10px 时偏高约 1.5px） */
  padding-bottom: 8px;
  white-space: nowrap;
}

/* 三级紧凑 pill：12px，seg padding 4px 12px，容器 padding 2 / radius 8 */
.pc-subnav-modes {
  margin-bottom: 0;
  min-width: 0;
}

.pc-subnav-modes.segmented {
  display: inline-flex;
  padding: 2px;
  border-radius: 8px;
  flex-wrap: wrap;
}

.pc-subnav-modes :deep(.seg) {
  flex: 0 0 auto;
  padding: 4px 12px;
  line-height: 18px;
  font-size: 12px;
  white-space: nowrap;
}

.pc-detail-content { min-width:0; }
.pc-card-workspace .pc-head-actions { flex-wrap:wrap; min-width:0; max-width:100%; }
@media (max-width:1199px) {
  .pc-workspace-view { padding-inline:24px; }
}
@media (max-width:1023px) {
  .pc-workspace-view { padding-inline:16px; }
  .pc-card-workspace { padding-inline:12px; }
  .pc-main-tabs :deep(.seg) { padding-inline:12px; }
  /* 窄档收紧父级间距与子级 pill；允许极端字体下自然换行。 */
  .pc-subnav-tabs.underline-tabs { gap:16px; }
  .pc-subnav-modes :deep(.seg) { padding-inline:10px; }
}
</style>
