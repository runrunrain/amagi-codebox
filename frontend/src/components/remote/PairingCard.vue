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
        <!-- 二维码与链接展示区（只要有有效 baseUrl 或用户填入的局域网 IP 即渲染） -->
        <div v-if="effectiveBaseUrl" class="pair-qr">
          <canvas ref="qrCanvas" class="pair-qr-canvas" data-testid="pairing-qr" aria-label="配对二维码" />
          <div class="pair-qr-sub">
            <span class="pair-qr-source-tag" :class="isCustomHost ? 'is-custom' : 'is-auto'">
              {{ isCustomHost ? `手动指定: ${activeHost}` : '自动解析 LAN' }}
            </span>
            <button
              type="button"
              class="rc-link switch-host-link"
              data-testid="toggle-custom-host-btn"
              @click="toggleCustomHostInput"
            >
              {{ showHostInput ? '收起配置' : '更换局域网地址' }}
            </button>
          </div>
          <p class="pair-qr-hint">用移动端相机扫码即可打开网页，并自动带入一次性配对码</p>
        </div>

        <!-- 无可用地址（addressRequired 且未提供自定义局域网 IP）兜底指引与输入 -->
        <div v-else class="pair-noaddr" role="note" data-testid="pairing-address-fallback">
          <div class="noaddr-title">
            <span class="noaddr-icon" aria-hidden="true">⚠️</span>
            <strong>需要提供本机局域网地址</strong>
          </div>
          <p class="noaddr-desc">
            当前监听地址为回环地址或未检测到确定网卡，二维码无法自动生成。请输入本机的局域网 IP 地址（例如同一 Wi-Fi 分配的 IP），即可生成可扫码的配对二维码。
          </p>

          <div class="noaddr-tips">
            <span>常见私网 IP：<code>192.168.x.x</code> / <code>10.x.x.x</code> / <code>172.16-31.x.x</code></span>
            <span class="noaddr-subtip">提示：可在终端运行 <code>ipconfig</code> (Windows) 或 <code>ifconfig</code> (macOS/Linux) 查看。</span>
          </div>

          <!-- R1 候选局域网地址单选（后端网卡枚举，P3-B）：点击即选中并生成二维码 -->
          <div v-if="lanCandidates.length > 0" class="noaddr-candidates" role="radiogroup" aria-label="本机局域网地址候选" data-testid="lan-candidate-group">
            <span class="noaddr-cand-title">检测到本机以下局域网地址，点击即可生成二维码：</span>
            <button
              v-for="c in lanCandidates"
              :key="c.interface + '-' + c.ip"
              type="button"
              class="noaddr-cand"
              role="radio"
              :aria-checked="selectedLanIp === c.ip"
              :class="{ 'is-selected': selectedLanIp === c.ip }"
              :data-testid="`lan-candidate-${c.ip}`"
              @click="selectLanCandidate(c.ip)"
            >
              <span class="cand-ip mono">{{ formatLanCandidateLabel(c) }}</span>
              <span class="cand-if">{{ c.interface }}{{ c.vpnLike ? ' · 疑似VPN' : '' }}{{ c.private ? '' : ' · 非私网' }}</span>
            </button>
          </div>

          <div class="noaddr-input-row">
            <input
              v-model="customHostDraft"
              class="rc-input mono noaddr-input"
              data-testid="manual-lan-host-input"
              placeholder="例如 192.168.1.100"
              autocomplete="off"
              spellcheck="false"
              @keyup.enter="applyCustomHost"
            />
            <button
              type="button"
              class="rc-btn rc-btn-primary noaddr-apply-btn"
              data-testid="apply-manual-lan-btn"
              @click="applyCustomHost"
            >
              生成二维码
            </button>
          </div>
          <p v-if="customHostError" class="noaddr-err" role="alert">{{ customHostError }}</p>
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

          <!-- 展开手动局域网 IP 修改输入（当已有二维码时允许换 IP） -->
          <div v-if="effectiveBaseUrl && showHostInput" class="pair-custom-host-box">
            <label class="custom-host-label" for="manual-lan-host">更换局域网访问 IP 或主机名</label>
            <div class="custom-host-row">
              <input
                id="manual-lan-host"
                v-model="customHostDraft"
                class="rc-input mono custom-host-input"
                data-testid="edit-lan-host-input"
                placeholder="例如 192.168.1.100"
                autocomplete="off"
                spellcheck="false"
                @keyup.enter="applyCustomHost"
              />
              <button
                type="button"
                class="rc-btn rc-btn-secondary"
                data-testid="save-lan-host-btn"
                @click="applyCustomHost"
              >
                应用
              </button>
              <button
                v-if="isCustomHost && activeWindow.baseUrl"
                type="button"
                class="rc-btn rc-btn-secondary"
                title="重置为后端自动解析的局域网地址"
                @click="resetToAutoBaseUrl"
              >
                还原自动
              </button>
            </div>
            <p v-if="customHostError" class="custom-host-err" role="alert">{{ customHostError }}</p>
          </div>

          <!-- 移动端访问完整链接一键复制 -->
          <div v-if="effectivePairingUrl" class="pair-url-box">
            <div class="pair-url-label">移动端配对链接</div>
            <div class="pair-url-text mono" data-testid="pairing-url-text" :title="effectivePairingUrl">
              {{ effectivePairingUrl }}
            </div>
            <button
              type="button"
              class="rc-btn rc-btn-secondary pair-copy-btn"
              data-testid="copy-pairing-url-btn"
              @click="copyPairingUrl"
            >
              <span v-if="copied" class="copied-indicator">已复制 ✓</span>
              <span v-else>复制配对链接</span>
            </button>
          </div>

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
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue';
import QRCode from 'qrcode';
import {
  createRemotePairingWindow,
  getRemotePairingWindow,
  cancelRemotePairingWindow,
  listLocalLanAddresses,
} from '../../api/remote';
import type { remote } from '../../../wailsjs/go/models';
import {
  classifyRemoteError,
  formatCountdown,
  isValidIpOrHost,
  formatPairingUrl,
  copyTextToClipboard,
  CUSTOM_LAN_HOST_STORAGE_KEY,
  type ClassifiedError,
} from './remoteShared';
import { useToast } from '../../composables/useToast';

