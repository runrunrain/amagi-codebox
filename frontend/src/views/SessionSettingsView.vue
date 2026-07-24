<template>
  <section class="view-settings">
    <PageHead title="会话设置" description="配置并启动一个新的 AI 编程会话" />

    <ConfigCard>
      <div class="card-head">
        <h2>快速启动</h2>
      </div>

      <Segmented
        :model-value="dashState.engine"
        :options="engineOptions"
        @update:model-value="handleEngineChange"
      />

      <div class="setting-list">
        <!-- 服务提供商 -->
        <div class="setting-row">
          <label>服务提供商</label>
          <select
            class="select"
            :value="currentProvider"
            :disabled="providerOptions.length === 0"
            @change="handleProviderChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled v-if="providerOptions.length === 0">暂无可用提供商</option>
            <option
              v-for="opt in providerOptions"
              :key="opt.value"
              :value="opt.value"
            >{{ opt.label }}</option>
          </select>
        </div>

        <!-- 预设配置 -->
        <div class="setting-row">
          <label>预设配置</label>
          <select
            class="select"
            :value="currentPreset"
            :disabled="presetOptions.length === 0"
            @change="handlePresetChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled v-if="presetOptions.length === 0">暂无可用预设</option>
            <option
              v-for="opt in presetOptions"
              :key="opt.value"
              :value="opt.value"
              :title="opt.title"
            >{{ opt.label }}</option>
          </select>
        </div>

        <!-- 启动模式 -->
        <div class="setting-row">
          <label>启动模式</label>
          <select
            class="select"
            :value="currentMode"
            @change="handleModeChange(($event.target as HTMLSelectElement).value)"
          >
            <option
              v-for="m in launchModes"
              :key="m.value"
              :value="m.value"
            >{{ m.label }}</option>
          </select>
        </div>

        <!-- 终端 Shell（仅内嵌模式可选） -->
        <div class="setting-row" v-if="currentMode === 'embedded'">
          <label>终端 Shell</label>
          <select
            class="select"
            :value="currentShell"
            @change="handleShellChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="">直接启动</option>
            <option
              v-for="s in builtinShellOptions"
              :key="s.value"
              :value="s.value"
            >{{ s.label }}</option>
            <option value="__custom__">自定义路径</option>
          </select>
        </div>

        <!-- 自定义 Shell 路径 -->
        <div class="setting-row" v-if="currentMode === 'embedded' && currentShell === '__custom__'">
          <label>Shell 路径</label>
          <div class="input-group">
            <TextInput
              :model-value="currentCustomShellPath"
              placeholder="/bin/zsh"
              mono
              @update:model-value="handleCustomShellChange"
            />
          </div>
        </div>

        <!-- 工作目录 -->
        <div class="setting-row workdir-row">
          <label>工作目录</label>
          <div class="workdir-container">
            <!-- 当前启动目录：本次会话的实际 cwd（单值，OS 进程本质只能单值） -->
            <div class="current-workdir">
              <div class="input-group">
                <TextInput
                  :model-value="dashState.workDir"
                  placeholder="选择或输入工作目录"
                  mono
                  @update:model-value="dashState.workDir = $event"
                />
                <button class="btn btn-ghost browse-btn" @click="handleBrowse" :disabled="browsing">
                  {{ browsing ? '…' : '浏览' }}
                </button>
              </div>
              <div class="current-workdir-hint">
                <span v-if="dashState.workDir" class="hint-static">本次会话启动时使用的目录</span>
                <span v-else class="hint-warn">尚未选择启动目录，OpenCode 引擎要求必填</span>
              </div>
            </div>

            <!-- 目录收藏夹：可保存任意多个目录，点击行即选用为启动目录 -->
            <div class="workdir-favorites" role="group" aria-label="目录收藏夹">
              <div class="favorites-header">
                <div class="favorites-title">
                  <span>目录收藏夹</span>
                  <span class="favorites-count" v-if="savedWorkDirs.length > 0">{{ savedWorkDirs.length }}</span>
                </div>
                <button
                  class="btn btn-ghost add-btn"
                  @click="handleBrowseAndAdd"
                  :disabled="browsing || addingDir || addDialogOpen"
                  :title="'浏览并添加到收藏夹'"
                >
                  {{ browsing ? '处理中…' : '浏览并添加' }}
                </button>
              </div>

              <!-- 空态 -->
              <div class="favorites-empty" v-if="savedWorkDirs.length === 0">
                <p>还没有保存的工作目录。</p>
                <p class="empty-hint">点击右上「浏览并添加」收藏你的第一个工作目录，下次启动可一键选用。</p>
              </div>

              <!-- 列表 -->
              <ul class="favorites-list" v-else>
                <li
                  v-for="entry in savedWorkDirs"
                  :key="entry.path"
                  class="favorite-item"
                  :class="{ active: dashState.workDir === entry.path }"
                  @click="selectWorkDir(entry.path)"
                  :title="dashState.workDir === entry.path ? `当前启动目录：${entry.path}` : `点击选用 ${entry.path}`"
                >
                  <div class="favorite-radio" aria-hidden="true">
                    <span class="radio-dot" v-if="dashState.workDir === entry.path"></span>
                  </div>
                  <div class="favorite-info">
                    <div class="favorite-label">{{ entry.label || fileNameOf(entry.path) }}</div>
                    <div class="favorite-path">{{ entry.path }}</div>
                  </div>
                  <button
                    class="favorite-remove"
                    @click.stop="handleRemoveSavedDir(entry.path)"
                    title="从收藏夹删除"
                    aria-label="从收藏夹删除"
                  >
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="12" height="12" aria-hidden="true">
                      <line x1="18" y1="6" x2="6" y2="18" />
                      <line x1="6" y1="6" x2="18" y2="18" />
                    </svg>
                  </button>
                </li>
              </ul>
            </div>
          </div>
        </div>

        <!-- 启用注入代理 -->
        <div class="setting-row">
          <label>启用注入代理</label>
          <Switch
            :model-value="dashState.useProxy"
            @update:model-value="dashState.useProxy = $event"
          />
        </div>

        <!-- 启用 Headroom 上下文压缩 -->
        <div class="setting-row">
          <label>启用 Headroom 上下文压缩</label>
          <Switch
            :model-value="dashState.useHeadroom"
            @update:model-value="dashState.useHeadroom = $event"
          />
        </div>
      </div>
    </ConfigCard>

    <!-- 添加目录到收藏夹 Dialog -->
    <Dialog
      :open="addDialogOpen"
      @update:open="addDialogOpen = $event"
      title="添加到目录收藏夹"
      :description="pendingDirPath ? `路径：${pendingDirPath}` : '已选择目录'"
    >
      <div class="add-form">
        <label class="add-form-label">标签（可选）</label>
        <TextInput
          :model-value="pendingDirLabel"
          @update:model-value="pendingDirLabel = $event"
          placeholder="留空则使用目录名"
          @keydown.enter="confirmAddDir"
        />
        <p class="add-form-hint">为这个目录起一个易记的名字（例如「主项目」「试验场」），便于在多个项目间区分。</p>
      </div>
      <template #footer>
        <button class="btn btn-ghost" @click="cancelAddDir">取消</button>
        <button
          class="btn btn-primary"
          @click="confirmAddDir"
          :disabled="addingDir || !pendingDirPath"
        >
          {{ addingDir ? '添加中…' : '添加' }}
        </button>
      </template>
    </Dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import Segmented from '../components/ui/Segmented.vue'
