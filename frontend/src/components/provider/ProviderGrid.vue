<template>
  <div class="provider-grid-panel">
    <!-- Loading state -->
    <LoadingState v-if="loading" message="加载提供商中..." />

    <!-- Error state -->
    <ErrorState
      v-else-if="error"
      :message="error"
      :on-retry="initialLoad"
    />

    <!-- Main content (show when local loading/error done and store has content) -->
    <template v-else>
    <!-- 区域标签（对照 demo .pc-zone-label）-->
    <div class="pc-zone-label">
      <span>底层资源</span>
      <span class="zn-sep">·</span>
      <span>API 凭证与端点</span>
      <span class="zn-count">· {{ providerEntries.length }} 个提供商</span>
    </div>

    <!-- 工具栏：筛选 Chip + 操作按钮 -->
    <div class="pc-toolbar">
      <div class="pc-filter">
        <Chip
          v-for="opt in FILTER_OPTIONS"
          :key="opt.value"
          :active="filter === opt.value"
          @click="setFilter(opt.value)"
        >{{ opt.label }}</Chip>
      </div>
      <div class="pc-actions">
        <AppButton variant="ghost" size="small" :disabled="loading" @click="$emit('export')">导出配置</AppButton>
        <AppButton variant="ghost" size="small" :disabled="loading" @click="$emit('import')">JSON 导入</AppButton>
        <AppButton variant="primary" size="small" :disabled="storeLoading" @click="showAddDialog = true">添加提供商</AppButton>
      </div>
    </div>

    <!-- 提供商卡片网格 -->
    <div v-if="storeLoading && filteredProviders.length === 0" class="pc-empty">加载中…</div>
    <div v-else-if="filteredProviders.length === 0" class="pc-empty">
      <template v-if="providerEntries.length === 0">暂无服务提供商，请点击右上角添加</template>
      <template v-else>当前筛选条件下无匹配的服务提供商</template>
    </div>
    <div v-else class="prov-grid">
      <article
        v-for="entry in filteredProviders"
        :key="entry.id"
        class="prov-card"
        @click="onCardClick(entry.id)"
      >
        <header class="prov-card-head">
          <h3 class="prov-name">{{ entry.id }}</h3>
          <div class="prov-formats">
            <span v-if="hasAnthropic(entry.provider)" class="fmt A" title="Anthropic 格式">A</span>
            <span v-if="hasOpenAI(entry.provider)" class="fmt O" title="OpenAI 格式">O</span>
            <span
              v-if="!hasAnthropic(entry.provider) && !hasOpenAI(entry.provider)"
              class="fmt legacy"
              :title="typeLabel(entry.provider)"
            >{{ typeInitial(entry.provider) }}</span>
          </div>
        </header>
        <div class="prov-row">
          <span class="prov-row-label">Base URL</span>
          <span class="prov-row-value mono" :title="baseUrl(entry.provider) || '未设置'">
            {{ baseUrl(entry.provider) || '未设置' }}
          </span>
        </div>
        <div class="prov-row">
          <span class="prov-row-label">默认模型</span>
          <span
            class="prov-row-value"
            :class="{ placeholder: !entry.provider.default_model }"
          >{{ entry.provider.default_model || '-' }}</span>
        </div>
        <div class="prov-row">
          <span class="prov-row-label">密钥</span>
          <span class="key-status">
            <span class="sess-dot" :style="{ background: entry.keyConfigured ? '#34C759' : '#8E8E93' }"></span>
            {{ entry.keyConfigured ? '已配置' : '未配置' }}
          </span>
        </div>
      </article>
    </div>

    <!-- 添加提供商弹窗 -->
    <AddProviderDialog
      v-model:open="showAddDialog"
      @saved="handleProviderSaved"
    />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { config } from '../../../wailsjs/go/models';
import Chip from '../ui/Chip.vue';
import AppButton from '../ui/AppButton.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import AddProviderDialog from './AddProviderDialog.vue';
import { useProviderStore, type ProviderFilter } from '../../stores/provider';

type Provider = config.Provider;

const emit = defineEmits<{
  export: [];
  import: [];
}>();

