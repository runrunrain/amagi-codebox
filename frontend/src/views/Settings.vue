<template>
  <div class="settings-layout">
    <!-- 左侧导航 -->
    <div class="settings-sidebar">
      <h1 class="page-title">设置</h1>
      <nav class="nav-tabs">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          class="nav-tab"
          :class="{ active: activeTab === tab.id }"
          @click="activeTab = tab.id"
        >
          <span class="tab-icon">{{ tab.icon }}</span>
          <span class="tab-label">{{ tab.label }}</span>
        </button>
      </nav>
    </div>

    <!-- 右侧内容 -->
    <div class="settings-content-wrapper">
      <transition name="fade-slide" mode="out-in">
        
        <!-- 常规设置 -->
        <div v-if="activeTab === 'general'" key="general" class="settings-section">
          <div class="section-header">
            <h2>仪表盘默认配置</h2>
            <p>配置仪表盘各选项的初始默认值，下次启动应用时生效。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">服务提供商</h3>
            <div class="form-row">
              <div class="form-group flex-1">
                <label>默认服务提供商</label>
                <div class="select-wrapper">
                  <select v-model="defaults.provider" class="input-field">
                    <option value="">（不指定）</option>
                    <option v-for="(_, name) in anthropicProviders" :key="name" :value="name">{{ name }}</option>
                  </select>
                </div>
              </div>
              <div class="form-group flex-1">
                <label>默认预设配置</label>
                <div class="select-wrapper">
                  <select v-model="defaults.preset" class="input-field" :disabled="!defaults.provider">
                    <option value="">（不指定）</option>
                    <option v-for="(preset, name) in availablePresets" :key="name" :value="name">
                      {{ preset.name || name }} ({{ preset.model }})
                    </option>
                  </select>
                </div>
              </div>
            </div>
            
            <div class="form-row" style="margin-top: 8px;">
              <div class="form-group flex-1">
                <label>默认 OpenCode 服务提供商</label>
                <div class="select-wrapper">
                  <select v-model="defaults.openCodeProvider" class="input-field">
                    <option value="">（不指定，沿用本机 OpenCode 登录）</option>
                    <option v-for="(_, name) in openCodeProviders" :key="name" :value="name">{{ name }}</option>
                  </select>
                </div>
              </div>
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">引擎默认配置</h3>
            <div class="engine-tabs">
              <button 
                v-for="eng in engines" 
                :key="eng.id" 
                class="engine-tab"
                :class="{ active: activeEngineTab === eng.id }"
                @click="activeEngineTab = eng.id"
              >{{ eng.label }}</button>
            </div>

            <div class="engine-content">
              <div class="form-group">
                <label>启动模式</label>
                <div class="mode-selector">
                  <button
                    v-for="m in launchModes"
                    :key="m.value"
                    class="mode-btn"
                    :class="{ active: currentEngineMode === m.value }"
                    @click="currentEngineMode = m.value"
                  >
                    <span class="mode-icon">{{ m.icon }}</span>
                    <span class="mode-label">{{ m.label }}</span>
                  </button>
                </div>
              </div>
              <div class="form-group" style="margin-top: 24px;">
                <label>默认 Shell</label>
                <div class="shell-pills">
                  <button
                    v-for="s in shellOptions"
                    :key="s.value"
                    class="shell-pill"
                    :class="{ active: currentEngineShell === s.value }"
                    @click="currentEngineShell = s.value"
                  >
                    {{ s.label }}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">网络</h3>
            <div class="toggle-row">
              <div class="toggle-info">
                <label>默认启用注入代理</label>
                <span class="field-desc">自动设置环境变量以代理请求</span>
              </div>
              <button 
                class="ios-toggle" 
                :class="{ active: defaults.useProxy }"
                @click="defaults.useProxy = !defaults.useProxy"
              ></button>
            </div>
          </div>

          <div class="section-footer">
            <button class="btn primary" @click="saveDefaults" :disabled="saving">
              {{ saving ? '保存中...' : '保存默认配置' }}
            </button>
          </div>
        </div>

        <!-- 自定义 Shell -->
        <div v-if="activeTab === 'shell'" key="shell" class="settings-section">
          <div class="section-header">
            <h2>自定义 Shell 路径</h2>
            <p>添加自定义 Shell 可执行文件路径，在仪表盘中可快速切换。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">添加新 Shell</h3>
            <div class="add-shell-card">
              <input type="text" class="input-field" v-model="newShellLabel" placeholder="名称（如 Git Bash）" style="width: 180px;" />
              <input type="text" class="input-field flex-1" v-model="newShellPath" placeholder="Shell 可执行文件路径" />
              <button class="btn primary" @click="addShell" :disabled="!newShellPath">添加</button>
            </div>
          </div>

          <div class="setting-group" style="margin-top: 32px;">
            <h3 class="group-header">已保存的路径</h3>
            <div class="shell-list" v-if="shellPaths.length > 0">
              <div class="shell-list-item" v-for="entry in shellPaths" :key="entry.path">
                <div class="shell-info">
                  <span class="shell-label">{{ entry.label }}</span>
                  <span class="shell-path">{{ entry.path }}</span>
                </div>
                <button class="btn small danger delete-btn" @click="removeShell(entry.path)">删除</button>
              </div>
            </div>
            <div class="empty-state" v-else>
              <span>暂无自定义 Shell 路径</span>
            </div>
          </div>
        </div>

        <!-- 终端设置 -->
        <div v-if="activeTab === 'terminal'" key="terminal" class="settings-section">
          <div class="section-header">
            <h2>终端设置</h2>
            <p>配置内嵌终端的显示参数与行为。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">滚动缓冲</h3>
            <div class="form-group">
              <label>缓冲行数 (Scrollback)</label>
              <div class="range-with-input" style="margin-top: 12px;">
                <input type="range" class="range-slider flex-1" v-model.number="terminalScrollback" min="1000" max="10000000" step="10000" />
                <input type="number" class="input-field" v-model.number="terminalScrollback" style="width: 140px;" min="1000" max="10000000" step="10000" />
              </div>
              <p class="field-desc" style="margin-top: 12px;">保留在内存中的终端输出行数。范围 1,000 ~ 10,000,000。较高值可能占用更多内存。</p>
            </div>
          </div>

          <div class="section-footer">
            <button class="btn primary" @click="saveTerminal" :disabled="savingTerminal">
              {{ savingTerminal ? '保存中...' : '保存终端设置' }}
            </button>
          </div>
        </div>

        <!-- 远程控制 -->
        <div v-if="activeTab === 'remote'" key="remote" class="settings-section">
          <div class="section-header">
            <h2>远程控制</h2>
            <p>允许移动端通过局域网连接并控制 Amagi CodeBox。</p>
          </div>

          <div class="remote-hero">
            <div class="remote-status" :class="{ active: remoteEnabled }">
              <div class="status-ring"></div>
              <div class="status-info">
                <h4>{{ remoteEnabled ? '服务运行中' : '服务已停止' }}</h4>
                <p>{{ remoteEnabled ? `正在监听 ${remoteStatus.host || '0.0.0.0'}:${remoteStatus.port}` : '启用以允许外部连接' }}</p>
              </div>
            </div>
            <button 
              class="ios-toggle large-toggle" 
              :class="{ active: remoteEnabled }"
              @click="toggleRemote"
              :disabled="togglingRemote"
            ></button>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">连接设置</h3>
            <div class="form-row">
              <div class="form-group" style="flex: 1;">
                <label>监听地址</label>
                <div class="inline-input-group">
                  <input type="text" class="input-field" v-model="remoteHost" placeholder="0.0.0.0" />
                </div>
              </div>
              <div class="form-group" style="width: 180px;">
                <label>监听端口</label>
                <div class="inline-input-group">
                  <input type="number" class="input-field" v-model.number="remotePort" min="1024" max="65535" />
                </div>
              </div>
              <div class="form-group" style="align-self: flex-end;">
                <button class="btn primary small" @click="applyHostPort" :disabled="savingPort">应用</button>
              </div>
            </div>

            <div class="form-group">
              <label>访问 Token</label>
              <div class="inline-input-group">
                <input :type="showToken ? 'text' : 'password'" class="input-field monospace token-input flex-1" :value="remoteToken" readonly />
                <button class="btn small" @click="showToken = !showToken">{{ showToken ? '隐藏' : '显示' }}</button>
                <button class="btn small" @click="copyToken">复制</button>
                <button class="btn small danger" @click="regenerateToken" :disabled="regenerating">刷新</button>
              </div>
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">移动端 Web 资源</h3>
            <div class="form-group">
              <label>前端构建目录</label>
              <p class="field-desc" style="margin-bottom: 8px;">指向 amagi-codebox-mobile 的 dist 目录。配置后可在同一端口直接访问移动端页面。</p>
              <div class="inline-input-group">
                <input type="text" class="input-field flex-1" v-model="mobileWebRoot" placeholder="例如：C:\projects\amagi-codebox-mobile\dist" />
                <button class="btn primary small" @click="saveMobileWebRoot" :disabled="savingWebRoot">保存</button>
              </div>
            </div>
          </div>

          <transition name="fade-slide">
            <div v-if="remoteEnabled" class="qr-section">
              <div class="group-separator"></div>
              <h3 class="group-header">快速连接</h3>
              <div class="qr-frame">
                <canvas ref="qrCanvas" class="qr-canvas"></canvas>
                <p>使用移动端扫描二维码快速建立连接</p>
              </div>
            </div>
          </transition>
        </div>

        <!-- 软件更新 -->
        <div v-if="activeTab === 'updates'" key="updates" class="settings-section">
          <div class="section-header">
            <h2>软件更新</h2>
            <p>检查并安装来自 GitHub Releases 的更新。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">版本信息</h3>
            <div class="update-hero">
              <div class="version-info">
                <span class="version-label">当前版本</span>
                <span class="version-badge">v{{ currentVersion }}</span>
              </div>
              <button class="btn primary" @click="checkForUpdate" :disabled="checking || downloading">
                {{ checking ? '检查中...' : '检查更新' }}
              </button>
            </div>

            <!-- Update Available Card -->
            <div v-if="updateInfo && updateInfo.hasUpdate" class="update-card">
              <div class="update-card-header">
                <span class="status-dot online"></span>
                <span class="update-title">发现新版本</span>
                <span class="update-version-new">v{{ updateInfo.latestVersion }}</span>
              </div>
              <p class="update-date">发布于：{{ updateInfo.publishedAt }}</p>
              
              <div class="release-notes" v-if="updateInfo.releaseNotes">
                <pre>{{ updateInfo.releaseNotes }}</pre>
              </div>

              <div class="update-actions">
                <button class="btn primary" @click="downloadAndApply" :disabled="downloading">
                  {{ downloading ? '下载中...' : '下载并安装' }}
                </button>
              </div>

              <div v-if="downloading" class="progress-container">
                <div class="progress-bar">
                  <div class="progress-fill" :style="{ width: progressPercent + '%' }"></div>
                </div>
                <span class="progress-text">{{ progressText }}</span>
              </div>
            </div>

            <!-- Up to date -->
            <div v-else-if="updateInfo && !updateInfo.hasUpdate" class="update-uptodate">
              <span class="status-dot online"></span>
              <span>当前已是最新版本</span>
            </div>

            <div v-if="updateError" class="update-error">
              {{ updateError }}
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">GitHub 授权</h3>
            <div class="form-group">
              <label>Personal Access Token</label>
              <p class="field-desc" style="margin-bottom: 8px;">获取私有仓库的 Releases 需要配置含有 repo 权限的 Token。</p>
              <div class="inline-input-group">
                <input
                  :type="showGHToken ? 'text' : 'password'"
                  class="input-field flex-1"
                  v-model="githubToken"
                  placeholder="ghp_xxxxxxxxxxxx"
                />
                <button class="btn small" @click="showGHToken = !showGHToken">
                  {{ showGHToken ? '隐藏' : '显示' }}
                </button>
                <button class="btn primary small" @click="saveGitHubToken" :disabled="savingGHToken">
                  {{ savingGHToken ? '保存中...' : '保存' }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- 关于 -->
        <div v-if="activeTab === 'about'" key="about" class="settings-section">
          <div class="about-container">
            <div class="app-logo">
              <span class="app-icon">▨</span>
            </div>
            <h2 class="app-name">Amagi CodeBox</h2>
            <p class="app-version">Version {{ currentVersion }}</p>
            
            <div class="about-details">
              <div class="detail-row">
                <span class="detail-label">配置目录</span>
                <span class="detail-value monospace">~/.amagi-codebox/</span>
              </div>
            </div>
          </div>
        </div>

      </transition>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, reactive, computed, onMounted, watch, nextTick } from 'vue'
