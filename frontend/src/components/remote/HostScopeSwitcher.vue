<!--
  HostScopeSwitcher（RC1-6 桌面端互联 · 主机切换层）
  交互稿 §1：全局位于侧栏顶部（本应用无顶栏，侧栏顶部为常驻全局位），
  下拉 = 本机 + 已登记主机（状态灯绿/灰/红）+ 添加入口；
  快捷键 Cmd/Ctrl+Shift+H 呼出菜单（不偷输入焦点，§5）。
-->
<template>
  <div class="host-switcher" ref="rootEl">
    <button
      type="button"
      class="hs-trigger"
      :class="{ remote: store.isRemoteMode }"
      aria-haspopup="menu"
      :aria-expanded="open"
      title="切换主机（Cmd/Ctrl+Shift+H）"
      @click="toggleMenu"
    >
      <span class="hs-dot" :class="triggerDotClass" aria-hidden="true"></span>
      <span class="hs-label">{{ triggerLabel }}</span>
      <Badge v-if="store.isRemoteMode" type="scope" text="远程" />
      <span class="hs-caret" aria-hidden="true">▾</span>
    </button>

    <Teleport to="body">
      <transition name="hs-fade">
        <div
          v-if="open"
          ref="menuEl"
          class="hs-menu"
          role="menu"
          aria-label="主机切换"
          :style="menuStyle"
        >
          <button
            type="button"
            class="hs-item"
            :class="{ selected: !store.isRemoteMode }"
            role="menuitemradio"
            :aria-checked="!store.isRemoteMode"
            :disabled="switching"
            @click="chooseLocal"
          >
            <span class="hs-dot dot-green" aria-hidden="true"></span>
            <span class="hs-item-main">
              <span class="hs-item-name">本机</span>
              <span class="hs-item-sub">默认</span>
            </span>
            <svg v-if="!store.isRemoteMode" class="hs-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>

          <div class="hs-group-label">已登记主机</div>
          <template v-if="store.hosts.length > 0">
            <button
              v-for="h in store.hosts"
              :key="h.id"
              type="button"
              class="hs-item"
              :class="{ selected: store.scope === h.id && store.isRemoteMode }"
              role="menuitemradio"
              :aria-checked="store.scope === h.id && store.isRemoteMode"
              :disabled="switching"
              @click="chooseHost(h)"
            >
              <span class="hs-dot" :class="dotClass(h.health)" aria-hidden="true"></span>
              <span class="hs-item-main">
                <span class="hs-item-name">{{ h.displayName }}</span>
                <span class="hs-item-sub">{{ h.hostPort }} · {{ hostHealthLabel(h.health) }}<template v-if="!h.deviceId">（未配对）</template></span>
              </span>
              <svg v-if="store.scope === h.id && store.isRemoteMode" class="hs-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
            </button>
          </template>
          <div v-else class="hs-empty">尚未登记主机</div>

          <div class="hs-divider" aria-hidden="true"></div>
          <button type="button" class="hs-item hs-add" role="menuitem" @click="openWizard">
            <span class="hs-plus" aria-hidden="true">＋</span>
            <span class="hs-item-name">添加主机…</span>
          </button>
        </div>
      </transition>
    </Teleport>

    <!-- 配对向导（Dialog 内部 Teleport 到 body） -->
    <PairingWizardDialog
      :open="store.pairingWizardOpen"
      @update:open="store.pairingWizardOpen = $event"
      @paired="onPaired"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import Badge from '../ui/Badge.vue';
import PairingWizardDialog from './PairingWizardDialog.vue';
import { useRemoteClientStore } from '../../stores/remoteClient';
import { useToast } from '../../composables/useToast';
import { hostHealthLabel, hostHealthTone, copyForRemoteError } from './remoteClientShared';
import type { HostEntry, PairingResult } from '../../api/remoteClient';

const store = useRemoteClientStore();
const { showSuccess, showError } = useToast();

const open = ref(false);
const rootEl = ref<HTMLElement | null>(null);
const menuEl = ref<HTMLElement | null>(null);
const menuStyle = ref<Record<string, string>>({});

const switching = computed(() => store.connectState === 'connecting');

const triggerLabel = computed(() => {
  if (store.connectState === 'connecting') return '连接中…';
  if (store.isRemoteMode) return store.currentHostName;
  return '本机';
});

const triggerDotClass = computed(() => {
  if (!store.isRemoteMode) return 'dot-green';
  if (store.connectState === 'connecting') return 'dot-gray dot-pulse';
  return dotClass(store.currentHost?.health ?? 'reachable');
});

function dotClass(health: string): string {
  switch (hostHealthTone(health)) {
    case 'green':
      return 'dot-green';
    case 'red':
      return 'dot-red';
    default:
      return 'dot-gray';
  }
}

function toggleMenu() {
  open.value = !open.value;
}

