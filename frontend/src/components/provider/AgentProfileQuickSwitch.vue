<template>
  <!--
    AgentProfileQuickSwitch - Provider Center 顶部快捷切换 Agent 配置档组件。
    支持：
      1. 展示当前已应用的配置档名（若有）
      2. 下拉展开已保存的配置档列表，一键切换（调用 ApplyAgentProfile）
      3. 快捷从当前 live 配置快照为新配置档
      4. 一键跳转至设置中的 Agent 配置档管理页
  -->
  <div class="ap-qs-root" ref="rootEl">
    <button
      type="button"
      class="ap-qs-trigger"
      :class="{ 'has-active': !!lastAppliedName, open }"
      :disabled="loading || !!applyingName"
      @click="toggleOpen"
      title="快速切换或保存 CLI Agent 模型配置档（家/公司）"
    >
      <span class="ap-qs-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <rect x="9" y="9" width="13" height="13" rx="2"/>
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
        </svg>
      </span>
      <span v-if="lastAppliedName" class="ap-qs-active-dot" aria-hidden="true" />
      <span class="ap-qs-label">
        {{ triggerLabel }}
      </span>
      <span class="ap-qs-caret" aria-hidden="true">▾</span>
    </button>

    <Teleport to="body">
      <transition name="ap-qs-fade">
        <div
          v-if="open"
          ref="menuEl"
          class="ap-qs-menu"
          role="dialog"
          aria-label="Agent 配置档快速切换"
          :style="menuStyle"
        >
          <!-- 头部 -->
          <div class="ap-qs-header">
            <div class="ap-qs-header-title">
              <span>Agent 配置档</span>
              <span class="ap-qs-subtitle">pi / omp live 配置</span>
            </div>
            <button
              type="button"
              class="ap-qs-refresh-btn"
              :disabled="loading"
              @click="loadProfiles(true)"
              title="刷新配置档列表"
            >
              <svg :class="{ spinning: loading }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="23 4 23 10 17 10"/>
                <polyline points="1 20 1 14 7 14"/>
                <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
              </svg>
            </button>
          </div>

          <!-- 当前状态提示 -->
          <div class="ap-qs-status-bar">
            <template v-if="lastAppliedName">
              <span class="ap-qs-status-dot green"></span>
              <span class="ap-qs-status-text">当前生效：<strong>{{ lastAppliedName }}</strong></span>
            </template>
            <template v-else>
              <span class="ap-qs-status-dot gray"></span>
              <span class="ap-qs-status-text">当前使用实时 live 文件（未指定命名档）</span>
            </template>
          </div>

          <!-- 配置档列表 -->
          <div class="ap-qs-list-container">
            <div v-if="profileEntries.length === 0" class="ap-qs-empty">
              暂无保存的配置档
            </div>
            <div v-else class="ap-qs-list">
              <div
                v-for="item in profileEntries"
                :key="item.name"
                class="ap-qs-item"
                :class="{ active: item.name === lastAppliedName, busy: applyingName === item.name }"
                @click="handleApply(item.name)"
              >
                <div class="ap-qs-item-info">
                  <div class="ap-qs-item-name-row">
                    <span class="ap-qs-item-name">{{ item.name }}</span>
                    <span v-if="item.name === lastAppliedName" class="ap-qs-badge">当前生效</span>
                  </div>
                  <span class="ap-qs-item-time">{{ formatTime(item.profile.updatedAt) }}</span>
                </div>
                <button
                  type="button"
                  class="ap-qs-apply-btn"
                  :disabled="!!applyingName"
                >
                  {{ applyingName === item.name ? '切换中...' : (item.name === lastAppliedName ? '重新应用' : '切换') }}
                </button>
              </div>
            </div>
          </div>

          <!-- 快速快照栏 -->
          <div class="ap-qs-snapshot-section">
            <div class="ap-qs-section-title">从当前实时配置快照</div>
            <div class="ap-qs-snapshot-form">
              <input
                v-model="snapshotName"
                type="text"
                class="ap-qs-input"
                placeholder="新配置档名称（如：家 / 公司）"
                :disabled="capturing"
                @keydown.enter.prevent="handleCapture"
              />
              <button
                type="button"
                class="ap-qs-btn-primary"
                :disabled="capturing || !snapshotName.trim()"
                @click="handleCapture"
              >
                {{ capturing ? '保存中...' : '快照' }}
              </button>
            </div>
          </div>

          <!-- 底部管理入口 -->
          <div class="ap-qs-footer">
            <button type="button" class="ap-qs-manage-link" @click="goToSettings">
              <span>前往配置档完整管理</span>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="9 18 15 12 9 6"/>
              </svg>
            </button>
          </div>
        </div>
      </transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue';
