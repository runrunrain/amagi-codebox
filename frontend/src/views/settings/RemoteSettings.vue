<template>
  <div class="set-card">
    <h2>远程控制服务</h2>
    <p class="set-sub">通过 HTTP/WebSocket 从其他设备访问本机工作台</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>服务状态</label>
        <div class="row-status">
          <span class="sess-dot" :class="statusDotClass" />
          <span class="status-text">{{ statusText }}</span>
          <Switch
            :model-value="running"
            :disabled="toggling"
            @update:model-value="toggleRemote"
          />
        </div>
      </div>

      <div class="setting-row">
        <label>监听地址</label>
        <input
          class="text-input mono"
          v-model="hostDraft"
          placeholder="0.0.0.0"
          :disabled="!running"
        />
      </div>

      <div class="setting-row">
        <label>监听端口</label>
        <input
          class="text-input mono"
          type="number"
          v-model.number="portDraft"
          placeholder="8680"
          min="1"
          max="65535"
          :disabled="!running"
        />
      </div>

      <div class="setting-row">
        <label></label>
        <AppButton
          variant="primary"
          size="small"
          :disabled="!running || applying"
          @click="applyEndpoint"
        >
          {{ applying ? '应用中...' : '应用' }}
        </AppButton>
      </div>
    </div>
  </div>

  <div class="set-card">
    <h2>访问 Token</h2>
    <p class="set-sub">远程连接需携带此 Token，请妥善保管</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>当前 Token</label>
        <div class="row-token">
          <MaskedValue :value="token" />
          <AppButton variant="ghost" size="small" @click="copyToken">复制</AppButton>
          <AppButton
            variant="ghost"
            size="small"
            :disabled="regenerating"
            @click="regenerate"
          >
            {{ regenerating ? '刷新中...' : '刷新' }}
          </AppButton>
        </div>
      </div>
    </div>
  </div>

  <div class="set-card" v-if="running">
    <h2>快速连接</h2>
    <p class="set-sub">移动端扫描二维码快速建立连接（二维码中包含访问地址与 Token）</p>
    <div class="qr-wrap">
      <canvas ref="qrCanvas" class="qr-canvas" />
      <div class="qr-hint">{{ qrUrl || '正在生成本机访问地址...' }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick, onUnmounted } from 'vue'
import QRCode from 'qrcode'
import {
  getRemoteStatus,
  getRemoteToken,
  toggleRemoteServer,
  setRemoteHost,
  setRemotePort,
  regenerateRemoteToken,
} from '../../api/remote'
import { useToast } from '../../composables/useToast'
import Switch from '../../components/ui/Switch.vue'
import MaskedValue from '../../components/ui/MaskedValue.vue'
import AppButton from '../../components/ui/AppButton.vue'

const { showSuccess, showError } = useToast()

interface RemoteStatusPayload {
  host?: string
  port?: number | string
  token?: string
  running?: boolean
}

interface RemoteStatus {
  host: string
  port: number
  token: string
  running: boolean
}

const status = ref<RemoteStatus>({ host: '0.0.0.0', port: 8680, token: '', running: false })
const token = ref('')
const hostDraft = ref('0.0.0.0')
const portDraft = ref(8680)

const toggling = ref(false)
const applying = ref(false)
const regenerating = ref(false)

const qrCanvas = ref<HTMLCanvasElement | null>(null)
const qrUrl = ref('')

const running = computed(() => !!status.value.running)

const statusText = computed(() => {
  if (toggling.value) return '切换中...'
  if (running.value) {
    const host = status.value.host || '0.0.0.0'
    const port = status.value.port || 8680
    return `运行中 · ${host}:${port}`
  }
  return '已停止'
})

const statusDotClass = computed(() => (running.value ? 'dot-on' : 'dot-off'))

async function loadStatus() {
  try {
    const s = (await getRemoteStatus()) as RemoteStatusPayload
    status.value = {
      host: s.host || '0.0.0.0',
      port: Number(s.port) || 8680,
      token: s.token || '',
      running: !!s.running,
    }
    hostDraft.value = status.value.host
    portDraft.value = status.value.port
    token.value = s.token || ''
  } catch (err: any) {
    showError('读取远程状态失败: ' + (err?.message || err))
  }
}

