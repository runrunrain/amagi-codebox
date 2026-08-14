<template>
  <!--
    Provider Center - 两级导航容器（对照 demo + 交接说明 §5.3）。
    一级 Segmented(pill): 「服务提供商」|「预设」
    区域标签：服务提供商区描述「底层资源」；预设区描述「启动配置」
    服务提供商 tab（本批实现）：网格 + 详情模式切换
    预设 tab：二级导航拆两组——「格式预设」（Anthropic/OpenAI 公共协议格式）
    与「CLI 独立配置」（OpenCode/Pi/OMP 专属配置文件），组间视觉分隔；
    CLI 独立配置组件全部 defineAsyncComponent 懒加载，退出入口 chunk。
  -->
  <section class="view-provider">
    <PageHead title="Provider Center" description="统一管理服务提供商与可跨 CLI 复用的公共预设" />

    <!-- 详情模式：覆盖网格视图（对照 demo .pc-detail）-->
    <ProviderDetailView
      v-if="store.activeProviderId && store.activeProvider"
      @back="store.closeProvider"
      @saved="store.loadProviders"
    />

    <!-- 列表模式 -->
    <ConfigCard v-else class="pc-card">
      <!-- 一级 pill 导航 + center 级导出/导入（作用于整个 config，两个 tab 下均可见）-->
      <div class="pc-head">
        <Segmented
          v-model="mainTab"
          :options="MAIN_TABS"
          variant="pill"
          class="pc-main-tabs"
        />
        <div class="pc-head-actions">
          <AppButton variant="ghost" size="small" :disabled="transferring" @click="requestExport">
            {{ transferAction === 'export' ? '导出中...' : '导出完整配置' }}
          </AppButton>
          <AppButton variant="ghost" size="small" :disabled="transferring" @click="requestImport">
            {{ transferAction === 'import' ? '导入中...' : '导入完整配置' }}
          </AppButton>
        </div>
      </div>

      <!-- 服务提供商区 -->
      <div v-if="mainTab === 'providers'" class="pc-panel">
        <ProviderGrid
          @add="handleAdd"
        />
      </div>

      <!-- 预设区（启动配置）-->
      <div v-else class="pc-panel pc-presets-panel">
        <div class="pc-zone-label">
          <span>启动配置</span>
          <span class="zn-sep">·</span>
          <span>绑定提供商与模型参数</span>
        </div>

        <!-- 二级导航拆两组：格式预设（跨 CLI 复用的公共协议格式）与 CLI 独立配置（各 CLI 专属配置文件，互不共享）。 -->
        <div class="pc-engine-group">
          <div class="pc-zone-label">
            <span>格式预设</span>
            <span class="zn-sep">·</span>
            <span>跨 CLI 复用的公共协议格式</span>
          </div>
          <Segmented
            v-model="formatModel"
            :options="FORMAT_TABS"
            variant="underline"
            class="pc-engine-tabs"
          />
        </div>

        <div class="pc-group-divider" aria-hidden="true" />

        <div class="pc-engine-group">
          <div class="pc-zone-label">
            <span>CLI 独立配置</span>
            <span class="zn-sep">·</span>
            <span>各 CLI 专属配置文件，互不共享</span>
          </div>
          <Segmented
            v-model="cliModel"
            :options="CLI_TABS"
            variant="underline"
            class="pc-engine-tabs"
          />
        </div>

        <!-- Claude Code 复用 Anthropic 格式公共预设。 -->
        <PresetList
          v-if="store.presetEngine === 'anthropic'"
          format="anthropic"
        />
        <!-- Codex / Pi / OMP 及后续兼容 CLI 共用 OpenAI 格式预设。 -->
        <PresetList
          v-else-if="store.presetEngine === 'openai'"
          format="openai"
        />
        <!-- Pi（amagi-pi）结构化配置：三级 Tab = Agent 配置 | 模型提供商注册表 | 认证登录 -->
        <template v-else-if="store.presetEngine === 'pi'">
          <Segmented
            v-model="piMode"
            :options="PI_MODES"
            variant="pill"
            class="oc-mode-tabs"
          />
          <PiAmagiConfig v-if="piMode === 'agents'" :key="presetLoadKey" />
          <ProviderRegistryEditor v-else-if="piMode === 'registry'" :key="presetLoadKey" engine="pi" />
          <AuthConfigEditor v-else :key="presetLoadKey" />
        </template>

        <!-- OMP (oh-my-pi) 结构化配置：三级 Tab = Agent 配置 | 模型提供商注册表 -->
        <template v-else-if="store.presetEngine === 'omp'">
          <Segmented
            v-model="ompMode"
            :options="OMP_MODES"
            variant="pill"
            class="oc-mode-tabs"
          />
          <OmpGlobalConfig v-if="ompMode === 'agents'" :key="presetLoadKey" />
          <ProviderRegistryEditor v-else :key="presetLoadKey" engine="omp" />
        </template>

        <!-- OpenCode 预设（特殊性：配置文件管理 + 可视化/JSON 双模式）-->
        <template v-else-if="store.presetEngine === 'opencode'">
          <!-- 三级 Segmented：预设管理 | 全局配置 -->
          <Segmented
            v-model="openCodeMode"
            :options="OPENCODE_MODES"
            variant="pill"
            class="oc-mode-tabs"
          />
          <!-- OpenCode 预设管理 -->
          <OpenCodePresets
            v-if="openCodeMode === 'presets'"
            :key="presetLoadKey"
          />
          <!-- OpenCode 全局配置 -->
          <OpenCodeGlobalConfig v-else :key="presetLoadKey" />
        </template>
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
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, defineAsyncComponent, defineComponent, h, type Component } from 'vue';
import PageHead from '../components/ui/PageHead.vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import Segmented from '../components/ui/Segmented.vue';
import AppButton from '../components/ui/AppButton.vue';
import ConfirmDialog from '../components/ui/ConfirmDialog.vue';
import LoadingState from '../components/ui/LoadingState.vue';
import ErrorState from '../components/ui/ErrorState.vue';
import ProviderGrid from '../components/provider/ProviderGrid.vue';
import PresetList from '../components/provider/PresetList.vue';
import ProviderDetailView from './ProviderDetailView.vue';
import { useProviderStore, type PresetEngine } from '../stores/provider';
import { ExportConfigToFile, ImportConfigFromFile } from '../../wailsjs/go/main/App';
import { useToast } from '../composables/useToast';