import Switch from '../components/ui/Switch.vue'
import TextInput from '../components/ui/TextInput.vue'
import Dialog from '../components/ui/Dialog.vue'

import { useDashboardState } from '../composables/useDashboardState'
import { usePlatformCapabilities } from '../composables/usePlatformCapabilities'
import { useToast } from '../composables/useToast'

import * as providerApi from '../api/provider'
import { BrowseDirectory, GetSavedWorkDirs, AddSavedWorkDir, RemoveSavedWorkDir } from '../../wailsjs/go/main/App'
import { GetOpenCodePresets } from '../../wailsjs/go/config/ConfigService'
import { config, settings } from '../../wailsjs/go/models'

// 复用 wailsjs 已生成的类型（单一真相源）：后端 schema 变更时会随 wails generate 同步
type WorkDirEntry = settings.WorkDirEntry

type Provider = config.Provider
type MergedTerminalPreset = config.MergedTerminalPreset

const { state: dashState, initDefaults } = useDashboardState()
const platformCaps = usePlatformCapabilities()
const { showSuccess, showError, showInfo } = useToast()

const browsing = ref(false)
const savedWorkDirs = ref<WorkDirEntry[]>([])

// 添加目录到收藏夹相关状态
const addDialogOpen = ref(false)
const pendingDirPath = ref('')
const pendingDirLabel = ref('')
const addingDir = ref(false)