import { GetDashboardDefaults, SetDashboardDefaults, GetShellPaths, AddShellPath, RemoveShellPath, GetTerminalSettings, SetTerminalSettings, GetMobileWebRoot, SetMobileWebRoot } from '../../wailsjs/go/settings/Service'
import { GetProviders } from '../../wailsjs/go/config/ConfigService'
import { GetRemoteStatus, GetRemoteToken, RegenerateRemoteToken, ToggleRemoteServer, SetRemoteHost, SetRemotePort, CheckForUpdate, DownloadAndApplyUpdate, GetAppInfo, GetGitHubToken, SetGitHubToken, GetMergedTerminalPresets } from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { config } from '../../wailsjs/go/models'
import { useToast } from '../composables/useToast'
import QRCode from 'qrcode'

const { showSuccess, showError } = useToast()

const activeTab = ref('general')
const tabs = [
  { id: 'general', label: '常规设置', icon: '⚙' },
  { id: 'shell', label: 'Shell', icon: '⌨' },
  { id: 'terminal', label: '终端设置', icon: '🖥' },
  { id: 'remote', label: '远程控制', icon: '🌐' },
  { id: 'updates', label: '软件更新', icon: '⟳' },
  { id: 'about', label: '关于', icon: 'ℹ' },
]

