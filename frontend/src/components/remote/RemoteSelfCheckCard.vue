<!--
  远程服务自检卡（P3-A / PG-05 增强）
  环境与运行状态多维自检：
  1. 服务运行状态（running / stopped）
  2. 监听地址与局域网网络可达性（0.0.0.0 通配 / 127.0.0.1 回环警告 / 指定网卡）
  3. 监听端口状态与端口占用告警（1024–65535 范围、被占用排查建议与候选端口推荐）
  4. 安全迁移门禁状态（GetStartupWarnings 透出 migrationWarn*，fail-closed 拦截警告）
  5. 安全子系统就绪状态（SecurityHealth securityReady）
  6. 移动端 Web UI 托管可用性（GetRemoteWebUIStatus 检查内嵌 mobile/dist 资源）
  7. 宿主可用性探测降级提示（P2-B 移交 + P3-B R2 数据源：由远程状态绑定透出
     的 hostSummaryDegraded（v1 HostSummary 缓存最近错误态）判定，替代桌面
     无法直读的 /host/summary fetch 探测）
  另：移动端访问基准地址展示与一键复制（通配时优先用配对卡手动局域网地址）
-->
<template>
  <section class="rc-card selfcheck-card" aria-labelledby="rc-selfcheck-title" data-testid="remote-selfcheck-card">
    <header class="rc-card-head selfcheck-head">
      <div class="selfcheck-title-wrap">
        <h2 id="rc-selfcheck-title" class="rc-card-title">远程服务自检</h2>
        <p class="rc-card-sub">多维环境自检 · 监听范围与端口评估 · 迁移门禁核验</p>
      </div>

      <div class="selfcheck-actions">
        <!-- 总体健康徽标 -->
        <span
          class="selfcheck-badge"
          :class="overallStatusClass"
          data-testid="selfcheck-overall-badge"
        >
          <span class="badge-dot" aria-hidden="true" />
          {{ overallStatusText }}
        </span>

        <button
          type="button"
          class="rc-btn rc-btn-secondary selfcheck-refresh-btn"
          data-testid="selfcheck-refresh-btn"
          :disabled="refreshing"
          :aria-busy="refreshing"
          @click="onRefresh"
        >
          {{ refreshing ? '诊断中…' : '重新自检' }}
        </button>
      </div>
    </header>

    <!-- 告警与指引横幅区 -->
    <div v-if="gateBlockedWarnings.length > 0" class="selfcheck-banner is-danger" role="alert" data-testid="gate-blocked-warning">
      <div class="banner-icon" aria-hidden="true">⛔</div>
      <div class="banner-content">
        <strong class="banner-title">安全迁移门禁已禁止远程功能启动（Fail-Closed 保护）</strong>
        <p v-for="(warn, idx) in gateBlockedWarnings" :key="idx" class="banner-msg">{{ warn }}</p>
        <p class="banner-hint">
          安全迁移门禁在检测到版本不匹配、未完成迁移痕迹或配置损坏时，会禁止对外开启远程网络监听，以防未受保护的接口泄露。若提示需要手动修复，请检查 settings.json 并清理迁移标记后重启应用。
        </p>
      </div>
    </div>

    <div v-if="hasPortConflict" class="selfcheck-banner is-danger" role="alert" data-testid="port-conflict-warning">
      <div class="banner-icon" aria-hidden="true">🔴</div>
      <div class="banner-content">
        <strong class="banner-title">监听端口 {{ port }} 已被占用或不可用</strong>
        <p class="banner-msg">
          无法在当前端口建立 TCP 监听。其他后台进程或残留的 CodeBox 进程可能正在占用此端口。
        </p>
        <p class="banner-hint">
          <strong>排查建议：</strong>请在下方『远程服务』卡片中，将监听端口更换为其他空闲端口（例如 <strong>{{ recommendedPort }}</strong>），点击『应用』后重试启动。
        </p>
      </div>
    </div>

    <div v-if="hostSummaryDegraded" class="selfcheck-banner is-warning" role="alert" data-testid="hostsummary-degraded-warning">
      <div class="banner-icon" aria-hidden="true">🛡️</div>
      <div class="banner-content">
        <strong class="banner-title">宿主探测降级中 · 移动端将显示全部 CLI 不可启动</strong>
        <p class="banner-msg">
          宿主可用性探测失败，服务端已降级为保守响应（全部 CLI 标记为不可启动，版本未知）。已配对设备的会话浏览不受影响，但移动端无法远程启动新会话。
        </p>
        <p class="banner-hint">
          常见原因：提供商配置异常或 CLI 探测超时。建议检查提供商配置后重试自检；重启应用可清除探测缓存。
        </p>
      </div>
    </div>

    <div v-if="isLoopback" class="selfcheck-banner is-warning" role="alert" data-testid="loopback-scope-warning">
      <div class="banner-icon" aria-hidden="true">⚠️</div>
      <div class="banner-content">
        <strong class="banner-title">当前监听在回环地址 ({{ host }}) · 局域网移动设备无法访问</strong>
        <p class="banner-msg">
          回环地址仅允许本机内部进程互联。同一 Wi-Fi 或局域网内的手机扫码时将无法连接。
        </p>
        <div class="banner-actions">
          <button
            type="button"
            class="rc-btn rc-btn-secondary"
            data-testid="fix-host-wildcard-btn"
            :disabled="running"
            @click="emit('set-host-wildcard')"
          >
            填入 0.0.0.0（通配监听）
          </button>
          <span v-if="running" class="banner-action-note">（需先停止服务后再修改监听地址）</span>
        </div>
      </div>
    </div>

    <div v-if="!lanConfirmed && !running && gateBlockedWarnings.length === 0" class="selfcheck-banner is-info" role="note">
      <div class="banner-icon" aria-hidden="true">ℹ️</div>
      <div class="banner-content">
        <strong class="banner-title">开启服务前需确认 LAN 暴露风险</strong>
        <p class="banner-msg">
          根据安全规范，远程服务对外暴露前需要显式确认局域网暴露风险。
        </p>
        <button
          type="button"
          class="rc-link"
          style="padding: 0; min-height: unset;"
          @click="emit('goto-lan-confirm')"
        >
          前往确认卡片 ↓
        </button>
      </div>
    </div>

    <!-- 自检七项指标网格 -->
    <div class="selfcheck-grid">
      <!-- 1. 服务状态 -->
      <div class="sc-item" :class="`status-${running ? 'ok' : 'stopped'}`" data-testid="sc-item-service">
        <div class="sc-item-head">
          <span class="sc-label">服务进程</span>
          <span class="sc-status-pill">{{ running ? '运行中' : '未启动' }}</span>
        </div>
        <div class="sc-val mono">{{ running ? `TCP :${port} 活跃` : '进程未监听' }}</div>
        <div class="sc-detail">{{ running ? 'HTTP 与 WebSocket 服务正常接收连接' : '可通过下方远程服务卡开启' }}</div>
      </div>

      <!-- 2. 监听网卡与范围 -->
      <div class="sc-item" :class="`status-${isLoopback ? 'warning' : 'ok'}`" data-testid="sc-item-network">
        <div class="sc-item-head">
          <span class="sc-label">监听范围</span>
          <span class="sc-status-pill">{{ isLoopback ? '回环监听' : '局域网可见' }}</span>
        </div>
        <div class="sc-val mono">{{ host }}</div>
        <div class="sc-detail">
          {{ isLoopback ? '回环地址，移动端无法连接' : (isWildcard ? '通配所有网卡，移动端可通过本机 IP 访问' : '指定网卡 IP 监听') }}
        </div>
      </div>

      <!-- 3. 端口状态 -->
      <div class="sc-item" :class="`status-${hasPortConflict ? 'danger' : 'ok'}`" data-testid="sc-item-port">
        <div class="sc-item-head">
          <span class="sc-label">监听端口</span>
          <span class="sc-status-pill">{{ hasPortConflict ? '端口占用' : '端口正常' }}</span>
        </div>
        <div class="sc-val mono">:{{ port }}</div>
        <div class="sc-detail">
          {{ hasPortConflict ? `端口冲突，建议更换为 ${recommendedPort}` : '处于合法范围 (1024–65535)' }}
        </div>
      </div>

      <!-- 4. 安全迁移门禁 -->
      <div class="sc-item" :class="`status-${gateBlockedWarnings.length > 0 ? 'danger' : 'ok'}`" data-testid="sc-item-gate">
        <div class="sc-item-head">
          <span class="sc-label">安全迁移门禁</span>
          <span class="sc-status-pill">{{ gateBlockedWarnings.length > 0 ? '门禁拦截' : '检查通过' }}</span>
        </div>
        <div class="sc-val">{{ gateBlockedWarnings.length > 0 ? '禁止启动' : '通过' }}</div>
        <div class="sc-detail">
          {{ gateBlockedWarnings.length > 0 ? '存在配置迁移警告，fail-closed' : '设置版本正常，无阻断警告' }}
        </div>
      </div>

      <!-- 5. 安全子系统就绪 -->
      <div class="sc-item" :class="`status-${securitySubsystemReady ? 'ok' : 'warning'}`" data-testid="sc-item-security">
        <div class="sc-item-head">
          <span class="sc-label">安全子系统</span>
          <span class="sc-status-pill">{{ securitySubsystemReady ? '正常就绪' : '部分受限' }}</span>
        </div>
        <div class="sc-val">{{ securitySubsystemReady ? '凭据系统就绪' : '存储受限' }}</div>
        <div class="sc-detail">
          {{ securitySubsystemReady ? '设备注册表、配对窗口与事件审计正常' : '安全存储或设备注册表未完全就绪' }}
        </div>
      </div>

      <!-- 6. 移动端 Web UI 静态托管 -->
      <div class="sc-item" :class="`status-${webUIAvailable ? 'ok' : 'warning'}`" data-testid="sc-item-webui">
        <div class="sc-item-head">
          <span class="sc-label">移动端 Web UI</span>
          <span class="sc-status-pill">{{ webUIAvailable ? '内嵌就绪' : '不可用' }}</span>
        </div>
        <div class="sc-val">{{ webUIAvailable ? 'mobile/dist' : '缺失' }}</div>
        <div class="sc-detail">
          {{ webUIAvailable ? '已集成内嵌移动端 Web 控制界面' : (webUIReason || '静态资源不可用') }}
        </div>
      </div>

      <!-- 7. 宿主可用性探测（P2-B 降级透出） -->
      <div class="sc-item" :class="hostSummaryItemClass" data-testid="sc-item-hostsummary">
        <div class="sc-item-head">
          <span class="sc-label">宿主可用性探测</span>
          <span class="sc-status-pill">{{ hostSummaryItemPill }}</span>
        </div>
        <div class="sc-val">{{ hostSummaryItemValue }}</div>
        <div class="sc-detail">{{ hostSummaryItemDetail }}</div>
      </div>
    </div>

    <!-- 移动端访问基准地址展示与一键复制 -->
    <div class="selfcheck-access">
      <div class="access-head">
        <span class="access-title">移动端访问基准地址</span>
        <span v-if="!running" class="access-state-hint">服务停止中 · 开启后可用于局域网浏览器访问</span>
      </div>

      <div class="access-body">
        <div class="access-url-wrap">
          <span class="access-url mono" data-testid="base-access-url">{{ displayAccessUrl }}</span>
          <span v-if="showWildcardIpTip" class="access-ip-tip">（注：需将 0.0.0.0 替换为本机实际局域网 IP，或在下方配对卡直接扫码）</span>
        </div>

        <button
          type="button"
          class="rc-btn rc-btn-secondary copy-btn"
          data-testid="copy-base-url-btn"
          :disabled="!running"
          @click="onCopyUrl"
        >
          <span v-if="copied" class="copied-text">已复制 ✓</span>
          <span v-else>复制访问地址</span>
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import type { remote, main } from '../../../wailsjs/go/models';
import { getStartupWarnings, getRemoteWebUIStatus } from '../../api/remote';
import {
  isLoopbackHost,
  isWildcardHost,
  copyTextToClipboard,
  readCustomLanHost,
  hostSummaryStateFromStatus,
  type ClassifiedError,
  type HostSummaryProbeState,
} from './remoteShared';
import { useToast } from '../../composables/useToast';

