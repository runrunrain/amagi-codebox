<template>
  <div class="set-card" v-if="health && health.available">
    <h2>Docker Desktop ↔ WSL 集成</h2>
    <p class="set-sub">
      Docker Desktop 的 WSL2 集成共享挂载在冷启动竞态下可能挂坏（docker-desktop-user-distro
      变为 0 字节空文件 → WSL 集成无限弹窗报错）。一键自愈仅重启 docker-desktop
      工具发行版，Ubuntu 发行版与 CodeBox 会话不受影响；冷启动等待最长 3-4 分钟。
    </p>

    <div class="wsl-meta">
      <span class="mono">挂载: {{ mountText }}</span>
      <span class="badge" :class="mountBadgeClass">{{ mountBadgeText }}</span>
      <span class="mono">引擎: {{ health.engineReady ? `就绪 ${health.engineVersion}` : '未就绪' }}</span>
      <span class="mono">Desktop 进程: {{ health.desktopProcessesRunning }}</span>
      <span class="mono">工具发行版: {{ health.toolDistroPresent ? (health.toolDistroRunning ? '运行中' : '已停止') : '未注册' }}</span>
    </div>

    <div v-if="health.issues.length" class="issue-list">
      <div v-for="(issue, i) in health.issues" :key="i" class="issue-row">
        <span class="badge" :class="health.recommendSelfHeal ? 'badge-bad' : 'badge-missing'">问题</span>
        <span class="issue-text">{{ issue }}</span>
      </div>
    </div>

    <div class="heal-actions">
      <AppButton variant="primary" size="small" :disabled="busy" @click="onHealClick">
        {{ busy ? '自愈执行中…' : healArmed ? '确认执行自愈？' : '一键自愈' }}
      </AppButton>
      <AppButton size="small" :disabled="busy" @click="load">重新检测</AppButton>
    </div>

    <p v-if="busy" class="set-hint">
      正在执行自愈序列：全量退出 Docker Desktop → 终止 docker-desktop 工具发行版 →
      全新启动 → 等待引擎就绪（冷启动可达 3-4 分钟，请勿关闭应用）→ 复核集成挂载…
    </p>

    <div v-if="report" class="heal-report">
      <p class="heal-summary" :class="report.success ? 'ok' : 'bad'">{{ report.summary }}</p>
      <div v-for="(step, i) in report.steps" :key="i" class="step-row">
        <span class="badge" :class="step.ok ? 'badge-ok' : 'badge-bad'">{{ stepLabel(step.name) }}</span>
        <span class="mono step-detail">{{ step.detail }}</span>
        <span class="mono step-dur">{{ step.durationMs }}ms</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { getDockerWSLHealth, selfHealDockerWSLIntegration, type DockerWSLHealth, type DockerWSLSelfHealReport } from '../../api/dockerwsl'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'

const { showSuccess, showError, showInfo } = useToast()

const health = ref<DockerWSLHealth | null>(null)
const report = ref<DockerWSLSelfHealReport | null>(null)
const busy = ref(false)
// 自愈影响面大（重启 Docker Desktop / 容器），按钮采用两段确认：首击进入待确认态。
const healArmed = ref(false)
let disarmTimer: ReturnType<typeof setTimeout> | undefined

const STEP_LABELS: Record<string, string> = {
  'exit-desktop': '① 全量退出',
  'terminate-tool-distro': '② 终止工具发行版',
  'start-desktop': '③ 全新启动',
  'wait-engine': '④ 等待引擎就绪',
  'verify-integration': '⑤ 复核集成',
}

function stepLabel(name: string): string {
  return STEP_LABELS[name] || name
}

const mountText = ref('')
const mountBadgeText = ref('')
const mountBadgeClass = ref('badge-missing')

function formatBytes(n: number): string {
  if (n < 0) return '不可见'
  return n.toLocaleString('en-US') + ' 字节'
}

function applyHealth(h: DockerWSLHealth) {
  health.value = h
  switch (h.integrationMountState) {
    case 'ok':
      mountText.value = formatBytes(h.integrationMountBytes)
      mountBadgeText.value = '健康'
      mountBadgeClass.value = 'badge-ok'
      break
    case 'broken':
      mountText.value = '0 字节空文件'
      mountBadgeText.value = '已挂坏'
      mountBadgeClass.value = 'badge-bad'
      break
    case 'absent':
      mountText.value = '不可见'
      mountBadgeText.value = '未建立'
      mountBadgeClass.value = 'badge-missing'
      break
    default:
      mountText.value = '未知'
      mountBadgeText.value = '未知'
      mountBadgeClass.value = 'badge-missing'
  }
}

async function load() {
  try {
    applyHealth(await getDockerWSLHealth())
  } catch (err) {
    console.error('load dockerwsl health:', err)
    // 探测失败时隐藏卡片（非 Docker 环境静默）。
    health.value = null
  }
}

function onHealClick() {
  if (busy.value) return
  if (!healArmed.value) {
    healArmed.value = true
    disarmTimer = setTimeout(() => (healArmed.value = false), 5000)
    return
  }
  clearTimeout(disarmTimer)
  healArmed.value = false
  void heal()
}

async function heal() {
  if (busy.value) return
  busy.value = true
  report.value = null
  showInfo('Docker WSL 集成自愈已开始（冷启动等待最长 3-4 分钟）')
  try {
    const rep = await selfHealDockerWSLIntegration()
    report.value = rep
    if (rep.success) {
      showSuccess('Docker WSL 集成自愈完成')
    } else {
      showError(rep.summary || '自愈未完全成功，请查看步骤回执')
    }
    await load()
  } catch (err: any) {
    showError('自愈失败: ' + (err?.message || err))
  } finally {
    busy.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => clearTimeout(disarmTimer))
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
  align-items: center;
  gap: 12px 16px;
  margin-bottom: 10px;
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
  flex-shrink: 0;
}

.badge-ok {
  color: var(--success, #2ea043);
  background: color-mix(in srgb, var(--success, #2ea043) 14%, transparent);
}

.badge-bad {
  color: var(--danger, #e5484d);
  background: color-mix(in srgb, var(--danger, #e5484d) 14%, transparent);
  font-weight: 600;
}

.badge-missing {
  color: var(--tertiary);
  background: var(--control);
}

.issue-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 10px;
}

.issue-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.issue-text {
  font-size: 13px;
  color: var(--secondary);
  word-break: break-all;
}

.heal-actions {
  display: flex;
  gap: 10px;
  margin: 6px 0;
}

.set-hint {
  margin-top: 10px;
  font-size: 12px;
  color: var(--tertiary);
}

.heal-report {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  border-top: 1px solid var(--separator);
  padding-top: 10px;
}

.heal-summary {
  font-size: 13px;
  font-weight: 600;
}

.heal-summary.ok {
  color: var(--success, #2ea043);
}

.heal-summary.bad {
  color: var(--danger, #e5484d);
}

.step-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.step-detail {
  flex: 1;
}

.step-dur {
  color: var(--tertiary);
  flex-shrink: 0;
}
</style>