const activeEngineTab = ref('claude')
const engines = [
  { id: 'claude', label: 'ClaudeCode' },
  { id: 'opencode', label: 'OpenCode' },
  { id: 'codex', label: 'Codex' }
]

const providers = ref<Record<string, config.Provider>>({})
const shellPaths = ref<Array<{ path: string; label: string }>>([])
const saving = ref(false)

// Merged terminal presets for claude_code (lightweight, for default preset dropdown only)
interface MergedPresetEntry { key: string; label: string; provider: string; model: string }
const settingsMergedPresets = ref<MergedPresetEntry[]>([])

// Provider classification helpers (consistent with ProviderCenter)
function isAnthropicCompatible(p: any): boolean {
  return !!(p?.anthropic?.enabled) || ((!p?.openai?.enabled) && (p?.type || 'anthropic') !== 'openai' && p?.auth_key !== 'OPENAI_API_KEY')
}
function isOpenAICompatible(p: any): boolean {
  return !!(p?.openai?.enabled) || (p?.type || '').toLowerCase() === 'openai' || p?.auth_key === 'OPENAI_API_KEY'
}

const defaults = reactive({
  provider: '',
  preset: '',
  openCodeProvider: '',
  openCodePreset: '',
  mode: 'embedded',
  shell: 'pwsh',
  claudeMode: 'embedded',
  claudeShell: 'pwsh',
  openCodeMode: 'embedded',
  openCodeShell: 'pwsh',
  codexMode: 'embedded',
  codexShell: 'pwsh',
  amagiCodePreset: '',
  amagiCodeMode: 'embedded',
  amagiCodeShell: 'pwsh',
  useProxy: false,
})

