<script setup lang="ts">
/**
 * ConnectPage — PG-01 连接与配对页（#/connect，M1-D1）
 * ---------------------------------------------------------------------------
 * 权威依据：P5 v1.2 §4 PG-01 / §5 状态矩阵 / §6 组件契约；P3 E-01～E-04/E-11。
 * 契约：mobile/src/lib/contract（M0-03 冻结，仅 import，不复制字符串）。
 *
 * 全流程：
 *   ① 自动诊断四类分类（网络不可达 / 服务未开启或版本不兼容 / 未配对 / 已授权可进）
 *   ② 扫码（html5-qrcode；权限拒绝/无摄像头分类降级到手动）与手动地址+配对码两路径
 *   ③ 配对窗口倒计时（等宽数字；过期自动回态；无 expiresAt 时诚实标注"预计"）
 *   ④ E-01～E-04 状态呈现（分类文案+可执行动作，禁笼统失败 AC-23）
 *   ⑤ LAN 明文 HTTP 风险提示（RiskBanner，无"不再提示"）
 *   ⑥ E-11 壳回落：JS/Web API 不可用 → 静态说明块；相机不可用 → 手动入口
 * 配对成功 → auth store → 会话大厅占位路由（#/lobby，大厅本体是 M2 交付）。
 * ---------------------------------------------------------------------------
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import {
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_AUTH_UNPAIRED,
  ERROR_CODE_AUTH_WINDOW_EXPIRED,
  ERROR_CODE_BAD_REQUEST,
  ERROR_CODE_NET_UNREACHABLE,
  ERROR_CODE_RATE_LIMITED,
  DETAIL_REASON_UNSUPPORTED_API_VERSION,
} from '../lib/contract';
import { completePairing, getHostSummary, toApiRequestError, ApiRequestError } from '../lib/api';
import { useAuthStore } from '../stores/auth';
import DiagnosisCard, { type DiagnosisKind } from '../components/connect/DiagnosisCard.vue';
import RiskBanner from '../components/connect/RiskBanner.vue';
import CountdownChip from '../components/connect/CountdownChip.vue';
import QrScannerPanel from '../components/connect/QrScannerPanel.vue';

/** P6 §6.1 暂定值：桌面配对窗口默认 3min。仅用于无 expiresAt 时的本地估算。 */
const PAIRING_WINDOW_ESTIMATE_MS = 3 * 60 * 1000;

const route = useRoute();
const router = useRouter();
const auth = useAuthStore();

// ---------------------------------------------------------------------------
// E-11：Web API 能力检测（JS 被禁用由 index.html <noscript> 承担静态说明）
// ---------------------------------------------------------------------------
const webApiAvailable =
  typeof window !== 'undefined' &&
  typeof window.fetch === 'function' &&
  typeof Promise !== 'undefined';

// ---------------------------------------------------------------------------
// 诊断（E-01 家族）
// ---------------------------------------------------------------------------
const diagnosis = ref<DiagnosisKind>('checking');
const diagnosisDetail = ref('');
const retrying = ref(false);

/** 版本不兼容（bad_request + details.reason，I-11）判定。 */
function isUnsupportedApiVersion(err: ApiRequestError): boolean {
  return (
    err.code === ERROR_CODE_BAD_REQUEST &&
    err.details?.reason === DETAIL_REASON_UNSUPPORTED_API_VERSION
  );
}