// --- 数据源 ---
// 后端 GetProvidersByType 已按 IsAnthropicCompatible / IsOpenAICompatible 过滤，
// 这里分别缓存两份，作为对应引擎下拉的真相源。
const anthropicProviders = ref<Record<string, Provider>>({})
const openaiProviders = ref<Record<string, Provider>>({})
const claudePresets = ref<MergedTerminalPreset[]>([])
const codexPresets = ref<MergedTerminalPreset[]>([])
const piPresets = ref<MergedTerminalPreset[]>([])
const openCodePresetList = ref<Array<{ key: string; name: string; description: string; bindingCount: number }>>([])

// --- 引擎选项 ---
const engineOptions = [
  { value: 'claudecode', label: 'ClaudeCode' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'codex', label: 'Codex' },
  { value: 'pi', label: 'Pi' },
]

// --- 引擎相关计算属性 ---
const currentMode = computed(() => {
  if (dashState.engine === 'claudecode') return dashState.claudeMode
  if (dashState.engine === 'opencode') return dashState.openCodeMode
  if (dashState.engine === 'pi') return dashState.piMode
  return dashState.codexMode
})
function setMode(v: string) {
  if (dashState.engine === 'claudecode') dashState.claudeMode = v
  else if (dashState.engine === 'opencode') dashState.openCodeMode = v
  else if (dashState.engine === 'pi') dashState.piMode = v
  else dashState.codexMode = v
}

const currentShell = computed(() => {
  if (dashState.engine === 'claudecode') return dashState.claudeShell
  if (dashState.engine === 'opencode') return dashState.openCodeShell
  if (dashState.engine === 'pi') return dashState.piShell
  return dashState.codexShell
})
function setShell(v: string) {
  if (dashState.engine === 'claudecode') dashState.claudeShell = v
  else if (dashState.engine === 'opencode') dashState.openCodeShell = v
  else if (dashState.engine === 'pi') dashState.piShell = v
  else dashState.codexShell = v
}

const currentCustomShellPath = computed(() => {
  if (dashState.engine === 'claudecode') return dashState.claudeCustomShellPath
  if (dashState.engine === 'opencode') return dashState.openCodeCustomShellPath
  if (dashState.engine === 'pi') return dashState.piCustomShellPath
  return dashState.codexCustomShellPath
})
function setCustomShellPath(v: string) {
  if (dashState.engine === 'claudecode') dashState.claudeCustomShellPath = v
  else if (dashState.engine === 'opencode') dashState.openCodeCustomShellPath = v
  else if (dashState.engine === 'pi') dashState.piCustomShellPath = v
  else dashState.codexCustomShellPath = v
}

const currentProvider = computed(() => {
  if (dashState.engine === 'codex') return dashState.codexProvider
  if (dashState.engine === 'pi') return dashState.piProvider
  return dashState.provider
})

