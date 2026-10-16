<template>
  <div class="set-card" v-if="showPanel">
    <h2>WSL 内的 CLI</h2>
    <p class="set-sub">
      Windows 上终端默认在 WSL (Linux) 中启动。将 CLI 安装到 WSL 内部，即可在 Linux 环境原生运行。
    </p>

    <div v-if="!status || !status.available" class="empty-row">
      {{ status?.reason || '正在检测 WSL 环境…' }}
    </div>

    <template v-else>
      <div class="wsl-meta">
        <span class="mono">发行版: {{ status.distro }}</span>
        <span v-if="status.distroWSLVersion === 2" class="badge badge-wsl2">WSL2</span>
        <span v-else-if="status.distroWSLVersion === 1" class="badge">WSL1</span>
        <span class="mono">
          Node: {{ status.nodeVersion || '未安装 (安装 CLI 时会自动安装 Node 20)' }}
        </span>
      </div>

      <div class="setting-list">
        <div v-for="t in status.tools" :key="t.tool" class="setting-row">
          <label>{{ displayName(t.tool) }}</label>
          <div class="row-value">
            <span v-if="t.installed" class="badge badge-ok">已安装 {{ t.version }}</span>
            <span v-else class="badge badge-missing">未安装</span>
            <AppButton
              variant="primary"
              size="small"
              :disabled="busyTool === t.tool"
              @click="install(t.tool)"
            >
              {{ busyTool === t.tool ? '安装中…' : (t.installed ? '重新安装' : '安装到 WSL') }}
            </AppButton>
          </div>
        </div>
      </div>

      <p v-if="busyTool" class="set-hint">
        正在 WSL 内安装 {{ displayName(busyTool) }}，首次可能需要安装 Node 运行时，请稍候…
      </p>

      <!-- P3-S1 最小「配置状态」区：托管面同步状态 + 视觉清单共享同意入口 -->
      <div v-if="supply" class="supply-section">
        <h3>配置状态</h3>
        <p class="set-sub">
          Windows 面板保存后，托管配置按需同步到 WSL（{{ supply.distro }}）；
          已安装不等于已同步，安装成功也不改写 WSL 内的用户自有文件。
        </p>
        <div class="supply-list">
          <div v-for="f in supply.faces" :key="f.key" class="supply-row">
            <label>{{ f.label }}</label>
            <div class="row-value">
              <span class="badge" :class="faceBadgeClass(f.state)">{{ faceStateText(f.state) }}</span>
              <span class="supply-detail">{{ f.detail }}</span>
              <span v-if="f.lastCheck" class="supply-time">检查于 {{ formatTime(f.lastCheck) }}</span>
            </div>
          </div>
        </div>
        <div v-if="visionAwaitingConsent" class="consent-row">
          <span class="supply-detail">
            视觉清单含 API 密钥副本；向 WSL 共享前需要你的明确同意（文件将以 0600 权限写入，
            仅在 distro 内可读）。
          </span>
          <AppButton
            variant="primary"
            size="small"
            :disabled="consentBusy"
            @click="grantConsent"
          >
            {{ consentBusy ? '授权中…' : '同意共享到 WSL' }}
          </AppButton>
        </div>
        <p v-if="supply.pendingPush" class="set-hint">
          有保存后的变更待同步：将在 WSL 空闲时自动推送，或于下次启动会话时补齐。
        </p>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getWSLCLIStatus, installCLIToWSL, type WSLStatus } from '../../api/wslsetup'
import {
  getWSLConfigSupplyStatus,
  grantWSLVisionConsent,
  type WSLConfigSupplyStatus,
  type WSLConfigSupplyFace,
} from '../../api/wslSupply'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'

const { showSuccess, showError, showInfo } = useToast()

const status = ref<WSLStatus | null>(null)
const busyTool = ref<string>('')
// P3-S1「配置状态」区：脱敏快照（只读，不触发同步）。
const supply = ref<WSLConfigSupplyStatus | null>(null)
const consentBusy = ref(false)
// Only show the panel when WSL is relevant (available), or while first loading.
const showPanel = ref(true)

const NAMES: Record<string, string> = {
  claude: 'Claude Code',
  opencode: 'OpenCode',
  codex: 'Codex',
  pi: 'Pi',
}
function displayName(tool: string): string {
  return NAMES[tool] || tool
}

