<!--
  UpdateDialog - 「关于」弹窗（§8.2 项13）
  版本胶囊点击触发，采用苹果 HIG 关于面板风格：
  应用图标 + 名称 + 版本行（粗体）+ 构建元信息小字 + 操作区（检查更新/关闭）
  保留检查更新、Release Notes、下载安装流程。
-->
<template>
  <Dialog
    :open="open"
    title="关于"
    @update:open="handleClose"
  >
    <div class="about-content">
      <!-- 应用信息区（苹果 HIG 关于面板风格）-->
      <div class="app-hero">
        <div class="app-icon">A</div>
        <div class="app-meta">
          <h2 class="app-name">{{ productName }}</h2>
          <p class="app-tagline">跨平台多提供商 AI CLI 工作台</p>
          <p class="app-version">版本 {{ currentVersion }}</p>
        </div>
      </div>

      <!-- 构建元信息 -->
      <div class="build-info">
        <div v-if="buildTime" class="meta-row">
          <span class="meta-label">构建时间</span>
          <span class="meta-value">{{ buildTime }}</span>
        </div>
        <div v-if="gitCommit" class="meta-row">
          <span class="meta-label">Git Commit</span>
          <span class="meta-value mono">{{ gitCommit }}</span>
        </div>
        <div v-if="goVersion" class="meta-row">
          <span class="meta-label">Go 版本</span>
          <span class="meta-value">{{ goVersion }}</span>
        </div>
        <div v-if="platform" class="meta-row">
          <span class="meta-label">平台</span>
          <span class="meta-value">{{ platform }}</span>
        </div>
      </div>

      <!-- 更新检查结果 -->
      <div v-if="updateInfo.hasUpdate" class="version-row">
        <span class="version-label">最新版本</span>
        <span class="version-value highlight">{{ updateInfo.latestVersion }}</span>
      </div>
      <div v-if="updateInfo.publishedAt" class="version-meta">
        发布于：{{ updateInfo.publishedAt }}
      </div>

      <!-- Release Notes -->
      <div v-if="updateInfo.releaseNotes" class="release-notes-section">
        <label>Release Notes</label>
        <textarea
          :value="updateInfo.releaseNotes"
          class="release-notes"
          rows="8"
          readonly
        />
      </div>

      <!-- Update Actions -->
      <div class="update-actions">
        <AppButton
          v-if="!updateInfo.hasUpdate && !checking"
          variant="primary"
          @click="handleCheckUpdate"
        >
          检查更新
        </AppButton>
        <div v-else-if="checking" class="checking-state">
          <span class="spinner"></span>
          <span>检查更新中...</span>
        </div>
        <template v-else-if="updateInfo.hasUpdate">
          <p v-if="updateInfo.updateAction === 'install'" class="install-hint">
            应用将退出并重启以完成更新
          </p>
          <div class="update-buttons">
            <AppButton
              variant="primary"
              :disabled="downloading"
              @click="handleDownloadAndApply"
            >
              {{ downloading ? '下载中...' : '下载并安装' }}
            </AppButton>
            <AppButton
              variant="ghost"
              @click="handleOpenReleasePage"
            >
              手动下载
            </AppButton>
          </div>
        </template>
        <div v-else-if="updateInfo.hasUpdate === false" class="uptodate-state">
          <span class="status-dot"></span>
          <span>当前已是最新版本</span>
        </div>
      </div>

      <!-- Error -->
      <div v-if="updateError" class="update-error">
        {{ updateError }}
      </div>
    </div>
    <template #footer>
      <AppButton variant="ghost" @click="handleClose">
        关闭
      </AppButton>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';
import { CheckForUpdate, DownloadAndApplyUpdate, GetAppInfo } from '../../../wailsjs/go/main/App';
import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime';

interface UpdateInfo {
  hasUpdate: boolean;
  latestVersion: string;
  publishedAt: string;
  releaseNotes: string;
  updateAction: 'install' | 'manual';
}

interface Props {
  open?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
}>();

const currentVersion = ref('v1.0.0');
const productName = ref('Amagi CodeBox');
const buildTime = ref('');
const gitCommit = ref('');
const goVersion = ref('');
const platform = ref('');
const updateInfo = ref<UpdateInfo>({
  hasUpdate: false,
  latestVersion: '',
  publishedAt: '',
  releaseNotes: '',
  updateAction: 'manual',
});
const checking = ref(false);
const downloading = ref(false);
const updateError = ref('');