const currentPreset = computed(() => {
  if (dashState.engine === 'codex') return dashState.codexModel
  if (dashState.engine === 'pi') return dashState.piModel
  if (dashState.engine === 'opencode') return dashState.openCodePresetKey
  return dashState.preset
})

// --- 下拉选项 ---
const providerOptions = computed(() => {
  const map = (dashState.engine === 'codex' || dashState.engine === 'pi') ? openaiProviders.value : anthropicProviders.value
  return Object.keys(map).sort().map(name => ({ value: name, label: name }))
})

const presetOptions = computed(() => {
  if (dashState.engine === 'opencode') {
    // 开头添加"使用全局配置"选项
    const globalOption = { value: '', label: '使用全局配置', title: '' }
    const presetOptions = openCodePresetList.value.map(p => ({
      value: p.key,
      label: p.bindingCount > 0 ? `${p.name} (${p.bindingCount})` : p.name,
      title: extractOpenCodeModelName(p),
    }))
    return [globalOption, ...presetOptions]
  }
  const list = dashState.engine === 'codex' ? codexPresets.value
    : dashState.engine === 'pi' ? piPresets.value
    : claudePresets.value
  const targetProvider = currentProvider.value
  return list
    .filter(p => !targetProvider || p.provider === targetProvider)
    .map(p => ({ value: p.key, label: p.label || p.key, title: p.model || '' }))
})

// 从 OpenCodePreset 提取模型名用于 hover 显示
function extractOpenCodeModelName(p: { key: string; name: string; description: string }): string {
  // 尝试从 description 解析模型名（常见格式：Model: xxx）
  const modelMatch = p.description.match(/model[:\s]+([^\n,]+)/i)
  if (modelMatch) {
    return modelMatch[1].trim()
  }
  // 兜底：返回描述（若不为空）
  return p.description || ''
}

const launchModes = computed(() => platformCaps.launchModes.value)
const builtinShellOptions = computed(() => platformCaps.builtinShellOptions.value)

// --- 校验与自动同步（照搬旧逻辑） ---
function validateClaudePreset() {
  if (dashState.engine !== 'claudecode') return
  if (dashState.preset) {
    const entry = claudePresets.value.find(p => p.key === dashState.preset)
    if (entry && entry.provider === dashState.provider) return
  }
  if (claudePresets.value.length > 0) {
    const match = claudePresets.value.find(p => p.provider === dashState.provider)
    if (match) {
      dashState.preset = match.key
    } else {
      const first = claudePresets.value[0]
      dashState.provider = first.provider
      dashState.preset = first.key
    }
  } else {
    dashState.preset = ''
  }
}

function validateCodexPreset() {
  if (dashState.engine !== 'codex') return
  if (dashState.codexModel) {
    const entry = codexPresets.value.find(p => p.key === dashState.codexModel)
    if (entry && entry.provider === dashState.codexProvider) return
  }
  if (codexPresets.value.length > 0) {
    const match = codexPresets.value.find(p => p.provider === dashState.codexProvider)
    if (match) {
      dashState.codexModel = match.key
    } else {
      const first = codexPresets.value[0]
      dashState.codexProvider = first.provider
      dashState.codexModel = first.key
    }
  } else {
    dashState.codexModel = ''
  }
}

function validatePiPreset() {
  if (dashState.engine !== 'pi') return
  if (dashState.piModel) {
    const entry = piPresets.value.find(p => p.key === dashState.piModel)
    if (entry && entry.provider === dashState.piProvider) return
  }
  if (piPresets.value.length > 0) {
    const match = piPresets.value.find(p => p.provider === dashState.piProvider)
    if (match) {
      dashState.piModel = match.key
    } else {
      const first = piPresets.value[0]
      dashState.piProvider = first.provider
      dashState.piModel = first.key
    }
  } else {
    dashState.piModel = ''
  }
}

// --- 事件处理 ---
function handleEngineChange(v: string) {
  dashState.engine = v as any
}

