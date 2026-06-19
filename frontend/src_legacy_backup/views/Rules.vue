<template>
  <div class="rules-page">
    <div class="page-header">
      <h1 class="page-title">注入规则管理</h1>
    </div>

    <!-- Proxy Control Card -->
    <div class="card proxy-card">
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
          <input type="number" v-model.number="port" class="input-field" :disabled="isRunning" />
        </div>
        <div class="control-group flex-1">
          <label>目标后端 URL</label>
          <div class="backend-url-input-group">
            <el-autocomplete
              v-model="backendURL"
              :fetch-suggestions="queryBackendURLs"
              placeholder="例如: https://api.anthropic.com"
              class="backend-url-autocomplete"
              :disabled="isRunning"
              @select="onBackendURLSelect"
              @blur="onBackendURLBlur"
              :debounce="0"
              popper-class="backend-url-dropdown"
              clearable
            >
              <template #default="{ item }">
                <div class="backend-url-item">
                  <span class="backend-url-text">{{ item.value }}</span>
                  <el-icon class="backend-url-delete" @click.stop="removeBackendURLFromHistory(item.value)">
                    <Close />
                  </el-icon>
                </div>
              </template>
              <template #empty>
                <div class="backend-url-empty">暂无历史URL</div>
              </template>
            </el-autocomplete>
            <button class="btn-secondary backend-url-save-btn" @click="handleSaveBackendURL" :disabled="isRunning || loading || !backendURL?.trim()">
              保存
            </button>
          </div>
        </div>
        <div class="control-actions">
          <button v-if="!isRunning" class="btn-primary" @click="startProxy" :disabled="loading">
            {{ loading ? '启动中...' : '启动代理' }}
          </button>
          <button v-else class="btn-danger" @click="stopProxy" :disabled="loading">
            {{ loading ? '停止中...' : '停止代理' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Rules List -->
    <div class="section">
      <div class="section-header">
        <h2 class="section-title">注入规则</h2>
        <button class="btn-primary btn-small" @click="openAddModal">添加规则</button>
      </div>

      <div class="rules-list">
        <div v-if="rules.length === 0" class="empty-state">暂无规则</div>
        <div v-for="rule in rules" :key="rule.id" class="card rule-card">
          <div class="rule-header">
            <div class="rule-title-group">
              <h3 class="rule-name">{{ rule.name }}</h3>
              <span class="priority-badge">优先级: {{ rule.priority }}</span>
            </div>
            <div class="rule-actions">
              <label class="switch">
                <input type="checkbox" v-model="rule.enabled" @change="toggleRuleEnabled(rule)">
                <span class="slider"></span>
              </label>
              <button class="btn-secondary btn-small" @click="editRule(rule)" :disabled="loading">编辑</button>
              <button class="btn-danger btn-small" @click="deleteRule(rule.id)" :disabled="loading">删除</button>
            </div>
          </div>
          
          <div class="rule-keywords" v-if="rule.keywords && rule.keywords.length > 0">
            <span v-for="kw in rule.keywords" :key="kw" class="keyword-tag">{{ kw }}</span>
          </div>
          
          <div class="rule-prompt">
            {{ rule.prompt }}
          </div>
        </div>
      </div>
    </div>

    <!-- Injection Logs -->
    <div class="section">
      <div class="section-header">
        <h2 class="section-title">注入日志</h2>
        <button class="btn-secondary btn-small" @click="fetchLogs">刷新</button>
      </div>

      <div class="card logs-card">
        <div v-if="logs.length === 0" class="empty-state">暂无日志</div>
        <table v-else class="logs-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>匹配规则</th>
              <th>状态码</th>
              <th>请求预览</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(log, index) in logs" :key="index">
              <td class="log-time">{{ log.time }}</td>
              <td>
                <div class="log-rules">
                  <span v-for="rn in log.ruleNames" :key="rn" class="rule-tag">{{ rn }}</span>
                </div>
              </td>
              <td>
                <span :class="['status-code', log.status >= 400 ? 'status-error' : 'status-success']">
                  {{ log.status }}
                </span>
              </td>
              <td class="log-preview">{{ log.preview }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Edit Modal -->
    <div v-if="showModal" class="modal-overlay" @click.self="closeModal">
      <div class="modal-content card">
        <h2 class="modal-title">{{ isEditing ? '编辑规则' : '添加规则' }}</h2>
        
        <div class="form-group">
          <label>规则名称</label>
          <input type="text" v-model="currentRule.name" class="input-field" placeholder="输入规则名称" />
        </div>
        
        <div class="form-group">
          <label>优先级 (数字越大优先级越高)</label>
          <input type="number" v-model.number="currentRule.priority" class="input-field" />
        </div>
        
        <div class="form-group">
          <label>触发关键词 (输入后按回车添加)</label>
          <div class="keyword-input-container">
            <div class="keyword-tags">
              <span v-for="(kw, index) in currentRule.keywords" :key="index" class="keyword-tag editable">
                {{ kw }}
                <span class="remove-tag" @click="removeKeyword(index)">×</span>
              </span>
            </div>
            <input 
              type="text" 
              v-model="newKeyword" 
              @keydown.enter.prevent="addKeyword"
              class="input-field keyword-input" 
              placeholder="输入关键词..." 
            />
          </div>
        </div>
        
        <div class="form-group">
          <label>注入提示词 (Prompt)</label>
          <textarea v-model="currentRule.prompt" class="input-field textarea-field" rows="5" placeholder="输入要注入的提示词内容..."></textarea>
        </div>
        
        <div class="form-group checkbox-group">
          <label class="checkbox-label">
            <input type="checkbox" v-model="currentRule.enabled" />
            启用此规则
          </label>
        </div>
        
        <div class="modal-actions">
          <button class="btn-secondary" @click="closeModal" :disabled="loading">取消</button>
          <button class="btn-primary" @click="saveRule" :disabled="loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { Close } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useToast } from '../composables/useToast'
import { GetRules, AddRule, UpdateRule, DeleteRule, Start, Stop, GetStatus, GetLogs } from '../../wailsjs/go/proxy/ProxyService'
import { GetProxyBackendURLHistory, AddProxyBackendURL, RemoveProxyBackendURL } from '../../wailsjs/go/main/App'
import { proxy } from '../../wailsjs/go/models'

// State
const { showSuccess, showError } = useToast()
const loading = ref(false)
const isRunning = ref(false)
const port = ref(5280)
const backendURL = ref('https://api.anthropic.com')
const backendURLHistory = ref<string[]>([])
const ruleCount = ref(0)

const rules = ref<proxy.InjectionRule[]>([])
const logs = ref<proxy.InjectionLog[]>([])

// Modal State
const showModal = ref(false)
const isEditing = ref(false)
const newKeyword = ref('')
const currentRule = ref<proxy.InjectionRule>(new proxy.InjectionRule())

let statusInterval: number | null = null

// Backend URL History Methods
const fetchBackendURLHistory = async () => {
  try {
    const history = await GetProxyBackendURLHistory()
    backendURLHistory.value = history || []
  } catch (err) {
    console.error('Failed to fetch backend URL history:', err)
  }
}

const queryBackendURLs = (queryString: string, cb: (results: { value: string }[]) => void) => {
  const results = queryString
    ? backendURLHistory.value.filter(url => url.toLowerCase().includes(queryString.toLowerCase()))
    : backendURLHistory.value
  cb(results.map(url => ({ value: url })))
}

const onBackendURLSelect = (item: { value: string }) => {
  backendURL.value = item.value
}

const onBackendURLBlur = async () => {
  const trimmedURL = backendURL.value.trim()
  if (trimmedURL) {
    try {
      await AddProxyBackendURL(trimmedURL)
      await fetchBackendURLHistory()
    } catch (err) {
      console.error('Failed to add backend URL to history:', err)
    }
  }
}

const removeBackendURLFromHistory = async (url: string) => {
  try {
    await RemoveProxyBackendURL(url)
    await fetchBackendURLHistory()
    ElMessage.success('已删除历史URL')
  } catch (err) {
    console.error('Failed to remove backend URL from history:', err)
    ElMessage.error('删除历史URL失败')
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
    console.error('Failed to save backend URL to history:', err)
    showError('保存失败: ' + err)
  }
}

// Methods
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
    console.error('Failed to fetch status:', err)
  }
}

const fetchRules = async () => {
  try {
    const fetchedRules = await GetRules()
    rules.value = fetchedRules || []
  } catch (err) {
    console.error('Failed to fetch rules:', err)
  }
}

const fetchLogs = async () => {
  try {
    const fetchedLogs = await GetLogs()
    logs.value = fetchedLogs || []
  } catch (err) {
    console.error('Failed to fetch logs:', err)
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
    // 添加到历史记录
    await AddProxyBackendURL(trimmedURL)
    await fetchBackendURLHistory()
    // 启动代理
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
    rule.enabled = !rule.enabled // revert on failure
  }
}

const openAddModal = () => {
  isEditing.value = false
  currentRule.value = new proxy.InjectionRule({
    id: crypto.randomUUID(),
    name: '',
    keywords: [],
    prompt: '',
    enabled: true,
    priority: 0
  })
  newKeyword.value = ''
  showModal.value = true
}

const editRule = (rule: proxy.InjectionRule) => {
  isEditing.value = true
  currentRule.value = new proxy.InjectionRule({
    id: rule.id,
    name: rule.name,
    keywords: [...(rule.keywords || [])],
    prompt: rule.prompt,
    enabled: rule.enabled,
    priority: rule.priority
  })
  newKeyword.value = ''
  showModal.value = true
}

const closeModal = () => {
  showModal.value = false
}

const addKeyword = () => {
  const kw = newKeyword.value.trim()
  if (kw && !currentRule.value.keywords.includes(kw)) {
    currentRule.value.keywords.push(kw)
  }
  newKeyword.value = ''
}

const removeKeyword = (index: number) => {
  currentRule.value.keywords.splice(index, 1)
}

const saveRule = async () => {
  if (!currentRule.value.name || !currentRule.value.prompt) {
    showError('规则名称和提示词不能为空')
    return
  }
  
  loading.value = true
  try {
    if (isEditing.value) {
      await UpdateRule(currentRule.value)
    } else {
      await AddRule(currentRule.value)
    }
    await fetchRules()
    closeModal()
    showSuccess('保存规则成功')
  } catch (err) {
    console.error('Failed to save rule:', err)
    showError('保存规则失败: ' + err)
  } finally {
    loading.value = false
  }
}

const deleteRule = async (id: string) => {
  if (!confirm('确定要删除此规则吗？')) return
  
  loading.value = true
  try {
    await DeleteRule(id)
    await fetchRules()
    showSuccess('删除规则成功')
  } catch (err) {
    console.error('Failed to delete rule:', err)
    showError('删除规则失败: ' + err)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchStatus()
  fetchRules()
  fetchLogs()
  fetchBackendURLHistory()
  
  statusInterval = window.setInterval(() => {
    fetchStatus()
  }, 3000)
})

onUnmounted(() => {
  if (statusInterval) {
    clearInterval(statusInterval)
  }
})
</script>

<style scoped>
.rules-page {
  display: flex;
  flex-direction: column;
  gap: 24px;
  padding: 0;
}

.page-header {
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

.card {
  background-color: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 20px;
}

.section {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.section-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
}

/* Proxy Card */
.proxy-card {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.proxy-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-bottom: 16px;
  border-bottom: 1px solid #2a2f3e;
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
  background-color: #66bb6a;
  box-shadow: 0 0 8px rgba(102, 187, 106, 0.5);
}

.dot-stopped {
  background-color: #5a6a7a;
}

.status-text {
  font-weight: 600;
  color: #e0e6ed;
}

.stats {
  display: flex;
  gap: 8px;
  font-size: 14px;
}

.stat-label {
  color: #8899aa;
}

.stat-value {
  color: #4fc3f7;
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
  gap: 8px;
}

.flex-1 {
  flex: 1;
  min-width: 200px;
}

.control-group label {
  font-size: 12px;
  color: #8899aa;
  font-weight: 600;
}

.control-actions {
  margin-bottom: 2px;
}

/* Rules List */
.rules-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.rule-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  transition: all 0.15s ease;
}

.rule-card:hover {
  border-color: #4fc3f7;
}

.rule-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.rule-title-group {
  display: flex;
  align-items: center;
  gap: 12px;
}

.rule-name {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

.priority-badge {
  font-size: 12px;
  background-color: rgba(79, 195, 247, 0.1);
  color: #4fc3f7;
  padding: 2px 8px;
  border-radius: 12px;
  border: 1px solid rgba(79, 195, 247, 0.2);
}

.rule-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.rule-keywords {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.keyword-tag {
  font-size: 12px;
  background-color: #2a2f3e;
  color: #e0e6ed;
  padding: 4px 8px;
  border-radius: 4px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.keyword-tag.editable {
  padding-right: 4px;
}

.remove-tag {
  cursor: pointer;
  color: #8899aa;
  font-weight: bold;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  transition: all 0.15s ease;
}

.remove-tag:hover {
  background-color: #ef5350;
  color: #fff;
}

.rule-prompt {
  font-size: 14px;
  color: #8899aa;
  background-color: #0f1219;
  padding: 12px;
  border-radius: 6px;
  border: 1px solid #2a2f3e;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: pre-wrap;
}

/* Logs */
.logs-card {
  padding: 0;
  overflow: hidden;
}

.logs-table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
  font-size: 14px;
}

.logs-table th {
  padding: 12px 16px;
  background-color: rgba(42, 47, 62, 0.5);
  color: #8899aa;
  font-weight: 600;
  border-bottom: 1px solid #2a2f3e;
}

.logs-table td {
  padding: 12px 16px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
}

.logs-table tr:last-child td {
  border-bottom: none;
}

.logs-table tr:hover td {
  background-color: rgba(42, 47, 62, 0.3);
}

.log-time {
  color: #8899aa;
  white-space: nowrap;
}

.log-rules {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.rule-tag {
  font-size: 12px;
  background-color: rgba(79, 195, 247, 0.1);
  color: #4fc3f7;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid rgba(79, 195, 247, 0.2);
}

.status-code {
  font-weight: 600;
  font-family: monospace;
}

.status-success {
  color: #66bb6a;
}

.status-error {
  color: #ef5350;
}

.log-preview {
  color: #8899aa;
  max-width: 300px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Modal */
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(15, 18, 25, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.modal-content {
  width: 100%;
  max-width: 600px;
  max-height: 90vh;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 20px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
}

.modal-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: #e0e6ed;
  border-bottom: 1px solid #2a2f3e;
  padding-bottom: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 14px;
  color: #8899aa;
  font-weight: 600;
}

.keyword-input-container {
  display: flex;
  flex-direction: column;
  gap: 8px;
  background-color: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 8px;
  transition: all 0.15s ease;
}

.keyword-input-container:focus-within {
  border-color: #4fc3f7;
}

.keyword-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.keyword-input {
  border: none !important;
  padding: 4px !important;
  background: transparent !important;
}

.keyword-input:focus {
  border: none !important;
  outline: none !important;
}

.checkbox-group {
  flex-direction: row;
  align-items: center;
}

.checkbox-label {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  color: #e0e6ed !important;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
  padding-top: 16px;
  border-top: 1px solid #2a2f3e;
}

/* Common Elements */
.input-field {
  background-color: #0f1219;
  border: 1px solid #2a2f3e;
  color: #e0e6ed;
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 14px;
  outline: none;
  transition: all 0.15s ease;
  font-family: inherit;
}

.input-field:focus {
  border-color: #4fc3f7;
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.textarea-field {
  resize: vertical;
  min-height: 100px;
  font-family: monospace;
}

button {
  cursor: pointer;
  font-size: 14px;
  font-weight: 600;
  padding: 8px 16px;
  border-radius: 6px;
  transition: all 0.15s ease;
  outline: none;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-primary {
  background-color: #4fc3f7;
  color: #0f1219;
  border: 1px solid #4fc3f7;
}

.btn-primary:hover:not(:disabled) {
  background-color: #29b6f6;
  border-color: #29b6f6;
}

.btn-secondary {
  background-color: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn-secondary:hover:not(:disabled) {
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.btn-danger {
  background-color: transparent;
  color: #ef5350;
  border: 1px solid #ef5350;
}

.btn-danger:hover:not(:disabled) {
  background-color: rgba(239, 83, 80, 0.1);
}

.btn-small {
  padding: 4px 12px;
  font-size: 12px;
}

.empty-state {
  padding: 32px;
  text-align: center;
  color: #5a6a7a;
  font-size: 14px;
}

/* Toggle Switch */
.switch {
  position: relative;
  display: inline-block;
  width: 40px;
  height: 20px;
}

.switch input {
  opacity: 0;
  width: 0;
  height: 0;
}

.slider {
  position: absolute;
  cursor: pointer;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: #2a2f3e;
  transition: .15s;
  border-radius: 20px;
}

.slider:before {
  position: absolute;
  content: "";
  height: 14px;
  width: 14px;
  left: 3px;
  bottom: 3px;
  background-color: #8899aa;
  transition: .15s;
  border-radius: 50%;
}

input:checked + .slider {
  background-color: rgba(79, 195, 247, 0.2);
  border: 1px solid #4fc3f7;
}

input:checked + .slider:before {
  transform: translateX(20px);
  background-color: #4fc3f7;
}

/* Backend URL Autocomplete */
.backend-url-input-group {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}

.backend-url-autocomplete {
  width: 100%;
  flex: 1;
}

.backend-url-save-btn {
  margin-top: 1px;
  white-space: nowrap;
  padding: 8px 16px;
  height: 38px;
}

.backend-url-autocomplete :deep(.el-input__wrapper) {
  background-color: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 8px 12px;
  box-shadow: none;
  transition: all 0.15s ease;
}

.backend-url-autocomplete :deep(.el-input__wrapper:hover) {
  border-color: #4fc3f7;
}

.backend-url-autocomplete :deep(.el-input__wrapper.is-focus) {
  border-color: #4fc3f7;
  box-shadow: 0 0 0 1px #4fc3f7;
}

.backend-url-autocomplete :deep(.el-input__inner) {
  color: #e0e6ed;
  font-size: 14px;
  font-family: inherit;
}

.backend-url-autocomplete :deep(.el-input__inner::placeholder) {
  color: #5a6a7a;
}

.backend-url-autocomplete :deep(.el-input__inner:disabled) {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Dropdown Styles */
.backend-url-dropdown {
  background-color: #1a1f2e !important;
  border: 1px solid #2a2f3e !important;
  border-radius: 8px !important;
  padding: 8px !important;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4) !important;
}

.backend-url-dropdown .el-autocomplete-suggestion__wrap {
  max-height: 300px !important;
}

.backend-url-dropdown .el-autocomplete-suggestion__list {
  padding: 0 !important;
}

.backend-url-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-radius: 6px;
  transition: background-color 0.15s ease;
}

.backend-url-item:hover {
  background-color: rgba(79, 195, 247, 0.1);
}

.backend-url-text {
  color: #e0e6ed;
  font-size: 14px;
  font-family: monospace;
  word-break: break-all;
}

.backend-url-delete {
  color: #5a6a7a;
  font-size: 16px;
  cursor: pointer;
  transition: color 0.15s ease;
  flex-shrink: 0;
  margin-left: 8px;
}

.backend-url-delete:hover {
  color: #ef5350;
}

.backend-url-empty {
  padding: 16px;
  text-align: center;
  color: #5a6a7a;
  font-size: 14px;
}
</style>
