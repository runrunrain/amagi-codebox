<template>
  <section class="view-rules">
    <!-- Initial loading state -->
    <LoadingState v-if="initialLoading" message="加载规则配置中..." />

    <!-- Initial error state -->
    <ErrorState
      v-else-if="initialError"
      :message="initialError"
      :on-retry="handleRetry"
    />

    <!-- Main content -->
    <template v-else>
    <PageHead title="注入规则" description="管理 API 注入规则与代理状态" />

    <!-- Proxy Control Card -->
    <ConfigCard class="proxy-card">
      <div class="proxy-header">
        <div class="status-indicator">
          <span :class="['dot', isRunning ? 'dot-running' : 'dot-stopped']"></span>
          <span class="status-text">{{ isRunning ? '运行中' : '已停止' }}</span>
        </div>
        <div class="stats">
          <span class="stat-label">规则数量:</span>
          <span class="stat-value">{{ ruleCount }}</span>
        </div>
      </div>

      <div class="proxy-controls">
        <div class="control-group">
          <label>本地端口</label>
          <TextInput
            :model-value="String(port)"
            type="number"
            placeholder="5280"
            :disabled="isRunning"
            @update:model-value="port = Number($event)"
          />
        </div>
        <div class="control-group flex-1">
          <label>目标后端 URL</label>
          <div class="backend-url-group">
            <select
              v-model="backendURL"
              class="url-select"
              :disabled="isRunning"
            >
              <option value="" disabled>选择或输入 URL</option>
              <option v-for="url in backendURLHistory" :key="url" :value="url">
                {{ url }}
              </option>
            </select>
            <button
              class="btn btn-ghost btn-sm"
              :disabled="isRunning || loading || !backendURL?.trim()"
              @click="handleSaveBackendURL"
            >
              保存
            </button>
          </div>
        </div>
        <div class="control-actions">
          <AppButton
            v-if="!isRunning"
            variant="primary"
            :disabled="loading"
            @click="startProxy"
          >
            {{ loading ? '启动中...' : '启动代理' }}
          </AppButton>
          <AppButton
            v-else
            variant="danger"
            :disabled="loading"
            @click="stopProxy"
          >
            {{ loading ? '停止中...' : '停止代理' }}
          </AppButton>
        </div>
      </div>
    </ConfigCard>

    <!-- Rules List -->
    <ConfigCard>
      <div class="card-head">
        <h2>注入规则</h2>
        <AppButton variant="primary" size="small" @click="openAddModal">
          新建规则
        </AppButton>
      </div>

      <div v-if="rules.length === 0" class="empty-container">
        <EmptyState icon="" title="暂无规则" description="点击上方按钮创建第一条注入规则" />
      </div>

      <div v-else class="rules-list">
        <div v-for="rule in rules" :key="rule.id" class="rule-row">
          <div class="rule-main">
            <div class="rule-title">
              <h3>{{ rule.name }}</h3>
              <Badge type="source" :text="'优先级 ' + rule.priority" />
            </div>
            <div class="rule-keywords" v-if="rule.keywords?.length">
              <span v-for="kw in rule.keywords" :key="kw" class="keyword-chip">{{ kw }}</span>
            </div>
            <div class="rule-prompt">{{ rule.prompt }}</div>
          </div>
          <div class="rule-actions">
            <Switch :model-value="rule.enabled" @update:model-value="toggleRuleEnabled(rule)" />
            <AppButton size="small" @click="editRule(rule)">编辑</AppButton>
            <AppButton variant="danger" size="small" @click="deleteRule(rule.id)">删除</AppButton>
          </div>
        </div>
      </div>
    </ConfigCard>

    <!-- Injection Logs -->
    <ConfigCard>
      <div class="card-head">
        <h2>注入日志</h2>
        <AppButton variant="ghost" size="small" @click="fetchLogs">刷新</AppButton>
      </div>

      <div v-if="logs.length === 0" class="empty-container">
        <EmptyState icon="" title="暂无日志" description="代理启动后此处显示匹配记录" />
      </div>

      <div v-else class="inject-logs">
        <div v-for="(log, idx) in logs" :key="idx" class="inject-log">
          <span class="log-time">{{ log.time }}</span>
          <div class="log-rules">
            <Badge v-for="rn in log.ruleNames" :key="rn" type="source" :text="rn" />
          </div>
          <span :class="['log-status', log.status >= 400 ? 'status-error' : 'status-success']">
            {{ log.status }}
          </span>
          <span class="log-preview">{{ log.preview }}</span>
        </div>
      </div>
    </ConfigCard>

    <!-- Rule Dialog -->
    <RuleDialog
      v-model:open="showModal"
      :rule="currentRule"
      @success="fetchRules"
    />

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      v-model:open="showDeleteDialog"
      title="删除规则"
      message="确定要删除此规则吗？"
      danger
      confirm-text="删除"
      @confirm="confirmDeleteRule"
    />
    </template>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import Switch from '../components/ui/Switch.vue'