async function toggleRemote(next: boolean) {
  toggling.value = true
  try {
    await toggleRemoteServer(next)
    await loadStatus()
    showSuccess(next ? '远程控制已启用' : '远程控制已停止')
  } catch (err: any) {
    showError('切换失败: ' + (err?.message || err))
  } finally {
    toggling.value = false
  }
}

async function applyEndpoint() {
  applying.value = true
  try {
    const host = (hostDraft.value || '').trim() || '0.0.0.0'
    const port = Number(portDraft.value) || 8680
    await setRemoteHost(host)
    await setRemotePort(port)
    status.value.host = host
    status.value.port = port
    showSuccess('监听地址已更新')
  } catch (err: any) {
    showError('应用失败: ' + (err?.message || err))
  } finally {
    applying.value = false
  }
}

async function copyToken() {
  if (!token.value) return
  try {
    await navigator.clipboard.writeText(token.value)
    showSuccess('Token 已复制到剪贴板')
  } catch {
    showError('复制失败，请手动复制')
  }
}

async function regenerate() {
  regenerating.value = true
  try {
    const newToken = await regenerateRemoteToken()
    token.value = newToken
    status.value.token = newToken
    showSuccess('Token 已刷新')
  } catch (err: any) {
    showError('刷新失败: ' + (err?.message || err))
  } finally {
    regenerating.value = false
  }
}

async function getLocalIP(): Promise<string> {
  return new Promise((resolve) => {
    try {
      const pc = new RTCPeerConnection({ iceServers: [] })
      pc.createDataChannel('')
      pc.createOffer().then((offer) => pc.setLocalDescription(offer))
      pc.onicecandidate = (e) => {
        if (!e.candidate) return
        const m = e.candidate.candidate.match(/(\d+\.\d+\.\d+\.\d+)/)
        if (m && !m[1].startsWith('127.')) {
          pc.close()
          resolve(m[1])
        }
      }
      setTimeout(() => {
        pc.close()
        resolve('127.0.0.1')
      }, 1500)
    } catch {
      resolve('127.0.0.1')
    }
  })
}

async function renderQRCode() {
  if (!qrCanvas.value) return
  const host = status.value.host || '0.0.0.0'
  const localIP = await getLocalIP()
  const effectiveHost = host === '0.0.0.0' ? localIP : host
  const port = status.value.port || 8680
  const url = `http://${effectiveHost}:${port}`
  qrUrl.value = url
  const payload = JSON.stringify({ url, token: token.value })
  try {
    await QRCode.toCanvas(qrCanvas.value, payload, {
      width: 200,
      margin: 2,
      color: { dark: '#1d1d1f', light: '#ffffff' },
    })
  } catch (err) {
    console.error('QR render error:', err)
  }
}

watch(running, async (val) => {
  if (val) {
    await nextTick()
    await renderQRCode()
  }
})

watch(
  () => [status.value.host, status.value.port, token.value],
  async () => {
    if (running.value && qrCanvas.value) {
      await renderQRCode()
    }
  },
)

onMounted(async () => {
  await loadStatus()
  if (running.value) {
    await nextTick()
    await renderQRCode()
  }
})

onUnmounted(() => {
  // nothing to clean; no long-running listeners
})
</script>

<style scoped>
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
}

.row-status {
  display: flex;
  align-items: center;
  gap: 10px;
}

.sess-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.sess-dot.dot-on {
  background: var(--success, #34c759);
}

.sess-dot.dot-off {
  background: var(--tertiary, #8e8e93);
}

.status-text {
  font-size: 13px;
  color: var(--secondary);
  font-variant-numeric: tabular-nums;
}

.row-token {
  display: flex;
  align-items: center;
  gap: 10px;
}

.text-input {
  min-width: 220px;
  max-width: 280px;
  padding: 7px 12px;
  font-size: 13px;
  font-family: inherit;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
}

.text-input.mono {
  font-family: var(--mono);
}

.text-input:focus {
  border-color: var(--accent);
}

.text-input:disabled {
  opacity: 0.5;
}

.qr-wrap {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 12px 0 4px;
}

.qr-canvas {
  background: #ffffff;
  border-radius: 10px;
  padding: 6px;
}

.qr-hint {
  font-size: 12px;
  color: var(--tertiary);
  font-family: var(--mono);
}
</style>