import { useUIStore } from '../../stores/ui';
import { useToast } from '../../composables/useToast';
import {
  listAgentProfiles,
  applyAgentProfile,
  captureAgentProfile,
  type AgentProfileStore,
} from '../../api/agentProfile';

const emit = defineEmits<{
  (e: 'applied', name: string): void;
  (e: 'captured', name: string): void;
}>();

const uiStore = useUIStore();
const { showSuccess, showError } = useToast();

const rootEl = ref<HTMLElement | null>(null);
const menuEl = ref<HTMLElement | null>(null);
const open = ref(false);
const loading = ref(false);
const applyingName = ref<string | null>(null);
const capturing = ref(false);
const snapshotName = ref('');

const storeData = ref<AgentProfileStore>({
  version: 1,
  profiles: {},
  lastApplied: '',
});

const lastAppliedName = computed(() => storeData.value.lastApplied || '');

const triggerLabel = computed(() => {
  if (applyingName.value) return `切换至 ${applyingName.value}...`;
  if (lastAppliedName.value) return `配置档: ${lastAppliedName.value}`;
  return 'Agent 配置档';
});

const profileEntries = computed(() => {
  const map = storeData.value.profiles || {};
  return Object.entries(map)
    .map(([name, profile]) => ({ name, profile }))
    .sort((a, b) => (b.profile.updatedAt || 0) - (a.profile.updatedAt || 0));
});

// 定位计算（Teleport fixed）
const menuStyle = ref<Record<string, string>>({});
const MENU_WIDTH = 300;
const MENU_GAP = 6;
const VIEWPORT_MARGIN = 12;

function updatePosition(): boolean {
  const root = rootEl.value;
  if (!root) return false;
  const rect = root.getBoundingClientRect();
  const vh = window.innerHeight;
  const vw = window.innerWidth;

  if (rect.bottom <= 0 || rect.top >= vh) return false;

  const spaceBelow = vh - rect.bottom - VIEWPORT_MARGIN;
  const spaceAbove = rect.top - VIEWPORT_MARGIN;
  const up = spaceBelow < 360 && spaceAbove > spaceBelow;

  // 水平定位：优先右对齐到按钮
  let left = rect.right - MENU_WIDTH;
  if (left < VIEWPORT_MARGIN) {
    left = VIEWPORT_MARGIN;
  }
  if (left + MENU_WIDTH > vw - VIEWPORT_MARGIN) {
    left = vw - MENU_WIDTH - VIEWPORT_MARGIN;
  }

  const style: Record<string, string> = {
    left: `${left}px`,
    width: `${MENU_WIDTH}px`,
  };

  if (up) {
    style.bottom = `${vh - rect.top + MENU_GAP}px`;
  } else {
    style.top = `${rect.bottom + MENU_GAP}px`;
  }

  menuStyle.value = style;
  return true;
}

function onReposition() {
  if (!open.value) return;
  if (!updatePosition()) open.value = false;
}

function addRepositionListeners() {
  window.addEventListener('scroll', onReposition, true);
  window.addEventListener('resize', onReposition);
}

