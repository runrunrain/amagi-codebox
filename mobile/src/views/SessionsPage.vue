<script setup lang="ts">
/**
 * SessionsPage — PG-02 会话大厅（#/lobby，M2-B）
 * ---------------------------------------------------------------------------
 * 权威依据：Task Contract M2-B；P5 §PG-02/§PG-06（经 Task Contract 内联条款
 * 与同系报告转述）；design §5.2 端点语义 / §8.1-§8.3 错误分类。
 *
 * 组成：
 *   · StatusBar 五层芯片（连接/授权/会话/控制/历史；全正常可折叠，异常自动展开）；
 *   · CLI 启动器（四类 frozen CLI；available=false 禁用+原因；失败 AC-25 分类）；
 *   · 会话卡片列表（名称/CLI/状态/控制者投影/最后活动；overflow 危险菜单）；
 *   · 空态 / 错误态（分类+重试）/ 冲突态（控制权反馈，不静默覆盖）；
 *   · PG-06 危险操作确认流（ConfirmDialog：动词化按钮/后果/不可逆/焦点圈闭/
 *     Esc/提交态防连点）；成功后回执 + 记录占位说明（journal 查询面 M2-C）；
 *   · 观察者语义：无控制权写操作禁用并说明原因；
 *   · 点卡片 → PG-03 工作区占位路由（M2-C，诚实占位）。
 * 凭据：Cookie 唯一凭据载体；本页不展示、不存储任何凭据材料。
 * ---------------------------------------------------------------------------
 */
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { type CLIType, type SessionSummary } from '../lib/contract';
import { useAuthStore } from '../stores/auth';
import { useLobbyStore, type DangerousOperation } from '../stores/lobby';
import StatusBar, { type StatusLayer } from '../components/lobby/StatusBar.vue';
import CliLauncher from '../components/lobby/CliLauncher.vue';
import SessionCard from '../components/lobby/SessionCard.vue';
import ConfirmDialog from '../components/lobby/ConfirmDialog.vue';

const router = useRouter();
const auth = useAuthStore();
const lobby = useLobbyStore();

// --- 授权守卫：未配对直达大厅 → 拦回 PG-01（授权事实永远以服务端为准） ---
onMounted(async () => {
  if (auth.status !== 'paired') {
    await router.replace({ name: 'connect' });
    return;
  }
  await lobby.load();
});

// 授权失效（auth.* 401 于任何大厅请求）：清态踢回 PG-01。
watch(
  () => lobby.authLost,
  async (lost) => {
    if (lost === null) return;
    auth.invalidateAuthorization(lost === 'revoked' ? 'revoked' : 'expired');
    await router.replace({ name: 'connect', query: { reason: lost } });
  },
);

// M3-006 T1（design §6：T1=列表成功/空态/可操作错误态完成渲染）：
// loading true→false 后下一渲染 tick 打 T1——此时成功列表/空态/分类错误+重试
// 三分支之一已进 DOM。auth 失效踢回 PG-01 不是列表可交互终态，不打 T1
// （该导航无 T 样本，recorder 随下次 load 丢弃，fail-closed）。
watch(
  () => lobby.loading,
  async (loading, prev) => {
    if (prev !== true || loading !== false) return;
    await nextTick();
    if (lobby.authLost !== null) return;
    lobby.markListTimingT1();
  },
);

