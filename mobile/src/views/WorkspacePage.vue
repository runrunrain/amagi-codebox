<script setup lang="ts">
/**
 * WorkspacePage — PG-03 会话工作区本体（M2-C）+ PG-04 诊断视图（M2-D）
 * ---------------------------------------------------------------------------
 * 权威依据：Task Contract M2-C/M2-D + CHG-20260801-05（内容转化唯一主形态；
 * 原始终端降级为按需诊断视图——路由 ?view=terminal，菜单进入、非默认、
 * 非并列 tab；显式「停止运行」按钮；禁 KeyTray）。
 * 组成：header（返回/标题/菜单）→ E-09 引导 → StatusBar 五层（复用 M2-B）
 * → ControlBar → TimelineView（结构化主面）或 RawTerminalView（诊断面）
 * → ComposerBar（两面同一组件，权限语义不变——P-04）。
 * ---------------------------------------------------------------------------
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useWorkspaceStore } from '../stores/workspace';
import StatusBar from '../components/lobby/StatusBar.vue';
import ConfirmDialog from '../components/lobby/ConfirmDialog.vue';
import TimelineView from '../components/workspace/TimelineView.vue';
import ComposerBar from '../components/workspace/ComposerBar.vue';
import ControlBar from '../components/workspace/ControlBar.vue';
import ContinuityBanner from '../components/workspace/ContinuityBanner.vue';
import GuideCard from '../components/workspace/GuideCard.vue';
import RawTerminalView from '../components/workspace/RawTerminalView.vue';

const GUIDE_DISMISSED_KEY = 'amagi.pg03.guide.dismissed';

const route = useRoute();
const router = useRouter();
const store = useWorkspaceStore();

const sessionId = computed(() => String(route.params.sessionId ?? ''));

// --- E-09 引导（首次进入；关闭状态 localStorage，不含敏感信息） ---
const guideVisible = ref(false);
try {
  guideVisible.value = localStorage.getItem(GUIDE_DISMISSED_KEY) !== '1';
} catch {
  guideVisible.value = true;
}

function dismissGuide(): void {
  guideVisible.value = false;
  try {
    localStorage.setItem(GUIDE_DISMISSED_KEY, '1');
  } catch {
    /* 存储不可用时仅本次会话有效 */
  }
}

// --- PG-04 诊断视图（M2-D）：?view=terminal（P5 v1.2 §2.1 权威路由形态） ---
// 菜单进入用 push（Android 返回键 = 回到结构化面，§2.2 导航规则 3）；
// 直接深链允许（诊断场景可分享链接），页内明示诊断身份与返回入口。
// 同一 route record 的 query 变化不重挂载组件——WS attach/时间线状态保留，
// 诊断视图复用同一会话订阅，不重连。
const isDiagnostic = computed(() => route.query.view === 'terminal');
const menuOpen = ref(false);

function openDiagnostic(): void {
  menuOpen.value = false;
  void router.push({
    name: 'workspace',
    params: { sessionId: sessionId.value },
    query: { view: 'terminal' },
  });
}

/** 返回主阅读面（结构化面）：优先历史回退（与 Android 返回键同语义）；
 * 深链无历史时 replace 去掉 view 参数。 */
function leaveDiagnostic(): void {
  menuOpen.value = false;
  if (window.history.length > 1 && window.history.state?.back) {
    router.back();
  } else {
    void router.replace({ name: 'workspace', params: { sessionId: sessionId.value } });
  }
}

/** 诊断视图 fit 后的真实网格上报（与主面同一 sendResize 路径，PR-04 不变）。 */
function onTerminalGridResize(cols: number, rows: number): void {
  store.sendResize(cols, rows);
}

// --- 停止运行（显式按钮 → PG-06 确认） ---
const stopConfirmOpen = ref(false);

async function confirmStop(): Promise<void> {
  const ok = await store.stopRunning();
  if (ok) stopConfirmOpen.value = false;
}

// --- 生命周期 ---
onMounted(() => {
  void store.open(sessionId.value);
});
onUnmounted(() => {
  store.close();
});
watch(sessionId, (id, prev) => {
  if (id && id !== prev) void store.open(id);
});

// --- 授权失效：清态踢回 PG-01（同 M2-B 纪律） ---
watch(
  () => store.authLost,
  async (lost) => {
    if (lost) await router.replace({ name: 'connect', query: { reason: lost } });
  },
);

// --- resize：附着后上报终端尺寸（近似网格；窗口变化去抖重发） ---
let resizeTimer: ReturnType<typeof setTimeout> | null = null;

function reportResize(): void {
  // 诊断视图由 xterm fit 上报真实网格；主面维持近似换算，二者不重复上报。
  if (isDiagnostic.value) return;
  const cols = Math.max(20, Math.round(window.innerWidth / 8.2));
  const rows = Math.max(6, Math.round(window.innerHeight / 18));
  store.sendResize(cols, rows);
}

function onWindowResize(): void {
  if (resizeTimer) clearTimeout(resizeTimer);
  resizeTimer = setTimeout(reportResize, 250);
}