const store = useProviderStore();
const showAddDialog = ref(false);

// Loading and error states
const loading = ref(true);
const error = ref('');

// 添加提供商成功后刷新列表
async function handleProviderSaved() {
  loading.value = true;
  error.value = '';
  try {
    await store.loadProviders();
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

// Initial load
async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    await store.loadProviders();
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

const FILTER_OPTIONS: { value: ProviderFilter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
];

const filter = computed(() => store.filter);
const providerEntries = computed(() => store.providerEntries);
const filteredProviders = computed(() => store.filteredProviders);
const storeLoading = computed(() => store.loading);

function setFilter(next: ProviderFilter) {
  store.setFilter(next);
}

function onCardClick(id: string) {
  store.openProvider(id);
}

function hasAnthropic(p: Provider): boolean {
  return !!(p && (p as any).anthropic && (p as any).anthropic.enabled);
}
function hasOpenAI(p: Provider): boolean {
  return !!(p && (p as any).openai && (p as any).openai.enabled);
}
/** 当前有效 Base URL：优先 anthropic/openai 子块，回退 legacy base_url */
function baseUrl(p: Provider): string {
  if (!p) return '';
  const a = (p as any).anthropic;
  const o = (p as any).openai;
  if (a && a.enabled && a.base_url) return a.base_url;
  if (o && o.enabled && o.base_url) return o.base_url;
  return p.base_url || '';
}
function typeLabel(p: Provider): string {
  return ((p && (p as any).type) || 'anthropic').toUpperCase();
}
function typeInitial(p: Provider): string {
  const t = ((p && (p as any).type) || 'anthropic').toLowerCase();
  return t === 'openai' ? 'O' : 'A';
}

// Trigger initial load on mount
import { onMounted } from 'vue';
onMounted(() => {
  initialLoad();
});
</script>

<style scoped>
.provider-grid-panel {
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

.pc-zone-label .zn-count {
  color: var(--secondary);
  font-weight: 500;
}

.pc-zone-label .zn-sep {
  color: var(--separator);
}

.pc-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.pc-filter {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.pc-actions {
  display: flex;
  gap: 8px;
}

.btn-ghost,
.btn-primary {
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.12s, opacity 0.12s;
  border: none;
}

.btn-ghost {
  background: var(--control);
  color: var(--label);
  padding: 7px 13px;
}

.btn-ghost:hover:not(:disabled) {
  background: var(--controlHover);
}

.btn-primary {
  background: var(--accent);
  color: #fff;
  padding: 7px 14px;
}

.btn-primary:hover:not(:disabled) {
  background: var(--accentHover);
}

.btn-ghost:disabled,
.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.pc-empty {
  padding: 40px 16px;
  text-align: center;
  color: var(--tertiary);
  font-size: 13px;
}

/* 提供商卡片网格（对照 demo .prov-grid / .prov-card）*/
.prov-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(286px, 1fr));
  gap: 14px;
}

.prov-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 16px;
  cursor: pointer;
  transition: box-shadow 0.15s, transform 0.15s, border-color 0.15s;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.prov-card:hover {
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.08);
  transform: translateY(-1px);
  border-color: #c5c5cc;
}

.prov-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}

.prov-name {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  letter-spacing: -0.2px;
}

.prov-formats {
  display: flex;
  gap: 5px;
}

/* A/O 格式徽章：按类型着色（A 紫 / O 绿），非品牌色 */
.fmt {
  width: 20px;
  height: 20px;
  border-radius: 5px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
}

.fmt.A {
  background: var(--purple);
}

.fmt.O {
  background: var(--success);
}

.fmt.legacy {
  background: var(--tertiary);
}

.prov-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
}

.prov-row-label {
  color: var(--tertiary);
  flex-shrink: 0;
  min-width: 56px;
}

.prov-row-value {
  color: var(--secondary);
  flex: 1;
  word-break: break-all;
}

.prov-row-value.mono {
  font-family: var(--mono);
  font-size: 11px;
}

.prov-row-value.placeholder {
  color: var(--tertiary);
}

.key-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--secondary);
  font-size: 12px;
}

.sess-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
</style>
