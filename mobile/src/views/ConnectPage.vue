<script setup lang="ts">
import { ref, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useConnection } from '../stores/connection'
import { Html5QrcodeScanner, Html5QrcodeScanType } from 'html5-qrcode'

const router = useRouter()
const { serverUrl, token, isConnecting, lastError, setServer, testAndConnect, isConnected } = useConnection()

const urlInput = ref(serverUrl.value)
const tokenInput = ref(token.value)
const scanning = ref(false)
let qrScanner: Html5QrcodeScanner | null = null

// 远程访问时（非 localhost），Server URL 自动从 origin 获取，无需用户输入
const isRemoteAccess = !window.location.origin.includes('localhost') && !window.location.origin.includes('127.0.0.1')

async function handleConnect() {
  setServer(urlInput.value, tokenInput.value)
  const ok = await testAndConnect()
  if (ok) {
    router.push('/dashboard')
  }
}

function startScan() {
  scanning.value = true

  // 等下一帧 DOM 渲染好再初始化扫描器
  setTimeout(() => {
    qrScanner = new Html5QrcodeScanner(
      'qr-reader',
      {
        fps: 10,
        qrbox: { width: 250, height: 250 },
        supportedScanTypes: [Html5QrcodeScanType.SCAN_TYPE_CAMERA],
      },
      false
    )

    qrScanner.render(
      (decodedText) => {
        handleQRResult(decodedText)
      },
      (errorMessage) => {
        // 忽略扫描过程中的普通错误（帧级别未检测到 QR 码）
        console.debug('QR scan frame:', errorMessage)
      }
    )
  }, 100)
}

function handleQRResult(text: string) {
  try {
    const parsed = JSON.parse(text)
    if (parsed.url && parsed.token) {
      urlInput.value = parsed.url
      tokenInput.value = parsed.token
      stopScan()
    } else {
      console.warn('QR code missing url or token fields')
    }
  } catch {
    console.warn('QR code is not valid JSON:', text)
  }
}

function stopScan() {
  if (qrScanner) {
    qrScanner.clear().catch(() => {})
    qrScanner = null
  }
  scanning.value = false
}

onUnmounted(() => {
  stopScan()
})

if (isConnected.value) {
  router.replace('/dashboard')
}
</script>

<template>
  <div class="connect-page">
    <div class="logo-section">
      <div class="logo">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#58a6ff" stroke-width="1.5">
          <rect x="2" y="3" width="20" height="14" rx="2" />
          <line x1="8" y1="21" x2="16" y2="21" />
          <line x1="12" y1="17" x2="12" y2="21" />
          <path d="M7 8l3 3-3 3" stroke="#58a6ff" stroke-width="2" />
          <line x1="13" y1="14" x2="17" y2="14" stroke="#58a6ff" stroke-width="2" />
        </svg>
      </div>
      <h1 class="app-title">Amagi CodeBox Mobile</h1>
      <p class="app-subtitle">Remote terminal controller</p>
    </div>

    <!-- QR Scanner -->
    <div v-if="scanning" class="scanner-wrapper">
      <div id="qr-reader" class="qr-reader"></div>
      <button class="cancel-scan-btn" @click="stopScan">Cancel</button>
    </div>

    <form v-else class="connect-form" @submit.prevent="handleConnect">
      <div class="form-group" v-if="!isRemoteAccess">
        <label class="form-label">Server URL</label>
        <input
          v-model="urlInput"
          type="url"
          class="form-input"
          placeholder="http://192.168.1.100:8680"
        />
      </div>
      <div v-else class="server-auto-hint">
        <span class="auto-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#3fb950" stroke-width="2">
            <polyline points="20 6 9 17 4 12" />
          </svg>
        </span>
        <span>Server: {{ urlInput }}</span>
      </div>

      <div class="form-group">
        <label class="form-label">Token</label>
        <input
          v-model="tokenInput"
          type="password"
          class="form-input"
          placeholder="Enter access token"
        />
      </div>

      <div v-if="lastError" class="error-msg">
        {{ lastError }}
      </div>

      <button
        type="submit"
        class="connect-btn"
        :disabled="isConnecting || !urlInput"
      >
        <span v-if="isConnecting" class="spinner"></span>
        {{ isConnecting ? 'Connecting...' : 'Connect' }}
      </button>

      <button
        type="button"
        class="scan-btn"
        @click="startScan"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="23 7 23 1 17 1"></polyline>
          <line x1="16" y1="8" x2="23" y2="1"></line>
          <polyline points="1 17 1 23 7 23"></polyline>
          <line x1="8" y1="16" x2="1" y2="23"></line>
          <polyline points="23 17 23 23 17 23"></polyline>
          <line x1="16" y1="16" x2="23" y2="23"></line>
          <polyline points="1 7 1 1 7 1"></polyline>
          <line x1="8" y1="8" x2="1" y2="1"></line>
        </svg>
        Scan QR Code
      </button>
    </form>
  </div>
</template>

<style scoped>
.connect-page {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 100%;
  padding: 24px;
}

.logo-section {
  text-align: center;
  margin-bottom: 40px;
}

.logo {
  width: 80px;
  height: 80px;
  background: rgba(88, 166, 255, 0.1);
  border-radius: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto 16px;
}

.app-title {
  font-size: 24px;
  font-weight: 700;
  color: #f0f6fc;
  margin: 0 0 4px;
}

.app-subtitle {
  font-size: 14px;
  color: #8b949e;
  margin: 0;
}

.connect-form {
  width: 100%;
  max-width: 360px;
}

.form-group {
  margin-bottom: 16px;
}

.form-label {
  display: block;
  font-size: 13px;
  color: #8b949e;
  margin-bottom: 6px;
}

.form-input {
  width: 100%;
  padding: 10px 12px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 6px;
  color: #c9d1d9;
  font-size: 15px;
  outline: none;
  box-sizing: border-box;
}

.form-input:focus {
  border-color: #58a6ff;
  box-shadow: 0 0 0 3px rgba(88, 166, 255, 0.15);
}

.server-auto-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px;
  background: rgba(63, 185, 80, 0.08);
  border: 1px solid rgba(63, 185, 80, 0.3);
  border-radius: 6px;
  color: #8b949e;
  font-size: 13px;
  margin-bottom: 16px;
}

.auto-icon {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}

.error-msg {
  padding: 8px 12px;
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.3);
  border-radius: 6px;
  color: #f85149;
  font-size: 13px;
  margin-bottom: 16px;
}

.connect-btn {
  width: 100%;
  padding: 12px;
  background: #238636;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 16px;
  font-weight: 600;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.connect-btn:active {
  background: #2ea043;
}

.connect-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.scan-btn {
  width: 100%;
  margin-top: 12px;
  padding: 12px;
  background: transparent;
  color: #58a6ff;
  border: 1px solid #30363d;
  border-radius: 6px;
  font-size: 15px;
  font-weight: 500;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.scan-btn:active {
  background: rgba(88, 166, 255, 0.08);
}

/* Scanner */
.scanner-wrapper {
  width: 100%;
  max-width: 360px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
}

.qr-reader {
  width: 100%;
}

.cancel-scan-btn {
  padding: 10px 32px;
  background: transparent;
  color: #8b949e;
  border: 1px solid #30363d;
  border-radius: 6px;
  font-size: 15px;
  cursor: pointer;
}

.cancel-scan-btn:active {
  background: rgba(255, 255, 255, 0.05);
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