async function load() {
  try {
    const st = await getWSLCLIStatus()
    status.value = st
    if (st.available) {
      // 只读状态快照：失败不干扰安装卡片（配置状态区静默缺省）。
      getWSLConfigSupplyStatus()
        .then((s) => { supply.value = s; watchFaces() })
        .catch(() => { supply.value = null })
    }
    // Hide the whole panel on platforms/machines where WSL is unavailable AND
    // the reason indicates it's simply not a WSL host (keep it visible with the
    // reason otherwise so the user understands why).
    showPanel.value = st.available || !!st.reason
  } catch (err) {
    console.error('load WSL status:', err)
    showPanel.value = false
  }
}

async function install(tool: string) {
  if (busyTool.value) return
  busyTool.value = tool
  showInfo(`正在将 ${displayName(tool)} 安装到 WSL…`)
  try {
    const res = await installCLIToWSL(tool)
    if (res.success) {
      if (res.alreadyOK) {
        showSuccess(`${displayName(tool)} 已在 WSL 中 (${res.version})`)
      } else {
        showSuccess(`${displayName(tool)} 已安装到 WSL (${res.version})`)
      }
      await load()
    } else {
      showError(`安装失败: ${res.error || '未知错误'}`)
    }
  } catch (err: any) {
    showError('安装失败: ' + (err?.message || err))
  } finally {
    busyTool.value = ''
  }
}

const visionAwaitingConsent = ref(false)
function refreshConsentFlag(): void {
  const face = supply.value?.faces?.find((f) => f.key === 'vision-export')
  visionAwaitingConsent.value = face?.state === 'awaiting-consent'
}

function faceStateText(state: string): string {
  switch (state) {
    case 'synced': return '已同步'
    case 'stale': return '陈旧'
    case 'failed': return '失败'
    case 'frozen': return '冻结'
    case 'awaiting-consent': return '待授权'
    case 'consented': return '已授权，待发布'
    default: return '未检查'
  }
}
function faceBadgeClass(state: string): string {
  switch (state) {
    case 'synced': return 'badge-ok'
    case 'failed': return 'badge-missing'
    case 'stale': case 'awaiting-consent': case 'consented': return 'badge-warn'
    default: return ''
  }
}
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString()
}

async function grantConsent(): Promise<void> {
  if (consentBusy.value) return
  consentBusy.value = true
  try {
    await grantWSLVisionConsent()
    showSuccess('已授权向 WSL 共享视觉清单（密钥副本将以 0600 写入）')
    await load()
    refreshConsentFlag()
  } catch (err: any) {
    showError('授权失败: ' + (err?.message || err))
  } finally {
    consentBusy.value = false
  }
}

// faces 变化时同步同意标记（load 完成后调用）。
function watchFaces(): void {
  refreshConsentFlag()
}

onMounted(load)
</script>

<style scoped>
.supply-section {
  margin-top: 18px;
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}
.supply-section h3 {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 6px;
}
.supply-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 8px;
}
.supply-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
}
.supply-row label {
  color: var(--label);
  min-width: 140px;
}
.supply-row .row-value {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}
.supply-detail {
  color: var(--tertiary);
  font-size: 12px;
}
.supply-time {
  color: var(--tertiary);
  font-size: 11px;
}
.badge-warn {
  background: color-mix(in srgb, #d08770 22%, transparent);
  color: #d08770;
}
.consent-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin: 8px 0 4px;
}

.set-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 14px;
  padding: 20px 24px;
  box-shadow: var(--shadow);
}

.set-card h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.set-sub {
  font-size: 12px;
  color: var(--tertiary);
  margin-bottom: 14px;
}

.wsl-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-bottom: 8px;
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row:first-child {
  border-top: none;
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
  flex-shrink: 0;
}

.row-value {
  display: flex;
  align-items: center;
  gap: 12px;
}

.empty-row {
  padding: 12px 0;
  font-size: 13px;
  color: var(--tertiary);
}

.set-hint {
  margin-top: 10px;
  font-size: 12px;
  color: var(--tertiary);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--secondary);
  word-break: break-all;
}

.badge {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 6px;
}

.badge-ok {
  color: var(--success, #2ea043);
  background: color-mix(in srgb, var(--success, #2ea043) 14%, transparent);
}

.badge-wsl2 {
  color: var(--accent, #4c8dff);
  background: color-mix(in srgb, var(--accent, #4c8dff) 14%, transparent);
  font-weight: 600;
}

.badge-missing {
  color: var(--tertiary);
  background: var(--control);
}
</style>