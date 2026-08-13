<!--
  卡③ 配对卡（PG-05 PairingCard 契约：二维码 + 等宽倒计时 + 取消）
  - CreateRemotePairingWindow 需用户显式勾选 terminal-exposure 确认（不预勾选）。
  - QR 载荷为可直接打开的 Web URL；一次性配对材料只放在 hash query 中，
    不会随 HTTP 请求发送给服务器，也不含永久主凭据（PR-01）；
    addressRequired（无具体 LAN IP）时不渲染 QR，展示短码 + 手动输入指引。
  - 轮询 GetRemotePairingWindow 检测窗口结束（过期/配对完成/外部取消）。
-->
<template>
  <section class="rc-card" aria-labelledby="rc-pair-title">
    <header class="rc-card-head">
      <h2 id="rc-pair-title" class="rc-card-title">配对新设备</h2>
      <p class="rc-card-sub">短时配对窗口 · 配对材料一次性，不含永久主凭据</p>
    </header>

    <!-- 空闲态：发起入口 -->
    <template v-if="!activeWindow">
      <p v-if="!running" class="pair-off">远程服务未运行 — 请先在上方开启服务后再发起配对。</p>

      <label class="rc-check-row">
        <input
          v-model="exposureChecked"
          type="checkbox"
          class="rc-checkbox"
          data-testid="terminal-exposure-checkbox"
          :disabled="!running || creating"
        />
        <span>我确认配对后该设备可能看到终端输出内容（含命令与路径）</span>
      </label>

      <p v-if="exposureHint" class="pair-hint" role="alert">{{ exposureHint }}</p>

      <div class="pair-actions">
        <button
          type="button"
          class="rc-btn rc-btn-primary"
          data-testid="start-pairing-btn"
          :disabled="!running || creating"
          :aria-busy="creating"
          @click="startPairing"
        >
          {{ creating ? '正在创建窗口…' : '发起配对窗口' }}
        </button>
      </div>

      <p v-if="outcomeMessage" class="pair-outcome" role="status">{{ outcomeMessage }}</p>

      <div v-if="createError" class="rc-error" role="alert">
        <span>{{ createError.message }}</span>
        <span class="rc-error-detail">{{ createError.detail }}</span>
        <button type="button" class="rc-link" @click="startPairing">重试</button>
      </div>
    </template>

    <!-- 窗口进行中：QR + 等宽倒计时 + 取消 -->
    <template v-else>
      <div class="pair-live">
        <div v-if="activeWindow.baseUrl" class="pair-qr">
          <canvas ref="qrCanvas" class="pair-qr-canvas" data-testid="pairing-qr" aria-label="配对二维码" />
          <p class="pair-qr-hint">用系统相机扫码即可打开网页，并自动带入一次性配对码</p>
        </div>
        <div v-else class="pair-noaddr" role="note">
          当前监听地址不是具体局域网 IP，二维码不可用。请在远程设备上手动输入本机地址与下方配对码。
        </div>

        <div class="pair-meta">
          <div class="pair-code-row">
            <span class="pair-code-label">配对码</span>
            <span class="pair-code mono" data-testid="pairing-code">{{ activeWindow.code }}</span>
          </div>
          <div class="pair-countdown-row">
            <span class="pair-code-label">窗口剩余</span>
            <span class="pair-countdown mono" data-testid="pairing-countdown" aria-live="polite">{{ countdownText }}</span>
          </div>
          <p v-if="remainingAttempts !== null" class="pair-attempts">
            剩余尝试次数 {{ remainingAttempts }}
          </p>
          <p class="pair-note">短时有效 · 过期自动关闭</p>
          <button
            type="button"
            class="rc-btn rc-btn-secondary"
            data-testid="cancel-pairing-btn"
            :disabled="cancelling"
            :aria-busy="cancelling"
            @click="cancelPairing"
          >
            {{ cancelling ? '取消中…' : '取消窗口' }}
          </button>
        </div>
      </div>

      <div v-if="pollError" class="rc-error" role="alert">
        <span>{{ pollError.message }}</span>
        <span class="rc-error-detail">{{ pollError.detail }}</span>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onUnmounted } from 'vue';
import QRCode from 'qrcode';
import {
  createRemotePairingWindow,
  getRemotePairingWindow,
  cancelRemotePairingWindow,
} from '../../api/remote';
import type { remote } from '../../../wailsjs/go/models';
import { classifyRemoteError, formatCountdown, type ClassifiedError } from './remoteShared';
import { useToast } from '../../composables/useToast';

interface Props {
  running: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  /** 窗口结束（取消/过期/配对完成）或需要刷新设备与事件 */
  (e: 'changed'): void;
}>();

const { showSuccess, showError } = useToast();

// 不预勾选（P-02）：每次挂载/窗口结束后都回到未勾状态
const exposureChecked = ref(false);
const exposureHint = ref('');
const creating = ref(false);
const cancelling = ref(false);
const createError = ref<ClassifiedError | null>(null);
const pollError = ref<ClassifiedError | null>(null);
const outcomeMessage = ref('');

const activeWindow = ref<remote.PairingWindowInfo | null>(null);
const remainingAttempts = ref<number | null>(null);
const remainingMs = ref(0);
const qrCanvas = ref<HTMLCanvasElement | null>(null);

let countdownTimer: ReturnType<typeof setInterval> | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let pollInFlight = false;

const countdownText = computed(() => formatCountdown(remainingMs.value));