function removeRepositionListeners() {
  window.removeEventListener('scroll', onReposition, true);
  window.removeEventListener('resize', onReposition);
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

async function loadProfiles(silent = false) {
  if (loading.value) return;
  loading.value = true;
  try {
    storeData.value = await listAgentProfiles();
    if (silent) {
      showSuccess('配置档列表已刷新');
    }
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    showError(`加载配置档失败: ${msg}`);
  } finally {
    loading.value = false;
  }
}

function toggleOpen() {
  if (open.value) {
    open.value = false;
  } else {
    open.value = true;
    void loadProfiles();
  }
}

async function handleApply(name: string) {
  if (applyingName.value) return;
  applyingName.value = name;
  try {
    await applyAgentProfile(name);
    storeData.value.lastApplied = name;
    showSuccess(`已成功切换至配置档「${name}」`);
    emit('applied', name);
    open.value = false;
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    showError(`应用配置档「${name}」失败: ${msg}`);
  } finally {
    applyingName.value = null;
  }
}

async function handleCapture() {
  const name = snapshotName.value.trim();
  if (!name || capturing.value) return;
  capturing.value = true;
  try {
    await captureAgentProfile(name);
    snapshotName.value = '';
    await loadProfiles();
    showSuccess(`已将当前实时配置保存为「${name}」`);
    emit('captured', name);
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    showError(`快照失败: ${msg}`);
  } finally {
    capturing.value = false;
  }
}

function goToSettings() {
  open.value = false;
  uiStore.setActiveSettingKey('agent-profiles');
  uiStore.enterSettingsMode();
}

function formatTime(ms: number | undefined): string {
  if (!ms) return '';
  const d = new Date(ms);
  const now = Date.now();
  const diffMin = Math.floor((now - ms) / 60000);
  if (diffMin < 1) return '刚刚';
  if (diffMin < 60) return `${diffMin}分钟前`;
  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) return `${diffHours}小时前`;
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

watch(open, (val) => {
  if (val) {
    updatePosition();
    addRepositionListeners();
  } else {
    removeRepositionListeners();
  }
});

onMounted(() => {
  document.addEventListener('mousedown', onDocClick);
  document.addEventListener('keydown', onKey);
  void loadProfiles();
});

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick);
  document.removeEventListener('keydown', onKey);
  removeRepositionListeners();
});
</script>

<style scoped>
.ap-qs-root {
  position: relative;
  display: inline-flex;
}

.ap-qs-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 5px 10px;
  cursor: pointer;
  color: var(--label);
  font-family: inherit;
  font-size: 12px;
  font-weight: 500;
  transition: all 0.15s ease;
  height: 28px;
  box-sizing: border-box;
}

.ap-qs-trigger:hover:not(:disabled) {
  background: var(--controlHover);
  border-color: var(--secondary);
}

.ap-qs-trigger.open {
  background: var(--controlHover);
  border-color: var(--accent);
}

.ap-qs-trigger.has-active {
  border-color: rgba(52, 199, 89, 0.4);
}

.ap-qs-trigger:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.ap-qs-icon {
  display: flex;
  align-items: center;
  color: var(--secondary);
}

.ap-qs-icon svg {
  width: 14px;
  height: 14px;
}

.ap-qs-active-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--success);
  flex-shrink: 0;
}

.ap-qs-label {
  max-width: 140px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ap-qs-caret {
  font-size: 10px;
  color: var(--tertiary);
  margin-left: 2px;
}

/* 下拉菜单 */
.ap-qs-menu {
  position: fixed;
  z-index: 2100;
  background: #FFFFFF;
  --card: #FFFFFF;
  --window: #FBFBFD;
  --control: #F2F2F5;
  --controlHover: #E5E5EA;
  border: 1px solid var(--separator);
  border-radius: 12px;
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.18);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-sizing: border-box;
  font-size: 13px;
  color: var(--label);
}

.ap-qs-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 14px;
  border-bottom: 1px solid var(--separator);
  background: #FBFBFD;
}