import TextInput from '../components/ui/TextInput.vue'
import AppButton from '../components/ui/AppButton.vue'
import Badge from '../components/ui/Badge.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import LoadingState from '../components/ui/LoadingState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import RuleDialog from '../components/rules/RuleDialog.vue'

import { useToast } from '../composables/useToast'
import { proxy } from '../../wailsjs/go/models'
import {
  GetRules,
  AddRule,
  UpdateRule,
  DeleteRule,
  Start,
  Stop,
  GetStatus,
  GetLogs,
} from '../../wailsjs/go/proxy/ProxyService'
import { GetProxyBackendURLHistory, AddProxyBackendURL } from '../../wailsjs/go/main/App'

const { showSuccess, showError } = useToast()

const loading = ref(false)
const isRunning = ref(false)

// Loading and error states for initial data
const initialLoading = ref(true)
const initialError = ref('')
const port = ref(5280)
const backendURL = ref('https://api.anthropic.com')
const backendURLHistory = ref<string[]>([])
const ruleCount = ref(0)

const rules = ref<proxy.InjectionRule[]>([])
const logs = ref<proxy.InjectionLog[]>([])

const showModal = ref(false)
const currentRule = ref<proxy.InjectionRule | null>(null)
const ruleToDelete = ref<string | null>(null)
const showDeleteDialog = ref(false)

let statusInterval: number | null = null

// Retry function for ErrorState
const handleRetry = async () => {
  initialLoading.value = true
  initialError.value = ''
  try {
    await Promise.all([
      fetchStatus(),
      fetchRules(),
      fetchLogs(),
      fetchBackendURLHistory()
    ])
  } catch (err) {
    initialError.value = String(err)
  } finally {
    initialLoading.value = false
  }
}

const fetchBackendURLHistory = async () => {
  try {
    const history = await GetProxyBackendURLHistory()
    backendURLHistory.value = history || []
  } catch (err) {
    console.error('Failed to fetch backend URL history:', err)
  }
}

const handleSaveBackendURL = async () => {
  const trimmedURL = backendURL.value.trim()
  if (!trimmedURL) {
    showError('请输入目标后端 URL')
    return
  }
  try {
    await AddProxyBackendURL(trimmedURL)
    await fetchBackendURLHistory()
    showSuccess('URL 已保存到历史记录')
  } catch (err) {
    console.error('Failed to save backend URL:', err)
    showError('保存失败: ' + err)
  }
}

const fetchStatus = async () => {
  try {
    const status = await GetStatus()
    if (status) {
      isRunning.value = status.running || false
      if (status.port) port.value = status.port
      if (status.backendURL) backendURL.value = status.backendURL
      ruleCount.value = status.ruleCount || 0
    }
  } catch (err) {
    throw err
  }
}

const fetchRules = async () => {
  try {
    const fetchedRules = await GetRules()
    rules.value = fetchedRules || []
  } catch (err) {
    throw err
  }
}

const fetchLogs = async () => {
  try {
    const fetchedLogs = await GetLogs()
    logs.value = fetchedLogs || []
  } catch (err) {
    throw err
  }
}

const startProxy = async () => {
  const trimmedURL = backendURL.value.trim()
  if (!trimmedURL) {
    showError('请输入目标后端 URL')
    return
  }
  loading.value = true
  try {
    await AddProxyBackendURL(trimmedURL)
    await fetchBackendURLHistory()
    await Start(port.value, trimmedURL)
    await fetchStatus()
    showSuccess('代理已启动')
  } catch (err) {
    console.error('Failed to start proxy:', err)
    showError('启动代理失败: ' + err)
  } finally {
    loading.value = false
  }
}

const stopProxy = async () => {
  loading.value = true
  try {
    await Stop()
    await fetchStatus()
    showSuccess('代理已停止')
  } catch (err) {
    console.error('Failed to stop proxy:', err)
    showError('停止代理失败: ' + err)
  } finally {
    loading.value = false
  }
}

const toggleRuleEnabled = async (rule: proxy.InjectionRule) => {
  try {
    await UpdateRule(rule)
  } catch (err) {
    console.error('Failed to update rule:', err)
    showError('更新规则失败: ' + err)
    rule.enabled = !rule.enabled
  }
}

const openAddModal = () => {
  currentRule.value = null
  showModal.value = true
}

const editRule = (rule: proxy.InjectionRule) => {
  currentRule.value = rule
  showModal.value = true
}

const deleteRule = async (id: string) => {
  ruleToDelete.value = id
  showDeleteDialog.value = true
}