async function runDiagnosis(): Promise<void> {
  if (!webApiAvailable) return;
  diagnosis.value = 'checking';
  diagnosisDetail.value = '';
  try {
    const host = await getHostSummary();
    auth.applyAuthorized(host);
    diagnosis.value = 'authorized';
    diagnosisDetail.value = `宿主服务 ${host.serverVersion} · API ${host.apiVersion}`;
  } catch (rawErr) {
    const err = toApiRequestError(rawErr);
    // Major-07 精确四类映射：先读 auth 真因（revoked → expired → unpaired），
    // service-down 只留给真正的连接层/5xx/非契约错误；授权类失败绝不可落
    // service-down（撤销→E-03、凭据失效→E-04）。
    // 快照“本机持有凭据投影”必须在任何 auth transition 之前读取。
    const hadCredentials = auth.hasDeviceProjection || auth.status === 'paired';
    if (err.code === ERROR_CODE_AUTH_REVOKED) {
      // E-03：设备被撤销。
      auth.invalidateAuthorization('revoked');
      // M1-D2 真服务器证伪修正：revoked 信号可能经诊断响应首达（如撤销后整页
      // 重载，内存态丢失、无 ?reason= 深链），此时 initKickBanner 已跑过且读不到
      // revoked 状态；横幅必须随诊断真因同步竖起，不能只靠挂载时的深链/内存态。
      kickBanner.value = 'revoked';
      diagnosis.value = 'unpaired';
      diagnosisDetail.value = '这台设备的授权已被桌面端撤销，需要重新配对。';
    } else if (
      err.code === ERROR_CODE_AUTH_WINDOW_EXPIRED ||
      (hadCredentials && (err.code === ERROR_CODE_AUTH_UNPAIRED || err.status === 401))
    ) {
      // E-04：配对凭据失效——真实后端对过期凭据返回 401 auth.window_expired
      // （routes_v1.go authExpired 分支并清 Cookie）；凭据被清/畸形（后端
      // auth.unpaired + 清 Cookie）或无契约体的 401，只要本机持有凭据投影，
      // 同样按凭据失效分类，不得误报为服务未开启。
      auth.invalidateAuthorization('expired');
      kickBanner.value = 'expired';
      diagnosis.value = 'unpaired';
      diagnosisDetail.value = '配对凭据已失效，请重新完成短时配对恢复访问。';
    } else if (err.code === ERROR_CODE_AUTH_UNPAIRED) {
      auth.applyUnpaired();
      diagnosis.value = 'unpaired';
    } else if (isUnsupportedApiVersion(err)) {
      auth.applyUnpaired();
      diagnosis.value = 'service-down';
      diagnosisDetail.value = '宿主报告的协议版本与这台设备不兼容。';
    } else if (err.code === ERROR_CODE_NET_UNREACHABLE) {
      diagnosis.value = 'net-unreachable';
      diagnosisDetail.value = err.status === null ? '' : `HTTP ${err.status}`;
    } else {
      // service.down（如安全状态 503）/ 5xx / 非契约错误体：地址可达但服务异常。
      diagnosis.value = 'service-down';
      diagnosisDetail.value = err.message;
    }
  }
}

async function retryDiagnosis(): Promise<void> {
  retrying.value = true;
  try {
    await runDiagnosis();
  } finally {
    retrying.value = false;
  }
}

// ---------------------------------------------------------------------------
// 深链参数（QR 载荷为 JSON {v,url,code,expiresAt?}；跨宿主经 hash query 传递）
// ---------------------------------------------------------------------------
interface PairingDeepLink {
  code: string;
  expiresAt: number | null;
}

function readDeepLink(): PairingDeepLink {
  const query = route.query;
  const code = typeof query.code === 'string' ? query.code.trim() : '';
  let expiresAt: number | null = null;
  const rawExp = typeof query.expiresAt === 'string' ? query.expiresAt : '';
  if (rawExp) {
    const parsed = /^\d+$/.test(rawExp) ? Number(rawExp) : Date.parse(rawExp);
    if (Number.isFinite(parsed) && parsed > Date.now()) {
      expiresAt = parsed;
    }
  }
  return { code, expiresAt };
}

/** pairing 材料从地址栏清除（一次性材料不留历史）。
 *  用 history.replaceState 而非 router.replace：后者改变 route.fullPath 触发
 *  router-view :key 重挂载，会抹掉页内回态（如窗口已关闭面板）。 */
function stripPairingParams(): void {
  if (route.query.code === undefined && route.query.expiresAt === undefined && route.query.reason === undefined) return;
  const url = new URL(window.location.href);
  url.hash = '#/connect';
  window.history.replaceState(window.history.state, '', url);
}

// ---------------------------------------------------------------------------
// 配对流程（E-02/E-03/E-04）
// ---------------------------------------------------------------------------
type PairEntry = 'none' | 'scan' | 'manual';

