import { ref, computed } from 'vue'
import { apiClient, type AppInfo } from '../api/client'

// 自动检测 Server URL：如果页面从远程服务器加载，则 API 和页面同源；
// 仅在本地开发（localhost dev server）时使用 localStorage 中保存的地址。
function detectServerUrl(): string {
  const saved = localStorage.getItem('server_url')
  return import.meta.env.DEV ? (saved || 'http://localhost:8680') : window.location.origin
}

function isLoopbackServerUrl(value: string): boolean {
  try {
    const url = new URL(value)
    const hostname = url.hostname.trim().toLowerCase()
    return hostname === '127.0.0.1' || hostname === 'localhost' || hostname === '::1' || hostname === '[::1]'
  } catch {
    return false
  }
}

function getLaunchParams() {
  const params = new URLSearchParams(window.location.search)
  const hasLaunchParam = params.has('launch')
  const launch = params.get('launch')?.trim() || ''
  const hasTokenParam = params.has('token')
  const rawToken = params.get('token')
  const token = rawToken?.trim() || ''
  const hasAutoconnectParam = params.has('autoconnect')
  const autoconnectRaw = params.get('autoconnect')?.trim().toLowerCase() || ''
  const autoconnect = autoconnectRaw === '1' || autoconnectRaw === 'true' || autoconnectRaw === 'yes'
  const hasLaunchParams = hasLaunchParam || hasTokenParam || hasAutoconnectParam

  return {
    launch,
    hasLaunchParam,
    token,
    hasTokenParam,
    autoconnect,
    hasAutoconnectParam,
    hasLaunchParams,
    serverUrl: import.meta.env.DEV ? '' : window.location.origin.replace(/\/+$/, ''),
  }
}

function clearLaunchParams() {
  const current = new URL(window.location.href)
  current.searchParams.delete('launch')
  current.searchParams.delete('token')
  current.searchParams.delete('autoconnect')
  const nextSearch = current.searchParams.toString()
  const nextURL = `${current.pathname}${nextSearch ? `?${nextSearch}` : ''}${current.hash}`
  window.history.replaceState(window.history.state, '', nextURL)
}

const serverUrl = ref(detectServerUrl())
const token = ref(localStorage.getItem('server_token') || '')
const connected = ref(false)
const appInfo = ref<AppInfo | null>(null)
const connecting = ref(false)
const lastError = ref('')
const bootstrapHandled = ref(false)

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

  async function bootstrapFromLocation(): Promise<boolean> {
    if (bootstrapHandled.value) {
      return connected.value
    }
    bootstrapHandled.value = true

    const launch = getLaunchParams()
    if (!launch.hasLaunchParams) {
      return connected.value
    }

    const nextServerURL = launch.serverUrl || serverUrl.value
    const nextToken = launch.hasTokenParam ? launch.token : ''

    setServer(nextServerURL, nextToken)
    connected.value = false
    appInfo.value = null
    lastError.value = ''

    if (!launch.autoconnect) {
      return false
    }


    if (launch.hasLaunchParam) {
      try {
        await apiClient.consumeLaunchGrant(launch.launch)
      } catch (err) {
        lastError.value = err instanceof Error ? err.message : 'Launch bootstrap failed'
        connected.value = false
        return false
      }
    } else if (!nextToken && !isLoopbackServerUrl(nextServerURL)) {
      lastError.value = 'Missing access token in launch URL'
      connected.value = false
      return false
    }

    const ok = await testAndConnect()
    if (ok) {
      clearLaunchParams()
    }
    return ok
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
    bootstrapFromLocation,
    disconnect,
    getWsHost,
    getWsPort,
  }
}
