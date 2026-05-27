export interface AppInfo {
  version: string
  activeSessionCount: number
  uptime: string
}

export interface SessionInfo {
  id: string
  appType: string
  mode: string
  status: string
  provider: string
  preset: string
  model: string
  workDir: string
  startedAt: string
  pid: number
}

export interface ProviderSummary {
  id: string
  name: string
  type: string
  baseURL: string
  model: string
}

export interface ProviderDetail {
  id: string
  name: string
  type: string
  baseURL: string
  apiKey: string
  model: string
  maxTokens: number
  extraParams: Record<string, unknown>
}

export interface LaunchSessionRequest {
  providerName: string
  presetName: string
  mode: string
  workDir: string
  useProxy: boolean
  shellPath: string
}

export interface LaunchCodexRequest {
  modelName: string
  providerID: string
  mode: string
  workDir: string
  shellPath: string
}

export interface LaunchOpenCodeRequest {
  providerName: string
  presetName: string
  mode: string
  workDir: string
  shellPath: string
}

export interface LaunchProviderOption {
  id: string
  name: string
  type: string
  defaultModel?: string
}

export interface LaunchPresetOption {
  key: string
  label: string
  provider?: string
  model?: string
  source?: string
}

export interface LaunchOpenCodePresetOption {
  key: string
  label: string
  description?: string
  bindingCount?: number
  source?: string
}

export interface LaunchMetadataResponse {
  paths: string[]
  claude: {
    providers: LaunchProviderOption[]
    presets: LaunchPresetOption[]
  }
  opencode: {
    providers: LaunchProviderOption[]
    presets: LaunchOpenCodePresetOption[]
  }
  codex: {
    providers: LaunchProviderOption[]
    presets: LaunchPresetOption[]
  }
}

export interface SettingsData {
  remotePort: number
  remoteToken: string
  autoStart: boolean
  logLevel: string
}

export interface LogEntry {
  timestamp: string
  level: string
  message: string
  source: string
}

export interface LogQuery {
  level?: string
  source?: string
  keyword?: string
  limit?: number
}

class ApiClient {
  private baseURL: string
  private token: string

  constructor() {
    const saved = localStorage.getItem('server_url')
    this.baseURL = import.meta.env.DEV ? (saved || 'http://localhost:8680') : window.location.origin
    this.token = localStorage.getItem('server_token') || ''
  }

  setBaseURL(url: string) {
    this.baseURL = url.replace(/\/+$/, '')
    localStorage.setItem('server_url', this.baseURL)
  }

  setToken(token: string) {
    this.token = token
    localStorage.setItem('server_token', this.token)
  }

  getBaseURL(): string {
    return this.baseURL
  }

  getToken(): string {
    return this.token
  }

  private async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const url = `${this.baseURL}${path}`
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(options.headers as Record<string, string> || {}),
    }
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    const response = await fetch(url, {
      ...options,
      credentials: options.credentials ?? 'same-origin',
      headers,
    })

    if (!response.ok) {
      const text = await response.text().catch(() => '')
      throw new Error(`HTTP ${response.status}: ${text || response.statusText}`)
    }

    const contentType = response.headers.get('content-type')
    if (contentType && contentType.includes('application/json')) {
      return response.json()
    }
    return response.text() as unknown as T
  }

  async getAppInfo(): Promise<AppInfo> {
    return this.request<AppInfo>('/api/info')
  }

  async consumeLaunchGrant(launch: string): Promise<void> {
    await this.request<void>('/api/bootstrap/consume', {
      method: 'POST',
      body: JSON.stringify({ launch }),
    })
  }

  async getSessions(): Promise<SessionInfo[]> {
    return this.request<SessionInfo[]>('/api/sessions')
  }

  async getLaunchMetadata(): Promise<LaunchMetadataResponse> {
    return this.request<LaunchMetadataResponse>('/api/sessions/launch-meta')
  }

  async launchSession(req: LaunchSessionRequest): Promise<SessionInfo> {
    return this.request<SessionInfo>('/api/sessions/launch', {
      method: 'POST',
      body: JSON.stringify(req),
    })
  }

  async launchCodexSession(req: LaunchCodexRequest): Promise<SessionInfo> {
    return this.request<SessionInfo>('/api/sessions/launch-codex', {
      method: 'POST',
      body: JSON.stringify(req),
    })
  }

  async launchOpenCodeSession(req: LaunchOpenCodeRequest): Promise<SessionInfo> {
    return this.request<SessionInfo>('/api/sessions/launch-opencode', {
      method: 'POST',
      body: JSON.stringify(req),
    })
  }

  async stopSession(sessionId: string): Promise<void> {
    await this.request<void>(`/api/sessions/${sessionId}`, {
      method: 'DELETE',
    })
  }

  async removeSession(sessionId: string): Promise<void> {
    await this.request<void>(`/api/sessions/${sessionId}/remove`, {
      method: 'DELETE',
    })
  }

  async clearStoppedSessions(): Promise<void> {
    await this.request<void>('/api/sessions/clear-stopped', {
      method: 'POST',
    })
  }

  async resizeTerminal(sessionId: string, cols: number, rows: number): Promise<void> {
    await this.request<void>(`/api/sessions/${sessionId}/resize`, {
      method: 'POST',
      body: JSON.stringify({ cols, rows }),
    })
  }

  async getProviders(): Promise<ProviderSummary[]> {
    return this.request<ProviderSummary[]>('/api/providers')
  }

  async getProviderDetail(name: string): Promise<ProviderDetail> {
    return this.request<ProviderDetail>(`/api/providers/${encodeURIComponent(name)}`)
  }

  async saveProvider(name: string, provider: ProviderDetail): Promise<void> {
    await this.request<void>(`/api/providers/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(provider),
    })
  }

  async getProvidersByType(type: string): Promise<ProviderSummary[]> {
    return this.request<ProviderSummary[]>(`/api/providers-by-type/${encodeURIComponent(type)}`)
  }

  async getSettings(): Promise<SettingsData> {
    return this.request<SettingsData>('/api/settings')
  }

  async updateSettings(settings: Partial<SettingsData>): Promise<void> {
    await this.request<void>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify(settings),
    })
  }

  async getLogs(query: LogQuery = {}): Promise<LogEntry[]> {
    const params = new URLSearchParams()
    if (query.level) params.set('level', query.level)
    if (query.source) params.set('source', query.source)
    if (query.keyword) params.set('keyword', query.keyword)
    if (query.limit) params.set('limit', String(query.limit))
    const qs = params.toString()
    return this.request<LogEntry[]>(`/api/logs${qs ? '?' + qs : ''}`)
  }

  async saveConfig(): Promise<void> {
    await this.request<void>('/api/config/save', {
      method: 'POST',
    })
  }

  async getSecretsDiagnostics(): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>('/api/secrets/diagnostics')
  }

  async getPaths(): Promise<string[]> {
    return this.request<string[]>('/api/paths')
  }

  async testConnection(): Promise<boolean> {
    try {
      await this.getAppInfo()
      return true
    } catch {
      return false
    }
  }
}

export const apiClient = new ApiClient()