interface Props {
  running: boolean;
  host: string;
  port: number;
  lanConfirmed: boolean;
  health: remote.SecurityHealthSnapshot | null;
  lastServiceError?: ClassifiedError | null;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'refresh'): void;
  (e: 'goto-lan-confirm'): void;
  (e: 'set-host-wildcard'): void;
}>();

const { showSuccess, showError } = useToast();

const refreshing = ref(false);
const copied = ref(false);
const allStartupWarnings = ref<string[]>([]);
const webUIStatus = ref<main.RemoteWebUIStatusResult | null>(null);
/** 宿主可用性探测态（P3-B R2：由状态绑定透出的 hostSummaryDegraded 驱动） */
const hostSummaryState = ref<HostSummaryProbeState>('unreachable');
/** 配对卡持久化的手动局域网地址（通配监听时用于展示可用访问地址） */
const customLanHost = ref(readCustomLanHost());

const isLoopback = computed(() => isLoopbackHost(props.host));
const isWildcard = computed(() => isWildcardHost(props.host));

/**
 * 匹配安全迁移门禁（fail-closed）拦截警告。
 * 后端 migrationWarn* 全部以固定前缀「远程安全设置迁移：」开头（无泄漏文本），
 * 仅按前缀匹配，避免误伤其他启动警告（外部清理/预设迁移/环境检测等）。
 */