function handleProviderChange(v: string) {
  if (dashState.engine === 'codex') {
    dashState.codexProvider = v
    // 自动重置预设到该 provider 的第一个
    const first = codexPresets.value.find(p => p.provider === v)
    dashState.codexModel = first ? first.key : ''
  } else if (dashState.engine === 'pi') {
    dashState.piProvider = v
    const first = piPresets.value.find(p => p.provider === v)
    dashState.piModel = first ? first.key : ''
  } else {
    dashState.provider = v
    const first = claudePresets.value.find(p => p.provider === v)
    dashState.preset = first ? first.key : ''
  }
}

function handlePresetChange(v: string) {
  if (dashState.engine === 'codex') {
    dashState.codexModel = v
    const entry = codexPresets.value.find(p => p.key === v)
    if (entry && entry.provider) dashState.codexProvider = entry.provider
  } else if (dashState.engine === 'pi') {
    dashState.piModel = v
    const entry = piPresets.value.find(p => p.key === v)
    if (entry && entry.provider) dashState.piProvider = entry.provider
  } else if (dashState.engine === 'opencode') {
    dashState.openCodePresetKey = v
  } else {
    dashState.preset = v
    const entry = claudePresets.value.find(p => p.key === v)
    if (entry && entry.provider) dashState.provider = entry.provider
  }
}

function handleModeChange(v: string) {
  setMode(v)
}

function handleShellChange(v: string) {
  setShell(v)
}

function handleCustomShellChange(v: string) {
  setCustomShellPath(v)
}

async function handleBrowse() {
  browsing.value = true
  try {
    const dir = await BrowseDirectory()
    if (dir) dashState.workDir = dir
  } catch (err) {
    showError('选择目录失败: ' + err)
  } finally {
    browsing.value = false
  }
}

async function loadSavedWorkDirs() {
  try {
    savedWorkDirs.value = await GetSavedWorkDirs()
  } catch (err) {
    // 保留 console.error 用于调试，同时给用户明确反馈（不阻断：后续添加/删除会重新拉取刷新）
    console.error('[SessionSettingsView.loadSavedWorkDirs]', err)
    showError('加载工作目录收藏夹失败: ' + err)
  }
}

// 浏览并添加：选定目录后弹出 label 输入对话框，确认后调用后端 AddSavedWorkDir
async function handleBrowseAndAdd() {
  // 防御性守卫：Dialog 已打开 / 正在浏览 / 正在添加时禁止再次触发，避免重复弹原生对话框覆盖 pendingDirPath
  if (addDialogOpen.value || browsing.value || addingDir.value) return
  browsing.value = true
  try {
    const dir = await BrowseDirectory()
    if (!dir) return
    pendingDirPath.value = dir
    // 预填 label：若该目录已在收藏夹中，回填现有 label 便于参考编辑；否则留空（后端会用 base 名）
    const existed = savedWorkDirs.value.find(e => e.path === dir)
    pendingDirLabel.value = existed ? existed.label : ''
    addDialogOpen.value = true
  } catch (err) {
    showError('选择目录失败: ' + err)
  } finally {
    browsing.value = false
  }
}

function cancelAddDir() {
  addDialogOpen.value = false
  pendingDirPath.value = ''
  pendingDirLabel.value = ''
}

async function confirmAddDir() {
  if (!pendingDirPath.value) {
    showError('目录路径为空')
    return
  }
  const beforeLen = savedWorkDirs.value.length
  const targetPath = pendingDirPath.value
  addingDir.value = true
  try {
    const next = await AddSavedWorkDir(targetPath, pendingDirLabel.value.trim())
    savedWorkDirs.value = next
    if (next.length === beforeLen) {
      // 后端按 path 去重，长度未增说明已存在
      showInfo('该目录已在收藏夹中')
    } else {
      showSuccess('已添加到收藏夹')
      // 添加成功后自动选用为启动目录（用户刚刚收藏的目录通常就是想立即使用的）
      dashState.workDir = targetPath
    }
    addDialogOpen.value = false
    pendingDirPath.value = ''
    pendingDirLabel.value = ''
  } catch (err) {
    showError('添加失败: ' + err)
  } finally {
    addingDir.value = false
  }
}