const currentEngineMode = computed({
  get: () => {
    if (activeEngineTab.value === 'claude') return defaults.claudeMode;
    if (activeEngineTab.value === 'opencode') return defaults.openCodeMode;
    return defaults.codexMode;
  },
  set: (val) => {
    if (activeEngineTab.value === 'claude') defaults.claudeMode = val;
    else if (activeEngineTab.value === 'opencode') defaults.openCodeMode = val;
    else defaults.codexMode = val;
  }
})

const currentEngineShell = computed({
  get: () => {
    if (activeEngineTab.value === 'claude') return defaults.claudeShell;
    if (activeEngineTab.value === 'opencode') return defaults.openCodeShell;
    return defaults.codexShell;
  },
  set: (val) => {
    if (activeEngineTab.value === 'claude') defaults.claudeShell = val;
    else if (activeEngineTab.value === 'opencode') defaults.openCodeShell = val;
    else defaults.codexShell = val;
  }
})

const newShellLabel = ref('')
const newShellPath = ref('')
const terminalScrollback = ref(100000)
const savingTerminal = ref(false)

const currentVersion = ref('')
const updateInfo = ref<any>(null)
const checking = ref(false)
const downloading = ref(false)
const downloadProgress = ref({ downloaded: 0, total: 0 })
const updateError = ref('')
const githubToken = ref('')

const showGHToken = ref(false)
const savingGHToken = ref(false)

const progressPercent = computed(() => {
  const { downloaded, total } = downloadProgress.value
  if (total <= 0) return 0
  return Math.min(100, Math.round((downloaded / total) * 100))
})

const progressText = computed(() => {
  const { downloaded, total } = downloadProgress.value
  const fmt = (n: number) => (n / 1024 / 1024).toFixed(1) + ' MB'
  if (total <= 0) return '准备中...'
  return `${fmt(downloaded)} / ${fmt(total)}`
})

async function checkForUpdate() {
  checking.value = true
  updateError.value = ''
  updateInfo.value = null
  try {
    const info = await CheckForUpdate()
    updateInfo.value = info
  } catch (err) {
    updateError.value = '检查失败: ' + err
  } finally {
    checking.value = false
  }
}

async function downloadAndApply() {
  downloading.value = true
  downloadProgress.value = { downloaded: 0, total: 0 }
  updateError.value = ''
  try {
    EventsOn('update:progress', (progress: any) => {
      downloadProgress.value = progress
    })
    await DownloadAndApplyUpdate()
  } catch (err) {
    updateError.value = '下载失败: ' + err
    downloading.value = false
  }
}

async function saveGitHubToken() {
  savingGHToken.value = true
  try {
    await SetGitHubToken(githubToken.value.trim())
    showSuccess('GitHub Token 已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    savingGHToken.value = false
  }
}

const launchModes = [
  { value: 'embedded', label: '内嵌终端', icon: '▨' },
  { value: 'terminal', label: '独立窗口', icon: '⬛' },
]

const shellOptions = [
  { value: '', label: '直接 Claude' },
  { value: 'pwsh', label: 'PowerShell 7' },
  { value: 'powershell', label: 'Windows PowerShell' },
  { value: 'cmd', label: 'CMD' },
]

const availablePresets = computed(() => {
  if (!defaults.provider) return {}
  const result: Record<string, any> = {}
  for (const mp of settingsMergedPresets.value) {
    if (mp.provider === defaults.provider) {
      result[mp.key] = { name: mp.label, model: mp.model }
    }
  }
  return result
})

const anthropicProviders = computed(() => {
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (isAnthropicCompatible(provider)) {
      result[name] = provider
    }
  }
  return result
})

const openCodeProviders = computed(() => {
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (isOpenAICompatible(provider)) {
      result[name] = provider
    }
  }
  return result
})

watch(() => defaults.provider, (newVal) => {
  if (newVal) {
    const presets = availablePresets.value
    const presetKeys = Object.keys(presets)
    if (presetKeys.length > 0 && !presetKeys.includes(defaults.preset)) {
      defaults.preset = presetKeys[0]
    }
  } else {
    defaults.preset = ''
  }
})