async function startPairing() {
  exposureHint.value = '';
  createError.value = null;
  outcomeMessage.value = '';
  if (!exposureChecked.value) {
    exposureHint.value = '请先勾选上方终端输出暴露确认（不预勾选），再发起配对窗口。';
    return;
  }
  creating.value = true;
  try {
    const info = await createRemotePairingWindow(true);
    activeWindow.value = info;
    remainingAttempts.value = null;
    startTimers(info.expiresAt);
    if (info.baseUrl) {
      await nextTick();
      await renderQR(info);
    }
  } catch (err) {
    const c = classifyRemoteError(err);
    createError.value = c;
    showError(c.message);
  } finally {
    creating.value = false;
  }
}

async function renderQR(info: remote.PairingWindowInfo) {
  if (!qrCanvas.value || !info.baseUrl) return;
  // 使用标准 http(s) URL，让系统相机/扫码器可以直接打开。配对码与过期时间
  // 只存在于 hash query（# 后），浏览器首个 HTTP 请求不会把它们发送到服务器；
  // ConnectPage 读取后会立即从地址栏清除。不含 token/永久主凭据（PR-01）。
  const target = new URL(info.baseUrl);
  target.pathname = '/';
  target.search = '';
  const params = new URLSearchParams({ code: info.code, expiresAt: info.expiresAt });
  target.hash = `/connect?${params.toString()}`;
  const payload = target.toString();
  // QR 颜色取自 VT 令牌计算值（qrcode canvas API 需具体色值，禁硬编码）
  const scope = qrCanvas.value.closest('.remote-cc') ?? document.documentElement;
  const cs = getComputedStyle(scope as Element);
  const dark = cs.getPropertyValue('--vt-surface-dark').trim() || '#1F1E1B';
  const light = cs.getPropertyValue('--vt-canvas').trim() || '#FAF9F5';
  try {
    await QRCode.toCanvas(qrCanvas.value, payload, {
      width: 168,
      margin: 1,
      color: { dark, light },
    });
  } catch (err) {
    console.error('QR render error:', err);
  }
}

function startTimers(expiresAt: string) {
  stopTimers();
  const expiry = new Date(expiresAt).getTime();
  const tick = () => {
    remainingMs.value = Math.max(0, expiry - Date.now());
    if (remainingMs.value <= 0 && countdownTimer) {
      clearInterval(countdownTimer);
      countdownTimer = null;
    }
  };
  tick();
  countdownTimer = setInterval(tick, 250);
  pollTimer = setInterval(pollWindow, 2000);
}

function stopTimers() {
  if (countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

async function pollWindow() {
  if (pollInFlight) return;
  pollInFlight = true;
  try {
    const status = await getRemotePairingWindow();
    pollError.value = null;
    if (!status.active) {
      // 窗口已结束：过期、配对完成或外部取消
      finishWindow('配对窗口已结束（过期或配对完成），设备列表与记录已刷新。');
    } else if (typeof status.remainingAttempts === 'number') {
      remainingAttempts.value = status.remainingAttempts;
    }
  } catch (err) {
    pollError.value = classifyRemoteError(err);
  } finally {
    pollInFlight = false;
  }
}

async function cancelPairing() {
  const win = activeWindow.value;
  if (!win || cancelling.value) return;
  cancelling.value = true;
  try {
    const cancelled = await cancelRemotePairingWindow(win.generation);
    finishWindow(cancelled ? '配对窗口已取消。' : '配对窗口在此之前已结束。');
  } catch (err) {
    const c = classifyRemoteError(err);
    pollError.value = c;
    showError(c.message);
  } finally {
    cancelling.value = false;
  }
}

function finishWindow(message: string) {
  stopTimers();
  activeWindow.value = null;
  remainingAttempts.value = null;
  exposureChecked.value = false;
  outcomeMessage.value = message;
  showSuccess(message);
  emit('changed');
}

onUnmounted(stopTimers);
</script>

<style scoped>
.pair-off {
  margin: 0 0 10px;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.pair-hint {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--vt-warning);
}

.pair-actions {
  margin-top: 12px;
}

.pair-outcome {
  margin: 12px 0 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.pair-live {
  display: flex;
  gap: 20px;
  align-items: flex-start;
  flex-wrap: wrap;
}

.pair-qr {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.pair-qr-canvas {
  background: var(--vt-canvas);
  border: 1px solid var(--vt-border);
  border-radius: 8px;
  padding: 8px;
  width: 168px;
  height: 168px;
}

.pair-qr-hint {
  margin: 0;
  max-width: 200px;
  text-align: center;
  font-size: 12px;
  color: var(--vt-text-secondary);
  line-height: 1.5;
}

.pair-noaddr {
  max-width: 260px;
  padding: 12px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text);
  background: var(--vt-surface-raised);
  border: 1px solid var(--vt-border);
  border-radius: 8px;
}

.pair-meta {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 220px;
}

.pair-code-row,
.pair-countdown-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.pair-code-label {
  font-size: 13px;
  color: var(--vt-text-secondary);
  flex-shrink: 0;
}

.pair-code {
  font-size: 20px;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: var(--vt-text);
}

.pair-countdown {
  font-size: 20px;
  font-weight: 600;
  color: var(--vt-text);
}

.pair-attempts {
  margin: 0;
  font-size: 13px;
  color: var(--vt-warning);
}

.pair-note {
  margin: 0;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.pair-meta .rc-btn {
  align-self: flex-start;
  margin-top: 4px;
}
</style>
