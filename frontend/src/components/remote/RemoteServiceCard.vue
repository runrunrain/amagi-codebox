<!--
  卡① 远程服务开关卡（PG-05）：状态 + 启停 + 监听地址/端口。
  - 开启前置：LAN 暴露确认（P-02）；未确认时阻止并引导至卡②。
  - 提交中防重复（切换/应用均置提交态）；错误分类文案（禁笼统失败）。
-->
<template>
  <section class="rc-card" aria-labelledby="rc-svc-title">
    <header class="rc-card-head">
      <h2 id="rc-svc-title" class="rc-card-title">远程服务</h2>
      <p class="rc-card-sub">默认关闭；开启后局域网内设备可经配对访问本机</p>
    </header>

    <div class="svc-row svc-status-row">
      <div class="svc-status" aria-live="polite">
        <span class="svc-status-icon" :class="running ? 'is-on' : 'is-off'" aria-hidden="true">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path
              d="M2 8c1.8-3.4 4.2-5 6-5s4.2 1.6 6 5c-1.8 3.4-4.2 5-6 5s-4.2-1.6-6-5Z"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linejoin="round"
            />
            <circle cx="8" cy="8" r="2" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </span>
        <span class="svc-status-text">{{ statusText }}</span>
      </div>
      <button
        type="button"
        class="rc-switch"
        :class="{ off: !running }"
        role="switch"
        :aria-checked="running"
        :disabled="toggling"
        :aria-busy="toggling"
        :aria-label="running ? '关闭远程服务' : '开启远程服务'"
        @click="onToggle"
      >
        <span class="rc-switch-knob" />
      </button>
    </div>

    <p v-if="toggleBlockHint" class="svc-hint" role="alert">
      {{ toggleBlockHint }}
      <button type="button" class="rc-link" @click="$emit('goto-lan-confirm')">前往确认</button>
    </p>

    <div v-if="toggleError" class="rc-error" role="alert">
      <span>{{ toggleError.message }}</span>
      <span class="rc-error-detail">{{ toggleError.detail }}</span>
      <button type="button" class="rc-link" @click="retryToggle">重试</button>
    </div>

    <div class="svc-endpoint">
      <div class="svc-field">
        <label class="svc-label" for="rc-host">监听地址</label>
        <input
          id="rc-host"
          v-model="hostDraft"
          class="rc-input mono"
          placeholder="0.0.0.0"
          :disabled="running || applying"
          autocomplete="off"
          spellcheck="false"
        />
      </div>
      <div class="svc-field">
        <label class="svc-label" for="rc-port">监听端口</label>
        <input
          id="rc-port"
          v-model.number="portDraft"
          class="rc-input mono"
          type="number"
          min="1024"
          max="65535"
          placeholder="8680"
          :disabled="running || applying"
        />
      </div>
      <button
        type="button"
        class="rc-btn rc-btn-secondary svc-apply"
        :disabled="running || applying || !endpointDirty"
        :aria-busy="applying"
        @click="applyEndpoint"
      >
        {{ applying ? '应用中…' : '应用' }}
      </button>
    </div>
    <p class="svc-note">地址与端口仅可在服务停止时修改；端口范围 1024–65535。</p>

    <div v-if="applyError" class="rc-error" role="alert">
      <span>{{ applyError.message }}</span>
      <span class="rc-error-detail">{{ applyError.detail }}</span>
    </div>

    <!-- Major-05：关闭远程服务走 PG-06 危险动作确认（与撤销设备共用模板） -->
    <ConfirmDialog
      :open="stopConfirmOpen"
      title="关闭远程服务"
      consequence="远程服务将停止：当前已连接的设备会话会被断开，局域网内设备在服务停止期间无法连接本机。"
      irreversible-note="此操作可逆：之后可随时重新开启服务。已配对设备的信任关系保留，重新开启后无需重新配对。"
      confirm-text="关闭服务"
      :busy="toggling"
      busy-text="关闭中…"
      @confirm="confirmStop"
      @cancel="stopConfirmOpen = false"
    />
  </section>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { toggleRemoteServer, setRemoteEndpoint } from '../../api/remote';
import { classifyRemoteError, type ClassifiedError } from './remoteShared';
import { useToast } from '../../composables/useToast';
import ConfirmDialog from './ConfirmDialog.vue';

interface Props {
  running: boolean;
  host: string;
  port: number;
  /** LAN 暴露确认记录是否存在（卡②写入） */
  lanConfirmed: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'changed'): void;
  (e: 'goto-lan-confirm'): void;
}>();

const { showSuccess, showError } = useToast();

const hostDraft = ref(props.host);
const portDraft = ref(props.port);
watch(
  () => [props.host, props.port] as const,
  ([h, p]) => {
    hostDraft.value = h || '0.0.0.0';
    portDraft.value = Number(p) || 8680;
  },
);

