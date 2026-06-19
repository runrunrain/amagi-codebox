<template>
  <!--
    Provider Center - 两级导航容器（对照 demo + 交接说明 §5.3）。
    一级 Segmented(pill): 「服务提供商」|「预设」
    区域标签：服务提供商区描述「底层资源」；预设区描述「启动配置」
    服务提供商 tab（本批实现）：网格 + 详情模式切换
    预设 tab：EmptyState「即将上线」占位（P3-B 填）
  -->
  <section class="view-provider">
    <PageHead title="Provider Center" description="统一管理服务提供商与各引擎预设" />

    <!-- 详情模式：覆盖网格视图（对照 demo .pc-detail）-->
    <ProviderDetailView
      v-if="store.activeProviderId && store.activeProvider"
      @back="store.closeProvider"
      @saved="store.loadProviders"
    />

    <!-- 列表模式 -->
    <ConfigCard v-else class="pc-card">
      <!-- 一级 pill 导航 -->
      <Segmented
        v-model="mainTab"
        :options="MAIN_TABS"
        variant="pill"
        class="pc-main-tabs"
      />

      <!-- 服务提供商区 -->
      <div v-if="mainTab === 'providers'" class="pc-panel">
        <ProviderGrid
          @export="handleExport"
          @import="handleImport"
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

        <!-- 二级下划线 Tab：Claude Code | Codex | OpenCode（与一级 pill 区分层级）-->
        <Segmented
          v-model="engineModel"
          :options="ENGINE_TABS"
          variant="underline"
          class="pc-engine-tabs"
        />

        <!-- Claude Code 预设 -->
        <PresetList
          v-if="store.presetEngine === 'claude'"
          engine="claude"
          @add="handlePresetAdd"
        />
        <!-- Codex 预设（与 Claude Code 统一范式）-->
        <PresetList
          v-else-if="store.presetEngine === 'codex'"
          engine="codex"
          @add="handlePresetAdd"
        />
        <!-- OpenCode 预设（特殊性：配置文件管理 + 可视化/JSON 双模式）-->
        <OpenCodePresets
          v-else-if="store.presetEngine === 'opencode'"
          @add="handlePresetAdd"
        />
      </div>
    </ConfigCard>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import PageHead from '../components/ui/PageHead.vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import Segmented from '../components/ui/Segmented.vue';
import ProviderGrid from '../components/provider/ProviderGrid.vue';
import PresetList from '../components/provider/PresetList.vue';
import OpenCodePresets from '../components/provider/OpenCodePresets.vue';
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
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'opencode', label: 'OpenCode' },
];

const mainTab = ref<'providers' | 'presets'>('providers');

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

// 导出/导入沿用 legacy ProviderCenter 逻辑（真实调用 wailsjs）
function handleExport() {
  ExportConfigToFile()
    .then((path) => {
      if (path) showSuccess('配置已导出到: ' + path);
    })
    .catch((err) => showError('导出失败: ' + err));
}

function handleImport() {
  ImportConfigFromFile()
    .then((result) => {
      if (result) {
        showSuccess(result);
        return store.loadProviders();
      }
    })
    .catch((err) => showError('导入失败: ' + err));
}

// 添加提供商弹窗在 P7 批次实现，本批仅提示
function handleAdd() {
  showInfo('添加提供商功能将在 P7 弹窗批次实现');
}

// 添加预设弹窗在 P7 批次实现，本批仅提示（emit 占位）
function handlePresetAdd() {
  const label =
    store.presetEngine === 'claude'
      ? 'Claude Code'
      : store.presetEngine === 'codex'
      ? 'Codex'
      : 'OpenCode';
  showInfo(`${label} 添加预设功能将在 P7 弹窗批次实现`);
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
</style>
