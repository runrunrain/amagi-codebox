<template>
  <div class="set-card">
    <div class="about-block">
      <div class="about-icon">A</div>
      <div class="about-info">
        <h2>Amagi CodeBox</h2>
        <p>Version {{ currentVersion || '...' }} · 跨平台多提供商 AI CLI 工作台</p>
      </div>
    </div>
  </div>

  <div class="set-card">
    <h2>详细信息</h2>
    <div class="setting-list">
      <div class="setting-row">
        <label>当前版本</label>
        <div class="row-value">
          <span class="ver-badge">{{ currentVersion ? `v${currentVersion}` : '检测中' }}</span>
        </div>
      </div>
      <div class="setting-row">
        <label>配置目录</label>
        <div class="row-value">
          <span class="mono">{{ configDir || '—' }}</span>
        </div>
      </div>
      <div class="setting-row">
        <label>技术栈</label>
        <div class="row-value">
          <span>Wails v2 · Go · Vue 3 · Element Plus · xterm.js</span>
        </div>
      </div>
      <div class="setting-row">
        <label>系统托盘</label>
        <div class="row-value">
          <span :class="{ 'status-unsupported': !systemTraySupported }">{{ systemTrayStatusLabel }}</span>
        </div>
      </div>
      <div class="setting-row">
        <label>许可证</label>
        <div class="row-value">
          <span>MIT</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { GetAppInfo } from '../../../wailsjs/go/main/App'
import { usePlatformCapabilities } from '../../composables/usePlatformCapabilities'

const currentVersion = ref('')
const configDir = ref('')
const { caps, ensure } = usePlatformCapabilities()

onMounted(async () => {
  // Load platform capabilities
  await ensure()

  try {
    const info: any = await GetAppInfo()
    currentVersion.value = info?.version || ''
    configDir.value = info?.configDir || info?.configPath || info?.homeDir || ''
  } catch (err) {
    console.error('load app info:', err)
  }
})

const systemTraySupported = computed(() => caps.value?.systemTraySupported || false)
const systemTrayStatusLabel = computed(() => systemTraySupported.value ? '支持' : '不支持')
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

.about-block {
  display: flex;
  align-items: center;
  gap: 16px;
}

.about-icon {
  width: 56px;
  height: 56px;
  border-radius: 14px;
  background: var(--accent);
  color: #fff;
  font-size: 28px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.about-info p {
  font-size: 13px;
  color: var(--secondary);
  margin-top: 2px;
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
  flex-shrink: 0;
}

.row-value {
  font-size: 13px;
  color: var(--label);
  text-align: right;
  word-break: break-all;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--secondary);
}

.ver-badge {
  display: inline-block;
  padding: 2px 10px;
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 999px;
}

.status-unsupported {
  color: var(--tertiary);
  opacity: 0.6;
}
</style>
