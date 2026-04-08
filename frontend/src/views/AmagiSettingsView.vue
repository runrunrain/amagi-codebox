<template>
  <div class="amagi-settings-page">
    <div class="page-header">
      <div class="header-left">
        <div class="breadcrumb" v-if="currentView === 'detail'">
          <span class="breadcrumb-link" @click="navigateBack">AmagiCode 设置</span>
          <span class="breadcrumb-sep">/</span>
          <span class="breadcrumb-current">{{ selectedGroupName }}</span>
        </div>
        <h1 class="page-title" v-else>AmagiCode 设置</h1>
        <p class="page-description">管理 AmagiCode 模型预设配置，对应 settings_amagi.json</p>
      </div>
      <div class="header-actions">
        <button v-if="currentView === 'detail'" class="btn secondary" @click="navigateBack">
          <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"></polyline></svg>
          返回
        </button>
        <button v-if="currentView === 'list'" class="btn primary" @click="openCreateGroupDialog">
          <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>
          新建 ModelPreset
        </button>
      </div>
    </div>

    <div class="view-tabs">
      <button
        v-for="tab in viewTabs"
        :key="tab.key"
        :class="['view-tab', { active: activeView === tab.key }]"
        @click="activeView = tab.key"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- Form View -->
    <div v-if="activeView === 'form'" class="form-view">

      <!-- === Level 1: Group List === -->
      <template v-if="currentView === 'list'">
        <!-- Active Group Selector -->
        <div class="card">
          <div class="card-header">
            <h2>激活组 (model)</h2>
            <button class="btn secondary small" @click="saveActiveGroup" :disabled="loading">
              {{ loading ? '保存中...' : '保存' }}
            </button>
          </div>
          <div class="card-body">
            <div class="form-group">
              <div class="default-preset-row">
                <select v-model="activeGroupName" class="input-field default-preset-select">
                  <option value="">未指定</option>
                  <option v-for="(_, name) in groups" :key="name" :value="name">
                    {{ name }}
                  </option>
                </select>
                <span class="hint-text">启动 AmagiCode 时使用的 ModelPreset 组名</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Group Cards Grid -->
        <div v-if="groupEntries.length === 0" class="empty-state-card">
          <p class="muted">暂无 ModelPreset 组，点击"新建 ModelPreset"按钮创建。</p>
        </div>
        <div v-else class="preset-group-grid">
          <div
            v-for="[name, group] in groupEntries"
            :key="name"
            class="preset-group-card"
            :class="{ active: activeGroupName === name }"
            @click="navigateToGroup(name)"
          >
            <div class="card-top-row">
              <h3 class="group-name">{{ name }}</h3>
              <div class="card-actions" @click.stop>
                <button
                  class="btn-icon"
                  :class="{ 'is-active': activeGroupName === name }"
                  @click="setActiveGroup(name)"
                  title="设为激活"
                >
                  <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"></polygon></svg>
                </button>
                <button class="btn-icon danger" @click="deleteGroup(name)" title="删除组">
                  <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                </button>
              </div>
            </div>
            <p class="group-description" v-if="group.description">{{ group.description }}</p>
            <div class="group-meta">
              <span class="meta-badge">{{ getPresetCount(group) }} 个小预设</span>
              <span class="meta-badge default-meta" v-if="group.default_preset">
                默认: {{ group.default_preset }}
              </span>
            </div>
            <div class="group-preview" v-if="getPresetCount(group) > 0">
              <span
                v-for="item in getPresetPreview(group)"
                :key="item"
                class="preview-chip"
              >{{ item }}</span>
            </div>
            <div class="active-indicator" v-if="activeGroupName === name">
              当前激活
            </div>
          </div>
        </div>

        <!-- Available Models Preview -->
        <div class="card">
          <div class="card-header collapsible-header" @click="showPreview = !showPreview">
            <h2>预览: availableModels</h2>
            <svg class="expand-icon" :class="{ expanded: showPreview }" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
          </div>
          <div class="card-body" v-if="showPreview">
            <div class="models-preview">
              <span v-for="m in generateAvailableModels()" :key="m" class="model-chip">{{ m }}</span>
              <p v-if="generateAvailableModels().length === 0" class="muted">添加小预设后自动生成</p>
            </div>
            <span class="hint-text">保存时自动从小预设生成，包含预设名称和 provider/model 格式。</span>
            <div class="preview-actions">
              <button class="btn secondary small" @click="syncAvailableModels" :disabled="loading">
                {{ loading ? '同步中...' : '同步到后端' }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <!-- === Level 2: Group Detail === -->
      <template v-if="currentView === 'detail' && selectedGroup">
        <!-- Group Info Card -->
        <div class="card">
          <div class="card-header">
            <h2>组信息</h2>
            <button class="btn secondary small" @click="openEditGroupDialog" :disabled="loading">
              编辑
            </button>
          </div>
          <div class="card-body">
            <div class="group-detail-grid">
              <div class="detail-field">
                <span class="detail-label">组名</span>
                <span class="detail-value mono">{{ selectedGroupName }}</span>
              </div>
              <div class="detail-field">
                <span class="detail-label">描述</span>
                <span class="detail-value">{{ selectedGroup.description || '(无)' }}</span>
              </div>
              <div class="detail-field">
                <span class="detail-label">默认小预设</span>
                <div class="default-preset-inline">
                  <select
                    v-model="detailDefaultPreset"
                    class="input-field inline-select"
                    @change="updateGroupDefaultPreset"
                  >
                    <option value="">未指定</option>
                    <option
                      v-for="(_, pName) in (selectedGroup.presets || {})"
                      :key="pName"
                      :value="pName"
                    >
                      {{ pName }}
                    </option>
                  </select>
                </div>
              </div>
              <div class="detail-field">
                <span class="detail-label">激活状态</span>
                <span class="detail-value" :class="{ 'text-active': activeGroupName === selectedGroupName }">
                  {{ activeGroupName === selectedGroupName ? '当前激活' : '未激活' }}
                </span>
              </div>
            </div>
          </div>
        </div>

        <!-- Sub-Presets List -->
        <div class="card">
          <div class="card-header">
            <h2>小预设列表</h2>
            <button class="btn secondary" @click="openAddSubPresetDialog">
              <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>
              添加小预设
            </button>
          </div>
          <div class="card-body">
            <div class="section-label">
              <span>小预设</span>
              <span class="count-badge">{{ Object.keys(selectedGroup.presets || {}).length }} 个</span>
            </div>

            <div v-if="Object.keys(selectedGroup.presets || {}).length === 0" class="empty-state">
              <p class="muted">暂无小预设，点击"添加小预设"按钮创建。每个小预设可指定独立的提供商、模型、思考模式和努力级别。</p>
            </div>

            <div class="preset-list">
              <div
                v-for="(preset, presetName) in (selectedGroup.presets || {})"
                :key="presetName"
                class="preset-row"
                :class="{ 'is-default': selectedGroup.default_preset === presetName }"
              >
                <div class="preset-row-main">
                  <div class="preset-name-col">
                    <span class="preset-name">{{ presetName }}</span>
                    <span v-if="selectedGroup.default_preset === presetName" class="default-badge">默认</span>
                  </div>
                  <div class="preset-info-col">
                    <span class="info-tag provider-tag">{{ preset.provider || '--' }}</span>
                    <span class="info-tag model-tag">{{ preset.model || '--' }}</span>
                    <span class="info-tag" v-if="preset.temperature !== undefined">Temp: {{ preset.temperature }}</span>
                    <span class="info-tag" v-if="preset.effort_level">Effort: {{ preset.effort_level }}</span>
                    <span class="info-tag thinking-tag" v-if="preset.thinking?.type === 'enabled'">Thinking</span>
                  </div>
                  <div class="preset-actions-col">
                    <button class="btn-icon" @click="setSubPresetDefault(presetName as string)" title="设为默认">
                      <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"></polygon></svg>
                    </button>
                    <button class="btn-icon" @click="openEditSubPresetDialog(presetName as string, preset)" title="编辑">
                      <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                    </button>
                    <button class="btn-icon danger" @click="deleteSubPreset(presetName as string)" title="删除">
                      <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>

    <!-- JSON View -->
    <div v-else class="json-view">
      <div class="card">
        <div class="card-header">
          <h2>JSON 编辑器</h2>
        </div>
        <div class="card-body">
          <div class="json-editor-wrapper">
            <textarea
              v-model="jsonContent"
              class="json-textarea"
              spellcheck="false"
              placeholder="加载中..."
            ></textarea>
            <div class="json-status" :class="{ error: !!jsonError, success: !jsonError && jsonContent }">
              <span v-if="!jsonContent"></span>
              <span v-else-if="jsonError">语法错误: {{ jsonError }}</span>
              <span v-else>JSON 语法正确</span>
            </div>
          </div>
          <div v-if="jsonWarning" class="json-warning">
            <span class="warning-icon">!</span>
            <span>{{ jsonWarning }}</span>
          </div>
          <div class="json-actions">
            <button class="btn secondary" @click="loadJsonData">重新加载</button>
            <button class="btn primary" @click="saveJsonData" :disabled="!!jsonError || !jsonContent || loading">
              {{ loading ? '保存中...' : '保存 JSON' }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Create/Edit Group Dialog -->
    <div class="dialog-overlay" v-if="showGroupDialog" @click.self="showGroupDialog = false">
      <div class="dialog card group-dialog">
        <h2>{{ isEditingGroup ? '编辑组信息' : '新建 ModelPreset' }}</h2>
        <div class="dialog-scroll-area">
          <div class="form-group">
            <label>组名</label>
            <input type="text" v-model="editingGroupName" class="input-field" placeholder="例如: opus-thinking, fast-model" />
          </div>
          <div class="form-group">
            <label>描述 (可选)</label>
            <input type="text" v-model="editingGroupDescription" class="input-field" placeholder="例如: 高质量深度思考配置" />
          </div>
        </div>
        <div class="dialog-actions">
          <button class="btn secondary" @click="showGroupDialog = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="saveGroup" :disabled="!editingGroupName || loading">
            {{ loading ? '保存中...' : (isEditingGroup ? '更新' : '创建') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Sub-Preset Dialog -->
    <div class="dialog-overlay" v-if="showPresetDialog" @click.self="showPresetDialog = false">
      <div class="dialog card preset-dialog">
        <h2>{{ isEditingPreset ? '编辑小预设' : '添加小预设' }}</h2>

        <div class="dialog-scroll-area">
          <div class="form-group" v-if="!isEditingPreset">
            <label>预设名称</label>
            <input type="text" v-model="editingPresetName" class="input-field" placeholder="例如: master, explorer, coding" />
          </div>

          <div class="form-group">
            <label>提供商 (provider)</label>
            <select v-model="editingPreset.provider" class="input-field">
              <option value="">-- 选择已配置的提供商 --</option>
              <option v-for="(_, key) in configProviders" :key="key" :value="key">
                {{ key }}
              </option>
            </select>
            <span class="hint-text">从 config.json 中已配置的服务提供商选择</span>
          </div>

          <div class="form-group">
            <label>模型 (model)</label>
            <input type="text" v-model="editingPreset.model" class="input-field" placeholder="例如: gpt-5.4, glm-5.1" />
          </div>

          <div class="form-grid-2">
            <div class="form-group">
              <label>Temperature</label>
              <input type="number" v-model.number="editingPreset.temperature" class="input-field" step="0.1" min="0" max="2" placeholder="0.7" />
            </div>
            <div class="form-group">
              <label>Max Tokens</label>
              <input type="number" v-model.number="editingPreset.maxTokens" class="input-field" step="1" min="1" placeholder="4096" />
            </div>
          </div>

          <div class="section-subtitle">思考模式 (thinking)</div>
          <div class="form-group">
            <label>思考模式</label>
            <select v-model="editingThinkingType" class="input-field">
              <option value="">不配置</option>
              <option value="disabled">禁用 (Disabled)</option>
              <option value="enabled">启用 (Enabled)</option>
            </select>
          </div>

          <div class="form-group" v-if="editingThinkingType === 'enabled'">
            <label>思考预算 Tokens (budget_tokens)</label>
            <input type="number" v-model.number="editingThinkingBudget" class="input-field" step="1024" min="1024" placeholder="留空则自动" />
          </div>

          <div class="section-subtitle">努力级别 (effort_level)</div>
          <div class="form-group">
            <select v-model="editingPreset.effortLevel" class="input-field">
              <option value="">不配置</option>
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="xhigh">XHigh</option>
              <option value="max">Max</option>
            </select>
          </div>
        </div>

        <div class="dialog-actions">
          <button class="btn secondary" @click="showPresetDialog = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="saveSubPreset" :disabled="(!isEditingPreset && !editingPresetName) || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed, watch, onMounted } from 'vue'
import { useToast } from '../composables/useToast'
import {
  GetAmagiSettings,
  SaveAmagiModelPreset,
  DeleteAmagiModelPreset,
  RenameAmagiModelPreset,
  SaveAmagiSubPreset,
  DeleteAmagiSubPreset,
  GetAmagiSettingsJSON,
  SaveAmagiSettingsJSON,
  SetAmagiModel,
  SetAmagiAvailableModels,
} from '../../wailsjs/go/main/App'
import { amagi } from '../../wailsjs/go/models'
import { GetProviders } from '../../wailsjs/go/config/ConfigService'

const { showSuccess, showError } = useToast()
const loading = ref(false)

// Config.json providers (for provider selection in preset dialog)
const configProviders = ref<Record<string, any>>({})

// Preview toggle
const showPreview = ref(false)

// View tabs
const viewTabs = [
  { key: 'form', label: '表单视图' },
  { key: 'json', label: 'JSON 视图' },
]
const activeView = ref('form')

// --- Two-level navigation state ---
const currentView = ref<'list' | 'detail'>('list')
const selectedGroupName = ref('')
const activeGroupName = ref('')

// Core data: groups map
const groups = ref<Record<string, amagi.ModelPresetGroup>>({})

const groupEntries = computed(() => Object.entries(groups.value))

const selectedGroup = computed(() => {
  if (!selectedGroupName.value) return null
  return groups.value[selectedGroupName.value] || null
})

// Detail view: track default preset for the selected group
const detailDefaultPreset = ref('')

watch(selectedGroup, (g) => {
  detailDefaultPreset.value = g?.default_preset || ''
})

// Navigation
function navigateToGroup(name: string) {
  selectedGroupName.value = name
  currentView.value = 'detail'
}

function navigateBack() {
  currentView.value = 'list'
  selectedGroupName.value = ''
}

// --- Group Dialog ---
const showGroupDialog = ref(false)
const isEditingGroup = ref(false)
const editingGroupName = ref('')
const editingGroupDescription = ref('')

function openCreateGroupDialog() {
  isEditingGroup.value = false
  editingGroupName.value = ''
  editingGroupDescription.value = ''
  showGroupDialog.value = true
}

function openEditGroupDialog() {
  if (!selectedGroup.value) return
  isEditingGroup.value = true
  editingGroupName.value = selectedGroupName.value
  editingGroupDescription.value = selectedGroup.value.description || ''
  showGroupDialog.value = true
}

async function saveGroup() {
  const newName = editingGroupName.value.trim()
  if (!newName) {
    showError('请输入组名')
    return
  }

  try {
    loading.value = true

    if (isEditingGroup.value) {
      const oldName = selectedGroupName.value
      const needsRename = oldName !== newName

      if (needsRename) {
        if (groups.value[newName]) {
          showError('组名已存在: ' + newName)
          loading.value = false
          return
        }
        await RenameAmagiModelPreset(oldName, newName)
        groups.value[newName] = groups.value[oldName]
        delete groups.value[oldName]

        if (activeGroupName.value === oldName) {
          activeGroupName.value = newName
        }
      }

      const existing = groups.value[newName]
      const updated = new amagi.ModelPresetGroup({
        description: editingGroupDescription.value || undefined,
        default_preset: existing?.default_preset || undefined,
        presets: existing?.presets || {},
      })
      await SaveAmagiModelPreset(newName, updated)
      groups.value[newName] = updated

      if (needsRename) {
        selectedGroupName.value = newName
      }
    } else {
      if (groups.value[newName]) {
        showError('组名已存在: ' + newName)
        loading.value = false
        return
      }
      const newGroup = new amagi.ModelPresetGroup({
        description: editingGroupDescription.value || undefined,
        presets: {},
      })
      await SaveAmagiModelPreset(newName, newGroup)
      groups.value[newName] = newGroup
    }

    showGroupDialog.value = false
    showSuccess(isEditingGroup.value ? '组信息已更新' : '组已创建')

    if (!isEditingGroup.value) {
      navigateToGroup(newName)
    }
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function deleteGroup(name: string) {
  if (!confirm(`确定要删除 ModelPreset 组 "${name}" 及其所有小预设吗？`)) return
  try {
    loading.value = true
    await DeleteAmagiModelPreset(name)
    delete groups.value[name]
    if (activeGroupName.value === name) {
      activeGroupName.value = ''
    }
    if (selectedGroupName.value === name) {
      navigateBack()
    }
    showSuccess('组已删除: ' + name)
  } catch (err) {
    showError('删除失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function setActiveGroup(name: string) {
  try {
    loading.value = true
    await SetAmagiModel(name)
    activeGroupName.value = name
    showSuccess('已激活: ' + name)
  } catch (err) {
    showError('设置激活组失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function saveActiveGroup() {
  try {
    loading.value = true
    await SetAmagiModel(activeGroupName.value)
    const models = generateAvailableModels()
    await SetAmagiAvailableModels(models)
    showSuccess('激活组已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function updateGroupDefaultPreset() {
  if (!selectedGroupName.value || !selectedGroup.value) return
  try {
    loading.value = true
    const updated = new amagi.ModelPresetGroup({
      description: selectedGroup.value.description || undefined,
      default_preset: detailDefaultPreset.value || undefined,
      presets: selectedGroup.value.presets || {},
    })
    await SaveAmagiModelPreset(selectedGroupName.value, updated)
    groups.value[selectedGroupName.value] = updated
    showSuccess('默认小预设已更新')
  } catch (err) {
    showError('更新失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Sub-Preset Dialog ---
interface LocalPresetForm {
  provider: string
  model: string
  temperature?: number
  maxTokens?: number
  effortLevel: string
}

const showPresetDialog = ref(false)
const isEditingPreset = ref(false)
const editingPresetName = ref('')
const editingPresetOriginalName = ref('')
const editingPreset = ref<LocalPresetForm>({ provider: '', model: '', effortLevel: '' })
const editingThinkingType = ref('')
const editingThinkingBudget = ref<number | undefined>(undefined)

function openAddSubPresetDialog() {
  isEditingPreset.value = false
  editingPresetName.value = ''
  editingPresetOriginalName.value = ''
  editingPreset.value = { provider: '', model: '', effortLevel: '' }
  editingThinkingType.value = ''
  editingThinkingBudget.value = undefined
  showPresetDialog.value = true
}

function openEditSubPresetDialog(name: string, preset: amagi.AmagiModelPreset) {
  isEditingPreset.value = true
  editingPresetName.value = name
  editingPresetOriginalName.value = name
  editingPreset.value = {
    provider: preset.provider || '',
    model: preset.model || '',
    temperature: preset.temperature,
    maxTokens: preset.max_tokens,
    effortLevel: preset.effort_level || '',
  }
  editingThinkingType.value = preset.thinking?.type || ''
  editingThinkingBudget.value = preset.thinking?.budget_tokens
  showPresetDialog.value = true
}

function buildBackendPreset(): amagi.AmagiModelPreset {
  const form = editingPreset.value
  const thinkingObj = editingThinkingType.value
    ? new amagi.AmagiThinking({
        type: editingThinkingType.value,
        budget_tokens: editingThinkingType.value === 'enabled' ? editingThinkingBudget.value : undefined,
      })
    : undefined

  return new amagi.AmagiModelPreset({
    provider: form.provider || '',
    model: form.model || '',
    temperature: form.temperature,
    max_tokens: form.maxTokens,
    effort_level: form.effortLevel || '',
    thinking: thinkingObj,
  })
}

async function saveSubPreset() {
  const groupName = selectedGroupName.value
  if (!groupName) return

  const presetName = isEditingPreset.value ? editingPresetOriginalName.value : editingPresetName.value.trim()
  if (!presetName) {
    showError('请输入预设名称')
    return
  }

  try {
    loading.value = true
    const backendPreset = buildBackendPreset()
    await SaveAmagiSubPreset(groupName, presetName, backendPreset)

    const group = groups.value[groupName]
    if (group) {
      if (!group.presets) {
        group.presets = {}
      }
      group.presets[presetName] = backendPreset
    }

    showPresetDialog.value = false
    showSuccess(isEditingPreset.value ? '小预设已更新' : '小预设已添加')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function deleteSubPreset(presetName: string) {
  const groupName = selectedGroupName.value
  if (!groupName) return
  if (!confirm(`确定要删除小预设 "${presetName}" 吗？`)) return

  try {
    loading.value = true
    await DeleteAmagiSubPreset(groupName, presetName)

    const group = groups.value[groupName]
    if (group?.presets) {
      delete group.presets[presetName]
      if (group.default_preset === presetName) {
        group.default_preset = ''
      }
    }

    showSuccess('小预设已删除')
  } catch (err) {
    showError('删除失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function setSubPresetDefault(presetName: string) {
  if (!selectedGroupName.value || !selectedGroup.value) return
  detailDefaultPreset.value = presetName
  await updateGroupDefaultPreset()
}

// --- Helper functions ---
function getPresetCount(group: amagi.ModelPresetGroup): number {
  return Object.keys(group.presets || {}).length
}

function getPresetPreview(group: amagi.ModelPresetGroup): string[] {
  const entries = Object.entries(group.presets || {})
  return entries.slice(0, 3).map(([_, p]) => {
    if (p.provider && p.model) return `${p.provider}/${p.model}`
    return p.model || p.provider || '--'
  })
}

function generateAvailableModels(): string[] {
  const models = new Set<string>()
  for (const [_, group] of Object.entries(groups.value)) {
    for (const [presetName, preset] of Object.entries(group.presets || {})) {
      models.add(presetName)
      if (preset.provider && preset.model) {
        models.add(`${preset.provider}/${preset.model}`)
      }
    }
  }
  return Array.from(models)
}

async function syncAvailableModels() {
  try {
    loading.value = true
    const models = generateAvailableModels()
    await SetAmagiAvailableModels(models)
    showSuccess('availableModels 已同步')
  } catch (err) {
    showError('同步失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- JSON Editor ---
const jsonContent = ref('')
const jsonError = ref('')
const jsonWarning = ref('')

const whitelistFields = new Set([
  'model', 'providers', 'available_models', 'model_overrides',
  'model_capability_overrides', 'model_presets', 'always_thinking_enabled',
  'effort_level', 'advisor_model',
])

function validateJson() {
  if (!jsonContent.value.trim()) {
    jsonError.value = ''
    jsonWarning.value = ''
    return
  }
  try {
    const parsed = JSON.parse(jsonContent.value)
    jsonError.value = ''
    const fields = Object.keys(parsed)
    const nonWhitelist = fields.filter((f) => !whitelistFields.has(f))
    if (nonWhitelist.length > 0) {
      jsonWarning.value = `检测到非白名单字段: ${nonWhitelist.join(', ')}。这些字段将被忽略。`
    } else {
      jsonWarning.value = ''
    }
  } catch (err: any) {
    jsonError.value = err.message || '无效的 JSON'
    jsonWarning.value = ''
  }
}

async function loadJsonData() {
  try {
    jsonContent.value = await GetAmagiSettingsJSON()
    validateJson()
  } catch (err) {
    showError('加载失败: ' + err)
  }
}

async function saveJsonData() {
  validateJson()
  if (jsonError.value) return
  try {
    loading.value = true
    await SaveAmagiSettingsJSON(jsonContent.value)
    await loadSettings()
    showSuccess('JSON 已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Load settings ---
async function loadConfigProviders() {
  try {
    const providers = await GetProviders()
    configProviders.value = providers || {}
  } catch (_) {
    // non-critical
  }
}

async function loadSettings() {
  try {
    const data = await GetAmagiSettings()
    activeGroupName.value = data.model || ''
    groups.value = data.model_presets || {}
  } catch (err) {
    showError('加载设置失败: ' + err)
  }
}

watch(jsonContent, () => { validateJson() })
watch(activeView, (v) => { if (v === 'json') loadJsonData() })

onMounted(async () => {
  await Promise.all([loadSettings(), loadConfigProviders()])
})
</script>

<style scoped>
.amagi-settings-page {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
}

.header-left {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: #e0e6ed;
}

.page-description {
  margin: 0;
  font-size: 14px;
  color: #8899aa;
}

.header-actions {
  display: flex;
  gap: 12px;
}

/* Breadcrumb */
.breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 20px;
  font-weight: 600;
  color: #e0e6ed;
}

.breadcrumb-link {
  color: #5b8af0;
  cursor: pointer;
  transition: color 0.15s ease;
}

.breadcrumb-link:hover {
  color: #7da4f6;
}

.breadcrumb-sep {
  color: #5a6a7a;
  font-weight: 400;
}

.breadcrumb-current {
  color: #e0e6ed;
  font-family: 'Consolas', 'Monaco', monospace;
}

/* View Tabs */
.view-tabs {
  display: flex;
  gap: 8px;
  border-bottom: 1px solid #2a2f3e;
  padding-bottom: 0;
  margin-bottom: 24px;
}

.view-tab {
  padding: 10px 20px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: #8899aa;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: inherit;
}

.view-tab:hover {
  color: #ccd6e0;
  background: rgba(255, 255, 255, 0.03);
}

.view-tab.active {
  color: #5b8af0;
  border-bottom-color: #5b8af0;
}

.form-view {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

/* Card */
.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.card-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* Forms */
.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  color: #8899aa;
  font-size: 14px;
  font-weight: 500;
}

.input-field {
  width: 100%;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #e0e6ed;
  padding: 10px 12px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  transition: all 0.15s ease;
  outline: none;
  box-sizing: border-box;
}

.input-field:focus {
  border-color: #5b8af0;
}

.hint-text {
  font-size: 12px;
  color: #5a6a7a;
}

.default-preset-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.default-preset-select {
  max-width: 300px;
}

/* === Preset Group Grid (Level 1) === */
.preset-group-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

.preset-group-card {
  background: #232838;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 20px;
  cursor: pointer;
  transition: all 0.15s ease;
  position: relative;
}

.preset-group-card:hover {
  border-color: #3a4058;
  transform: translateY(-2px);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.2);
}

.preset-group-card.active {
  border-color: #5b8af0;
  box-shadow: 0 0 0 1px #5b8af0;
}

.card-top-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
}

.group-name {
  margin: 0;
  font-size: 16px;
  font-weight: 700;
  color: #e0e6ed;
  font-family: 'Consolas', 'Monaco', monospace;
}

.card-actions {
  display: flex;
  gap: 4px;
}

.group-description {
  margin: 0 0 10px 0;
  font-size: 13px;
  color: #8899aa;
  line-height: 1.4;
}

.group-meta {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.meta-badge {
  font-size: 12px;
  padding: 3px 10px;
  border-radius: 10px;
  background: rgba(91, 138, 240, 0.1);
  color: #5b8af0;
  font-weight: 600;
}

.meta-badge.default-meta {
  background: rgba(102, 187, 106, 0.1);
  color: #66bb6a;
}

.group-preview {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}

.preview-chip {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #8899aa;
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-family: 'Consolas', 'Monaco', monospace;
}

.active-indicator {
  display: inline-block;
  margin-top: 4px;
  font-size: 11px;
  font-weight: 700;
  color: #5b8af0;
  letter-spacing: 0.5px;
  text-transform: uppercase;
}

.empty-state-card {
  text-align: center;
  padding: 48px 32px;
  background: #1a1f2e;
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
}

/* === Level 2: Group Detail === */
.group-detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.detail-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.detail-label {
  font-size: 12px;
  font-weight: 600;
  color: #5a6a7a;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.detail-value {
  font-size: 14px;
  color: #e0e6ed;
}

.detail-value.mono {
  font-family: 'Consolas', 'Monaco', monospace;
}

.text-active {
  color: #5b8af0;
  font-weight: 600;
}

.default-preset-inline {
  display: flex;
}

.inline-select {
  max-width: 200px;
}

/* Sub-Presets Section */
.section-label {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  font-size: 14px;
  color: #8899aa;
  font-weight: 500;
}

.count-badge {
  background: rgba(91, 138, 240, 0.12);
  color: #5b8af0;
  padding: 2px 10px;
  border-radius: 10px;
  font-size: 12px;
  font-weight: 600;
}

.empty-state {
  text-align: center;
  padding: 32px;
  background: #0f1219;
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
}

.muted {
  color: #5a6a7a;
  font-size: 14px;
  margin: 0;
}

/* Preset List */
.preset-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.preset-row {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 12px 16px;
  transition: all 0.15s ease;
}

.preset-row:hover {
  border-color: #3a4f5e;
  background: #141822;
}

.preset-row.is-default {
  border-color: rgba(91, 138, 240, 0.35);
  background: rgba(91, 138, 240, 0.03);
}

.preset-row-main {
  display: flex;
  align-items: center;
  gap: 16px;
}

.preset-name-col {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 120px;
}

.preset-name {
  font-weight: 600;
  font-size: 14px;
  color: #e0e6ed;
  font-family: 'Consolas', 'Monaco', monospace;
}

.default-badge {
  font-size: 10px;
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(91, 138, 240, 0.15);
  color: #5b8af0;
  font-weight: 600;
  letter-spacing: 0.5px;
}

.preset-info-col {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  flex-wrap: wrap;
}

.info-tag {
  background: rgba(90, 106, 122, 0.2);
  color: #8899aa;
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 12px;
  border: 1px solid #2a2f3e;
  font-family: 'Consolas', 'Monaco', monospace;
}

.info-tag.provider-tag {
  border-color: rgba(16, 163, 127, 0.3);
  color: #10a37f;
  background: rgba(16, 163, 127, 0.08);
}

.info-tag.model-tag {
  border-color: rgba(91, 138, 240, 0.3);
  color: #5b8af0;
  background: rgba(91, 138, 240, 0.08);
}

.info-tag.thinking-tag {
  border-color: rgba(255, 183, 77, 0.3);
  color: #ffb74d;
  background: rgba(255, 183, 77, 0.08);
}

.preset-actions-col {
  display: flex;
  gap: 4px;
}

/* Available Models Preview */
.models-preview {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}

.model-chip {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #8899aa;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 12px;
  font-family: 'Consolas', 'Monaco', monospace;
}

.preview-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 4px;
}

.collapsible-header {
  cursor: pointer;
  user-select: none;
}

.collapsible-header:hover {
  opacity: 0.85;
}

.expand-icon {
  transition: transform 0.2s ease;
  color: #8899aa;
}

.expand-icon.expanded {
  transform: rotate(180deg);
}

/* JSON View */
.json-view {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.json-editor-wrapper {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.json-textarea {
  width: 100%;
  min-height: 400px;
  max-height: 55vh;
  background: #0a0d14;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #c9e0f0;
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.6;
  padding: 12px;
  resize: vertical;
  outline: none;
  box-sizing: border-box;
  transition: border-color 0.15s ease;
  tab-size: 2;
}

.json-textarea:focus {
  border-color: #5b8af0;
}

.json-status {
  font-size: 13px;
  padding: 4px 0;
  min-height: 20px;
  color: #5a6a7a;
}

.json-status.success {
  color: #66bb6a;
}

.json-status.error {
  color: #ef5350;
}

.json-warning {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  background: rgba(255, 167, 38, 0.1);
  border: 1px solid rgba(255, 167, 38, 0.3);
  border-radius: 6px;
  color: #ffb74d;
  font-size: 13px;
}

.json-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
}

/* Dialog */
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(15, 18, 25, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  backdrop-filter: blur(4px);
}

.dialog {
  width: 100%;
  max-width: 520px;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.group-dialog {
  max-width: 440px;
}

.preset-dialog,
.group-dialog {
  padding: 0;
}

.preset-dialog h2,
.group-dialog h2 {
  margin: 0;
  padding: 20px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
}

.dialog-scroll-area {
  padding: 20px;
  overflow-y: auto;
  flex: 1;
}

.form-grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.section-subtitle {
  margin: 20px 0 12px 0;
  font-size: 14px;
  color: #5b8af0;
  border-bottom: 1px dashed #2a2f3e;
  padding-bottom: 6px;
}

.dialog-actions {
  padding: 20px;
  border-top: 1px solid #2a2f3e;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

/* Buttons */
.btn {
  padding: 10px 20px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  border: none;
  outline: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn.small {
  padding: 6px 14px;
  font-size: 13px;
}

.btn.primary {
  background: #5b8af0;
  color: #0f1219;
}

.btn.primary:hover:not(:disabled) {
  background: #7da4f6;
}

.btn.secondary {
  background: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn.secondary:hover:not(:disabled) {
  border-color: #5a6a7a;
  background: rgba(255, 255, 255, 0.05);
}

.btn-icon {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s ease;
  color: #8899aa;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #e0e6ed;
}

.btn-icon.is-active {
  color: #5b8af0;
}

.btn-icon.danger:hover {
  background: rgba(239, 83, 80, 0.1);
  color: #ef5350;
}
</style>
