<template>
  <!-- 设计 §7：宿主细工具条（iframe 输入框下侧的宿主侧落点），高 30px。
       左=额度按钮+迷你摘要，右=16px 刷新按钮。 -->
  <div class="quota-strip" role="toolbar" aria-label="会话额度">
    <button
      ref="anchorRef"
      type="button"
      class="quota-strip-main"
      :class="`tone-${summary.tone}`"
      :disabled="summary.disabled"
      :title="summary.title || '查看额度详情'"
      :aria-expanded="popoverVisible"
      @click="onMainClick"
    >
      <span class="quota-strip-icon" aria-hidden="true">◔</span>
      <span class="quota-strip-label">额度</span>
      <span class="quota-strip-summary mono">{{ summary.text }}</span>
    </button>

    <button
      type="button"
      class="quota-strip-refresh"
      :disabled="!providerName || probing"
      :title="probing ? '探测中…' : `刷新 ${providerName || ''} 额度`"
      @click="onRefresh"
    >
      <span class="quota-strip-refresh-icon" :class="{ spinning: probing }">↻</span>
    </button>

    <!-- 弹层：向上弹 Popover（宽 340px），Teleport to body + 锚定按钮，
         非阻断点外关闭（模式对齐 TerminalView quick-menu 锚定浮层） -->
    <Teleport to="body">
      <div
        v-if="popoverVisible"
        ref="popoverRef"
        class="quota-popover"
        :style="popoverStyle"
        role="dialog"
        aria-label="额度详情"
      >
        <QuotaCard
          v-if="entry"
          :entry="entry"
          :title="popoverTitle"
          :probing="probing"
          compact
          @refresh="onRefresh"
        />
        <p v-else class="quota-popover-loading">额度数据加载中…</p>
        <div class="quota-popover-foot">
          <button type="button" class="quota-popover-link" @click="goQuotaPage">查看额度页 →</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
/**
 * WebPlaneQuotaStrip — pi Web 平面会话额度按钮（设计 §7）。
 *
 * 数据流：sessionId → sessionStore.sessions[i].provider → quotaStore
 * （GetProviderQuotas 缓存 + ensure 后台单飞探测）。
 * 交互：
 * - 常驻态迷你摘要（家族短名 + 主窗口剩余 % / ¥ 余额）；
 * - unsupported/no_key/no_plan → 灰字「额度不可查」+ 按钮禁用（title 原因）；
 * - error → 「额度获取失败」，点击主按钮 = 重试语义（不弹层）；
 * - 点击「额度」→ 向上弹锚定 Popover，内嵌 QuotaCard 精简渲染 +
 *   「查看额度页」链接（router.push('/usage/quota')）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import QuotaCard from '../usage/QuotaCard.vue';
import { useSessionStore } from '../../stores/session';
import { useQuotaStore } from '../../stores/quota';
import { familyDisplayName, quotaStripSummary } from '../usage/quotaModel';

const props = defineProps<{ sessionId: string }>();

const router = useRouter();
const sessionStore = useSessionStore();
const quotaStore = useQuotaStore();

const session = computed(() => sessionStore.sessions.find((s) => s.id === props.sessionId) || null);
const providerName = computed(() => session.value?.provider || '');
const entry = computed(() => (providerName.value ? quotaStore.quotaFor(providerName.value) : null));
const probing = computed(() => (providerName.value ? quotaStore.probing[providerName.value] === true : false));
const summary = computed(() => {
  // 首次探测进行中（尚无条目）：显示查询中而非误导性的「不可查」
  if (!entry.value && probing.value) {
    return { text: '额度查询中…', tone: 'muted', disabled: true, title: '探测中…' };
  }
  return quotaStripSummary(entry.value);
});

const popoverTitle = computed(() => {
  // 家族未知（unsupported 占位）时留空，让 QuotaCard 回落显示 provider 名
  const family = entry.value?.family ?? '';
  return family ? familyDisplayName(family) : '';
});

// ---- Popover：锚定定位 + 非阻断关闭（对齐 quick-menu / GitPanel 模式） ----
const popoverVisible = ref(false);
const anchorRef = ref<HTMLElement | null>(null);
const popoverRef = ref<HTMLElement | null>(null);
const popoverStyle = ref<Record<string, string>>({});

const POPOVER_WIDTH = 340;
const POPOVER_PAD = 12;

function updatePopoverPosition() {
  const anchor = anchorRef.value;
  if (!anchor) return;
  const rect = anchor.getBoundingClientRect();
  const width = Math.min(POPOVER_WIDTH, window.innerWidth - POPOVER_PAD * 2);
  const left = Math.max(POPOVER_PAD, Math.min(rect.left, window.innerWidth - width - POPOVER_PAD));
  // 向上弹：底边贴锚点顶部 + 8px；视口不够时夹在顶边
  const top = Math.max(POPOVER_PAD, rect.top - 8 - 320);
  popoverStyle.value = { left: `${left}px`, top: `${top}px`, width: `${width}px` };
}

function onPopoverOutsideDown(event: Event) {
  const target = event.target as Node | null;
  if (!target) return;
  if (popoverRef.value?.contains(target)) return;
  if (anchorRef.value?.contains(target)) return;
  popoverVisible.value = false;
}

function onPopoverKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && popoverVisible.value) popoverVisible.value = false;
}

watch(popoverVisible, (visible) => {
  if (visible) {
    updatePopoverPosition();
    window.addEventListener('resize', updatePopoverPosition);
    document.addEventListener('pointerdown', onPopoverOutsideDown, true);
    document.addEventListener('mousedown', onPopoverOutsideDown, true);
    document.addEventListener('keydown', onPopoverKeydown, true);
  } else {
    window.removeEventListener('resize', updatePopoverPosition);
    document.removeEventListener('pointerdown', onPopoverOutsideDown, true);
    document.removeEventListener('mousedown', onPopoverOutsideDown, true);
    document.removeEventListener('keydown', onPopoverKeydown, true);
  }
});

onBeforeUnmount(() => {
  if (popoverVisible.value) {
    window.removeEventListener('resize', updatePopoverPosition);
    document.removeEventListener('pointerdown', onPopoverOutsideDown, true);
    document.removeEventListener('mousedown', onPopoverOutsideDown, true);
    document.removeEventListener('keydown', onPopoverKeydown, true);
  }
});

// ---- 交互 ----

/** 主按钮点击：error 态 = 重试语义；其余弹层（unsupported 已禁用不可点） */
function onMainClick() {
  if (summary.value.tone === 'error') {
    onRefresh();
    return;
  }
  popoverVisible.value = !popoverVisible.value;
}