const confirmDeleteRule = async () => {
  if (!ruleToDelete.value) return

  loading.value = true
  try {
    await DeleteRule(ruleToDelete.value)
    await fetchRules()
    showSuccess('删除规则成功')
  } catch (err) {
    console.error('Failed to delete rule:', err)
    showError('删除规则失败: ' + err)
  } finally {
    loading.value = false
    showDeleteDialog.value = false
    ruleToDelete.value = null
  }
}

onMounted(async () => {
  initialLoading.value = true
  initialError.value = ''
  try {
    await Promise.all([
      fetchStatus(),
      fetchRules(),
      fetchLogs(),
      fetchBackendURLHistory()
    ])
  } catch (err) {
    initialError.value = String(err)
  } finally {
    initialLoading.value = false
  }

  statusInterval = window.setInterval(() => {
    fetchStatus().catch(console.error)
  }, 3000)
})

onUnmounted(() => {
  if (statusInterval) {
    clearInterval(statusInterval)
  }
})
</script>

<style scoped>
.view-rules {
  padding: 32px 36px;
  gap: 22px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.proxy-card {
  position: relative;
}

.proxy-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--separator);
  margin-bottom: 4px;
}

.status-indicator {
  display: flex;
  align-items: center;
  gap: 8px;
}

.dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.dot-running {
  background: var(--success);
  box-shadow: 0 0 8px rgba(52, 199, 89, 0.4);
}

.dot-stopped {
  background: var(--tertiary);
}

.status-text {
  font-weight: 600;
  font-size: 14px;
}

.stats {
  display: flex;
  gap: 6px;
  font-size: 13px;
}

.stat-label {
  color: var(--secondary);
}

.stat-value {
  color: var(--accent);
  font-weight: 600;
}

.proxy-controls {
  display: flex;
  gap: 16px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.control-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.control-group label {
  font-size: 12px;
  color: var(--secondary);
  font-weight: 500;
}

.flex-1 {
  flex: 1;
  min-width: 200px;
}

.backend-url-group {
  display: flex;
  gap: 8px;
}

.url-select {
  flex: 1;
  appearance: none;
  -webkit-appearance: none;
  background: var(--control);
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 6px 28px 6px 10px;
  font-size: 13px;
  color: var(--label);
  font-family: var(--mono);
  cursor: pointer;
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%238E8E93' stroke-width='2.5' stroke-linecap='round'><polyline points='6 9 12 15 18 9'/></svg>");
  background-repeat: no-repeat;
  background-position: right 9px center;
  transition: background-color 0.12s;
}

.url-select:hover:not(:disabled) {
  background-color: var(--controlHover);
}

.url-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.control-actions {
  margin-bottom: 2px;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}

.card-head h2 {
  font-size: 17px;
  font-weight: 600;
}

.empty-container {
  padding: 20px 0;
}

.rules-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.rule-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 14px;
  background: var(--control);
  border-radius: 10px;
  gap: 16px;
}

.rule-main {
  flex: 1;
  min-width: 0;
}

.rule-title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.rule-title h3 {
  font-size: 15px;
  font-weight: 600;
}

.rule-keywords {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}

.keyword-chip {
  font-size: 11px;
  background: var(--window);
  color: var(--label);
  padding: 3px 8px;
  border-radius: 4px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.keyword-chip .remove-btn {
  cursor: pointer;
  color: var(--tertiary);
  font-weight: bold;
}

.keyword-chip .remove-btn:hover {
  color: var(--danger);
}

.rule-prompt {
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: pre-wrap;
}

.rule-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.inject-logs {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 400px;
  overflow-y: auto;
}

.inject-log {
  display: grid;
  grid-template-columns: 80px 1fr auto 1fr;
  gap: 12px;
  align-items: center;
  padding: 10px 14px;
  background: var(--control);
  border-radius: 8px;
  font-size: 13px;
}

.log-time {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--tertiary);
}

.log-rules {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.log-status {
  font-family: var(--mono);
  font-weight: 600;
  font-size: 12px;
}

.status-success {
  color: var(--success);
}

.status-error {
  color: var(--danger);
}

.log-preview {
  color: var(--secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Modal */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.4);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.modal-content {
  width: 100%;
  max-width: 520px;
  background: var(--card);
  border-radius: 14px;
  padding: 24px;
  box-shadow: var(--shadow);
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-height: 90vh;
  overflow-y: auto;
}

.modal-content h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-group label {
  font-size: 13px;
  color: var(--secondary);
  font-weight: 500;
}

.textarea-field {
  width: 100%;
  min-height: 100px;
  padding: 10px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  font-size: 13px;
  font-family: inherit;
  resize: vertical;
}

.textarea-field:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px rgba(0, 122, 255, 0.1);
}

.keyword-input-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
  border: 1px solid var(--separator);
}

.keyword-input-group:focus-within {
  border-color: var(--accent);
}

.keyword-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.btn-sm {
  padding: 6px 12px;
  font-size: 12px;
}
</style>