async function handleRemoveSavedDir(path: string) {
  try {
    const next = await RemoveSavedWorkDir(path)
    savedWorkDirs.value = next
    // 若删除的正是当前启动目录，立即清空，避免界面仍显示已不存在的目录
    if (dashState.workDir === path) {
      dashState.workDir = ''
    }
    showSuccess('已从收藏夹移除')
  } catch (err) {
    showError('删除失败: ' + err)
  }
}

// 选用某已保存目录作为本次启动目录（写入单值 dashState.workDir）
function selectWorkDir(path: string) {
  dashState.workDir = path
}

// 从路径取末段（与后端 filepath.Base 一致），用于 label 缺省时的展示
function fileNameOf(path: string): string {
  if (!path) return ''
  const normalized = path.replace(/\\/g, '/')
  const parts = normalized.split('/').filter(Boolean)
  return parts.length > 0 ? parts[parts.length - 1] : path
}

// --- 数据加载 ---
async function loadProviders() {
  try {
    const [anthropic, openai] = await Promise.all([
      providerApi.getProvidersByType('anthropic'),
      providerApi.getProvidersByType('openai'),
    ])
    anthropicProviders.value = anthropic || {}
    openaiProviders.value = openai || {}
  } catch (err) {
    console.error('Failed to load providers:', err)
  }
}

async function loadTerminalPresets() {
  try {
    const [claude, codex, pi] = await Promise.all([
      providerApi.getMergedTerminalPresets('claude_code'),
      providerApi.getMergedTerminalPresets('codex'),
      providerApi.getMergedTerminalPresets('pi'),
    ])
    claudePresets.value = claude || []
    codexPresets.value = codex || []
    piPresets.value = pi || []
  } catch (err) {
    console.error('Failed to load terminal presets:', err)
  }
}

async function loadOpenCodePresets() {
  try {
    const map = await GetOpenCodePresets()
    const list: Array<{ key: string; name: string; description: string; bindingCount: number }> = []
    for (const [key, preset] of Object.entries(map || {})) {
      const p = preset as any
      list.push({
        key,
        name: p?.name || key,
        description: p?.description || '',
        bindingCount: p?.bindings ? Object.keys(p.bindings).length : 0,
      })
    }
    openCodePresetList.value = list
  } catch (err) {
    console.error('Failed to load OpenCode presets:', err)
    openCodePresetList.value = []
  }
}

// --- 监听 preset 列表变化时重新校验 ---
watch(claudePresets, () => { if (dashState.engine === 'claudecode') validateClaudePreset() })
watch(codexPresets, () => { if (dashState.engine === 'codex') validateCodexPreset() })
watch(piPresets, () => { if (dashState.engine === 'pi') validatePiPreset() })

onMounted(async () => {
  await platformCaps.ensure()
  await Promise.all([
    loadProviders(),
    loadTerminalPresets(),
    loadOpenCodePresets(),
    loadSavedWorkDirs(),
  ])
  await initDefaults()

  // 默认 provider 兜底
  if (!dashState.provider && Object.keys(anthropicProviders.value).length > 0) {
    dashState.provider = Object.keys(anthropicProviders.value)[0]
  }
  if (!dashState.codexProvider && Object.keys(openaiProviders.value).length > 0) {
    dashState.codexProvider = Object.keys(openaiProviders.value)[0]
  }
  if (!dashState.piProvider && Object.keys(openaiProviders.value).length > 0) {
    dashState.piProvider = Object.keys(openaiProviders.value)[0]
  }
  validateClaudePreset()
  validateCodexPreset()
  validatePiPreset()
})
</script>