const gateBlockedWarnings = computed(() =>
  allStartupWarnings.value.filter((w) => w.includes('远程安全设置迁移')),
);

/** 检测是否有端口冲突告警 */
const hasPortConflict = computed(() => {
  if (props.lastServiceError?.category === 'port-conflict') return true;
  return false;
});

/** 推荐更换的备选端口 */
const recommendedPort = computed(() => {
  const current = props.port || 8680;
  if (current >= 8680 && current < 8690) return current + 1;
  return 8681;
});

const securitySubsystemReady = computed(() => {
  if (!props.health) return true;
  return props.health.securityReady !== false;
});

const webUIAvailable = computed(() => {
  if (!webUIStatus.value) return true; // 默认假设可用
  return (
    webUIStatus.value.mobileWebAvailable ||
    webUIStatus.value.mobileWebEmbedded ||
    webUIStatus.value.openable
  );
});

const webUIReason = computed(() => webUIStatus.value?.reason || '');

/** 宿主探测降级中（保守 200：全 CLI 不可启动 + 版本未知） */
const hostSummaryDegraded = computed(
  () => props.running && hostSummaryState.value === 'degraded',
);

const hostSummaryItemClass = computed(() => {
  if (!props.running) return 'status-stopped';
  if (hostSummaryState.value === 'degraded') return 'status-warning';
  if (hostSummaryState.value === 'normal') return 'status-ok';
  return 'status-stopped';
});