const pairEntry = ref<PairEntry>('none');
const codeInput = ref('');
const deviceNameInput = ref('');
const addressInput = ref('');
const submitting = ref(false);
const pairError = ref<{ title: string; description: string } | null>(null);
/** 本地倒计时判定的窗口关闭（E-02 前奏：服务端判定为准，本地仅提示）。 */
const windowClosedLocally = ref(false);

// 倒计时
const countdownTarget = ref<number | null>(null);
const countdownIsEstimate = ref(false);
const countdownNow = ref(Date.now());
let countdownTimer: ReturnType<typeof setInterval> | null = null;

const countdownRemaining = computed(() =>
  countdownTarget.value === null ? 0 : Math.max(0, countdownTarget.value - countdownNow.value),
);
const showCountdown = computed(() => countdownTarget.value !== null && !windowClosedLocally.value);

function startCountdown(target: number, isEstimate: boolean): void {
  countdownTarget.value = target;
  countdownIsEstimate.value = isEstimate;
  windowClosedLocally.value = false;
  countdownNow.value = Date.now();
  if (countdownTimer) clearInterval(countdownTimer);
  countdownTimer = setInterval(() => {
    countdownNow.value = Date.now();
    if (countdownTarget.value !== null && countdownNow.value >= countdownTarget.value) {
      if (countdownTimer) clearInterval(countdownTimer);
      countdownTimer = null;
      // 过期自动回态：清配对材料，回到待发起态并明示下一步。
      windowClosedLocally.value = true;
      codeInput.value = '';
      stripPairingParams();
    }
  }, 500);
}

function stopCountdown(): void {
  if (countdownTimer) clearInterval(countdownTimer);
  countdownTimer = null;
  countdownTarget.value = null;
}

function suggestDeviceName(): string {
  const ua = navigator.userAgent;
  if (/android/i.test(ua)) return '我的 Android 设备';
  if (/iphone|ipad/i.test(ua)) return '我的 iPhone';
  return '我的移动设备';
}

function beginPairEntry(entry: PairEntry, seed?: PairingDeepLink): void {
  pairEntry.value = entry;
  pairError.value = null;
  windowClosedLocally.value = false;
  if (entry === 'manual' && !deviceNameInput.value) {
    deviceNameInput.value = suggestDeviceName();
  }
  if (seed?.code) {
    codeInput.value = seed.code;
    deviceNameInput.value = deviceNameInput.value || suggestDeviceName();
    if (seed.expiresAt !== null) {
      startCountdown(seed.expiresAt, false);
    } else {
      startCountdown(Date.now() + PAIRING_WINDOW_ESTIMATE_MS, true);
    }
    // 扫码/深链带入配对码后直接进入确认步骤（手动表单承担确认与设备名）。
    pairEntry.value = 'manual';
  }
}

// 扫码
function handleQrDecoded(text: string): void {
  let payload: Record<string, unknown> | null = null;
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === 'object' && parsed !== null) {
      payload = parsed as Record<string, unknown>;
    }
  } catch {
    payload = null;
  }

  const code = typeof payload?.code === 'string' ? payload.code.trim() : '';
  const url = typeof payload?.url === 'string' ? payload.url.trim() : '';
  const rawExp = payload?.expiresAt;
  const expiresAt =
    typeof rawExp === 'string' && Date.parse(rawExp) > Date.now()
      ? Date.parse(rawExp)
      : typeof rawExp === 'number' && rawExp > Date.now()
        ? rawExp
        : null;

  if (!code) {
    pairError.value = {
      title: '二维码不是有效的配对入口',
      description: '请扫描桌面端「设置 › 远程访问」中展示的配对二维码，或改用手动输入。',
    };
    pairEntry.value = 'none';
    return;
  }

  // 跨宿主：二维码指向另一台宿主 → 整页导航过去，配对材料经 hash query 一次性传递。
  if (url) {
    try {
      const target = new URL(url);
      if (target.origin !== window.location.origin) {
        const expQuery = expiresAt !== null ? `&expiresAt=${encodeURIComponent(String(expiresAt))}` : '';
        window.location.assign(
          `${target.origin}/#/connect?code=${encodeURIComponent(code)}${expQuery}`,
        );
        return;
      }
    } catch {
      // url 非法：按本机配对继续，不阻断。
    }
  }

  beginPairEntry('manual', { code, expiresAt });
}