interface Props {
  running: boolean;
  host?: string;
  port?: number;
}

const props = withDefaults(defineProps<Props>(), {
  host: '0.0.0.0',
  port: 8680,
});

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

// 手动局域网 IP 兜底状态
const customLanHost = ref('');
const customHostDraft = ref('');
const customHostError = ref('');
const showHostInput = ref(false);
const copied = ref(false);

// R1 候选局域网地址（后端网卡枚举，P3-B）：拉取失败静默降级为纯手动输入
const lanCandidates = ref<remote.LanAddressInfo[]>([]);
const selectedLanIp = ref('');

onMounted(async () => {
  try {
    lanCandidates.value = (await listLocalLanAddresses()) ?? [];
  } catch {
    // 候选拉取失败（如开发态无 Wails runtime）：保持空列表，兜底手动输入不变
  }
});

/** 候选项主文本：IP/掩码长度（如 192.168.31.55/24） */
function formatLanCandidateLabel(c: remote.LanAddressInfo): string {
  return c.prefixLen > 0 ? `${c.ip}/${c.prefixLen}` : c.ip;
}

/** 选中候选即套用为局域网地址（复用校验/持久化/QR 重渲染链路） */
function selectLanCandidate(ip: string) {
  selectedLanIp.value = ip;
  customHostDraft.value = ip;
  applyCustomHost();
}

try {
  const saved = localStorage.getItem(CUSTOM_LAN_HOST_STORAGE_KEY);
  if (saved && isValidIpOrHost(saved)) {
    customLanHost.value = saved;
    customHostDraft.value = saved;
  }
} catch {
  // localStorage 不可用时静默忽略
}