// --- 五层状态投影 ---
const layers = computed<StatusLayer[]>(() => {
  const err = lobby.loadError;
  const connectionLayer: StatusLayer =
    err?.kind === 'connection'
      ? { key: 'connection', label: '连接', text: '异常', tone: 'danger', detail: err.guidance }
      : { key: 'connection', label: '连接', text: '正常', tone: 'ok' };
  const authLayer: StatusLayer = { key: 'auth', label: '授权', text: '已配对', tone: 'ok' };
  const sessionLayer: StatusLayer =
    err !== null && err.kind !== 'connection'
      ? { key: 'session', label: '会话', text: '列表不可用', tone: 'warning', detail: err.guidance }
      : lobby.sessions.length === 0
        ? { key: 'session', label: '会话', text: '无会话', tone: 'neutral' }
        : {
            key: 'session',
            label: '会话',
            text: `${lobby.sessions.length} 个（${lobby.runningCount} 运行中）`,
            tone: lobby.runningCount > 0 ? 'ok' : 'neutral',
          };
  const controlLayer: StatusLayer =
    lobby.controlledCount > 0
      ? { key: 'control', label: '控制', text: `你控制 ${lobby.controlledCount} 个`, tone: 'ok' }
      : { key: 'control', label: '控制', text: '观察中', tone: 'neutral' };
  const historyLayer: StatusLayer = {
    key: 'history',
    label: '历史',
    text: '附着后可见',
    tone: 'neutral',
    detail: '终端回放连续性在进入会话工作区（M2-C）附着后呈现，大厅不伪造。',
  };
  return [connectionLayer, authLayer, sessionLayer, controlLayer, historyLayer];
});

// --- 启动器 ---
async function onLaunch(cliType: CLIType) {
  await lobby.launch(cliType);
}

// --- PG-06 危险操作确认流 ---
interface PendingOperation {
  operation: DangerousOperation;
  session: SessionSummary;
}

const pendingOperation = ref<PendingOperation | null>(null);
const operationSubmitting = ref(false);

const OPERATION_COPY: Record<
  DangerousOperation,
  { title: (t: string) => string; verb: string; consequences: string[]; irreversible: boolean }
> = {
  stop: {
    title: (t) => `停止会话「${t}」？`,
    verb: '停止会话',
    consequences: ['会话进程将被终止，未完成的交互会被中断。', '停止后可通过「重启会话」在同一记录下再次启动。'],
    irreversible: false,
  },
  restart: {
    title: (t) => `重启会话「${t}」？`,
    verb: '重启会话',
    consequences: ['当前运行状态会被中断，随后在相同配置下重新启动。', '会话标识保持不变，此前的终端记录保留在回放窗口内。'],
    irreversible: false,
  },
  remove: {
    title: (t) => `移除会话「${t}」？`,
    verb: '移除会话',
    consequences: ['会话会被终止并从列表中删除。', '该会话的终端输出记录将一并删除。'],
    irreversible: true,
  },
};

const pendingCopy = computed(() => (pendingOperation.value ? OPERATION_COPY[pendingOperation.value.operation] : null));

function requestOperation(operation: DangerousOperation, session: SessionSummary) {
  pendingOperation.value = { operation, session };
}

function cancelOperation() {
  if (operationSubmitting.value) return;
  pendingOperation.value = null;
}

async function confirmOperation() {
  const pending = pendingOperation.value;
  if (pending === null || operationSubmitting.value) return; // 防连点
  operationSubmitting.value = true;
  try {
    await lobby.runDangerousOperation(pending.operation, pending.session);
  } finally {
    operationSubmitting.value = false;
    pendingOperation.value = null;
  }
}

// --- 控制权操作 ---
async function onAcquire(session: SessionSummary) {
  await lobby.acquire(session);
}

async function onRelease(session: SessionSummary) {
  await lobby.release(session);
}

// --- 导航：大厅 → 工作区（PG-03 占位，M2-C 本体） ---
function openWorkspace(session: SessionSummary) {
  router.push({ name: 'workspace', params: { sessionId: session.id } });
}
</script>

