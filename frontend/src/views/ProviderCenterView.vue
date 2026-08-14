<template>
  <!--
    Provider Center - 两级导航容器（对照 demo + 交接说明 §5.3）。
    一级 Segmented(pill): 「服务提供商」|「预设」
    区域标签：服务提供商区描述「底层资源」；预设区描述「启动配置」
    服务提供商 tab（本批实现）：网格 + 详情模式切换
    预设 tab：EmptyState「即将上线」占位（P3-B 填）
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

        <!-- 二级下划线 Tab：两类公共协议格式 + OpenCode 独立配置。 -->
        <Segmented
          v-model="engineModel"
          :options="ENGINE_TABS"
          variant="underline"
          class="pc-engine-tabs"
        />

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
        <!-- Pi（amagi-pi）结构化配置：可视化/JSON 双模式 + 模型下拉关联 -->
        <PiAmagiConfig v-else-if="store.presetEngine === 'pi'" />

        <!-- OMP (oh-my-pi) 结构化配置：可视化/YAML 双模式 + 模型下拉关联 -->
        <OmpGlobalConfig v-else-if="store.presetEngine === 'omp'" />

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
          />
          <!-- OpenCode 全局配置 -->
          <OpenCodeGlobalConfig v-else />
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
import { ref, computed, onMounted, watch } from 'vue';
import PageHead from '../components/ui/PageHead.vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import Segmented from '../components/ui/Segmented.vue';
import AppButton from '../components/ui/AppButton.vue';
import ConfirmDialog from '../components/ui/ConfirmDialog.vue';
import ProviderGrid from '../components/provider/ProviderGrid.vue';
import PresetList from '../components/provider/PresetList.vue';
import OpenCodePresets from '../components/provider/OpenCodePresets.vue';
import OpenCodeGlobalConfig from '../components/provider/OpenCodeGlobalConfig.vue';
import PiAmagiConfig from '../components/provider/PiAmagiConfig.vue';
import OmpGlobalConfig from '../components/provider/OmpGlobalConfig.vue';
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

const ENGINE_TABS = [
  { value: 'anthropic', label: 'Anthropic 格式' },
  { value: 'openai', label: 'OpenAI 格式' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'pi', label: 'Pi' },
  { value: 'omp', label: 'OMP' },
];

const OPENCODE_MODES = [
  { value: 'presets', label: '预设管理' },
  { value: 'global', label: '全局配置' },
];

const mainTab = ref<'providers' | 'presets'>('providers');
const openCodeMode = ref<'presets' | 'global'>('global');
const showExportConfirm = ref(false);
const showImportConfirm = ref(false);
const transferAction = ref<'export' | 'import' | null>(null);
const transferring = computed(() => transferAction.value !== null);

// 二级 engine 双向绑定（写入 store + 触发按需加载）
const engineModel = computed<string>({
  get: () => store.presetEngine,
  set: (v: string) => store.setPresetEngine(v as PresetEngine),
});

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
