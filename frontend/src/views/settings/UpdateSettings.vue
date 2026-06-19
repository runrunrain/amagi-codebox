<template>
  <div class="set-card">
    <h2>软件更新</h2>
    <p class="set-sub">检查并安装 amagi-codebox 新版本</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>当前版本</label>
        <span class="mono version-current">{{ currentVersionLabel }}</span>
      </div>

      <div class="setting-row">
        <label>检查更新</label>
        <AppButton
          variant="primary"
          size="small"
          :disabled="checking || downloading"
          @click="checkUpdate"
        >
          {{ checking ? '检查中...' : '检查更新' }}
        </AppButton>
      </div>
    </div>

    <div v-if="updateError" class="update-error">{{ updateError }}</div>

    <div v-if="updateInfo" class="update-info">
      <div class="update-info-head">
        <div class="ui-title">
          <span v-if="updateInfo.hasUpdate" class="badge badge-warn">有新版本</span>
          <span v-else class="badge badge-ok">已是最新</span>
          <span class="mono version-latest">v{{ updateInfo.latestVersion || updateInfo.currentVersion }}</span>
        </div>
        <span v-if="updateInfo.publishedAt" class="update-date">
          发布于：{{ updateInfo.publishedAt }}
        </span>
      </div>

      <div v-if="updateInfo.releaseNotes" class="release-notes">
        <textarea
          class="release-area"
          :value="updateInfo.releaseNotes"
          readonly
          rows="10"
        />
      </div>

      <div v-if="updateInfo.hasUpdate" class="update-action">
        <ProgressBar :percent="progressPercent" />
        <div class="progress-meta">
          <span class="progress-text">{{ progressText }}</span>
          <AppButton
            variant="primary"
            size="small"
            :disabled="downloading"
            @click="downloadAndApply"
          >
            {{ downloading ? '下载安装中...' : '下载并安装' }}
          </AppButton>
        </div>
      </div>
    </div>
  </div>

  <div class="set-card">
    <h2>GitHub Token</h2>
    <p class="set-sub">用于访问 GitHub Release（提高私有仓库或 API 速率限制场景下的检查成功率）</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>Token</label>
        <input
          class="text-input mono"
          :type="tokenVisible ? 'text' : 'password'"
          v-model="tokenDraft"
          placeholder="ghp_..."
        />
      </div>

      <div class="setting-row">
        <label>显示 Token</label>
        <Switch v-model="tokenVisible" />
      </div>

      <div class="setting-row">
        <label></label>
        <div class="row-actions">
          <AppButton variant="ghost" size="small" :disabled="savingToken" @click="loadToken">
            {{ savingToken ? '读取中...' : '读取当前' }}
          </AppButton>
          <AppButton variant="primary" size="small" :disabled="savingToken" @click="saveToken">
            {{ savingToken ? '保存中...' : '保存' }}
          </AppButton>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { EventsOn, EventsOff } from '../../../wailsjs/runtime/runtime'
import {
  checkForUpdate,
  downloadAndApplyUpdate,
  getUpdaterGitHubToken,
  setUpdaterGitHubToken,
} from '../../api/updater'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import Switch from '../../components/ui/Switch.vue'
import ProgressBar from '../../components/ui/ProgressBar.vue'

import { updater } from '../../../wailsjs/go/models'

type UpdateInfo = updater.UpdateInfo

const { showSuccess, showError } = useToast()

const checking = ref(false)
const downloading = ref(false)
const updateInfo = ref<UpdateInfo | null>(null)
const updateError = ref('')
const downloadProgress = ref({ downloaded: 0, total: 0 })
let removeProgressListener: (() => void) | null = null

const tokenDraft = ref('')
const tokenVisible = ref(false)
const savingToken = ref(false)

const currentVersionLabel = computed(() => {
  if (updateInfo.value?.currentVersion) return 'v' + updateInfo.value.currentVersion
  return '未检测'
})

const progressPercent = computed(() => {
  const { downloaded, total } = downloadProgress.value
  if (!total || total <= 0) return downloading.value ? 5 : 0
  return Math.min(100, Math.round((downloaded / total) * 100))
})