const hostSummaryItemPill = computed(() => {
  if (!props.running) return '未运行';
  if (hostSummaryState.value === 'degraded') return '降级中';
  if (hostSummaryState.value === 'normal') return '探测正常';
  return '无法直读';
});

const hostSummaryItemValue = computed(() => {
  if (!props.running) return '服务未运行';
  if (hostSummaryState.value === 'degraded') return '保守降级响应';
  if (hostSummaryState.value === 'normal') return 'CLI 可用性正常';
  return '状态读取失败';
});

const hostSummaryItemDetail = computed(() => {
  if (!props.running) return '开启服务后可探测';
  if (hostSummaryState.value === 'degraded') {
    return '探测失败降级：移动端全部 CLI 显示不可启动，会话浏览不受影响';
  }
  if (hostSummaryState.value === 'normal') return '宿主摘要缓存健康，移动端可远程启动会话';
  return '远程状态绑定拉取失败，暂无法判定降级态；可重新自检重试';
});

const overallStatusClass = computed(() => {
  if (gateBlockedWarnings.value.length > 0 || hasPortConflict.value) return 'status-danger';
  if (isLoopback.value || !props.lanConfirmed) return 'status-warning';
  if (props.running) return 'status-ok';
  return 'status-stopped';
});

const overallStatusText = computed(() => {
  if (gateBlockedWarnings.value.length > 0) return '门禁拦截';
  if (hasPortConflict.value) return '端口占用';
  if (isLoopback.value) return '回环受限';
  if (!props.lanConfirmed && !props.running) return '待 LAN 确认';
  if (props.running) return '服务正常';
  return '就绪 · 待开启';
});