const loadData = async () => {
  try {
    providers.value = await GetProviders()
  } catch (err) {
    console.error('load providers:', err)
  }
  // Load merged terminal presets for default preset dropdown (lightweight)
  try {
    const presets = await GetMergedTerminalPresets('claude_code')
    settingsMergedPresets.value = presets || []
  } catch (err) {
    console.error('load merged presets:', err)
  }
  try {
    const d = await GetDashboardDefaults()
    defaults.provider = d.provider || ''
    defaults.preset = d.preset || ''
    defaults.openCodeProvider = d.openCodeProvider || ''
    defaults.openCodePreset = d.openCodePreset || ''
    defaults.mode = d.mode || 'embedded'
    defaults.shell = d.shell || 'pwsh'
    defaults.claudeMode = d.claudeMode || d.mode || 'embedded'
    defaults.claudeShell = d.claudeShell || d.shell || 'pwsh'
    defaults.openCodeMode = d.openCodeMode || d.mode || 'embedded'
    defaults.openCodeShell = d.openCodeShell || d.shell || 'pwsh'
    defaults.codexMode = d.codexMode || d.mode || 'embedded'
    defaults.codexShell = d.codexShell || d.shell || 'pwsh'
    defaults.amagiCodePreset = d.amagiCodePreset || ''
    defaults.amagiCodeMode = d.amagiCodeMode || d.mode || 'embedded'
    defaults.amagiCodeShell = d.amagiCodeShell || d.shell || 'pwsh'
    defaults.useProxy = d.useProxy || false
  } catch (err) {
    console.error('load defaults:', err)
  }
  try {
    shellPaths.value = await GetShellPaths()
  } catch (err) {
    console.error('load shell paths:', err)
  }
  try {
    const t = await GetTerminalSettings()
    terminalScrollback.value = t.scrollback || 100000
  } catch (err) {
    console.error('load terminal settings:', err)
  }
}