function handleScannerFailed(): void {
  // 分类文案已由 QrScannerPanel 呈现；降级到手动路径入口保持可见。
}

function handleScannerCancel(): void {
  pairEntry.value = 'none';
}

// 手动地址：异源 → 整页导航（页面由目标宿主同源伺服，Cookie 域正确）。
function navigateToAddress(): boolean {
  const raw = addressInput.value.trim();
  if (!raw) return false;
  let target: URL;
  try {
    target = new URL(/^https?:\/\//i.test(raw) ? raw : `http://${raw}`);
  } catch {
    pairError.value = {
      title: '地址格式不正确',
      description: '请输入类似 192.168.1.100:8680 的宿主地址。',
    };
    return false;
  }
  if (target.origin === window.location.origin) return false;
  const params = new URLSearchParams();
  if (codeInput.value.trim()) params.set('code', codeInput.value.trim());
  window.location.assign(`${target.origin}/#/connect${params.size ? `?${params.toString()}` : ''}`);
  return true;
}

const canSubmitPairing = computed(
  () => codeInput.value.trim().length > 0 && deviceNameInput.value.trim().length > 0 && !submitting.value,
);

async function submitPairing(): Promise<void> {
  if (!canSubmitPairing.value) return;
  pairError.value = null;
  // 手动地址指向异源：先导航，配对在目标宿主页面完成。
  if (addressInput.value.trim() && navigateToAddress()) return;

  submitting.value = true;
  try {
    const response = await completePairing(codeInput.value.trim(), deviceNameInput.value.trim());
    auth.applyPairing(response);
    stopCountdown();
    codeInput.value = '';
    // 一次性配对材料随地址栏一并清除：replace 进大厅，历史里不留 code。
    await router.replace({ name: 'lobby' });
  } catch (rawErr) {
    const err = toApiRequestError(rawErr);
    if (err.code === ERROR_CODE_AUTH_WINDOW_EXPIRED) {
      // E-02：窗口过期/被取消 → 明示原因，引导回桌面重新发起。
      windowClosedLocally.value = true;
      codeInput.value = '';
      stopCountdown();
      stripPairingParams();
      pairError.value = null;
    } else if (err.code === ERROR_CODE_AUTH_REVOKED) {
      // E-03：设备被撤销。
      auth.invalidateAuthorization('revoked');
      kickBanner.value = 'revoked';
      pairEntry.value = 'none';
      codeInput.value = '';
      stopCountdown();
      stripPairingParams();
      void runDiagnosis();
    } else if (err.code === ERROR_CODE_AUTH_UNPAIRED) {
      pairError.value = {
        title: '配对码不正确或已被使用',
        description: '请核对桌面端展示的最新配对码；配对码为一次性使用，过期或被使用后需回桌面重新发起。',
      };
    } else if (err.code === ERROR_CODE_RATE_LIMITED) {
      pairError.value = {
        title: '尝试次数过多，配对窗口已锁定',
        description: '请回到桌面端重新发起配对窗口，再用新的配对码重试。',
      };
    } else if (err.code === ERROR_CODE_NET_UNREACHABLE) {
      pairError.value = {
        title: '网络不可达',
        description: '配对请求没有到达宿主。请确认仍在同一局域网后重试。',
      };
      void runDiagnosis();
    } else {
      pairError.value = {
        title: '配对请求被拒绝',
        description: `${err.message}（${err.code}）。请回桌面端重新发起配对后重试。`,
      };
    }
  } finally {
    submitting.value = false;
  }
}

// ---------------------------------------------------------------------------
// E-03/E-04 踢回横幅（授权失效 → 清态踢回 PG-01；本地草稿不清由 M2 承担）
// ---------------------------------------------------------------------------
const kickBanner = ref<'revoked' | 'expired' | null>(null);

function initKickBanner(): void {
  const reason = typeof route.query.reason === 'string' ? route.query.reason : '';
  if (reason === 'revoked' || auth.status === 'revoked') {
    kickBanner.value = 'revoked';
  } else if (reason === 'expired' || auth.status === 'expired') {
    kickBanner.value = 'expired';
  }
  stripPairingParams();
}

// ---------------------------------------------------------------------------
// 顶部状态区：连接层 + 授权层（三通道：固定语义图标 + 文字 + 语义色）
// ---------------------------------------------------------------------------
const connectionChip = computed(() => {
  switch (diagnosis.value) {
    case 'checking':
      return { text: '连接：诊断中', tone: 'secondary' as const };
    case 'authorized':
    case 'unpaired':
      return { text: '连接：正常', tone: 'success' as const };
    default:
      return { text: '连接：异常', tone: 'danger' as const };
  }
});

const authChip = computed(() => {
  if (auth.status === 'paired') return { text: '授权：已配对', tone: 'success' as const };
  if (auth.status === 'revoked') return { text: '授权：已撤销', tone: 'danger' as const };
  if (auth.status === 'expired') return { text: '授权：已失效', tone: 'warning' as const };
  return { text: '授权：未配对', tone: 'secondary' as const };
});

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------
onMounted(() => {
  if (!webApiAvailable) return;
  initKickBanner();
  const deepLink = readDeepLink();
  void runDiagnosis().then(() => {
    // 深链带入配对码：诊断落在未配对态时直接进入确认步骤。
    if (deepLink.code && auth.status !== 'paired') {
      beginPairEntry('manual', deepLink);
    } else if (deepLink.code) {
      stripPairingParams();
    }
  });
});

onBeforeUnmount(() => {
  stopCountdown();
});
</script>

<template>
  <div class="connect-page">
    <!-- E-11 壳回落：Web API 不可用 → 静态说明块（不伪装可交互） -->
    <div v-if="!webApiAvailable" class="webapi-fallback">
      <h1 class="fallback-title">Amagi CodeBox 远程连接</h1>
      <p class="fallback-text">
        当前环境缺少必要的 Web 能力（fetch/Promise），无法运行连接与配对界面。
        请使用系统浏览器直接打开桌面端展示的宿主地址，或升级 Android 系统 WebView 后重试。
      </p>
    </div>

    <template v-else>
      <header class="page-header">
        <div class="brand">
          <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true">
            <rect x="2" y="3" width="20" height="14" rx="2" />
            <line x1="8" y1="21" x2="16" y2="21" />
            <line x1="12" y1="17" x2="12" y2="21" />
            <path d="M7 8l3 3-3 3" />
            <line x1="13" y1="14" x2="17" y2="14" />
          </svg>
          <div class="brand-text">
            <h1 class="brand-title">Amagi CodeBox</h1>
            <p class="brand-subtitle">连接与配对</p>
          </div>
        </div>

        <!-- 连接层 / 授权层状态（图标 + 文字 + 颜色三通道，不用圆点） -->
        <div class="status-chips" role="status" aria-live="polite">
          <span class="chip" :class="`chip--${connectionChip.tone}`">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true">
              <path d="M5 12.55a11 11 0 0 1 14.08 0" />
              <path d="M8.53 16.11a6 6 0 0 1 6.95 0" />
              <line x1="12" y1="20" x2="12.01" y2="20" />
            </svg>
            {{ connectionChip.text }}
          </span>
          <span class="chip" :class="`chip--${authChip.tone}`">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
            </svg>
            {{ authChip.text }}
          </span>
        </div>
      </header>

      <main class="page-main">
        <!-- E-03 / E-04 踢回横幅 -->
        <div v-if="kickBanner" class="kick-banner" role="alert">
          <template v-if="kickBanner === 'revoked'">
            这台设备的授权已被桌面端撤销。如需继续访问，请回桌面重新发起配对。
          </template>
          <template v-else>
            配对凭据已失效（可能因服务重启或凭据轮换）。本机草稿保留；请重新配对恢复访问。
          </template>
        </div>

        <!-- ① 诊断卡片（自动运行，四类分类） -->
        <DiagnosisCard
          :kind="diagnosis"
          :detail="diagnosisDetail"
          :retrying="retrying"
          @retry="retryDiagnosis"
        />

        <!-- 已授权：进入大厅 + 设备信息 -->
        <section v-if="diagnosis === 'authorized'" class="authorized-panel">
          <p v-if="auth.device" class="authorized-device">
            这台设备：{{ auth.device.name }} · 配对于 {{ auth.device.pairedAt }}
          </p>
          <button type="button" class="btn-primary" @click="router.push({ name: 'lobby' })">
            进入会话大厅
          </button>
        </section>

        <!-- 未配对：配对流程 -->
        <template v-if="diagnosis === 'unpaired'">
          <!-- ⑤ 风险提示（不可"不再提示"） -->
          <RiskBanner />

          <!-- ③ 配对窗口倒计时 -->
          <div v-if="showCountdown" class="countdown-row">
            <CountdownChip :remaining-ms="countdownRemaining" :is-estimate="countdownIsEstimate" />
          </div>

          <!-- E-02 窗口关闭回态 -->
          <div v-if="windowClosedLocally" class="window-closed" role="alert">
            <p class="window-closed-title">配对窗口已关闭</p>
            <p class="window-closed-text">
              桌面端的短时配对窗口已过期或被取消。请回桌面「设置 › 远程访问」重新发起配对，再用新配对码重试。
            </p>
          </div>

          <!-- 配对错误（分类，非笼统失败） -->
          <div v-if="pairError" class="pair-error" role="alert">
            <p class="pair-error-title">{{ pairError.title }}</p>
            <p class="pair-error-text">{{ pairError.description }}</p>
          </div>

          <!-- ② 扫码路径 -->
          <QrScannerPanel
            v-if="pairEntry === 'scan'"
            @decoded="handleQrDecoded"
            @failed="handleScannerFailed"
            @cancel="handleScannerCancel"
          />

          <!-- ② 手动路径：地址 + 配对码 + 设备名 -->
          <form v-else-if="pairEntry === 'manual'" class="pair-form" @submit.prevent="submitPairing">
            <div class="field">
              <label class="field-label" for="pair-address">宿主地址（留空 = 当前地址）</label>
              <input
                id="pair-address"
                v-model="addressInput"
                type="text"
                inputmode="url"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                class="field-input field-input--mono"
                placeholder="192.168.1.100:8680"
              />
            </div>
            <div class="field">
              <label class="field-label" for="pair-code">配对码</label>
              <input
                id="pair-code"
                v-model="codeInput"
                type="text"
                inputmode="text"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                class="field-input field-input--mono"
                placeholder="桌面端显示的一次性配对码"
                autocomplete="off"
              />
            </div>
            <div class="field">
              <label class="field-label" for="pair-device-name">这台设备的名称</label>
              <input
                id="pair-device-name"
                v-model="deviceNameInput"
                type="text"
                class="field-input"
                placeholder="桌面设备列表中显示的名称"
                maxlength="64"
              />
            </div>
            <button type="submit" class="btn-primary" :disabled="!canSubmitPairing">
              {{ submitting ? '正在配对…' : '完成配对' }}
            </button>
            <button type="button" class="btn-secondary" :disabled="submitting" @click="pairEntry = 'none'">
              返回
            </button>
          </form>

          <!-- 入口：扫码主操作 + 手动次操作 -->
          <div v-else class="pair-entries">
            <button type="button" class="btn-primary" @click="beginPairEntry('scan')">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                <path d="M3 7V5a2 2 0 0 1 2-2h2" />
                <path d="M17 3h2a2 2 0 0 1 2 2v2" />
                <path d="M21 17v2a2 2 0 0 1-2 2h-2" />
                <path d="M7 21H5a2 2 0 0 1-2-2v-2" />
                <line x1="7" y1="12" x2="17" y2="12" />
              </svg>
              扫码配对
            </button>
            <button type="button" class="btn-secondary" @click="beginPairEntry('manual')">
              手动输入地址与配对码
            </button>
          </div>
        </template>

        <!-- 恢复入口：这台设备曾配对过（投影非授权事实，点击重新诊断） -->
        <section
          v-if="diagnosis !== 'authorized' && auth.hasDeviceProjection && diagnosis !== 'checking'"
          class="recovery-entry"
        >
          <p class="recovery-text">
            这台设备曾配对为「{{ auth.device?.name }}」。
          </p>
          <button type="button" class="btn-secondary" :disabled="retrying" @click="retryDiagnosis">
            {{ retrying ? '正在重试…' : '重试连接' }}
          </button>
        </section>
      </main>
    </template>
  </div>
</template>

<style scoped>
.connect-page {
  min-height: 100%;
  background: var(--VT-canvas);
  color: var(--VT-text);
  padding: 24px 20px 40px;
  display: flex;
  flex-direction: column;
}

.page-header {
  margin-bottom: 20px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--VT-accent);
}