function updatePosition() {
  const root = rootEl.value;
  if (!root) return;
  const rect = root.getBoundingClientRect();
  const vw = window.innerWidth;
  const width = Math.max(rect.width, 232);
  const left = Math.min(Math.max(rect.left, 8), Math.max(vw - width - 8, 8));
  menuStyle.value = {
    left: `${left}px`,
    top: `${rect.bottom + 4}px`,
    width: `${width}px`,
  };
}

async function chooseLocal() {
  open.value = false;
  try {
    await store.switchToLocal();
  } catch (err) {
    showError(copyForRemoteError(err));
  }
}

async function chooseHost(h: HostEntry) {
  open.value = false;
  if (store.scope === h.id && store.isRemoteMode && store.connectState === 'connected') return;
  try {
    await store.switchToHost(h.id);
    showSuccess(`已切换到远程主机「${h.displayName}」`);
  } catch (err) {
    showError(copyForRemoteError(err));
  }
}

function openWizard() {
  open.value = false;
  store.openPairingWizard();
}

/** 配对成功：刷新登记簿并自动连接新主机（app 层注释约定）。 */
async function onPaired(result: PairingResult) {
  try {
    await store.loadHosts();
    await store.switchToHost(result.EntryID);
    showSuccess(`已连接到「${result.DeviceName}」`);
  } catch (err) {
    showError(copyForRemoteError(err));
  }
}

/* ---- 全局快捷键：Cmd/Ctrl+Shift+H 呼出主机菜单（不偷输入焦点） ---- */
function onGlobalKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.shiftKey && (e.key === 'H' || e.key === 'h')) {
    e.preventDefault();
    open.value = !open.value;
  }
}

function onDocClick(e: MouseEvent) {
  const target = e.target as Node;
  if (rootEl.value?.contains(target)) return;
  if (menuEl.value?.contains(target)) return;
  open.value = false;
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false;
}

function onReposition() {
  if (open.value) updatePosition();
}

onMounted(async () => {
  document.addEventListener('mousedown', onDocClick);
  document.addEventListener('keydown', onKey);
  window.addEventListener('keydown', onGlobalKeydown);
  window.addEventListener('resize', onReposition);
  window.addEventListener('scroll', onReposition, true);
  await store.loadHosts();
});

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick);
  document.removeEventListener('keydown', onKey);
  window.removeEventListener('keydown', onGlobalKeydown);
  window.removeEventListener('resize', onReposition);
  window.removeEventListener('scroll', onReposition, true);
});

// 打开菜单时定位 + 后台探活刷新状态灯（throttle 由 store 保证）。
watch(open, (val) => {
  if (val) {
    updatePosition();
    void store.probeHosts();
  }
});
</script>

<style scoped>
.host-switcher {
  position: relative;
}

.hs-trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 11px;
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--control);
  color: var(--label);
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.12s;
}

.hs-trigger:hover {
  background: var(--controlHover);
}

.hs-trigger.remote {
  border-color: var(--accent);
}

.hs-label {
  flex: 1;
  text-align: left;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hs-caret {
  color: var(--tertiary);
  font-size: 10px;
  flex-shrink: 0;
}

/* 状态灯（绿/灰/红；连接中呼吸复用现有 loading 语言） */
.hs-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.dot-green {
  background: var(--success);
}

.dot-gray {
  background: var(--tertiary);
}

.dot-red {
  background: var(--danger);
}

.dot-pulse {
  animation: hs-pulse 1.2s ease-in-out infinite;
}

@keyframes hs-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dot-pulse {
    animation: none;
  }
}

/* 菜单：Teleport 到 body，fixed 定位（同 Dropdown 组件的 overflow 规避策略） */
.hs-menu {
  position: fixed;
  z-index: 2000;
  margin: 0;
  padding: 4px;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 9px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.16);
  max-height: 320px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
}

.hs-item {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  padding: 8px 9px;
  border: none;
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  color: var(--label);
  text-align: left;
  transition: background 0.1s;
}

.hs-item:hover:not(:disabled) {
  background: var(--control);
}

.hs-item.selected {
  background: rgba(0, 122, 255, 0.08);
}

.hs-item:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.hs-item-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.hs-item-name {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hs-item-sub {
  font-size: 11px;
  color: var(--tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hs-check {
  width: 14px;
  height: 14px;
  color: var(--accent);
  flex-shrink: 0;
}

.hs-group-label {
  padding: 7px 9px 3px;
  font-size: 11px;
  font-weight: 500;
  color: var(--tertiary);
}

.hs-empty {
  padding: 10px 9px;
  font-size: 12px;
  color: var(--tertiary);
}

.hs-divider {
  height: 1px;
  background: var(--separator);
  margin: 4px 6px;
}

.hs-add .hs-plus {
  width: 8px;
  color: var(--accent);
  flex-shrink: 0;
}

.hs-fade-enter-active,
.hs-fade-leave-active {
  transition: opacity 0.12s, transform 0.12s;
}

.hs-fade-enter-from,
.hs-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

@media (prefers-reduced-motion: reduce) {
  .hs-fade-enter-active,
  .hs-fade-leave-active {
    transition: none;
  }
}
</style>