const saveDefaults = async () => {
  saving.value = true
  try {
    await SetDashboardDefaults({
      provider: defaults.provider,
      preset: defaults.preset,
      openCodeProvider: defaults.openCodeProvider,
      openCodePreset: defaults.openCodePreset,
      mode: defaults.claudeMode,
      shell: defaults.claudeShell,
      claudeMode: defaults.claudeMode,
      claudeShell: defaults.claudeShell,
      openCodeMode: defaults.openCodeMode,
      openCodeShell: defaults.openCodeShell,
      codexMode: defaults.codexMode,
      codexShell: defaults.codexShell,
      amagiCodePreset: defaults.amagiCodePreset,
      amagiCodeMode: defaults.amagiCodeMode,
      amagiCodeShell: defaults.amagiCodeShell,
      useProxy: defaults.useProxy,
    } as any)
    showSuccess('默认值已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    saving.value = false
  }
}

const saveTerminal = async () => {
  savingTerminal.value = true
  try {
    const val = Math.max(1000, Math.min(10000000, terminalScrollback.value || 100000))
    await SetTerminalSettings({ scrollback: val } as any)
    terminalScrollback.value = val
    showSuccess('终端设置已保存（重新打开终端后生效）')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    savingTerminal.value = false
  }
}

const addShell = async () => {
  if (!newShellPath.value) return
  try {
    await AddShellPath({ path: newShellPath.value, label: newShellLabel.value || basename(newShellPath.value) } as any)
    shellPaths.value = await GetShellPaths()
    newShellLabel.value = ''
    newShellPath.value = ''
    showSuccess('Shell 路径已添加')
  } catch (err: any) {
    if (err.toString().includes('already exists')) {
      showError('该路径已存在')
    } else {
      showError('添加失败: ' + err)
    }
  }
}

const removeShell = async (path: string) => {
  try {
    await RemoveShellPath(path)
    shellPaths.value = await GetShellPaths()
    showSuccess('已删除')
  } catch (err) {
    showError('删除失败: ' + err)
  }
}

function basename(p: string): string {
  const parts = p.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || p
}

// --- 远程控制 ---
const remoteStatus = ref<{ host: string; port: number; token: string; running: boolean }>({ host: '0.0.0.0', port: 8680, token: '', running: false })
const remoteEnabled = ref(false)
const remoteToken = ref('')
const remoteHost = ref('0.0.0.0')
const remotePort = ref(8680)
const showToken = ref(false)
const togglingRemote = ref(false)
const savingPort = ref(false)
const regenerating = ref(false)
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const mobileWebRoot = ref('')
const savingWebRoot = ref(false)

async function loadRemoteStatus() {
  try {
    const status = await GetRemoteStatus()
    remoteStatus.value = status as any
    remoteEnabled.value = (status as any).running || false
    remoteToken.value = (status as any).token || ''
    remoteHost.value = (status as any).host || '0.0.0.0'
    remotePort.value = (status as any).port || 8680
    if (remoteEnabled.value && activeTab.value === 'remote') {
      await nextTick()
      await renderQRCode()
    }
  } catch (err) {
    console.error('load remote status:', err)
  }
  try {
    mobileWebRoot.value = await GetMobileWebRoot()
  } catch (err) {
    console.error('load mobile web root:', err)
  }
}

watch(activeTab, async (newTab) => {
  if (newTab === 'remote' && remoteEnabled.value) {
    await nextTick()
    renderQRCode()
  }
})

async function renderQRCode() {
  if (!qrCanvas.value) return
  const localIP = await getLocalIP()
  const url = `http://${localIP}:${remotePort.value}`
  const payload = JSON.stringify({ url, token: remoteToken.value })
  try {
    await QRCode.toCanvas(qrCanvas.value, payload, {
      width: 200,
      margin: 2,
      color: { dark: '#e0e6ed', light: '#1a1f2e' },
    })
  } catch (err) {
    console.error('QR render error:', err)
  }
}

async function getLocalIP(): Promise<string> {
  return new Promise((resolve) => {
    try {
      const pc = new RTCPeerConnection({ iceServers: [] })
      pc.createDataChannel('')
      pc.createOffer().then(offer => pc.setLocalDescription(offer))
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

async function toggleRemote() {
  togglingRemote.value = true
  try {
    const newState = !remoteEnabled.value
    await ToggleRemoteServer(newState)
    remoteEnabled.value = newState
    if (newState) {
      showSuccess('远程服务器已启动')
      if (activeTab.value === 'remote') {
        await nextTick()
        await renderQRCode()
      }
    } else {
      showSuccess('远程服务器已停止')
    }
  } catch (err) {
    showError('操作失败: ' + err)
  } finally {
    togglingRemote.value = false
  }
}

async function applyHostPort() {
  savingPort.value = true
  try {
    await SetRemoteHost(remoteHost.value.trim() || '0.0.0.0')
    await SetRemotePort(remotePort.value)
    remoteStatus.value.host = remoteHost.value.trim() || '0.0.0.0'
    remoteStatus.value.port = remotePort.value
    showSuccess('监听地址已更新')
    if (remoteEnabled.value && activeTab.value === 'remote') {
      await nextTick()
      await renderQRCode()
    }
  } catch (err) {
    showError('设置失败: ' + err)
  } finally {
    savingPort.value = false
  }
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(remoteToken.value)
    showSuccess('Token 已复制')
  } catch {
    showError('复制失败')
  }
}

async function regenerateToken() {
  regenerating.value = true
  try {
    const newToken = await RegenerateRemoteToken()
    remoteToken.value = newToken
    showSuccess('Token 已刷新')
    if (remoteEnabled.value && activeTab.value === 'remote') {
      await nextTick()
      await renderQRCode()
    }
  } catch (err) {
    showError('刷新 Token 失败: ' + err)
  } finally {
    regenerating.value = false
  }
}

async function saveMobileWebRoot() {
  savingWebRoot.value = true
  try {
    await SetMobileWebRoot(mobileWebRoot.value.trim())
    showSuccess('移动端 Web 目录已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    savingWebRoot.value = false
  }
}

onMounted(async () => {
  await loadData()
  await loadRemoteStatus()
  try {
    const info = await GetAppInfo()
    currentVersion.value = (info as any).version || ''
  } catch {}
  try {
    githubToken.value = await GetGitHubToken()
  } catch {}
})
</script>

<style scoped>
/* App Colors */
.settings-layout {
  --bg: #0f1219;
  --surface: #1a1f2e;
  --elevated: #232a3b;
  --border: #2a2f3e;
  --border-hover: #3a4f5e;
  --text-primary: #e0e6ed;
  --text-secondary: #8899aa;
  --text-muted: #5a6a7a;
  --accent: #4fc3f7;
  --accent-hover: #7bd4f9;
  --success: #66bb6a;
  --error: #ef5350;
  
  display: flex;
  height: 100%;
  gap: 40px;
  color: var(--text-primary);
}

/* Sidebar */
.settings-sidebar {
  width: 200px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24px;
}

.nav-tabs {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.nav-tab {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  background: transparent;
  border: none;
  border-left: 3px solid transparent;
  border-radius: 0 6px 6px 0;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 14px;
  font-family: inherit;
  transition: background 0.2s, border-color 0.2s, color 0.2s;
  text-align: left;
}

.nav-tab:hover {
  background: var(--surface);
  color: var(--text-primary);
}

.nav-tab.active {
  border-left-color: var(--accent);
  background: rgba(79, 195, 247, 0.08);
  color: var(--accent);
  font-weight: 500;
}

.tab-icon {
  font-size: 16px;
  width: 20px;
  text-align: center;
}

/* Content Area */
.settings-content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding-right: 16px;
  position: relative;
}

/* Transitions */
.fade-slide-enter-active,
.fade-slide-leave-active {
  transition: all 0.2s cubic-bezier(0.25, 0.8, 0.25, 1);
}
.fade-slide-enter-from {
  opacity: 0;
  transform: translateX(15px);
}
.fade-slide-leave-to {
  opacity: 0;
  transform: translateX(-15px);
}

.settings-section {
  padding-bottom: 40px;
}

.section-header {
  margin-bottom: 32px;
}

.section-header h2 {
  font-size: 20px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 8px 0;
}

.section-header p {
  color: var(--text-secondary);
  font-size: 14px;
  margin: 0;
}

.group-header {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-secondary);
  margin: 0 0 16px 0;
  font-weight: 600;
}

.group-separator {
  height: 1px;
  background: var(--border);
  margin: 32px 0;
}

.setting-group {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-row {
  display: flex;
  gap: 24px;
}

.flex-1 { flex: 1; }

.form-group label {
  display: block;
  margin-bottom: 8px;
  color: var(--text-secondary);
  font-size: 14px;
}

.field-desc {
  color: var(--text-muted);
  font-size: 12px;
}

/* Inputs */
.input-field {
  width: 100%;
  background: var(--bg);
  border: 1px solid var(--border);
  color: var(--text-primary);
  padding: 10px 12px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  transition: border-color 0.15s, box-shadow 0.15s;
  outline: none;
  box-sizing: border-box;
}

.input-field:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px rgba(79, 195, 247, 0.15);
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.monospace {
  font-family: monospace;
}

/* Select */
.select-wrapper {
  position: relative;
}

.select-wrapper::after {
  content: '▼';
  font-size: 10px;
  color: var(--text-muted);
  position: absolute;
  right: 12px;
  top: 50%;
  transform: translateY(-50%);
  pointer-events: none;
}

.select-wrapper .input-field {
  appearance: none;
  -webkit-appearance: none;
  padding-right: 32px;
}

/* Inline Inputs */
.inline-input-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.token-input {
  letter-spacing: 2px;
}

/* Buttons */
.btn {
  padding: 10px 20px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.15s, box-shadow 0.15s, background 0.15s;
  border: none;
  outline: none;
  background: var(--surface);
  color: var(--text-primary);
  border: 1px solid var(--border);
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none !important;
  box-shadow: none !important;
}

.btn.small {
  padding: 6px 14px;
  font-size: 13px;
}

.btn.primary {
  background: var(--accent);
  color: var(--bg);
  border-color: transparent;
}

.btn.primary:hover:not(:disabled) {
  background: var(--accent-hover);
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(79, 195, 247, 0.2);
}

.btn.danger {
  background: transparent;
  color: var(--error);
  border-color: var(--error);
}

.btn.danger:hover:not(:disabled) {
  background: rgba(239, 83, 80, 0.1);
}

/* Toggle Switches */
.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px;
  background: var(--surface);
  border-radius: 8px;
}

.toggle-info label {
  color: var(--text-primary);
  font-size: 14px;
  margin: 0;
}

.toggle-info .field-desc {
  margin-top: 4px;
  display: block;
}

.ios-toggle {
  position: relative;
  width: 44px;
  height: 24px;
  background: var(--border);
  border-radius: 24px;
  cursor: pointer;
  transition: background 0.2s ease;
  border: none;
  outline: none;
  flex-shrink: 0;
}

.ios-toggle.large-toggle {
  width: 52px;
  height: 28px;
  border-radius: 28px;
}

.ios-toggle.active {
  background: var(--accent);
}

.ios-toggle::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  background: #fff;
  border-radius: 50%;
  transition: transform 0.2s cubic-bezier(0.25, 0.8, 0.25, 1), background 0.2s;
  box-shadow: 0 2px 4px rgba(0,0,0,0.2);
}

.ios-toggle.large-toggle::after {
  width: 24px;
  height: 24px;
}

.ios-toggle.active::after {
  transform: translateX(20px);
}

.ios-toggle.large-toggle.active::after {
  transform: translateX(24px);
}

/* Engine Tabs */
.engine-tabs {
  display: inline-flex;
  background: var(--surface);
  border-radius: 8px;
  padding: 4px;
  gap: 4px;
  border: 1px solid var(--border);
}

.engine-tab {
  flex: 1;
  padding: 8px 16px;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  transition: all 0.2s;
}

.engine-tab:hover {
  color: var(--text-primary);
}

.engine-tab.active {
  background: var(--elevated);
  color: var(--text-primary);
  box-shadow: 0 1px 3px rgba(0,0,0,0.2);
}

/* Mode Selector */
.mode-selector {
  display: flex;
  gap: 12px;
}

.mode-btn {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 0.2s ease;
}

.mode-btn:hover {
  border-color: var(--border-hover);
  background: var(--elevated);
  transform: translateY(-1px);
}

.mode-btn.active {
  border-color: var(--accent);
  color: var(--accent);
  background: rgba(79, 195, 247, 0.05);
  box-shadow: 0 0 0 1px var(--accent) inset, 0 4px 12px rgba(79, 195, 247, 0.1);
}

.mode-icon { font-size: 20px; }
.mode-label { font-size: 14px; font-weight: 500; }

/* Shell Pills */
.shell-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.shell-pill {
  padding: 8px 16px;
  background: var(--surface);
  border: 1px solid transparent;
  border-radius: 20px;
  color: var(--text-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.shell-pill:hover {
  background: var(--elevated);
  color: var(--text-primary);
}

.shell-pill.active {
  background: var(--accent);
  color: var(--bg);
  font-weight: 600;
}

/* Shell Paths */
.add-shell-card {
  display: flex;
  gap: 12px;
  background: var(--surface);
  padding: 16px;
  border-radius: 8px;
  border: 1px solid var(--border);
}

.shell-list {
  display: flex;
  flex-direction: column;
}

.shell-list-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  transition: background 0.2s, border-radius 0.2s;
}

.shell-list-item:hover {
  background: var(--surface);
  border-radius: 6px;
  border-bottom-color: transparent;
}

.shell-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.shell-label {
  font-size: 14px;
  color: var(--text-primary);
  font-weight: 500;
}

.shell-path {
  font-size: 12px;
  color: var(--text-muted);
}

.delete-btn {
  opacity: 0;
  transition: opacity 0.2s;
}

.shell-list-item:hover .delete-btn {
  opacity: 1;
}

.empty-state {
  padding: 32px;
  text-align: center;
  background: var(--surface);
  border: 1px dashed var(--border);
  border-radius: 8px;
  color: var(--text-muted);
  font-size: 14px;
}

/* Slider */
.range-with-input {
  display: flex;
  align-items: center;
  gap: 20px;
}

.range-slider {
  appearance: none;
  background: var(--surface);
  height: 6px;
  border-radius: 3px;
  outline: none;
}

.range-slider::-webkit-slider-thumb {
  appearance: none;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--accent);
  cursor: pointer;
  transition: transform 0.1s;
}

.range-slider::-webkit-slider-thumb:hover {
  transform: scale(1.2);
}

/* Remote */
.remote-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24px;
  background: var(--surface);
  border-radius: 12px;
  border: 1px solid var(--border);
}

.remote-status {
  display: flex;
  align-items: center;
  gap: 16px;
}

.status-ring {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--text-muted);
  position: relative;
}

.remote-status.active .status-ring {
  background: var(--success);
  box-shadow: 0 0 0 4px rgba(102, 187, 106, 0.2);
}

.status-info h4 {
  margin: 0 0 4px 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
}

.status-info p {
  margin: 0;
  font-size: 13px;
  color: var(--text-secondary);
}

.qr-frame {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  background: var(--surface);
  border-radius: 12px;
  padding: 24px;
  border: 1px solid var(--border);
  max-width: max-content;
}

.qr-canvas {
  border-radius: 8px;
  overflow: hidden;
}

.qr-frame p {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}

/* Updates */
.update-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--surface);
  padding: 16px 24px;
  border-radius: 8px;
  border: 1px solid var(--border);
}