const store = useProviderStore();
const { showSuccess, showError, showInfo } = useToast();

const MAIN_TABS = [
  { value: 'providers', label: '服务提供商' },
  { value: 'presets', label: '预设' },
];

// 二级导航拆两组（ENGINE_TABS 已拆分）：
// 格式预设 = 公共协议格式（terminal_presets，跨 CLI 复用）；
// CLI 独立配置 = 各 CLI 专属配置文件编辑，内部自带三级 Tab。
const FORMAT_TABS = [
  { value: 'anthropic', label: 'Anthropic 格式' },
  { value: 'openai', label: 'OpenAI 格式' },
];

const CLI_TABS = [
  { value: 'opencode', label: 'OpenCode' },
  { value: 'pi', label: 'Pi' },
  { value: 'omp', label: 'OMP' },
];

const OPENCODE_MODES = [
  { value: 'presets', label: '预设管理' },
  { value: 'global', label: '全局配置' },
];

const PI_MODES = [
  { value: 'agents', label: 'Agent 配置' },
  { value: 'registry', label: '模型提供商' },
  { value: 'auth', label: '认证登录' },
];

const OMP_MODES = [
  { value: 'agents', label: 'Agent 配置' },
  { value: 'registry', label: '模型提供商' },
];

const mainTab = ref<'providers' | 'presets'>('providers');
const openCodeMode = ref<'presets' | 'global'>('global');
const piMode = ref<'agents' | 'registry' | 'auth'>('agents');
const ompMode = ref<'agents' | 'registry'>('agents');
const showExportConfirm = ref(false);
const showImportConfirm = ref(false);
const transferAction = ref<'export' | 'import' | null>(null);
const transferring = computed(() => transferAction.value !== null);

// 二级 engine 双向绑定（写入 store + 触发按需加载）。
// store.presetEngine 仍是唯一选中态；两组 Segmented 各只反映本组内的值，
// 选中引擎不在本组时显示为空（无高亮），点选时写回 store。
const FORMAT_ENGINES: readonly PresetEngine[] = ['anthropic', 'openai'];
const CLI_ENGINES: readonly PresetEngine[] = ['opencode', 'pi', 'omp'];

const formatModel = computed<string>({
  get: () => (FORMAT_ENGINES.includes(store.presetEngine) ? store.presetEngine : ''),
  set: (v: string) => {
    if (v) store.setPresetEngine(v as PresetEngine);
  },
});

const cliModel = computed<string>({
  get: () => (CLI_ENGINES.includes(store.presetEngine) ? store.presetEngine : ''),
  set: (v: string) => {
    if (v) store.setPresetEngine(v as PresetEngine);
  },
});

// CLI 独立配置组件懒加载（AppShell.vue:37-43 模式）：合计约 4,100 行且非默认视图，
// 异步加载使其退出入口 chunk；delay 避免快速切换时 loading 闪烁。
// 更换 presetLoadKey 会重新挂载异步组件并重新执行 loader（动态 import 可重试）。
const presetLoadKey = ref(0);

const PresetLoadError = defineComponent({
  name: 'PresetLoadError',
  setup() {
    return () =>
      h(ErrorState, {
        title: '配置页加载失败',
        message: '配置界面资源加载失败，请重试。',
        onRetry: () => {
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

// 首次进入 presets tab 时加载当前 engine 数据
watch(mainTab, (tab) => {
  if (tab === 'presets') {
    void store.loadPresets(store.presetEngine);
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
}

.pc-main-tabs {
  align-self: flex-start;
  max-width: 320px;
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

.pc-panel {
  margin-top: 6px;
}

.pc-presets-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.pc-zone-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  padding: 0 2px;
  letter-spacing: 0.3px;
}

.pc-zone-label .zn-sep {
  color: var(--separator);
}

/* 二级导航两组：组标签（复用 .pc-zone-label 范式）+ underline Segmented */
.pc-engine-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* 组间视觉分隔 */
.pc-group-divider {
  height: 1px;
  background: var(--separator);
  opacity: 0.6;
}

/* 二级下划线 Tab：与一级 pill 区分层级（对照 demo .pc-engine-tabs） */
.pc-engine-tabs {
  align-self: flex-start;
}

.pc-engine-tabs :deep(.segmented) {
  display: inline-flex;
}

/* OpenCode 三级 Tab */
.oc-mode-tabs {
  align-self: flex-start;
  margin-top: 10px;
}

.oc-mode-tabs :deep(.segmented) {
  display: inline-flex;
}
</style>
