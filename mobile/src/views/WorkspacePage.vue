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
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
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
import WebPlaneView from '../components/workspace/WebPlaneView.vue';
import { useSessionWebUI } from '../composables/useSessionWebUI';

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

// --- 会话形态分支（P1-A）：pi/omp 为全屏 TUI，默认进入终端仿真面；其余 CLI 维持结构化时间线 ---
const isTuiCli = computed(() => {
  const cli = store.detail?.cliType;
  return cli === 'pi' || cli === 'omp';
});

// --- Web 会话平面探测与轮询（C2/C4）：仅针对 pi/omp 会话 ---
const {
  state: webuiState,
  url: webuiUrl,
  isAvailable: webuiAvailable,
  refresh: refreshWebUI,
} = useSessionWebUI(sessionId, isTuiCli);

export type WorkspaceView = 'timeline' | 'terminal' | 'webplane';

// 若显式带有 ?view=terminal 则进入终端面；?view=timeline 则进入时间线；?view=webplane 则进入 Web 平面；
// 缺省时：
//   - 非 TUI CLI（claudecode/opencode/codex）：默认结构化时间线（R3）
//   - TUI CLI（pi/omp）：默认视图策略：
//     - webui available → webplane
//     - probing → webplane（显示加载态，0.5–1s 轮询）
//     - unavailable/unknown → terminal（终端仿真，现状不回归）
const activeView = computed<WorkspaceView>(() => {
  const queryView = route.query.view;
  if (queryView === 'terminal') return 'terminal';
  if (queryView === 'timeline') return 'timeline';
  if (queryView === 'webplane') return 'webplane';

  if (!isTuiCli.value) {
    return 'timeline';
  }

  if (webuiState.value === 'available' || webuiState.value === 'probing') {
    return 'webplane';
  }
  return 'terminal';
});

const isTerminalView = computed(() => activeView.value === 'terminal');
const isWebPlaneView = computed(() => activeView.value === 'webplane');
const isTimelineView = computed(() => activeView.value === 'timeline');

// --- Web 平面控制权引导（UX 断层补齐）：iframe 内 POST /api/input 需要会话控制权
// （写面控制门，设备未持权恒 403 control.forbidden），而该视图隐藏外层
// ComposerBar（页面自带输入台）——视图内补显式提示 + 一键接管入口。 ---
// 条件：Web 平面视图 + webui 有 URL + 会话 running + 本设备未持控制权。
// acquire 成功后由 WS control.state 事件驱动 state==='you'，条件自然失效消失。
const webplaneControlHintVisible = computed(
  () =>
    isWebPlaneView.value &&
    !!webuiUrl.value &&
    store.sessionState === 'running' &&
    store.control.state !== 'you',
);

// 三态副行文案（对齐 store.writeBlockReason 既有风格：desktop/other/none 细分）
const webplaneControlHintDetail = computed<string>(() => {
  if (store.control.state === 'desktop') return '桌面端正在控制';
  if (store.control.state === 'other') return `控制权在 ${store.control.deviceName}`;
  return '当前无人持有控制权';
});

// 诊断视图语义：仅非 TUI CLI 主动开启终端网格时标记为「诊断视图」（PG-04 回归保持）
const isDiagnostic = computed(() => !isTuiCli.value && isTerminalView.value);
const menuOpen = ref(false);
const menuBtnRef = ref<HTMLButtonElement | null>(null);
const menuPanelRef = ref<HTMLElement | null>(null);

// M4-A 焦点管理：菜单打开 → 焦点进首项；Esc/点选关闭 → 焦点回触发钮。
watch(menuOpen, async (open) => {
  if (open) {
    await nextTick();
    menuPanelRef.value?.querySelector<HTMLElement>('.menu-item')?.focus();
  } else {
    menuBtnRef.value?.focus();
  }
});

function onMenuKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.stopPropagation();
    menuOpen.value = false;
  }
}

function openDiagnostic(): void {
  menuOpen.value = false;
  void router.push({
    name: 'workspace',
    params: { sessionId: sessionId.value },
    query: { view: 'terminal' },
  });
}

