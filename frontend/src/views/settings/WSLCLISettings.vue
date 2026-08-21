<template>
  <div class="set-card" v-if="showPanel">
    <h2>WSL 内的 CLI</h2>
    <p class="set-sub">
      Windows 上终端默认在 WSL (Linux) 中启动。将 CLI 安装到 WSL 内部，即可在 Linux 环境原生运行。
    </p>

    <div v-if="!status || !status.available" class="empty-row">
      {{ status?.reason || '正在检测 WSL 环境…' }}
    </div>

    <template v-else>
      <div class="wsl-meta">
        <span class="mono">发行版: {{ status.distro }}</span>
        <span v-if="status.distroWSLVersion === 2" class="badge badge-wsl2">WSL2</span>
        <span v-else-if="status.distroWSLVersion === 1" class="badge">WSL1</span>
        <span class="mono">
          Node: {{ status.nodeVersion || '未安装 (安装 CLI 时会自动安装 Node 20)' }}
        </span>
      </div>

      <div class="setting-list">
        <div v-for="t in status.tools" :key="t.tool" class="setting-row">
          <label>{{ displayName(t.tool) }}</label>
          <div class="row-value">
            <span v-if="t.installed" class="badge badge-ok">已安装 {{ t.version }}</span>
            <span v-else class="badge badge-missing">未安装</span>
            <AppButton
              variant="primary"
              size="small"
              :disabled="busyTool === t.tool"
              @click="install(t.tool)"
            >
              {{ busyTool === t.tool ? '安装中…' : (t.installed ? '重新安装' : '安装到 WSL') }}
            </AppButton>
          </div>
        </div>
      </div>

      <p v-if="busyTool" class="set-hint">
        正在 WSL 内安装 {{ displayName(busyTool) }}，首次可能需要安装 Node 运行时，请稍候…
      </p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getWSLCLIStatus, installCLIToWSL, type WSLStatus } from '../../api/wslsetup'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'

const { showSuccess, showError, showInfo } = useToast()

const status = ref<WSLStatus | null>(null)
const busyTool = ref<string>('')
// Only show the panel when WSL is relevant (available), or while first loading.
const showPanel = ref(true)

const NAMES: Record<string, string> = {
  claude: 'Claude Code',
  opencode: 'OpenCode',
  codex: 'Codex',
}
function displayName(tool: string): string {
  return NAMES[tool] || tool
}

async function load() {
  try {
    const st = await getWSLCLIStatus()
    status.value = st
    // Hide the whole panel on platforms/machines where WSL is unavailable AND
    // the reason indicates it's simply not a WSL host (keep it visible with the
    // reason otherwise so the user understands why).
    showPanel.value = st.available || !!st.reason
  } catch (err) {
    console.error('load WSL status:', err)
    showPanel.value = false
  }
}

async function install(tool: string) {
  if (busyTool.value) return
  busyTool.value = tool
  showInfo(`正在将 ${displayName(tool)} 安装到 WSL…`)
  try {
    const res = await installCLIToWSL(tool)
    if (res.success) {
      if (res.alreadyOK) {
        showSuccess(`${displayName(tool)} 已在 WSL 中 (${res.version})`)
      } else {
        showSuccess(`${displayName(tool)} 已安装到 WSL (${res.version})`)
      }
      await load()
    } else {
      showError(`安装失败: ${res.error || '未知错误'}`)
    }
  } catch (err: any) {
    showError('安装失败: ' + (err?.message || err))
  } finally {
    busyTool.value = ''
  }
}

onMounted(load)
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

.wsl-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-bottom: 8px;
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
  display: flex;
  align-items: center;
  gap: 12px;
}

.empty-row {
  padding: 12px 0;
  font-size: 13px;
  color: var(--tertiary);
}

.set-hint {
  margin-top: 10px;
  font-size: 12px;
  color: var(--tertiary);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--secondary);
  word-break: break-all;
}

.badge {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 6px;
}

.badge-ok {
  color: var(--success, #2ea043);
  background: color-mix(in srgb, var(--success, #2ea043) 14%, transparent);
}

.badge-wsl2 {
  color: var(--accent, #4c8dff);
  background: color-mix(in srgb, var(--accent, #4c8dff) 14%, transparent);
  font-weight: 600;
}

.badge-missing {
  color: var(--tertiary);
  background: var(--control);
}
</style>