<template>
  <div class="lobby-page">
    <header class="lobby-header">
      <div class="lobby-heading">
        <h1 class="lobby-title">会话大厅</h1>
        <p class="lobby-host-line">
          <template v-if="auth.device">{{ auth.device.name }} · </template>
          <template v-if="lobby.host">宿主 {{ lobby.host.serverVersion }} · API {{ lobby.host.apiVersion }}</template>
        </p>
      </div>
      <button
        type="button"
        class="refresh-btn"
        :disabled="lobby.loading"
        aria-label="刷新会话列表"
        @click="lobby.load()"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="23 4 23 10 17 10" />
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
        </svg>
      </button>
    </header>

    <StatusBar :layers="layers" />

    <main class="lobby-main">
      <!-- 操作回执（PG-06 完成后；含记录占位说明，journal 查询面 M2-C 预留） -->
      <div v-if="lobby.lastReceipt" class="receipt" role="status">
        <p class="receipt-text">
          {{ lobby.lastReceipt.resultText }}会话「{{ lobby.lastReceipt.sessionTitle }}」。
        </p>
        <p class="receipt-note">
          操作记录已写入桌面端会话操作日志；远程查询入口将随 M2-C 交付。
        </p>
        <button type="button" class="receipt-dismiss" aria-label="关闭回执" @click="lobby.dismissReceipt()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      <!-- 控制权冲突反馈（不静默覆盖） -->
      <div v-if="lobby.controlConflict" class="conflict-banner" role="alert">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
        <p class="conflict-text">{{ lobby.controlConflict.message }}</p>
        <button type="button" class="receipt-dismiss" aria-label="关闭提示" @click="lobby.dismissConflict()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      <!-- CLI 启动器 -->
      <CliLauncher
        v-if="lobby.loaded && lobby.host"
        :availability="lobby.cliAvailability"
        :launching="lobby.launching"
        @launch="onLaunch"
      />

      <!-- 启动失败分类（AC-25：四类文案，不笼统失败） -->
      <div v-if="lobby.launchError" class="launch-error" role="alert">
        <div class="launch-error-main">
          <p class="launch-error-title">{{ lobby.launchError.title }}</p>
          <p class="launch-error-detail">{{ lobby.launchError.detail }}</p>
          <p class="launch-error-guidance">{{ lobby.launchError.guidance }}</p>
          <p class="launch-error-code">错误码 {{ lobby.launchError.code }}</p>
        </div>
        <button type="button" class="receipt-dismiss" aria-label="关闭错误提示" @click="lobby.dismissLaunchError()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      <!-- 加载骨架（初次） -->
      <div v-if="!lobby.loaded" class="loading-skeleton" role="status" aria-label="正在加载会话列表">
        <div v-for="i in 3" :key="i" class="skeleton-card">
          <div class="skeleton-line skeleton-line--title"></div>
          <div class="skeleton-line skeleton-line--meta"></div>
          <div class="skeleton-line skeleton-line--short"></div>
        </div>
      </div>

      <template v-else>
        <!-- 错误态：分类 + 重试（列表不可用但页面保留可操作面） -->
        <div v-if="lobby.loadError && lobby.sessions.length === 0" class="lobby-error-card" role="alert">
          <p class="error-title">{{ lobby.loadError.title }}</p>
          <p class="error-guidance">{{ lobby.loadError.guidance }}</p>
          <p class="error-code">错误码 {{ lobby.loadError.code }}</p>
          <button type="button" class="btn-secondary" :disabled="lobby.loading" @click="lobby.load()">
            重试
          </button>
        </div>

        <!-- 空态：图标 + 说明 + 主操作 -->
        <div v-else-if="lobby.sessions.length === 0" class="empty-state">
          <div class="empty-icon" aria-hidden="true">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
              <rect x="2" y="3" width="20" height="14" rx="2" />
              <line x1="8" y1="21" x2="16" y2="21" />
              <line x1="12" y1="17" x2="12" y2="21" />
              <path d="M7 8l3 3-3 3" />
              <line x1="13" y1="14" x2="17" y2="14" />
            </svg>
          </div>
          <p class="empty-title">还没有会话</p>
          <p class="empty-desc">从上方选择一类 CLI 启动新会话；启动后即可在这里观察与控制。</p>
        </div>

        <!-- 会话卡片列表 -->
        <div v-else class="session-list" aria-label="会话列表">
          <SessionCard
            v-for="session in lobby.sessions"
            :key="session.id"
            :session="session"
            @open="openWorkspace"
            @acquire="onAcquire"
            @release="onRelease"
            @stop="requestOperation('stop', session)"
            @restart="requestOperation('restart', session)"
            @remove="requestOperation('remove', session)"
          />
        </div>
      </template>
    </main>

    <!-- PG-06 危险操作确认对话 -->
    <ConfirmDialog
      v-if="pendingOperation && pendingCopy"
      :title="pendingCopy.title(pendingOperation.session.title)"
      :verb="pendingCopy.verb"
      :consequences="pendingCopy.consequences"
      :irreversible="pendingCopy.irreversible"
      :submitting="operationSubmitting"
      @confirm="confirmOperation"
      @cancel="cancelOperation"
    />
  </div>