async function onRefresh() {
  if (!providerName.value || probing.value) return;
  try {
    await quotaStore.probe(providerName.value);
  } catch (err) {
    console.warn('[WebPlaneQuotaStrip] probe failed:', err);
  }
}

function goQuotaPage() {
  popoverVisible.value = false;
  void router.push('/usage/quota');
}

// ---- 数据初始化：先读本地缓存（避免重复探测），再按冷却策略 ensure ----
onMounted(async () => {
  await quotaStore.loadAll({ silent: true });
  if (providerName.value) void quotaStore.ensure(providerName.value);
});

// 会话切换（/resume、fork 等 provider 演进）时重新 ensure
watch(providerName, (name) => {
  popoverVisible.value = false;
  if (name) void quotaStore.ensure(name);
});
</script>

<style scoped>
.quota-strip {
  flex: 0 0 30px;
  height: 30px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 10px;
  border-top: 1px solid var(--separator);
  /* 透皮下 --control 半透明：backdrop-filter 压花保证可读（对齐 ended-bar） */
  background: var(--control);
  backdrop-filter: blur(10px);
}

.quota-strip-main {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  border: none;
  border-radius: 7px;
  padding: 3px 8px;
  background: transparent;
  color: var(--secondary);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  transition: background 0.15s;
}

.quota-strip-main:hover:not(:disabled) { background: var(--controlHover); }
.quota-strip-main:disabled { cursor: not-allowed; opacity: 0.6; }

.quota-strip-icon { font-size: 13px; line-height: 1; color: var(--accent); }
.tone-muted .quota-strip-icon,
.quota-strip-main:disabled .quota-strip-icon { color: var(--tertiary); }
.tone-error .quota-strip-icon { color: var(--danger, #FF3B30); }

.quota-strip-label { flex-shrink: 0; font-weight: 600; }
.tone-muted .quota-strip-summary { color: var(--tertiary); }
.tone-error .quota-strip-summary { color: var(--danger, #FF3B30); }

.quota-strip-summary {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 11px;
  color: var(--label);
}

.quota-strip-refresh {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border: none;
  border-radius: 4px;
  padding: 0;
  background: transparent;
  color: var(--tertiary);
  cursor: pointer;
}

.quota-strip-refresh:hover:not(:disabled) { color: var(--accent); background: var(--controlHover); }
.quota-strip-refresh:disabled { cursor: default; opacity: 0.6; }
.quota-strip-refresh-icon { font-size: 13px; line-height: 1; }
.quota-strip-refresh-icon.spinning { animation: quota-strip-spin 0.8s linear infinite; display: inline-block; }
@keyframes quota-strip-spin { to { transform: rotate(360deg); } }

/* 弹层：与 quick-menu 同为 fixed + Teleport（非阻断） */
.quota-popover {
  position: fixed;
  z-index: 3000;
  display: flex;
  flex-direction: column;
  gap: 8px;
  border-radius: 12px;
  padding: 10px;
  background: var(--card, #fff);
  border: 1px solid var(--separator);
  box-shadow: var(--shadow);
}

.quota-popover :deep(.config-card) {
  /* 弹层内的 QuotaCard 去卡片投影，避免双层卡片感 */
  box-shadow: none;
  border: 1px solid var(--separator);
}

.quota-popover-loading {
  margin: 0;
  padding: 18px 12px;
  color: var(--tertiary);
  font-size: 13px;
  text-align: center;
}

.quota-popover-foot {
  display: flex;
  justify-content: flex-end;
}

.quota-popover-link {
  border: none;
  background: transparent;
  color: var(--accent);
  font-size: 12px;
  cursor: pointer;
  padding: 4px 6px;
  border-radius: 6px;
}
.quota-popover-link:hover { background: var(--control); }
</style>
