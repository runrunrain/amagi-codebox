<script setup lang="ts">
/**
 * SettingsPage — 移动伴侣端设置与桌面端管理引导页
 * ---------------------------------------------------------------------------
 * 权威依据：执行计划 Phase 2 (P2-A)；调研报告素材 D §2/§3。
 *
 * 架构说明：
 *   · 移动端作为轻量伴侣控制界面，提供远程会话监控、启动与终端交互能力；
 *   · 三个 legacy 页面（Dashboard / Providers / Settings）服务端要求 loopback-only
 *     且依赖 Bearer 鉴权，在移动端功能性不可用；
 *   · 本页下线 legacy apiClient，全面消费 v1 契约（useAuthStore / useLobbyStore），
 *     提供清晰明确的「请在桌面端管理」静态引导与真实宿主状态展示。
 * ---------------------------------------------------------------------------
 */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useLobbyStore } from '../stores/lobby'

const router = useRouter()
const auth = useAuthStore()
const lobby = useLobbyStore()

const serverUrl = computed(() => (typeof window !== 'undefined' ? window.location.origin : ''))
const hostVersion = computed(() => auth.host?.serverVersion ?? lobby.host?.serverVersion ?? '已连接')
const apiVersion = computed(() => auth.host?.apiVersion ?? lobby.host?.apiVersion ?? 'v1')

function goLobby() {
  router.push('/lobby')
}

function handleDisconnect() {
  auth.clearLocal()
  router.push('/connect')
}
</script>

<template>
  <div class="settings-page" data-testid="settings-page">
    <!-- 页头导航 -->
    <header class="settings-header">
      <button
        type="button"
        class="header-back-btn"
        aria-label="返回会话大厅"
        data-testid="settings-back-btn"
        @click="goLobby"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        <span>大厅</span>
      </button>
      <h1 class="header-title">设置与管理</h1>
      <div class="header-placeholder" aria-hidden="true"></div>
    </header>

    <main class="settings-content">
      <!-- 桌面端专属管理提示区 -->
      <section class="guide-section" data-testid="desktop-mgmt-section" aria-labelledby="mgmt-heading">
        <div class="section-title-wrap">
          <h2 id="mgmt-heading" class="section-title">桌面端专属管理</h2>
          <span class="badge badge--desktop">桌面端操作</span>
        </div>
        <p class="section-intro">
          CodeBox 移动端作为伴侣控制面，专注于远程监控与终端交互。以下配置与高级审计涉及系统安全与本地存储，仅限在桌面端 CodeBox 管理：
        </p>

        <div class="guide-cards">
          <!-- Card 1: Providers -->
          <article class="guide-card" data-testid="desktop-providers-guide">
            <div class="card-icon card-icon--purple" aria-hidden="true">
              <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 2L2 7l10 5 10-5-10-5z" />
                <path d="M2 17l10 5 10-5" />
                <path d="M2 12l10 5 10-5" />
              </svg>
            </div>
            <div class="card-body">
              <div class="card-header">
                <h3 class="card-title">模型服务商配置 (Providers)</h3>
                <span class="card-tag">Desktop Only</span>
              </div>
              <p class="card-desc">
                添加或修改 AI 服务商、调整模型端点与参数，以及通过系统钥匙串（macOS Keychain / Windows DPAPI）加密存储 API Key，请在桌面端「服务商设置」中完成。移动端创建新会话时自动沿用已配置好的服务商与预设。
              </p>
            </div>
          </article>

          <!-- Card 2: Server Settings -->
          <article class="guide-card" data-testid="desktop-server-guide">
            <div class="card-icon card-icon--green" aria-hidden="true">
              <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="3" />
                <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
              </svg>
            </div>
            <div class="card-body">
              <div class="card-header">
                <h3 class="card-title">服务端与网络配置 (Host Settings)</h3>
                <span class="card-tag">Desktop Only</span>
              </div>
              <p class="card-desc">
                远程控制启用开关、监听端口、LAN 网络接口绑定、日志等级及开机自启属于宿主全局系统配置，请直接在桌面端「远程控制」面板中调整。
              </p>
            </div>
          </article>

          <!-- Card 3: Dashboard & Usage -->
          <article class="guide-card" data-testid="desktop-dashboard-guide">
            <div class="card-icon card-icon--blue" aria-hidden="true">
              <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="3" y="3" width="7" height="7" />
                <rect x="14" y="3" width="7" height="7" />
                <rect x="3" y="14" width="7" height="7" />
                <rect x="14" y="14" width="7" height="7" />
              </svg>
            </div>
            <div class="card-body">
              <div class="card-header">
                <h3 class="card-title">使用量与统计大盘 (Dashboard)</h3>
                <span class="card-tag">Desktop Only</span>
              </div>
              <p class="card-desc">
                各 CLI 会话详细的 Token 消耗、本地 SQLite 记账与成本分析报表已在桌面端就绪。移动端会话大厅与工作区实时提供运行中会话的状态投影与交互。
              </p>
            </div>
          </article>
        </div>
      </section>

      <!-- 移动伴侣与宿主连接信息 -->
      <section class="status-section" data-testid="host-info-section" aria-labelledby="status-heading">
        <h2 id="status-heading" class="section-title">系统与配对信息</h2>
        <div class="info-table" role="table" aria-label="系统与配对信息详情">
          <div class="info-row" role="row">
            <span class="info-label" role="rowheader">移动端应用版本</span>
            <span class="info-value" role="cell">v1.0.5</span>
          </div>
          <div class="info-row" role="row">
            <span class="info-label" role="rowheader">宿主版本</span>
            <span class="info-value" role="cell">{{ hostVersion }}</span>
          </div>
          <div class="info-row" role="row">
            <span class="info-label" role="rowheader">契约 API 版本</span>
            <span class="info-value" role="cell">{{ apiVersion }}</span>
          </div>
          <div class="info-row" role="row">
            <span class="info-label" role="rowheader">当前配对设备</span>
            <span class="info-value" role="cell">{{ auth.device?.name ?? '已授权伴侣设备' }}</span>
          </div>
          <div class="info-row" role="row">
            <span class="info-label" role="rowheader">配对状态</span>
            <span class="info-value highlight-success" role="cell">
              {{ auth.isPaired ? '已配对 (Cookie 凭据受保护)' : '未配对' }}
            </span>
          </div>
          <div class="info-row" role="row">
            <span class="info-label" role="rowheader">宿主访问地址</span>
            <span class="info-value truncate" role="cell" :title="serverUrl">{{ serverUrl }}</span>
          </div>
        </div>
      </section>

      <!-- 底部快捷操作 -->
      <section class="actions-section" data-testid="settings-actions">
        <button
          type="button"
          class="btn-primary"
          data-testid="go-lobby-btn"
          @click="goLobby"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <rect x="2" y="3" width="20" height="14" rx="2" />
            <line x1="8" y1="21" x2="16" y2="21" />
            <line x1="12" y1="17" x2="12" y2="21" />
          </svg>
          <span>进入会话大厅</span>
        </button>

        <button
          type="button"
          class="btn-danger-outline"
          data-testid="disconnect-btn"
          @click="handleDisconnect"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
            <polyline points="16 17 21 12 16 7" />
            <line x1="21" y1="12" x2="9" y2="12" />
          </svg>
          <span>断开设备配对</span>
        </button>
      </section>
    </main>
  </div>