const displayAccessUrl = computed(() => {
  let h = props.host || '127.0.0.1';
  if (isWildcardHost(h)) {
    // 通配监听时 0.0.0.0 不是可直达地址：优先展示配对卡持久化的手动局域网地址
    h = customLanHost.value || '0.0.0.0';
  }
  const hostPart = h.includes(':') && !h.startsWith('[') ? `[${h}]` : h;
  return `http://${hostPart}:${props.port}/`;
});

/** 通配且无手动局域网地址时，需提示用户替换为实际 IP */
const showWildcardIpTip = computed(
  () => isWildcard.value && !customLanHost.value && props.running,
);

async function loadDiagnostics() {
  try {
    const tasks: [Promise<any>, Promise<any> | null] = [
      getStartupWarnings(),
      typeof (window as any)?.go?.main?.App?.GetRemoteWebUIStatus === 'function'
        ? getRemoteWebUIStatus()
        : null,
    ];

    const [warnsRes, webRes] = await Promise.allSettled(
      tasks.map((t) => (t ? t : Promise.resolve(null))),
    );

    if (warnsRes.status === 'fulfilled' && Array.isArray(warnsRes.value)) {
      allStartupWarnings.value = warnsRes.value.filter((w) => typeof w === 'string' && w);
    }
    if (webRes.status === 'fulfilled' && webRes.value) {
      webUIStatus.value = webRes.value;
    }

    // 宿主可用性探测（P3-B R2 数据源替换）：由状态绑定透出的
    // hostSummaryDegraded（hostSummary 缓存最近错误态）判定降级；绑定拉取
    // 失败时 webUIStatus 为空 → 诚实收敛为 unreachable（无法直读），不伪造。
    hostSummaryState.value = hostSummaryStateFromStatus(props.running, webUIStatus.value);
    // 配对卡可能在期间写入手动局域网地址，同步刷新展示
    customLanHost.value = readCustomLanHost();
  } catch {
    // 诊断拉取非关键，维持降级展示
  }
}

async function onRefresh() {
  refreshing.value = true;
  emit('refresh');
  await loadDiagnostics();
  setTimeout(() => {
    refreshing.value = false;
    showSuccess('自检诊断已刷新');
  }, 300);
}

async function onCopyUrl() {
  if (!props.running) return;
  const ok = await copyTextToClipboard(displayAccessUrl.value);
  if (ok) {
    copied.value = true;
    showSuccess('基准访问地址已复制到剪贴板');
    setTimeout(() => {
      copied.value = false;
    }, 2000);
  } else {
    showError('复制失败，请手动选取复制');
  }
}

onMounted(() => {
  void loadDiagnostics();
});
</script>

<style scoped>
.selfcheck-card {
  border-left: 4px solid var(--vt-control);
}

.selfcheck-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.selfcheck-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.selfcheck-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 600;
  border-radius: 999px;
  line-height: 1.2;
}

.badge-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
}

.selfcheck-badge.status-ok {
  background: rgba(47, 125, 70, 0.12);
  color: var(--vt-success);
}