onMounted(async () => {
  try {
    const info = await GetAppInfo();
    // GetAppInfo 当前仅返回 version；buildTime/gitCommit/goVersion/platform/productName
    // 待鲁班扩展后端后填充，前端按字段存在性防御读取，缺失则隐藏对应行。
    if (info?.version) {
      currentVersion.value = `v${info.version}`;
    }
    if (info?.productName) productName.value = String(info.productName);
    if (info?.buildTime) buildTime.value = String(info.buildTime);
    if (info?.gitCommit) gitCommit.value = String(info.gitCommit);
    if (info?.goVersion) goVersion.value = String(info.goVersion);
    if (info?.platform) platform.value = String(info.platform);
  } catch (error) {
    console.error('[UpdateDialog] Failed to get app info:', error);
  }
});

async function handleCheckUpdate() {
  checking.value = true;
  updateError.value = '';
  try {
    const info = await CheckForUpdate();
    if (info) {
      updateInfo.value = {
        hasUpdate: info.hasUpdate || false,
        latestVersion: info.latestVersion || '',
        publishedAt: info.publishedAt || '',
        releaseNotes: info.releaseNotes || '',
        updateAction: (info.updateAction as 'install' | 'manual') || 'manual',
      };
    }
  } catch (error) {
    console.error('[UpdateDialog] Check update failed:', error);
    updateError.value = '检查更新失败';
  } finally {
    checking.value = false;
  }
}

async function handleDownloadAndApply() {
  downloading.value = true;
  updateError.value = '';
  try {
    await DownloadAndApplyUpdate();
  } catch (error) {
    console.error('[UpdateDialog] Download failed:', error);
    updateError.value = '下载更新失败';
  } finally {
    downloading.value = false;
  }
}

function handleOpenReleasePage() {
  // GitHub repository URL: https://github.com/runrunrain/amagi-codebox
  // Can be made configurable via build flag if needed
  const url = `https://github.com/runrunrain/amagi-codebox/releases/tag/${updateInfo.value.latestVersion}`;
  BrowserOpenURL(url);
}

function handleClose() {
  emit('update:open', false);
}
</script>

<style scoped>
.about-content {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

/* 应用信息区：苹果 HIG 关于面板风格 */
.app-hero {
  display: flex;
  align-items: center;
  gap: 16px;
}

.app-icon {
  width: 64px;
  height: 64px;
  border-radius: 14px;
  background: var(--accent);
  color: #fff;
  font-size: 32px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.app-meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.app-name {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--label);
  letter-spacing: -0.2px;
}

.app-tagline {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
}

.app-version {
  margin: 4px 0 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  font-family: var(--mono);
}

/* 构建元信息：等宽对齐的小字行 */
.build-info {
  display: flex;
  flex-direction: column;
  padding: 10px 0 0;
  border-top: 1px solid var(--separator);
}

.meta-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 6px 0;
  font-size: 12px;
}

.meta-label {
  color: var(--tertiary);
  flex-shrink: 0;
}

.meta-value {
  color: var(--secondary);
  text-align: right;
  word-break: break-all;
}

.meta-value.mono {
  font-family: var(--mono);
}

.version-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
}

.version-label {
  color: var(--secondary);
}

.version-value {
  font-weight: 600;
  color: var(--label);
  font-family: var(--mono);
}

.version-value.highlight {
  color: var(--accent);
}

.version-meta {
  font-size: 11px;
  color: var(--tertiary);
  padding-left: 2px;
}

.release-notes-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.release-notes-section label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.release-notes {
  width: 100%;
  min-height: 120px;
  max-height: 200px;
  padding: 10px 12px;
  font-size: 12px;
  font-family: var(--mono);
  line-height: 1.5;
  color: var(--secondary);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  resize: vertical;
}

.update-actions {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.checking-state,
.uptodate-state {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: var(--secondary);
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--separator);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--success);
}

.install-hint {
  font-size: 11px;
  color: var(--tertiary);
  margin: 0;
  padding: 4px 0;
}

.update-buttons {
  display: flex;
  gap: 8px;
}

.update-error {
  font-size: 12px;
  color: var(--danger);
  padding: 8px 12px;
  background: rgba(255, 59, 48, 0.05);
  border-radius: 8px;
}
</style>