.brand-text {
  min-width: 0;
}

.brand-title {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
  color: var(--VT-text);
}

.brand-subtitle {
  margin: 2px 0 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
}

.status-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 32px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid var(--VT-border);
  background: var(--VT-surface);
  font-size: 13px;
  font-weight: 600;
  color: var(--VT-text);
}

.chip--success { border-color: var(--VT-success); }
.chip--success svg { color: var(--VT-success); }
.chip--warning { border-color: var(--VT-warning); }
.chip--warning svg { color: var(--VT-warning); }
.chip--danger { border-color: var(--VT-danger); }
.chip--danger svg { color: var(--VT-danger); }
.chip--secondary svg { color: var(--VT-secondary); }

.page-main {
  display: flex;
  flex-direction: column;
  gap: 16px;
  flex: 1;
}

.kick-banner {
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-danger);
  border-radius: 10px;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

.authorized-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.authorized-device {
  margin: 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
}

.countdown-row {
  display: flex;
  justify-content: center;
}

.window-closed {
  padding: 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-warning);
  border-radius: 10px;
}

.window-closed-title {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.window-closed-text {
  margin: 0;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

.pair-error {
  padding: 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-danger);
  border-radius: 10px;
}

.pair-error-title {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.pair-error-text {
  margin: 0;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

.pair-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.field-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--VT-text);
}

.field-input {
  width: 100%;
  min-height: 44px;
  padding: 10px 12px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  color: var(--VT-text);
  font-size: 16px;
  box-sizing: border-box;
}

.field-input--mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
  font-variant-numeric: tabular-nums;
}