let countdownTimer: ReturnType<typeof setInterval> | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let pollInFlight = false;

const countdownText = computed(() => formatCountdown(remainingMs.value));

/** 解析当前生效的 Base URL（自动解析或用户手动输入） */
const effectiveBaseUrl = computed(() => {
  if (!activeWindow.value) return '';
  // 若用户手动设置了合法的 LAN Host，优先采用（便于多网卡/VPN用户覆盖）
  if (customLanHost.value) {
    const cleanHost = customLanHost.value.trim();
    const hostPart = cleanHost.includes(':') && !cleanHost.startsWith('[') ? `[${cleanHost}]` : cleanHost;
    return `http://${hostPart}:${props.port || 8680}`;
  }
  // 否则采用后端返回的 baseUrl
  if (activeWindow.value.baseUrl) {
    return activeWindow.value.baseUrl;
  }
  return '';
});

const isCustomHost = computed(() => !!customLanHost.value);

const activeHost = computed(() => {
  if (customLanHost.value) return customLanHost.value;
  if (activeWindow.value?.baseUrl) {
    try {
      const u = new URL(activeWindow.value.baseUrl);
      return u.hostname;
    } catch {
      return '';
    }
  }
  return '';
});

/** 完整的移动端配对 Web 访问 URL */
const effectivePairingUrl = computed(() => {
  if (!activeWindow.value || !effectiveBaseUrl.value) return '';
  return formatPairingUrl(
    activeHost.value,
    props.port || 8680,
    activeWindow.value.code,
    activeWindow.value.expiresAt,
  );
});

watch(effectiveBaseUrl, async (newVal) => {
  if (newVal && activeWindow.value) {
    await nextTick();
    await renderQR();
  }
});

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
    if (effectiveBaseUrl.value) {
      await nextTick();
      await renderQR();
    }
  } catch (err) {
    const c = classifyRemoteError(err);
    createError.value = c;
    showError(c.message);
  } finally {
    creating.value = false;
  }
}

async function renderQR() {
  if (!qrCanvas.value || !activeWindow.value || !effectivePairingUrl.value) return;
  const payload = effectivePairingUrl.value;
  // QR 颜色取自 VT 令牌计算值（qrcode canvas API 需具体色值，禁硬编码）
  const scope = qrCanvas.value.closest('.remote-cc') ?? document.documentElement;
  const cs = getComputedStyle(scope as Element);
  const dark = cs.getPropertyValue('--vt-surface-dark').trim() || '#1F1E1B';
  const light = cs.getPropertyValue('--vt-canvas').trim() || '#FAF9F5';
  try {
    await QRCode.toCanvas(qrCanvas.value, payload, {
      width: 176,
      margin: 1,
      color: { dark, light },
    });
  } catch (err) {
    console.error('QR render error:', err);
  }
}

function toggleCustomHostInput() {
  showHostInput.value = !showHostInput.value;
  if (showHostInput.value) {
    customHostDraft.value = customLanHost.value || activeHost.value || '';
    customHostError.value = '';
  }
}

function applyCustomHost() {
  customHostError.value = '';
  const s = (customHostDraft.value || '').trim();
  if (!s) {
    customHostError.value = '请输入本机的局域网 IP 地址或主机名。';
    return;
  }
  if (!isValidIpOrHost(s)) {
    customHostError.value = '地址格式不正确，请输入类似 192.168.1.100 的合法 IP 或主机名。';
    return;
  }
  customLanHost.value = s;
  try {
    localStorage.setItem(CUSTOM_LAN_HOST_STORAGE_KEY, s);
  } catch {
    /* ignore */
  }
  showHostInput.value = false;
  showSuccess(`已设置局域网地址为 ${s}，二维码已更新`);
}

function resetToAutoBaseUrl() {
  customLanHost.value = '';
  customHostDraft.value = '';
  selectedLanIp.value = '';
  try {
    localStorage.removeItem(CUSTOM_LAN_HOST_STORAGE_KEY);
  } catch {
    /* ignore */
  }
  showHostInput.value = false;
  showSuccess('已恢复后端自动解析的局域网地址');
}