watch(
  () => store.wsState,
  (state) => {
    if (state === 'attached') reportResize();
  },
);
onMounted(() => window.addEventListener('resize', onWindowResize));
onUnmounted(() => {
  window.removeEventListener('resize', onWindowResize);
  if (resizeTimer) clearTimeout(resizeTimer);
});

const title = computed(() => store.detail?.title ?? sessionId.value);

// --- M3-C NoticeStack 优先级（design §7）：P0 fatal > P1 lastError > P2 E-07 > P3 degraded ---
// M3-008：优先级不仅计算还实际控制渲染——P1/P3 横幅按 noticeLevel 互斥，
// P0 隐藏 E-06 notice 与 ContinuityBanner 并禁用 ControlBar；P1 暂压 E-07/P3，
// dismiss 后若状态仍有效则由 v-if 条件自然恢复（store 状态不被压制方改变）。
const noticeLevel = computed(() => store.primaryNotice);

// E-07「跳到缺口」：滚动到首个原位缺口标记。
const timelineRef = ref<InstanceType<typeof TimelineView> | null>(null);

function jumpToGap(): void {
  const id = store.firstGapEntryId;
  if (id) timelineRef.value?.scrollToItem(id);
}
</script>

<template>
  <div class="workspace-page">
    <header class="workspace-header">
      <!-- 结构化面：返回大厅；诊断面：返回主阅读面（P5 §PG-04 离开方式） -->
      <button
        v-if="!isDiagnostic"
        type="button"
        class="back-btn"
        aria-label="返回会话大厅"
        @click="router.replace({ name: 'lobby' })"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        大厅
      </button>
      <button
        v-else
        type="button"
        class="back-btn back-btn--primary"
        @click="leaveDiagnostic"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        返回主阅读面
      </button>
      <h1 class="workspace-title">
        <span class="title-text">{{ title }}</span>
        <span v-if="isDiagnostic" class="diagnostic-badge">诊断视图</span>
      </h1>
      <div class="menu-wrap">
        <button type="button" class="menu-btn" aria-label="更多操作" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle cx="12" cy="5" r="1" /><circle cx="12" cy="12" r="1" /><circle cx="12" cy="19" r="1" />
          </svg>
        </button>
        <div v-if="menuOpen" class="menu-panel" role="menu">
          <button
            v-if="!isDiagnostic"
            type="button"
            class="menu-item"
            role="menuitem"
            @click="openDiagnostic"
          >
            原始终端诊断视图
          </button>
          <button
            v-else
            type="button"
            class="menu-item"
            role="menuitem"
            @click="router.replace({ name: 'lobby' })"
          >
            返回会话大厅
          </button>
        </div>
      </div>
    </header>

    <GuideCard v-if="guideVisible && !isDiagnostic && !store.loading && !store.loadError" @dismiss="dismissGuide" @open-diagnostic="openDiagnostic" />

    <StatusBar :layers="store.statusLayers" />

    <ControlBar
      :control="store.control"
      :notice="noticeLevel === 'fatal' ? null : store.controlNotice"
      :busy="store.wsState !== 'attached' || noticeLevel === 'fatal'"
      @acquire="store.acquire()"
      @release="store.release()"
      @dismiss-notice="store.controlNotice = null"
    />

    <!-- E-07 ContinuityBanner（design §7：ControlBar 之后；P0/P1 压制时隐藏） -->
    <ContinuityBanner
      v-if="store.recoveryEpisode && noticeLevel === 'recovery'"
      :episode="store.recoveryEpisode"
      @jump-gap="jumpToGap"
      @dismiss="store.dismissRecovery()"
    />

    <!-- 加载/错误/降级横幅 -->
    <div v-if="store.loading" class="banner banner--neutral" role="status">正在加载会话…</div>
    <div v-else-if="store.loadError" class="banner banner--danger" role="alert">
      <div class="banner-body">
        <strong>{{ store.loadError.title }}</strong>
        <span>{{ store.loadError.guidance }}</span>
        <span class="banner-code">{{ store.loadError.code }}</span>
      </div>
      <button type="button" class="banner-action" @click="store.open(sessionId)">重试</button>
    </div>
    <!-- M3-008：P3/P1 横幅按 noticeLevel 互斥（design §7 冻结优先级）；
         高压级在位时压制，dismiss 后若状态仍有效则自然恢复。 -->
    <div v-if="noticeLevel === 'degraded' && store.degradedNotice" class="banner banner--warning" role="status">
      <div class="banner-body"><span>{{ store.degradedNotice }}</span></div>
      <button type="button" class="banner-action" @click="store.dismissDegraded()">知道了</button>
    </div>
    <div v-if="noticeLevel === 'error' && store.lastError" class="banner banner--danger" role="alert">
      <div class="banner-body">
        <span>{{ store.lastError.message }}</span>
        <span class="banner-code">{{ store.lastError.code }}</span>
      </div>
      <button type="button" class="banner-action" @click="store.dismissError()">关闭</button>
    </div>
    <div v-if="store.sessionState === 'removed'" class="banner banner--danger" role="alert">
      <div class="banner-body"><strong>会话已被移除</strong><span>它不再存在，请返回大厅。</span></div>
      <button type="button" class="banner-action" @click="router.replace({ name: 'lobby' })">返回大厅</button>
    </div>
    <!-- P0 terminal fatal（design §7：terminal 覆盖恢复条，不再显示「已恢复」） -->
    <div v-if="store.wsState === 'closed' && store.terminalReason" class="banner banner--danger" role="alert" data-testid="terminal-banner">
      <div class="banner-body"><strong>连接已终止</strong><span>{{ store.terminalReason }}</span></div>
      <button type="button" class="banner-action" @click="router.replace({ name: 'lobby' })">返回大厅</button>
    </div>

    <!-- 主阅读面：结构化内容转化时间线（唯一主形态） -->
    <TimelineView
      v-if="!isDiagnostic"
      ref="timelineRef"
      :items="store.timelineItems"
      :output-version="store.latestSeq"
      :can-answer="store.canWrite"
      :can-control="store.control.state === 'you'"
      :stopping="store.stopping"
      :filling-gap-ids="store.fillingGapIds"
      @answer="(input: string) => store.sendAnswer(input)"
      @stop="stopConfirmOpen = true"
      @fill-gap="(id: string) => store.requestGapFill(id)"
      @open-diagnostic="openDiagnostic"
    />

    <!-- PG-04 诊断面：原始输出只读网格（按需，非并列 tab；xterm 动态导入） -->
    <RawTerminalView
      v-else
      :initial-transcript="store.getRawTranscript()"
      :subscribe="store.subscribeRawOutput"
      :ws-attached="store.wsState === 'attached'"
      @resize="onTerminalGridResize"
    />

    <!-- Composer：两面同一组件/同一 store 过滤路径——诊断视图不扩权限（P-04） -->
    <ComposerBar
      :draft="store.draft"
      :sending="store.sending"
      :stopping="store.stopping"
      :can-write="store.canWrite"
      :can-control="store.control.state === 'you'"
      :block-reason="store.writeBlockReason"
      :history="store.commandHistory"
      :outbox="store.outboxView"
      @update:draft="(v: string) => (store.draft = v)"
      @send="store.sendDraft()"
      @stop="stopConfirmOpen = true"
      @reuse="(t: string) => store.reuseCommand(t)"
    />

    <!-- 停止运行确认（PG-06 复用 M2-B ConfirmDialog） -->
    <ConfirmDialog
      v-if="stopConfirmOpen"
      title="停止运行"
      verb="停止运行"
      :consequences="['会话进程将被停止', '进行中的输出会中断', '会话保留，可稍后重启']"
      :submitting="store.stopping"
      @confirm="confirmStop"
      @cancel="stopConfirmOpen = false"
    />
  </div>
