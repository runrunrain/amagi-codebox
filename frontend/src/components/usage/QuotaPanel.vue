<template>
  <div class="quota-panel">
    <LoadingState v-if="quotaStore.loading && !hasEntries" message="加载额度数据中..." />

    <ErrorState
      v-else-if="quotaStore.error && !hasEntries"
      :message="quotaStore.error"
      :on-retry="handleRetry"
    />

    <EmptyState
      v-else-if="familyCards.length === 0 && otherProviders.length === 0"
      title="暂无额度数据"
      description="GLM Coding Plan / DeepSeek / Codex 订阅的额度尚未探测；点击下方按钮立即刷新"
    >
      <template #action>
        <AppButton variant="primary" :disabled="quotaStore.probingAll" @click="handleProbeAll">
          {{ quotaStore.probingAll ? '刷新中...' : '全部刷新' }}
        </AppButton>
      </template>
    </EmptyState>

    <template v-else>
      <!-- 卡片网格：auto-fill minmax(320px,1fr)（设计 §6） -->
      <div class="quota-grid">
        <QuotaCard
          v-for="card in familyCards"
          :key="card.key"
          :entry="card.entry"
          :title="card.title"
          :probing="quotaStore.probing[card.entry.provider] === true"
          @refresh="handleCardRefresh(card.entry.provider)"
        />

        <!-- 「其他提供商」聚合灰卡：不在三家内的 provider 列名 + 不可查说明 -->
        <ConfigCard v-if="otherProviders.length > 0" class="quota-card quota-card-others">
          <div class="q-head">
            <div class="q-title">
              <h3 class="q-name">其他提供商</h3>
              <span class="q-sub">{{ otherProviders.length }} 个</span>
            </div>
          </div>
          <div class="q-gray">
            <p class="q-gray-text">该服务暂不支持额度查询</p>
            <ul class="q-others">
              <li v-for="name in otherProviders" :key="name" class="mono">{{ name }}</li>
            </ul>
          </div>
        </ConfigCard>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
/**
 * QuotaPanel — 额度查询页（设计 §6）。
 * 挂载加载 GetProviderQuotas 全量缓存；四类卡：GLM 双通道（有则渲染）、
 * DeepSeek、Codex 订阅（固定键 codex）、「其他提供商」聚合灰卡。
 * 「全部刷新」按钮与数据时间在壳层 PageHead actions（UsageView 按
 * 子页切换）；本页负责网格、单卡刷新与空/错态。
 */
import { computed, onMounted } from 'vue';
import ConfigCard from '../ui/ConfigCard.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import QuotaCard from './QuotaCard.vue';
import { useQuotaStore } from '../../stores/quota';
import { CODEX_QUOTA_KEY } from '../../stores/quota';
import { useProviderStore } from '../../stores/provider';
import {
  QUOTA_FAMILY_GLM_BIGMODEL,
  QUOTA_FAMILY_GLM_ZAI,
  QUOTA_FAMILY_DEEPSEEK,
  QUOTA_FAMILY_CODEX_SUB,
  SUPPORTED_QUOTA_FAMILIES,
  familyDisplayName,
} from './quotaModel';
import type { ProviderQuotaEntry } from './quotaModel';

const quotaStore = useQuotaStore();
const providerStore = useProviderStore();

const hasEntries = computed(() => Object.keys(quotaStore.entries).length > 0);

interface FamilyCard {
  key: string;
  title: string;
  entry: ProviderQuotaEntry;
}

/** 同族多 provider 时取 probed_at 最新一条（探测时钟序即代表最新快照） */
function latestOfFamily(family: string): ProviderQuotaEntry | null {
  const list = Object.values(quotaStore.entries).filter((e) => e?.family === family);
  if (list.length === 0) return null;
  return list.reduce((latest, cur) =>
    Date.parse(cur.probed_at ?? '') > Date.parse(latest.probed_at ?? '') ? cur : latest,
  );
}

const familyCards = computed<FamilyCard[]>(() => {
  const cards: FamilyCard[] = [];
  // GLM 双通道各一卡（无对应条目则不渲染）
  for (const family of [QUOTA_FAMILY_GLM_BIGMODEL, QUOTA_FAMILY_GLM_ZAI, QUOTA_FAMILY_DEEPSEEK]) {
    const entry = latestOfFamily(family);
    if (entry) cards.push({ key: family, title: familyDisplayName(family), entry });
  }
  // Codex 订阅：固定键 codex（family=codex-sub）
  const codex = quotaStore.entries[CODEX_QUOTA_KEY];
  if (codex && codex.family === QUOTA_FAMILY_CODEX_SUB) {
    cards.push({ key: CODEX_QUOTA_KEY, title: familyDisplayName(QUOTA_FAMILY_CODEX_SUB), entry: codex });
  }
  return cards;
});

/** provider store 中不在三家内的 → 「其他提供商」聚合卡（含探测过但 unsupported 的） */
const otherProviders = computed<string[]>(() =>
  providerStore.providerEntries
    .map((e) => e.id)
    .filter((id) => {
      const q = quotaStore.entries[id];
      return !(q && SUPPORTED_QUOTA_FAMILIES.has(q.family));
    })
    .sort(),
);

function handleRetry(): void {
  void quotaStore.loadAll();
}

function handleProbeAll(): void {
  void quotaStore.probeAll();
}

function handleCardRefresh(provider: string): void {
  if (!provider) return;
  void quotaStore.probe(provider);
}

onMounted(() => {
  void quotaStore.loadAll();
  // provider 名单用于「其他提供商」聚合卡；已有数据时不重复拉取
  if (Object.keys(providerStore.providers).length === 0) {
    void providerStore.loadProviders();
  }
});
</script>

<style scoped>
.quota-panel { display: flex; flex-direction: column; gap: 18px; }

/* 卡片网格：auto-fill 自适应列，窄窗自动降列（设计 §6 线框） */
.quota-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

/* 聚合灰卡复用 QuotaCard 的头/灰区视觉（本组件内联精简版，避免跨组件样式穿透） */
.quota-card-others { background: var(--sidebar); box-shadow: none; }
.q-head { display: flex; align-items: center; gap: 8px; }
.q-title { display: flex; align-items: baseline; gap: 8px; min-width: 0; flex: 1; }
.q-name { margin: 0; font-size: 15px; font-weight: 600; color: var(--secondary); }
.q-sub { color: var(--tertiary); font-size: 12px; }
.q-gray { display: flex; flex-direction: column; gap: 8px; }
.q-gray-text { margin: 0; color: var(--secondary); font-size: 13px; }
.q-others { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 4px; }
.q-others li { color: var(--tertiary); font-size: 12px; }
</style>