async function copyPairingUrl() {
  if (!effectivePairingUrl.value) return;
  const ok = await copyTextToClipboard(effectivePairingUrl.value);
  if (ok) {
    copied.value = true;
    showSuccess('配对链接已复制到剪贴板');
    setTimeout(() => {
      copied.value = false;
    }, 2000);
  } else {
    showError('复制失败，请手动选取复制');
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
  max-width: 320px;
  padding: 14px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text);
  background: var(--vt-surface-raised);
  border: 1px solid var(--vt-border);
  border-radius: 8px;
}

.noaddr-title {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
  color: var(--vt-text);
}

.noaddr-icon {
  font-size: 14px;
}

.noaddr-desc {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.noaddr-tips {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-bottom: 10px;
  font-size: 11px;
  color: var(--vt-text-secondary);
}

.noaddr-tips code,
.noaddr-subtip code {
  background: var(--vt-canvas);
  padding: 1px 4px;
  border-radius: 4px;
  border: 1px solid var(--vt-border);
}

.noaddr-input-row {
  display: flex;
  gap: 8px;
}

.noaddr-candidates {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 10px;
}

.noaddr-cand-title {
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.noaddr-cand {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  padding: 7px 10px;
  font-size: 13px;
  text-align: left;
  background: var(--vt-canvas);
  border: 1px solid var(--vt-border);
  border-radius: 8px;
  color: var(--vt-text);
  cursor: pointer;
}

.noaddr-cand:hover {
  border-color: var(--vt-control);
}

.noaddr-cand.is-selected {
  border-color: var(--vt-control);
  background: var(--vt-surface-raised);
  box-shadow: inset 0 0 0 1px var(--vt-control);
}

.cand-ip {
  font-weight: 600;
}

.cand-if {
  font-size: 11px;
  color: var(--vt-text-secondary);
  white-space: nowrap;
}

.noaddr-input {
  min-width: 160px;
  flex: 1;
  min-height: 38px;
  padding: 6px 10px;
  font-size: 13px;
}

.noaddr-apply-btn {
  min-height: 38px;
  padding: 6px 12px;
  font-size: 13px;
  flex-shrink: 0;
}

.noaddr-err,
.custom-host-err {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--vt-danger);
}

.pair-qr-sub {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.pair-qr-source-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--vt-canvas);
  border: 1px solid var(--vt-border);
  color: var(--vt-text-secondary);
}

.pair-qr-source-tag.is-custom {
  border-color: var(--vt-control);
  color: var(--vt-control);
}

.switch-host-link {
  font-size: 12px;
  padding: 2px 4px;
  min-height: unset;
}

.pair-custom-host-box {
  margin: 6px 0;
  padding: 10px;
  background: var(--vt-canvas);
  border: 1px solid var(--vt-border);
  border-radius: 8px;
}

.custom-host-label {
  display: block;
  font-size: 12px;
  color: var(--vt-text-secondary);
  margin-bottom: 6px;
}

.custom-host-row {
  display: flex;
  gap: 8px;
  align-items: center;
}

.custom-host-input {
  min-width: 140px;
  flex: 1;
  min-height: 36px;
  padding: 4px 8px;
  font-size: 13px;
}

.pair-url-box {
  margin: 4px 0 8px;
  padding: 10px 12px;
  background: var(--vt-canvas);
  border: 1px solid var(--vt-border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pair-url-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--vt-text-secondary);
}

.pair-url-text {
  font-size: 12px;
  line-height: 1.4;
  color: var(--vt-text);
  word-break: break-all;
  user-select: all;
}

.pair-copy-btn {
  align-self: flex-start;
  min-height: 34px;
  padding: 4px 10px;
  font-size: 12px;
}

.copied-indicator {
  color: var(--vt-success);
  font-weight: 600;
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