</template>

<style scoped>
.lobby-page {
  min-height: 100%;
  background: var(--VT-canvas);
  color: var(--VT-text);
  padding: 16px 20px 40px;
  /* M4-A safe-area：顶部刘海 + 横屏左右 + 底部 home 指示条 */
  padding-top: calc(16px + env(safe-area-inset-top, 0px));
  padding-left: calc(20px + env(safe-area-inset-left, 0px));
  padding-right: calc(20px + env(safe-area-inset-right, 0px));
  padding-bottom: calc(40px + env(safe-area-inset-bottom, 0px));
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* M4-A 横屏紧凑模式：矮视口压缩页头与间距，列表/启动器优先。 */
@media (orientation: landscape) and (max-height: 500px) {
  .lobby-page {
    gap: 10px;
    padding-top: calc(8px + env(safe-area-inset-top, 0px));
    padding-bottom: calc(16px + env(safe-area-inset-bottom, 0px));
  }
  .lobby-main {
    gap: 10px;
  }
  .lobby-title {
    font-size: 17px;
  }
  .empty-state {
    padding: 16px 24px 12px;
  }
}

.lobby-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.lobby-heading {
  min-width: 0;
}

.lobby-title {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
}

.lobby-host-line {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--VT-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.refresh-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 10px;
  background: transparent;
  color: var(--VT-text);
  cursor: pointer;
  flex-shrink: 0;
}

.refresh-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.refresh-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.lobby-main {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* 回执 */
.receipt {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-success);
  border-radius: 10px;
}

.receipt-text {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--VT-text);
}

.receipt-note {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--VT-text-secondary);
  line-height: 1.5;
}

.receipt-dismiss {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  margin: -12px -10px 0 auto;
  border: none;
  background: transparent;
  color: var(--VT-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  border-radius: 8px;
}

.receipt-dismiss:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

/* 冲突反馈 */
.conflict-banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-warning);
  border-radius: 10px;
  color: var(--VT-warning);
}

.conflict-text {
  margin: 0;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

/* 启动失败分类 */
.launch-error {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-danger);
  border-radius: 10px;
}

.launch-error-main {
  flex: 1;
  min-width: 0;
}

.launch-error-title {
  margin: 0;
  font-size: 14px;
  font-weight: 700;
  color: var(--VT-text);
}

.launch-error-detail {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--VT-text);
  line-height: 1.5;
}

.launch-error-guidance {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
  line-height: 1.5;
}

.launch-error-code,
.error-code {
  margin: 6px 0 0;
  font-size: 11px;
  color: var(--VT-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

/* 骨架 */
.loading-skeleton {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skeleton-card {
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skeleton-line {
  height: 14px;
  border-radius: 4px;
  background: var(--VT-surface-raised);
}

.skeleton-line--title { width: 40%; height: 18px; }
.skeleton-line--meta { width: 65%; }
.skeleton-line--short { width: 35%; }

/* 错误态 */
.lobby-error-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-danger);
  border-radius: 10px;
}

.error-title {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.error-guidance {
  margin: 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
  line-height: 1.5;
}

/* 空态 */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 40px 24px 32px;
  text-align: center;
}

.empty-icon {
  width: 64px;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 16px;
  color: var(--VT-text-secondary);
}

.empty-title {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.empty-desc {
  margin: 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
  line-height: 1.6;
  max-width: 280px;
}

.btn-secondary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  padding: 0 20px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  align-self: flex-start;
}

.btn-secondary:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

/* 会话列表 */
.session-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

@media (hover: hover) {
  .refresh-btn:hover:not(:disabled) {
    background: var(--VT-surface-raised);
  }
  .btn-secondary:hover {
    background: var(--VT-surface-raised);
  }
}

@media (hover: hover) and (pointer: fine) {
  .lobby-page {
    max-width: 720px;
    margin: 0 auto;
    width: 100%;
    box-sizing: border-box;
  }
}

@media (prefers-reduced-motion: reduce) {
  .skeleton-line {
    animation: none;
  }
}
</style>