</template>

<style scoped>
.settings-page {
  display: flex;
  flex-direction: column;
  min-height: 100%;
  background: #0d1117;
  color: #c9d1d9;
}

.settings-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 52px;
  padding: 0 16px;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  flex-shrink: 0;
}

.header-back-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 44px;
  min-width: 44px;
  padding: 0 8px;
  margin-left: -8px;
  background: transparent;
  border: none;
  color: #58a6ff;
  font-size: 15px;
  font-weight: 500;
  cursor: pointer;
  border-radius: 6px;
}

.header-back-btn:hover {
  background: rgba(88, 166, 255, 0.1);
}

.header-back-btn:focus-visible {
  outline: 2px solid #58a6ff;
  outline-offset: 2px;
}

.header-title {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
  color: #f0f6fc;
}

.header-placeholder {
  width: 44px;
}

.settings-content {
  flex: 1;
  padding: 20px 16px calc(24px + env(safe-area-inset-bottom, 0px));
  display: flex;
  flex-direction: column;
  gap: 24px;
  max-width: 640px;
  margin: 0 auto;
  width: 100%;
  box-sizing: border-box;
}

.section-title-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.section-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: #f0f6fc;
}

.badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 11px;
  font-weight: 600;
}

.badge--desktop {
  background: rgba(88, 166, 255, 0.15);
  color: #58a6ff;
  border: 1px solid rgba(88, 166, 255, 0.3);
}

.section-intro {
  margin: 0 0 14px;
  font-size: 13px;
  line-height: 1.5;
  color: #8b949e;
}

.guide-cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.guide-card {
  display: flex;
  gap: 14px;
  padding: 14px;
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 10px;
}

.card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 8px;
  flex-shrink: 0;
}

.card-icon--purple {
  background: rgba(210, 168, 255, 0.15);
  color: #d2a8ff;
}

.card-icon--green {
  background: rgba(63, 185, 80, 0.15);
  color: #3fb950;
}

.card-icon--blue {
  background: rgba(88, 166, 255, 0.15);
  color: #58a6ff;
}

.card-body {
  flex: 1;
  min-width: 0;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: #f0f6fc;
}

.card-tag {
  font-size: 11px;
  color: #8b949e;
  background: #21262d;
  padding: 1px 6px;
  border-radius: 4px;
  white-space: nowrap;
}

.card-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: #8b949e;
}

.status-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.info-table {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 10px;
  overflow: hidden;
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 14px;
  border-bottom: 1px solid #21262d;
  font-size: 13px;
}

.info-row:last-child {
  border-bottom: none;
}

.info-label {
  color: #8b949e;
  flex-shrink: 0;
  margin-right: 12px;
}

.info-value {
  color: #f0f6fc;
  font-weight: 500;
  text-align: right;
}

.highlight-success {
  color: #3fb950;
}

.truncate {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 220px;
}

.actions-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 4px;
}

.btn-primary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  padding: 0 16px;
  background: #238636;
  color: #ffffff;
  border: 1px solid rgba(240, 246, 252, 0.1);
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s ease;
}

.btn-primary:hover {
  background: #2ea043;
}

.btn-primary:focus-visible {
  outline: 2px solid #58a6ff;
  outline-offset: 2px;
}

.btn-danger-outline {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  padding: 0 16px;
  background: transparent;
  color: #f85149;
  border: 1px solid rgba(248, 81, 73, 0.3);
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s ease, border-color 0.15s ease;
}

.btn-danger-outline:hover {
  background: rgba(248, 81, 73, 0.1);
  border-color: #f85149;
}

.btn-danger-outline:focus-visible {
  outline: 2px solid #f85149;
  outline-offset: 2px;
}
</style>