function switchToWebPlane(): void {
  menuOpen.value = false;
  void router.push({
    name: 'workspace',
    params: { sessionId: sessionId.value },
    query: { view: 'webplane' },
  });
}

function switchToTimeline(): void {
  menuOpen.value = false;
  void router.push({
    name: 'workspace',
    params: { sessionId: sessionId.value },
    query: { view: 'timeline' },
  });
}

function switchToTerminal(): void {
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
  // 终端仿真面由 xterm fit 上报真实网格；Web 平面自理；主时间线面维持近似换算，二者不重复上报。
  if (isTerminalView.value || isWebPlaneView.value) return;
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
      <!-- 页面返回按钮：
           1. 诊断面（非 TUI CLI 临时查看终端）：返回主阅读面；
           2. TUI 会话（pi/omp）切到时间线时：返回终端面；
           3. 默认主面（非 TUI 在时间线，或 TUI 在终端/Web 平面）：返回会话大厅 -->
      <button
        v-if="isDiagnostic"
        type="button"
        class="back-btn back-btn--primary"
        @click="leaveDiagnostic"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        <span class="btn-label">返回主阅读面</span>
      </button>
      <button
        v-else-if="isTuiCli && isTimelineView"
        type="button"
        class="back-btn back-btn--primary"
        @click="switchToTerminal"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        <span class="btn-label">返回终端面</span>
      </button>
      <button
        v-else
        type="button"
        class="back-btn"
        aria-label="返回会话大厅"
        @click="router.replace({ name: 'lobby' })"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        <span class="btn-label">大厅</span>
      </button>
      <h1 class="workspace-title">
        <span class="title-text">{{ title }}</span>
        <span v-if="isDiagnostic" class="diagnostic-badge">诊断视图</span>
        <span v-else-if="isTuiCli && isTerminalView" class="tui-cli-badge">终端仿真</span>
        <span v-else-if="isTuiCli && isWebPlaneView" class="webplane-badge">Web 平面</span>
      </h1>
      <div class="menu-wrap" @keydown="onMenuKeydown">
        <button ref="menuBtnRef" type="button" class="menu-btn" aria-label="更多操作" aria-haspopup="menu" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle cx="12" cy="5" r="1" /><circle cx="12" cy="12" r="1" /><circle cx="12" cy="19" r="1" />
          </svg>
        </button>
        <div v-if="menuOpen" ref="menuPanelRef" class="menu-panel" role="menu" aria-label="会话操作菜单">
          <!-- TUI 会话 (pi/omp) 菜单项：三面切换（Web 平面/时间线/终端），webui 非 available 时隐藏 Web 平面入口 -->
          <template v-if="isTuiCli">
            <button
              v-if="webuiAvailable && activeView !== 'webplane'"
              type="button"
              class="menu-item"
              role="menuitem"
              @click="switchToWebPlane"
            >
              切换至 Web 平面视图
            </button>
            <button
              v-if="activeView !== 'terminal'"
              type="button"
              class="menu-item"
              role="menuitem"
              @click="switchToTerminal"
            >
              {{ activeView === 'webplane' ? '切换至终端仿真视图' : '返回终端仿真视图' }}
            </button>
            <button
              v-if="activeView !== 'timeline'"
              type="button"
              class="menu-item"
              role="menuitem"
              @click="switchToTimeline"
            >
              切换至时间线视图
            </button>
            <button
              type="button"
              class="menu-item"
              role="menuitem"
              @click="router.replace({ name: 'lobby' })"
            >
              返回会话大厅
            </button>
          </template>

          <!-- 常规 CLI 会话 (claudecode/opencode/codex) 保持原有菜单行为 -->
          <template v-else>
            <button
              v-if="!isTerminalView"
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
          </template>
        </div>
      </div>
    </header>

    <GuideCard v-if="guideVisible && !isTerminalView && !isWebPlaneView && !store.loading && !store.loadError" @dismiss="dismissGuide" @open-diagnostic="openDiagnostic" />

    <StatusBar :layers="store.statusLayers" :suppress-auto-expand="isWebPlaneView" />

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

    <!-- 主阅读面：结构化内容转化时间线（其余 CLI 默认，或 pi/omp 切换至此） -->
    <TimelineView
      v-if="activeView === 'timeline'"
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

    <!-- 终端仿真面：xterm 仿真网格（pi/omp 默认主面，其余 CLI 诊断面） -->
    <RawTerminalView
      v-else-if="activeView === 'terminal'"
      :subscribe="store.subscribeRawOutput"
      :ws-attached="store.wsState === 'attached'"
      :readonly="!store.canWrite"
      :readonly-reason="store.writeBlockReason"
      @resize="onTerminalGridResize"
      @data="(data: string) => store.sendRaw(data)"
    />

    <!-- Web 会话平面：嵌入 pi webui（pi/omp 默认/首选，C2/C4） -->
    <template v-else-if="activeView === 'webplane'">
      <!-- 控制权引导条（条幅在上、iframe 在下，不遮挡 iframe 输入台；
           接管为显式用户动作，失败走 store 既有 controlNotice 展示） -->
      <div
        v-if="webplaneControlHintVisible"
        class="banner banner--warning webplane-control-hint"
        role="status"
        aria-label="Web 平面控制权提示"
        data-testid="webplane-control-hint"
      >
        <div class="banner-body">
          <strong>Web 平面输入需要会话控制权</strong>
          <span>{{ webplaneControlHintDetail }}</span>
        </div>
        <button
          type="button"
          class="banner-action"
          data-testid="webplane-acquire-btn"
          @click="store.acquire()"
        >
          接管控制
        </button>
      </div>
      <!-- probing 探测加载态（0.5–1s 轮询中） -->
      <div
        v-if="webuiState === 'probing'"
        class="webplane-probing"
        role="status"
        data-testid="webplane-probing"
      >
        <div class="webplane-spinner" aria-hidden="true" />
        <span>正在连接 Web 会话平面…</span>
      </div>
      <!-- Web 会话平面 iframe 宿主 -->
      <WebPlaneView
        v-else-if="webuiUrl"
        :url="webuiUrl"
        :session-id="sessionId"
        :ended="webuiState === 'ended'"
        @retry="refreshWebUI"
        @switch-to-terminal="switchToTerminal"
        @switch-to-timeline="switchToTimeline"
      />
      <!-- 显式进入 ?view=webplane 但不可用时的降级提示 -->
      <div
        v-else
        class="webplane-unavailable-card"
        role="alert"
        data-testid="webplane-unavailable"
      >
        <strong>Web 会话平面当前不可用</strong>
        <span>pi webui 服务未就绪或未安装对应插件。</span>
        <button type="button" class="fallback-btn" @click="switchToTerminal">
          切换至终端仿真
        </button>
      </div>
    </template>

    <!-- Composer：两面同一组件/同一 store 过滤路径；terminalMode 激活 KeyTray；
         Web 会话平面内隐藏外层 ComposerBar 与 KeyTray（页面自带输入台，避免双输入台） -->
    <ComposerBar
      v-if="activeView !== 'webplane'"
      :draft="store.draft"
      :sending="store.sending"
      :stopping="store.stopping"
      :can-write="store.canWrite"
      :can-control="store.control.state === 'you'"
      :block-reason="store.writeBlockReason"
      :history="store.commandHistory"
      :outbox="store.outboxView"
      :terminal-mode="activeView === 'terminal'"
      @update:draft="(v: string) => (store.draft = v)"
      @send="store.sendDraft()"
      @stop="stopConfirmOpen = true"
      @reuse="(t: string) => store.reuseCommand(t)"
      @special-key="(seq: string) => store.sendRaw(seq)"
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
  /* M4-A：软键盘收缩跟随 visualViewport（iOS 必需；Android 与 dvh 一致；
     无 API 时回落 dvh，不劣化）。 */
  height: var(--vvh, 100dvh);
  background: var(--VT-canvas);
  color: var(--VT-text);
}