.version-info {
  display: flex;
  align-items: center;
  gap: 16px;
}

.version-label {
  color: var(--text-secondary);
  font-size: 14px;
}

.version-badge {
  display: inline-block;
  padding: 4px 12px;
  background: rgba(79, 195, 247, 0.1);
  color: var(--accent);
  border-radius: 20px;
  font-family: monospace;
  font-weight: 600;
}

.update-card {
  background: var(--surface);
  border-left: 4px solid var(--success);
  border-radius: 8px;
  padding: 24px;
  margin-top: 24px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
}

.update-card-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-dot.online { background: var(--success); }

.update-title {
  color: var(--text-primary);
  font-weight: 600;
  font-size: 16px;
}

.update-version-new {
  color: var(--success);
  font-family: monospace;
  font-weight: 600;
}

.update-date {
  color: var(--text-muted);
  font-size: 12px;
  margin: 8px 0 16px 0;
}

.release-notes {
  background: var(--bg);
  padding: 16px;
  border-radius: 6px;
  margin-bottom: 24px;
  max-height: 200px;
  overflow-y: auto;
}

.release-notes pre {
  margin: 0;
  color: var(--text-secondary);
  font-size: 13px;
  font-family: inherit;
  white-space: pre-wrap;
  line-height: 1.5;
}