const toggling = ref(false);
const applying = ref(false);
const toggleError = ref<ClassifiedError | null>(null);
const applyError = ref<ClassifiedError | null>(null);
const toggleBlockHint = ref('');
/** Major-05：关闭服务前的 PG-06 确认对话开关 */
const stopConfirmOpen = ref(false);
let lastToggleTarget: boolean | null = null;

const endpointDirty = computed(
  () =>
    (hostDraft.value || '').trim() !== (props.host || '') ||
    Number(portDraft.value) !== Number(props.port),
);

const statusText = computed(() => {
  if (toggling.value) return '切换中…';
  if (props.running) return `运行中 · 监听 ${props.host}:${props.port}${props.lanConfirmed ? '（LAN 已确认）' : ''}`;
  return '已停止';
});

async function onToggle() {
  const next = !props.running;
  toggleBlockHint.value = '';
  toggleError.value = null;
  if (next && !props.lanConfirmed) {
    // P-02：未显式确认 LAN 暴露风险前不允许开启
    toggleBlockHint.value = '开启前需先在下方完成 LAN 暴露风险确认（不预勾选）。';
    emit('goto-lan-confirm');
    return;
  }
  if (!next) {
    // Major-05 / PG-06：关闭远程服务是危险动作，必须先经确认对话；
    // 确认前不触碰后端。
    stopConfirmOpen.value = true;
    return;
  }
  lastToggleTarget = next;
  await performToggle(next);
}

/** PG-06 确认通过后才真正关闭服务 */
async function confirmStop() {
  if (toggling.value) return;
  stopConfirmOpen.value = false;
  lastToggleTarget = false;
  await performToggle(false);
}

async function retryToggle() {
  if (lastToggleTarget === null) return;
  await performToggle(lastToggleTarget);
}

async function performToggle(next: boolean) {
  toggling.value = true;
  toggleError.value = null;
  try {
    await toggleRemoteServer(next);
    emit('changed');
    showSuccess(next ? '远程服务已开启' : '远程服务已停止');
  } catch (err) {
    const c = classifyRemoteError(err);
    toggleError.value = c;
    showError(c.message);
  } finally {
    toggling.value = false;
  }
}

async function applyEndpoint() {
  applying.value = true;
  applyError.value = null;
  try {
    const host = (hostDraft.value || '').trim() || '0.0.0.0';
    const port = Number(portDraft.value) || 8680;
    if (port < 1024 || port > 65535) {
      applyError.value = {
        category: 'invalid-input',
        message: '端口需在 1024–65535 范围内。',
        detail: `port=${port}`,
      };
      return;
    }
    // Minor-02：地址+端口单次事务提交（后端 SetRemoteEndpoint），
    // 失败时无任何一侧被持久化，回执即为真实结果。
    await setRemoteEndpoint(host, port);
    emit('changed');
    showSuccess(`监听地址已更新为 ${host}:${port}`);
  } catch (err) {
    const c = classifyRemoteError(err);
    applyError.value = c;
    showError(c.message);
  } finally {
    applying.value = false;
  }
}
</script>

<style scoped>
.svc-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.svc-status-row {
  min-height: 44px;
  padding: 8px 0;
}

.svc-status {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: var(--vt-text);
}

.svc-status-icon {
  display: inline-flex;
}

.svc-status-icon.is-on {
  color: var(--vt-success);
}

.svc-status-icon.is-off {
  color: var(--vt-secondary);
}

.svc-status-text {
  font-variant-numeric: tabular-nums;
}

/* 开关：44px 高命中区（硬规则：按钮/控件 ≥44px） */
.rc-switch {
  width: 56px;
  height: 44px;
  border: none;
  background: transparent;
  cursor: pointer;
  position: relative;
  padding: 0;
}

.rc-switch::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 4px;
  right: 4px;
  height: 28px;
  transform: translateY(-50%);
  border-radius: 999px;
  background: var(--vt-success);
  transition: background 0.15s ease;
}

.rc-switch.off::before {
  background: var(--vt-border-strong);
}

.rc-switch-knob {
  position: absolute;
  top: 50%;
  left: calc(100% - 28px);
  width: 24px;
  height: 24px;
  transform: translateY(-50%);
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(31, 30, 27, 0.25);
  transition: left 0.15s ease;
}

.rc-switch.off .rc-switch-knob {
  left: 6px;
}

.rc-switch:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.rc-switch:focus-visible {
  outline: 2px solid var(--vt-accent);
  outline-offset: 2px;
  border-radius: 8px;
}

.svc-hint {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--vt-warning);
}

.svc-endpoint {
  display: flex;
  align-items: flex-end;
  gap: 14px;
  flex-wrap: wrap;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--vt-border);
}

.svc-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.svc-label {
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.svc-apply {
  margin-left: auto;
}

.svc-note {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

@media (prefers-reduced-motion: reduce) {
  .rc-switch::before,
  .rc-switch-knob {
    transition: none;
  }
}
</style>