.workspace-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  /* M4-A safe-area：顶部刘海 + 横屏左右 */
  padding-top: calc(8px + env(safe-area-inset-top, 0px));
  padding-left: calc(12px + env(safe-area-inset-left, 0px));
  padding-right: calc(12px + env(safe-area-inset-right, 0px));
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
  /* M4-A safe-area：横屏下横幅不贴刘海边 */
  margin-left: calc(12px + env(safe-area-inset-left, 0px));
  margin-right: calc(12px + env(safe-area-inset-right, 0px));
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

.tui-cli-badge {
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

.webplane-badge {
  display: inline-block;
  flex-shrink: 0;
  padding: 2px 8px;
  border: 1px solid var(--VT-control);
  border-radius: 999px;
  color: var(--VT-control);
  font-size: 11px;
  font-weight: 600;
  vertical-align: middle;
}

.webplane-control-hint {
  /* 引导条与 iframe 之间收紧间距，内容区让位输入台；左右沿用 banner safe-area */
  margin-bottom: 0;
}

.webplane-probing {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  background: var(--VT-surface);
  color: var(--VT-text-secondary);
  font-size: 14px;
  padding: 24px 16px;
  text-align: center;
}

.webplane-spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--VT-border);
  border-top-color: var(--VT-accent);
  border-radius: 50%;
  animation: plane-spin 0.8s linear infinite;
}