</template>

<style scoped>
.workspace-page {
  display: flex;
  flex-direction: column;
  height: 100dvh;
  background: var(--VT-canvas);
  color: var(--VT-text);
}

.workspace-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
}

.back-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  min-height: 44px;
  padding: 0 12px 0 6px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}

.back-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .back-btn:hover {
    background: var(--VT-surface-raised);
  }
}

.workspace-title {
  flex: 1;
  min-width: 0;
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 17px;
  font-weight: 700;
}

.title-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.menu-wrap {
  position: relative;
  flex-shrink: 0;
}

.menu-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  cursor: pointer;
}

.menu-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.menu-panel {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  z-index: 20;
  min-width: 200px;
  padding: 6px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
}

.menu-item {
  width: 100%;
  min-height: 44px;
  padding: 6px 10px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 14px;
  text-align: left;
  cursor: pointer;
}

@media (hover: hover) {
  .menu-item:hover {
    background: var(--VT-surface-raised);
  }
}

.menu-item:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.banner {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 6px 12px;
  padding: 10px 12px;
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.5;
}

.banner--neutral {
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  color: var(--VT-text-secondary);
}

.banner--warning {
  background: var(--VT-surface);
  border: 1px solid var(--VT-warning);
  border-left: 4px solid var(--VT-warning);
  color: var(--VT-text);
}

.banner--danger {
  background: var(--VT-surface);
  border: 1px solid var(--VT-danger);
  border-left: 4px solid var(--VT-danger);
  color: var(--VT-text);
}

.banner-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.banner-code {
  font-size: 11px;
  color: var(--VT-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.banner-action {
  flex-shrink: 0;
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.banner-action:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

/* PG-04 诊断视图身份标识与返回入口 */
.back-btn--primary {
  background: var(--VT-accent-strong);
  border-color: var(--VT-accent-strong);
  color: var(--VT-canvas);
}

@media (hover: hover) {
  .back-btn--primary:hover {
    background: var(--VT-accent-strong);
    opacity: 0.92;
  }
}

.diagnostic-badge {
  display: inline-block;
  flex-shrink: 0;
  padding: 2px 8px;
  border: 1px solid var(--VT-accent-strong);
  border-radius: 999px;
  color: var(--VT-accent-strong);
  font-size: 11px;
  font-weight: 600;
  vertical-align: middle;
}
</style>
