<script setup lang="ts">
/**
 * SessionCard — PG-02 会话卡片（M2-B）
 * ---------------------------------------------------------------------------
 * 契约（Task Contract M2-B / design §5.3 投影）：
 *   · 字段：名称（host 安全 title）/ CLI 类型 / 运行状态 / 控制者投影
 *     （ControlSnapshot 四变体）/ 最后活动时间；
 *   · overflow 危险菜单：获取/释放控制权、停止、重启、移除（⋯ 44px 按钮）；
 *   · 观察者语义：无控制权时写操作禁用并说明原因（disabled + reason，不伪装可点）；
 *   · 状态呈现：图标 + 文字 + 颜色三通道，禁用彩色圆点。
 * ---------------------------------------------------------------------------
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { SessionState, SessionSummary } from '../../lib/contract';
import { cliLabel, controlProjection } from '../../stores/lobby';

const props = defineProps<{
  session: SessionSummary;
}>();

const emit = defineEmits<{
  open: [session: SessionSummary];
  acquire: [session: SessionSummary];
  release: [session: SessionSummary];
  stop: [session: SessionSummary];
  restart: [session: SessionSummary];
  remove: [session: SessionSummary];
}>();

const control = computed(() => controlProjection(props.session.control));

// --- 运行状态三通道映射（图标 + 文字 + 颜色；removed 永不出现在列表，design §5.3） ---
const STATE_MAP: Record<Exclude<SessionState, 'removed'>, { label: string; tone: 'ok' | 'warning' | 'neutral'; icon: string }> = {
  running: { label: '运行中', tone: 'ok', icon: 'M5 3l14 9-14 9V3z' },
  stopped: { label: '已停止', tone: 'neutral', icon: 'M6 6h12v12H6z' },
  exited: { label: '已退出', tone: 'neutral', icon: 'M18 6L6 18M6 6l12 12' },
  unavailable: { label: '不可用', tone: 'warning', icon: 'M12 9v4M12 17h.01M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z' },
};

const stateMeta = computed(() => STATE_MAP[props.session.state as Exclude<SessionState, 'removed'>] ?? STATE_MAP.unavailable);

// --- 最后活动时间（相对时间，等宽数字） ---
const relativeActivity = computed(() => {
  const ts = Date.parse(props.session.lastActivityAt);
  if (Number.isNaN(ts)) return props.session.lastActivityAt;
  const diffSec = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (diffSec < 60) return '刚刚';
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)} 分钟前`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)} 小时前`;
  return `${Math.floor(diffSec / 86400)} 天前`;
});

// --- overflow 菜单 ---
const menuOpen = ref(false);
const menuRef = ref<HTMLElement | null>(null);

function toggleMenu() {
  menuOpen.value = !menuOpen.value;
}

function closeMenu() {
  menuOpen.value = false;
}

function onDocPointerDown(event: Event) {
  if (!menuOpen.value) return;
  const root = menuRef.value;
  if (root && event.target instanceof Node && !root.contains(event.target)) closeMenu();
}

function onDocKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && menuOpen.value) closeMenu();
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocPointerDown);
  document.addEventListener('keydown', onDocKeydown);
});

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointerDown);
  document.removeEventListener('keydown', onDocKeydown);
});

function onCardClick() {
  emit('open', props.session);
}

function onCardKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    emit('open', props.session);
  }
}

type MenuAction = 'acquire' | 'release' | 'stop' | 'restart' | 'remove';

function onMenuAction(action: MenuAction) {
  closeMenu();
  const s = props.session;
  if (action === 'acquire') emit('acquire', s);
  else if (action === 'release') emit('release', s);
  else if (action === 'stop') emit('stop', s);
  else if (action === 'restart') emit('restart', s);
  else emit('remove', s);
}
</script>

<template>
  <article
    class="session-card"
    tabindex="0"
    role="button"
    :aria-label="`打开会话 ${session.title}`"
    @click="onCardClick"
    @keydown="onCardKeydown"
  >
    <div class="card-main">
      <!-- 顶行：CLI 类型 + 运行状态（三通道） -->
      <div class="card-top">
        <span class="cli-badge">{{ cliLabel(session.cliType) }}</span>
        <span class="state-chip" :class="`state-chip--${stateMeta.tone}`">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path :d="stateMeta.icon" />
          </svg>
          {{ stateMeta.label }}
        </span>
      </div>

      <!-- 名称（host 生成的安全 title，不含 provider/model/终端内容） -->
      <h3 class="session-title">{{ session.title }}</h3>

      <!-- 控制者投影 + 最后活动 -->
      <div class="card-meta">
        <span class="control-chip" :class="{ 'control-chip--you': session.control.state === 'you' }">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M18 11V6a2 2 0 0 0-4 0v5M14 10V4a2 2 0 0 0-4 0v6M10 10.5V6a2 2 0 0 0-4 0v8M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15" />
          </svg>
          {{ control.text }}
        </span>
        <span class="activity-time">最后活动 · {{ relativeActivity }}</span>
      </div>
    </div>

    <!-- overflow 危险菜单 -->
    <div ref="menuRef" class="card-menu" @click.stop @keydown.stop>
      <button
        type="button"
        class="menu-btn"
        aria-haspopup="menu"
        :aria-expanded="menuOpen"
        :aria-label="`会话 ${session.title} 的操作菜单`"
        @click="toggleMenu"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="12" cy="5" r="1.8" />
          <circle cx="12" cy="12" r="1.8" />
          <circle cx="12" cy="19" r="1.8" />
        </svg>
      </button>

      <div v-if="menuOpen" class="menu-pop" role="menu">
        <!-- 控制权操作 -->
        <button
          v-if="session.control.state === 'none'"
          type="button"
          role="menuitem"
          class="menu-item"
          @click="onMenuAction('acquire')"
        >
          获取控制权
        </button>
        <button
          v-else-if="session.control.state === 'you'"
          type="button"
          role="menuitem"
          class="menu-item"
          @click="onMenuAction('release')"
        >
          释放控制权
        </button>
        <div v-else class="menu-item menu-item--note" role="menuitem" aria-disabled="true">
          控制权被占用，无法获取
        </div>

        <div class="menu-divider" aria-hidden="true"></div>

        <!-- 危险操作：观察者语义（无控制权禁用 + 原因） -->
        <button
          type="button"
          role="menuitem"
          class="menu-item menu-item--danger"
          :disabled="!control.writable"
          @click="onMenuAction('stop')"
        >
          停止会话…
        </button>
        <button
          type="button"
          role="menuitem"
          class="menu-item menu-item--danger"
          :disabled="!control.writable"
          @click="onMenuAction('restart')"
        >
          重启会话…
        </button>
        <button
          type="button"
          role="menuitem"
          class="menu-item menu-item--danger"
          :disabled="!control.writable"
          @click="onMenuAction('remove')"
        >
          移除会话…
        </button>
        <div v-if="!control.writable" class="menu-reason" role="note">
          {{ control.reason }}
        </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.session-card {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  padding: 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
  cursor: pointer;
}

.session-card:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.session-card:active {
  background: var(--VT-surface-raised);
}

.card-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.cli-badge {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.3px;
  padding: 3px 8px;
  border-radius: 5px;
  border: 1px solid var(--VT-border-strong);
  color: var(--VT-text);
  background: var(--VT-canvas);
  line-height: 1.4;
  flex-shrink: 0;
}

.state-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  font-weight: 600;
  color: var(--VT-text);
}

.state-chip--ok svg {
  color: var(--VT-success);
}

.state-chip--warning svg {
  color: var(--VT-warning);
}

.state-chip--neutral svg {
  color: var(--VT-secondary);
}

.session-title {
  margin: 0;
  font-size: 16px;
  font-weight: 700;
  color: var(--VT-text);
  line-height: 1.4;
  word-break: break-word;
}

.card-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 12px;
}

.control-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  font-weight: 600;
  color: var(--VT-text);
}

.control-chip svg {
  color: var(--VT-secondary);
}

.control-chip--you {
  color: var(--VT-control);
}

.control-chip--you svg {
  color: var(--VT-control);
}

.activity-time {
  font-size: 12px;
  color: var(--VT-text-secondary);
  font-variant-numeric: tabular-nums;
}

/* overflow 菜单 */
.card-menu {
  position: relative;
  flex-shrink: 0;
}