@keyframes plane-spin {
  to {
    transform: rotate(360deg);
  }
}

.webplane-unavailable-card {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  background: var(--VT-surface);
  color: var(--VT-text);
  padding: 24px 16px;
  text-align: center;
}

.webplane-unavailable-card > strong {
  font-size: 16px;
  color: var(--VT-text);
}

.webplane-unavailable-card > span {
  font-size: 13px;
  color: var(--VT-text-secondary);
  max-width: 280px;
}

.fallback-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  padding: 0 16px;
  font-size: 14px;
  font-weight: 600;
  border-radius: 8px;
  border: 1px solid var(--VT-border-strong);
  background: var(--VT-surface-raised);
  color: var(--VT-text);
  cursor: pointer;
  touch-action: manipulation;
}

.fallback-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

/* M4-R1：超窄逻辑视口（≤240px，含 200% 缩放等效 180px）header 防裁切。
   实测（谛听 M4-006 补全覆盖后浮出）：诊断面 back-btn「返回主阅读面」自然宽
   ~126px + menu-btn 44px 在 180px 下溢出右缘 18px——flex 溢出被裁不产生文档
   级横向滚动，scrollWidth 检测为盲。优先级：返回动作 > 菜单 > 标题；诊断身份
   由主色返回钮与菜单项承担，badge 让位隐藏；btn-label ellipsis 保底。 */
@media (max-width: 240px) {
  .workspace-header {
    gap: 6px;
    padding-left: calc(8px + env(safe-area-inset-left, 0px));
    padding-right: calc(8px + env(safe-area-inset-right, 0px));
  }
  .back-btn {
    flex-shrink: 1;
    min-width: 44px;
    overflow: hidden;
  }
  .back-btn .btn-label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .diagnostic-badge,
  .tui-cli-badge,
  .webplane-badge {
    display: none;
  }
}

/* M4-A 横屏紧凑模式：矮视口（手机横屏 ≤500px）头部/横幅压缩，
   主阅读面与时间线优先占高；竖屏与桌面不受影响。 */
@media (orientation: landscape) and (max-height: 500px) {
  .workspace-header {
    padding-top: calc(4px + env(safe-area-inset-top, 0px));
    padding-bottom: 4px;
  }
  .back-btn {
    min-height: 44px;
    padding-top: 0;
    padding-bottom: 0;
  }
  .workspace-title {
    font-size: 15px;
  }
  .banner {
    margin-top: 4px;
    margin-bottom: 4px;
    padding: 6px 12px;
  }
}
</style>