.selfcheck-badge.status-warning {
  background: rgba(138, 90, 18, 0.12);
  color: var(--vt-warning);
}

.selfcheck-badge.status-danger {
  background: rgba(188, 63, 63, 0.12);
  color: var(--vt-danger);
}

.selfcheck-badge.status-stopped {
  background: rgba(108, 106, 100, 0.12);
  color: var(--vt-text-secondary);
}

.selfcheck-refresh-btn {
  min-height: 36px;
  padding: 6px 12px;
  font-size: 13px;
}

/* 告警横幅样式 */
.selfcheck-banner {
  display: flex;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 10px;
  margin-bottom: 14px;
  font-size: 13px;
  line-height: 1.55;
}

.selfcheck-banner.is-danger {
  background: rgba(188, 63, 63, 0.08);
  border: 1px solid rgba(188, 63, 63, 0.3);
  color: var(--vt-text);
}

.selfcheck-banner.is-warning {
  background: rgba(138, 90, 18, 0.08);
  border: 1px solid rgba(138, 90, 18, 0.3);
  color: var(--vt-text);
}

.selfcheck-banner.is-info {
  background: rgba(51, 96, 125, 0.08);
  border: 1px solid rgba(51, 96, 125, 0.25);
  color: var(--vt-text);
}

.banner-icon {
  font-size: 16px;
  flex-shrink: 0;
  line-height: 1.4;
}

.banner-content {
  flex: 1;
  min-width: 0;
}

.banner-title {
  display: block;
  font-weight: 600;
  margin-bottom: 2px;
  color: var(--vt-text);
}

.banner-msg {
  margin: 2px 0;
  color: var(--vt-text);
}

.banner-hint {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.banner-actions {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 10px;
}

.banner-action-note {
  font-size: 12px;
  color: var(--vt-text-secondary);
}

/* 六项指标网格 */
.selfcheck-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.sc-item {
  background: var(--vt-canvas);
  border: 1px solid var(--vt-border);
  border-radius: 10px;
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.sc-item.status-ok {
  border-left: 3px solid var(--vt-success);
}

.sc-item.status-warning {
  border-left: 3px solid var(--vt-warning);
}

.sc-item.status-danger {
  border-left: 3px solid var(--vt-danger);
}

.sc-item.status-stopped {
  border-left: 3px solid var(--vt-border-strong);
}

.sc-item-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.sc-label {
  font-size: 12px;
  color: var(--vt-text-secondary);
  font-weight: 500;
}

.sc-status-pill {
  font-size: 11px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 4px;
}

.status-ok .sc-status-pill {
  background: rgba(47, 125, 70, 0.12);
  color: var(--vt-success);
}

.status-warning .sc-status-pill {
  background: rgba(138, 90, 18, 0.12);
  color: var(--vt-warning);
}

.status-danger .sc-status-pill {
  background: rgba(188, 63, 63, 0.12);
  color: var(--vt-danger);
}

.status-stopped .sc-status-pill {
  background: rgba(108, 106, 100, 0.12);
  color: var(--vt-text-secondary);
}

.sc-val {
  font-size: 14px;
  font-weight: 600;
  color: var(--vt-text);
}

.sc-detail {
  font-size: 12px;
  color: var(--vt-text-secondary);
  line-height: 1.4;
}

/* 移动端访问基准地址 */
.selfcheck-access {
  background: var(--vt-surface-raised);
  border: 1px solid var(--vt-border);
  border-radius: 10px;
  padding: 12px 14px;
}

.access-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  margin-bottom: 6px;
}

.access-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--vt-text);
}

.access-state-hint {
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.access-body {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.access-url-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.access-url {
  font-size: 14px;
  font-weight: 600;
  color: var(--vt-text);
  word-break: break-all;
}

.access-ip-tip {
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.copy-btn {
  min-height: 38px;
  padding: 6px 14px;
  font-size: 13px;
  flex-shrink: 0;
}

.copied-text {
  color: var(--vt-success);
  font-weight: 600;
}
</style>