const progressText = computed(() => {
  const { downloaded, total } = downloadProgress.value
  const fmt = (n: number) => (n / 1024 / 1024).toFixed(1) + ' MB'
  if (!downloading.value) return ''
  if (total <= 0) return '准备中...'
  return `${fmt(downloaded)} / ${fmt(total)}`
})

function normalizeUpdateError(err: any): string {
  if (!err) return '未知错误'
  if (typeof err === 'string') return err
  return err?.message || JSON.stringify(err)
}

async function checkUpdate() {
  checking.value = true
  updateError.value = ''
  updateInfo.value = null
  try {
    const info = await checkForUpdate()
    updateInfo.value = info
    if (!info?.hasUpdate) {
      showSuccess('当前已是最新版本')
    }
  } catch (err) {
    updateError.value = '检查失败: ' + normalizeUpdateError(err)
  } finally {
    checking.value = false
  }
}

async function downloadAndApply() {
  downloading.value = true
  downloadProgress.value = { downloaded: 0, total: 0 }
  updateError.value = ''
  cleanupProgressListener()
  try {
    removeProgressListener = EventsOn('update:progress', (progress: any) => {
      if (progress && typeof progress === 'object') {
        downloadProgress.value = {
          downloaded: Number(progress.downloaded) || 0,
          total: Number(progress.total) || 0,
        }
      }
    })
    await downloadAndApplyUpdate()
    showSuccess('更新已开始应用，应用可能即将重启')
  } catch (err: any) {
    updateError.value = '安装失败: ' + (err?.message || err)
  } finally {
    downloading.value = false
    cleanupProgressListener()
  }
}

function cleanupProgressListener() {
  if (removeProgressListener) {
    try {
      EventsOff('update:progress')
    } catch {
      // ignore
    }
    removeProgressListener = null
  }
}

async function loadToken() {
  savingToken.value = true
  try {
    const v = await getUpdaterGitHubToken()
    tokenDraft.value = v || ''
    showSuccess('已读取当前 Token')
  } catch (err: any) {
    showError('读取失败: ' + (err?.message || err))
  } finally {
    savingToken.value = false
  }
}

async function saveToken() {
  savingToken.value = true
  try {
    await setUpdaterGitHubToken((tokenDraft.value || '').trim())
    showSuccess('Token 已保存')
  } catch (err: any) {
    showError('保存失败: ' + (err?.message || err))
  } finally {
    savingToken.value = false
  }
}

onMounted(async () => {
  try {
    const v = await getUpdaterGitHubToken()
    tokenDraft.value = v || ''
  } catch (err) {
    console.warn('load github token failed:', err)
  }
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

.mono {
  font-family: var(--mono);
}

.version-current {
  font-size: 13px;
  color: var(--secondary);
}

.update-error {
  margin-top: 12px;
  padding: 8px 12px;
  font-size: 12px;
  color: var(--error, #ff3b30);
  background: rgba(255, 59, 48, 0.08);
  border-radius: 8px;
}

.update-info {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--separator);
}

.update-info-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.ui-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 999px;
  font-weight: 500;
}

.badge-warn {
  color: #b25000;
  background: rgba(255, 159, 10, 0.16);
}

.badge-ok {
  color: #1d6a3a;
  background: rgba(52, 199, 89, 0.16);
}

.version-latest {
  font-size: 14px;
  color: var(--label);
  font-weight: 600;
}

.update-date {
  font-size: 11px;
  color: var(--tertiary);
}

.release-notes {
  margin: 10px 0;
}

.release-area {
  width: 100%;
  resize: vertical;
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.6;
  color: var(--secondary);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 10px 12px;
  outline: none;
}

.update-action {
  margin-top: 8px;
}

.progress-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.progress-text {
  font-size: 11px;
  color: var(--tertiary);
  font-variant-numeric: tabular-nums;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.text-input {
  min-width: 240px;
  max-width: 320px;
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
</style>