.menu-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  margin: -10px -10px 0 0;
  border: none;
  background: transparent;
  color: var(--VT-text-secondary);
  cursor: pointer;
  border-radius: 8px;
}

.menu-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.menu-pop {
  position: absolute;
  top: 36px;
  right: -6px;
  z-index: 50;
  min-width: 200px;
  background: var(--VT-canvas);
  border: 1px solid var(--VT-border-strong);
  border-radius: 10px;
  padding: 6px;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
}

.menu-item {
  display: block;
  width: 100%;
  min-height: 44px;
  padding: 10px 12px;
  border: none;
  background: transparent;
  color: var(--VT-text);
  font-size: 14px;
  font-weight: 600;
  text-align: left;
  cursor: pointer;
  border-radius: 6px;
}

.menu-item:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: -2px;
}

.menu-item--danger {
  color: var(--VT-danger);
}

.menu-item:disabled {
  color: var(--VT-text-disabled);
  cursor: not-allowed;
}

.menu-item--note {
  color: var(--VT-text-secondary);
  font-weight: 500;
  cursor: default;
}

.menu-divider {
  height: 1px;
  margin: 4px 8px;
  background: var(--VT-border);
}

.menu-reason {
  padding: 8px 12px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--VT-text-secondary);
  border-top: 1px dashed var(--VT-border);
}

@media (hover: hover) {
  .session-card:hover {
    background: var(--VT-surface-raised);
    border-color: var(--VT-border-strong);
  }
  .menu-item:hover:not(:disabled) {
    background: var(--VT-surface-raised);
  }
}

@media (prefers-reduced-motion: reduce) {
  .session-card,
  .menu-pop {
    transition: none;
  }
}
</style>