.progress-container {
  margin-top: 16px;
  display: flex;
  align-items: center;
  gap: 16px;
}

.progress-bar {
  flex: 1;
  height: 6px;
  background: var(--border);
  border-radius: 3px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 3px;
  transition: width 0.3s ease;
}

.progress-text {
  color: var(--text-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.update-uptodate {
  margin-top: 24px;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--success);
  font-size: 14px;
}

.update-error {
  margin-top: 16px;
  color: var(--error);
  font-size: 13px;
}

/* About */
.about-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
}

.app-logo {
  width: 80px;
  height: 80px;
  background: var(--surface);
  border-radius: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 24px;
  box-shadow: 0 8px 24px rgba(0,0,0,0.2);
  border: 1px solid var(--border);
}

.app-icon {
  font-size: 36px;
  color: var(--accent);
}

.app-name {
  font-size: 28px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 8px 0;
}

.app-version {
  color: var(--text-muted);
  font-size: 14px;
  margin: 0 0 40px 0;
}

.about-details {
  width: 100%;
  max-width: 400px;
  background: var(--surface);
  border-radius: 8px;
  padding: 16px 24px;
  border: 1px solid var(--border);
}

.detail-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.detail-label { color: var(--text-secondary); font-size: 14px; }
.detail-value { color: var(--text-primary); font-size: 14px; }

.section-footer {
  margin-top: 32px;
  display: flex;
  justify-content: flex-end;
}
</style>