.field-input:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 1px;
  border-color: var(--VT-accent);
}

.field-input::placeholder {
  color: var(--VT-text-secondary);
}

.btn-primary,
.btn-secondary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  min-height: 44px;
  padding: 0 20px;
  border-radius: 8px;
  font-size: 16px;
  font-weight: 600;
  cursor: pointer;
}

.btn-primary {
  border: none;
  background: var(--VT-accent-strong);
  color: #fff;
}

.btn-primary:disabled {
  background: var(--VT-surface-raised);
  color: var(--VT-text-disabled);
  cursor: not-allowed;
}

.btn-secondary {
  border: 1px solid var(--VT-border-strong);
  background: transparent;
  color: var(--VT-text);
}

.btn-secondary:disabled {
  border-color: var(--VT-border);
  color: var(--VT-text-disabled);
  cursor: not-allowed;
}

.btn-primary:focus-visible,
.btn-secondary:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .btn-primary:hover:not(:disabled) {
    background: var(--VT-accent);
  }
  .btn-secondary:hover:not(:disabled) {
    background: var(--VT-surface-raised);
  }
}

.pair-entries {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.recovery-entry {
  margin-top: auto;
  padding-top: 20px;
  border-top: 1px solid var(--VT-border);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.recovery-text {
  margin: 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
}

.webapi-fallback {
  padding: 32px 4px;
}

.fallback-title {
  margin: 0 0 12px;
  font-size: 20px;
  font-weight: 700;
  color: var(--VT-text);
}

.fallback-text {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--VT-text);
}

/* reduced-motion：本页无装饰动效；spinner 降级在 DiagnosisCard 内处理 */
@media (prefers-reduced-motion: reduce) {
  .connect-page * {
    transition: none !important;
    animation-duration: 0.01ms !important;
  }
}
</style>