.ap-qs-header-title {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.ap-qs-header-title span:first-child {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.ap-qs-subtitle {
  font-size: 11px;
  color: var(--tertiary);
}

.ap-qs-refresh-btn {
  background: none;
  border: none;
  padding: 4px;
  cursor: pointer;
  color: var(--secondary);
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.ap-qs-refresh-btn:hover:not(:disabled) {
  background: #E5E5EA;
  color: var(--label);
}

.ap-qs-refresh-btn svg {
  width: 14px;
  height: 14px;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.ap-qs-status-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  background: #F2F2F5;
  font-size: 11px;
  color: var(--secondary);
  border-bottom: 1px solid var(--separator);
}

.ap-qs-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.ap-qs-status-dot.green {
  background: var(--success);
}

.ap-qs-status-dot.gray {
  background: var(--tertiary);
}

.ap-qs-status-text strong {
  color: var(--label);
  font-weight: 600;
}

.ap-qs-list-container {
  max-height: 180px;
  overflow-y: auto;
  padding: 4px;
  background: #FFFFFF;
}

.ap-qs-empty {
  padding: 16px;
  text-align: center;
  color: var(--tertiary);
  font-size: 12px;
}

.ap-qs-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ap-qs-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.12s ease;
}

.ap-qs-item:hover {
  background: #F2F2F5;
}

.ap-qs-item.active {
  background: rgba(52, 199, 89, 0.12);
}

.ap-qs-item-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.ap-qs-item-name-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.ap-qs-item-name {
  font-weight: 600;
  font-size: 13px;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ap-qs-badge {
  font-size: 10px;
  background: var(--success);
  color: #fff;
  padding: 1px 5px;
  border-radius: 10px;
  font-weight: 500;
  flex-shrink: 0;
}

.ap-qs-item-time {
  font-size: 11px;
  color: var(--tertiary);
}

.ap-qs-apply-btn {
  background: #F2F2F5;
  border: 1px solid var(--separator);
  border-radius: 6px;
  padding: 3px 8px;
  font-size: 11px;
  font-weight: 500;
  color: var(--label);
  cursor: pointer;
  white-space: nowrap;
  transition: all 0.12s ease;
  flex-shrink: 0;
  margin-left: 8px;
}

.ap-qs-item:hover .ap-qs-apply-btn {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}

.ap-qs-item.active:hover .ap-qs-apply-btn {
  background: #F2F2F5;
  color: var(--label);
  border-color: var(--separator);
}

.ap-qs-snapshot-section {
  padding: 10px 14px;
  border-top: 1px solid var(--separator);
  background: #FBFBFD;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ap-qs-section-title {
  font-size: 11px;
  font-weight: 600;
  color: var(--secondary);
}

.ap-qs-snapshot-form {
  display: flex;
  gap: 6px;
}

.ap-qs-input {
  flex: 1;
  background: #FFFFFF;
  border: 1px solid var(--separator);
  border-radius: 6px;
  padding: 4px 8px;
  font-size: 12px;
  color: var(--label);
  outline: none;
}

.ap-qs-input:focus {
  border-color: var(--accent);
}

.ap-qs-btn-primary {
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.12s ease;
}

.ap-qs-btn-primary:hover:not(:disabled) {
  background: var(--accentHover);
}

.ap-qs-btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ap-qs-footer {
  padding: 8px 14px;
  border-top: 1px solid var(--separator);
  background: #F2F2F5;
  display: flex;
  align-items: center;
  justify-content: flex-end;
}

.ap-qs-manage-link {
  background: none;
  border: none;
  padding: 0;
  color: var(--accent);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 3px;
}

.ap-qs-manage-link:hover {
  text-decoration: underline;
}

.ap-qs-manage-link svg {
  width: 12px;
  height: 12px;
}

/* 动效 */
.ap-qs-fade-enter-active,
.ap-qs-fade-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.ap-qs-fade-enter-from,
.ap-qs-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