<style scoped>
.view-settings {
  padding: 32px 36px;
  gap: 22px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-head h2 {
  font-size: 17px;
  font-weight: 600;
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
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

.select {
  appearance: none;
  -webkit-appearance: none;
  background: var(--control);
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 6px 28px 6px 10px;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  cursor: pointer;
  min-width: 180px;
  max-width: 320px;
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%238E8E93' stroke-width='2.5' stroke-linecap='round'><polyline points='6 9 12 15 18 9'/></svg>");
  background-repeat: no-repeat;
  background-position: right 9px center;
  transition: background-color 0.12s, box-shadow 0.12s;
}

.select:hover:not(:disabled) {
  background-color: var(--controlHover);
}

.select:focus {
  outline: none;
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.2);
  border-color: var(--accent);
}

.select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.input-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.input-group :deep(.text-input) {
  min-width: 280px;
}

.browse-btn {
  padding: 6px 12px;
  font-size: 12px;
  flex-shrink: 0;
}

.workdir-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
  max-width: 520px;
}

.workdir-row {
  align-items: flex-start;
}

.workdir-row > label {
  padding-top: 6px;
}

.current-workdir {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.current-workdir-hint {
  font-size: 11px;
  color: var(--tertiary);
  padding-left: 2px;
  min-height: 14px;
}

.current-workdir-hint .hint-warn {
  color: var(--secondary);
}

/* 目录收藏夹 */
.workdir-favorites {
  border-top: 1px dashed var(--separator);
  padding-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.favorites-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.favorites-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--secondary);
  font-weight: 500;
}

.favorites-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 6px;
  border-radius: 9px;
  background: var(--control);
  color: var(--secondary);
  font-size: 11px;
  font-weight: 500;
}

.add-btn {
  padding: 4px 10px;
  font-size: 12px;
  height: 24px;
}

.favorites-empty {
  padding: 14px 12px;
  border: 1px dashed var(--separator);
  border-radius: 8px;
  text-align: center;
  color: var(--tertiary);
  font-size: 12px;
  line-height: 1.6;
}

.favorites-empty p {
  margin: 0;
}

.favorites-empty .empty-hint {
  margin-top: 4px;
  font-size: 11px;
  color: var(--tertiary);
}

.favorites-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 260px;
  overflow-y: auto;
}

.favorite-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 8px;
  background: transparent;
  border: 1px solid transparent;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.favorite-item:hover {
  background: var(--control);
}

.favorite-item.active {
  background: var(--controlHover, var(--control));
  border-color: var(--accent);
}

.favorite-radio {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  border: 1.5px solid var(--separator);
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: border-color 0.15s;
}

.favorite-item.active .favorite-radio {
  border-color: var(--accent);
}

.radio-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
}

.favorite-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.favorite-label {
  font-size: 13px;
  color: var(--label);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.favorite-item.active .favorite-label {
  color: var(--accent);
}

.favorite-path {
  font-size: 11px;
  color: var(--tertiary);
  font-family: var(--mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.favorite-remove {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: var(--tertiary);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s, color 0.15s;
}

.favorite-remove:hover {
  background: var(--controlHover, var(--separator));
  color: var(--label);
}

/* 添加目录 Dialog 表单 */
.add-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.add-form-label {
  font-size: 12px;
  color: var(--secondary);
}

.add-form-hint {
  font-size: 11px;
  color: var(--tertiary);
  line-height: 1.5;
  margin: 4px 0 0 0;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  padding: 9px 16px;
  transition: background 0.15s, box-shadow 0.15s, opacity 0.15s;
  font-family: inherit;
}

.btn-primary {
  background: var(--accent);
  color: #fff;
}

.btn-primary:hover:not(:disabled) {
  background: var(--accentHover);
}

.btn-primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.btn-ghost {
  background: var(--control);
  color: var(--secondary);
}

.btn-ghost:hover:not(:disabled) {
  background: var(--controlHover);
}

.btn-ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.launch-btn {
  padding: 8px 16px;
}

.spin {
  animation: luoshen-spin 0.8s linear infinite;
}

@keyframes luoshen-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
