import { ref, computed } from 'vue'
import { apiClient, type AppInfo } from '../api/client'

// 自动检测 Server URL：如果页面从远程服务器加载，则 API 和页面同源；
// 仅在本地开发（localhost dev server）时使用 localStorage 中保存的地址。
function detectServerUrl(): string {
  const saved = localStorage.getItem('server_url')
  const origin = window.location.origin
  // 本地开发服务器（Vite）端口通常是 5173/5178 等
  if (origin.includes('localhost') || origin.includes('127.0.0.1')) {
    return saved || 'http://localhost:8680'
  }
  // 远程访问：页面和 API 同源
  return origin
}

const serverUrl = ref(detectServerUrl())
const token = ref(localStorage.getItem('server_token') || '')
const connected = ref(false)
const appInfo = ref<AppInfo | null>(null)
const connecting = ref(false)
const lastError = ref('')

export function useConnection() {
  const isConnected = computed(() => connected.value)
  const isConnecting = computed(() => connecting.value)

  function setServer(url: string, tok: string) {
    serverUrl.value = url.replace(/\/+$/, '')
    token.value = tok
    apiClient.setBaseURL(serverUrl.value)
    apiClient.setToken(token.value)
    localStorage.setItem('server_url', serverUrl.value)
    localStorage.setItem('server_token', token.value)
  }

  async function testAndConnect(): Promise<boolean> {
    connecting.value = true
    lastError.value = ''

    apiClient.setBaseURL(serverUrl.value)
    apiClient.setToken(token.value)

    try {
      const info = await apiClient.getAppInfo()
      appInfo.value = info
      connected.value = true
      return true
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : 'Connection failed'
      connected.value = false
      return false
    } finally {
      connecting.value = false
    }
  }

  function disconnect() {
    connected.value = false
    appInfo.value = null
  }

  function getWsHost(): string {
    return serverUrl.value.replace(/^https?:\/\//, '')
  }

  function getWsPort(): string {
    const url = new URL(serverUrl.value)
    return url.port || (url.protocol === 'https:' ? '443' : '80')
  }

  return {
    serverUrl,
    token,
    connected,
    appInfo,
    connecting,
    lastError,
    isConnected,
    isConnecting,
    setServer,
    testAndConnect,
    disconnect,
    getWsHost,
    getWsPort,
  }
}
