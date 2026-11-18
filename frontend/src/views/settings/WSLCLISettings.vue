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
          Node: {{ status.nodeVersion || '未安装 (安装 CLI 时会自动安装 Node 22)' }}
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
              {{ busyTool === t.tool ? (t.installed ? '升级中…' : '安装中…') : (t.installed ? '升级' : '安装到 WSL') }}
            </AppButton>
          </div>
        </div>
      </div>

      <p v-if="busyTool" class="set-hint">
        正在 WSL 内{{ toolInstalled(busyTool) ? '升级' : '安装' }} {{ displayName(busyTool) }}，首次可能需要安装 Node 运行时，请稍候…
      </p>

      <section v-if="supply" class="supply-section" aria-labelledby="wsl-sync-heading">
        <h3 id="wsl-sync-heading">WSL 配置同步</h3>
        <div class="sync-mode" :class="{ 'sync-mode-active': supply.unified }">
          <div class="sync-copy">
            <div class="item-title">
              <strong>Windows → {{ supply.distro }}</strong>
              <span class="badge" :class="{ 'badge-ok': supply.unified }">
                {{ supply.unified ? '自动纳入已启用' : '需手动选择新项目' }}
              </span>
            </div>
            <p v-if="supply.unified" class="item-note">
              新项目会自动纳入同步。停止纳入后，已授权的项目和现有副本会保留。要停止跟随单个项目，请选择「保留本地」。
            </p>
            <p v-else class="item-note">
              统一管理会自动纳入 Claude Code、Codex 的受支持配置与资源，以及视觉清单。MCP 环境变量和视觉清单可能包含密钥，副本以 0600 权限写入 WSL。
            </p>
          </div>
          <AppButton v-if="supply.unified" variant="outline" size="small" :disabled="unifiedBusy" @click="disableUnified">
            {{ unifiedBusy ? '处理中…' : '停止自动纳入新项' }}
          </AppButton>
          <AppButton v-else variant="primary" size="small" :disabled="unifiedBusy" @click="enableUnified">
            {{ unifiedBusy ? '启用中…' : '启用统一管理' }}
          </AppButton>
        </div>
        <p v-if="supply.pendingPush" class="sync-pending" role="status">
          有变更待同步，将在 WSL 空闲时或下次启动会话时推送。
        </p>

        <div class="sync-list">
          <details class="sync-group">
            <summary>
              <span class="group-name">共享技能</span>
              <span class="badge" :class="faceBadgeClass(skillsFace?.state || 'unchecked')">
                {{ skillsFace?.state === 'synced' ? '已链接' : faceStateText(skillsFace?.state || 'unchecked') }}
              </span>
            </summary>
            <div class="group-content group-action">
              <div>
                <p v-if="skillsFace?.state === 'synced'" class="item-note">{{ skillsFace.detail }}</p>
                <p v-else class="item-note">
                  WSL 的 <code>~/.agents/skills</code> 通过符号链接读取 Windows 技能目录。首次建立链接时会备份 WSL 原有目录。
                </p>
                <p v-if="skillsFace?.detail && skillsFace.state !== 'synced'" class="item-note">{{ skillsFace.detail }}</p>
                <p v-if="sharedRoot?.backup" class="item-note">旧目录已备份至 <code>{{ sharedRoot.backup }}</code></p>
                <p v-if="skillsFace?.lastCheck" class="item-note">检查于 {{ formatTime(skillsFace.lastCheck) }}</p>
              </div>
              <AppButton variant="outline" size="small" :disabled="sharedRootBusy" @click="ensureSharedRoot">
                {{ sharedRootBusy ? '核验中…' : '检查并修复' }}
              </AppButton>
            </div>
          </details>

          <template v-for="(st, cli) in cliStatus" :key="cli">
            <details v-if="st" class="sync-group">
              <summary>
                <span class="group-name">{{ displayName(cli) }}</span>
                <span v-if="!st.available" class="badge">不可用</span>
                <span v-else-if="cliIssueCounts[cli]" class="badge badge-warn">{{ cliIssueCounts[cli] }} 项需查看</span>
                <span v-else class="group-meta">配置文件与资源</span>
              </summary>
              <div v-if="!st.available" class="group-content">
                <p class="item-note">{{ st.reason || '配置导入不可用' }}</p>
              </div>
              <div v-else class="group-content cli-content">
                <section class="cli-settings">
                  <div class="section-heading">
                    <h4>配置文件</h4>
                    <AppButton v-if="st.units.some(u => u.actions.includes('apply'))" variant="outline" size="small" :disabled="cliBusy" @click="applyCli(cli)">
                      应用安全更新
                    </AppButton>
                  </div>
                  <div v-for="u in st.units" :key="u.unit" class="sync-item">
                    <div class="item-main">
                      <div class="item-title">
                        <code>{{ u.relPath }}</code>
                        <span class="badge" :class="unitBadgeClass(u.state)">{{ unitStateText(u.state) }}</span>
                      </div>
                      <p class="item-note">{{ u.detail }}</p>
                    </div>
                    <div class="item-actions">
                      <AppButton v-if="u.actions.includes('preview')" size="small" :disabled="cliBusy" :aria-expanded="cliPreviews[cli]?.unit === u.unit" @click="previewCliUnit(cli, u.unit)">
                        {{ cliPreviews[cli]?.unit === u.unit ? '收起预览' : '预览' }}
                      </AppButton>
                      <AppButton v-if="u.actions.includes('keep-local')" variant="outline" size="small" :disabled="cliBusy" @click="keepCliUnit(cli, u.unit)">
                        保留本地
                      </AppButton>
                    </div>

                    <div v-if="cliPreviews[cli]?.unit === u.unit" class="item-preview">
                      <p v-if="!cliPreviews[cli].keys?.length" class="item-note">
                        {{ previewBlockNotes[cliPreviews[cli].blockedReason || ''] || '没有可预览的配置项。' }}
                      </p>
                      <ul v-else class="key-list">
                        <li v-for="k in cliPreviews[cli].keys" :key="k.key">
                          <div class="item-title">
                          <label v-if="k.importable || k.selected">
                            <input type="checkbox" :checked="cliKeySelection[cli]?.includes(k.key)" :disabled="cliBusy || !k.importable" @change="toggleCliKey(cli, k.key)" />
                              <code>{{ k.key }}</code>
                            </label>
                            <code v-else>{{ k.key }}</code>
                            <span class="badge" :class="keyBadgeClass(k.action)">{{ previewActionText(k.action) }}</span>
                          </div>
                        <p v-if="k.before || k.after" class="item-note">
                          {{ k.before === k.after ? k.before : `${k.before || '—'} → ${k.after || '—'}` }}
                        </p>
                          <p v-if="k.reason" class="item-note">{{ k.reason }}</p>
                        </li>
                      </ul>
                      <p v-for="note in cliPreviews[cli].notes" :key="note" class="item-note">{{ note }}</p>
                      <div class="item-actions preview-actions">
                        <AppButton v-if="u.actions.includes('takeover') && cliPreviews[cli].targetHash" variant="outline" size="small" :disabled="cliBusy" @click="takeOverCliUnit(cli, u.unit, cliPreviews[cli].targetHash)">
                          确认接管
                        </AppButton>
                        <AppButton v-if="cliKeySelection[cli]?.length || cliPreviews[cli].keys?.some(key => key.selected)" :variant="cliKeySelection[cli]?.length ? 'primary' : 'outline'" size="small" :disabled="cliBusy" @click="importCliKeys(cli, u.unit)">
                          {{ cliKeySelection[cli]?.length ? `导入所选（${cliKeySelection[cli].length}）` : '撤回全部导入' }}
                        </AppButton>
                      </div>
                    </div>
                  </div>
                </section>

                <details class="resource-group">
                  <summary>
                    <span class="group-name">用户资源</span>
                    <span v-if="st.resources.scanRunning" class="badge badge-warn" role="status">
                      扫描中 {{ st.resources.scanScanned }}/{{ st.resources.scanTotal }}
                    </span>
                    <span v-else-if="st.resources.scanIncomplete" class="badge badge-warn">扫描未完成</span>
                    <span v-else class="group-meta">{{ st.resources.skills?.length ?? 0 }} 项</span>
                  </summary>
                  <div class="resource-content">
                    <div class="section-heading resource-toolbar">
                      <p class="item-note">选择要同步到 WSL 的资源。导入前会做兼容性检查。</p>
                      <div class="item-actions">
                        <AppButton variant="outline" size="small" :disabled="cliBusy || st.resources.scanRunning" @click="checkCliResources(cli)">检查资源</AppButton>
                        <AppButton v-if="selectedResources(cli).length" variant="primary" size="small" :disabled="cliBusy" @click="applyCliResources(cli)">
                          导入所选资源（{{ selectedResources(cli).length }}）
                        </AppButton>
                      </div>
                    </div>
                    <p v-if="!st.resources.skills?.length && !st.resources.scanRunning" class="item-note">
                      {{ st.resources.reason || '暂无可显示的资源。' }}
                    </p>
                    <div v-for="s in st.resources.skills" :key="s.name" class="sync-item">
                      <div class="item-main">
                        <div class="item-title">
                          <label v-if="s.selectable">
                            <input type="checkbox" :checked="cliResSelection[cli]?.includes(s.name)" :disabled="cliBusy" @change="toggleCliRes(cli, s.name)" />
                            <code>{{ s.name }}</code>
                          </label>
                          <code v-else>{{ s.name }}</code>
                          <span class="badge" :class="skillBadgeClass(s.state)">{{ skillStateText(s.state) }}</span>
                        </div>
                        <p class="item-note">{{ resourceDetail(s.state, s.detail) }}<template v-if="s.files"> · {{ s.files }} 个文件</template></p>
                        <details v-if="s.incompatible?.length" class="compat-details">
                          <summary>查看 {{ s.incompatible.length }} 项不兼容内容</summary>
                          <ul>
                            <li v-for="it in s.incompatible" :key="it">{{ it }}</li>
                          </ul>
                        </details>
                      </div>
                      <div class="item-actions">
                        <AppButton v-if="s.actions.includes('preview')" size="small" :disabled="cliBusy" :aria-expanded="cliResPreviews[cli]?.name === s.name" @click="previewCliResource(cli, s.name)">
                          {{ cliResPreviews[cli]?.name === s.name ? '收起预览' : '预览' }}
                        </AppButton>
                        <AppButton v-if="s.actions.includes('apply')" variant="outline" size="small" :disabled="cliBusy" @click="applyCliResources(cli, [s.name])">应用安全更新</AppButton>
                        <AppButton v-if="s.actions.includes('keep-local')" variant="outline" size="small" :disabled="cliBusy" @click="keepCliResource(cli, s.name)">保留本地</AppButton>
                      </div>
                      <div v-if="cliResPreviews[cli]?.name === s.name" class="item-preview">
                        <p class="item-note">{{ previewActionText(cliResPreviews[cli].action) }}<template v-if="cliResPreviews[cli].excluded"> · 排除 {{ cliResPreviews[cli].excluded }} 项</template></p>
                        <p v-for="note in cliResPreviews[cli].notes" :key="note" class="item-note">{{ note }}</p>
                        <div v-if="s.actions.includes('takeover') && cliResPreviews[cli].targetHash" class="item-actions preview-actions">
                          <AppButton variant="outline" size="small" :disabled="cliBusy" @click="takeOverCliResource(cli, s.name, cliResPreviews[cli].targetHash)">确认接管</AppButton>
                        </div>
                      </div>
                    </div>
                  </div>
                </details>
              </div>
            </details>
          </template>

          <details v-if="ocLayerFace || ocMcp.length" class="sync-group">
            <summary>
              <span class="group-name">OpenCode</span>
              <span v-if="ocMcp.some(e => e.globalConflict)" class="badge badge-warn">有同名 MCP</span>
              <span v-if="ocLayerFace" class="badge" :class="faceBadgeClass(ocLayerFace.state)">
                {{ ocLayerFace.state === 'unchecked' ? '未写入' : faceStateText(ocLayerFace.state) }}
              </span>
            </summary>
            <div class="group-content">
              <p v-if="ocLayerFace" class="item-note">
                附加配置{{ ocLayerFace.state === 'unchecked' ? '将在启动 OpenCode 的 WSL 会话时写入。' : `：${ocLayerFace.detail || faceStateText(ocLayerFace.state)}` }}
              </p>
              <template v-if="ocMcp.length">
                <div class="section-heading open-code-heading">
                  <h4>预设 MCP</h4>
                  <AppButton v-if="ocOverrideOptions" variant="primary" size="small" :disabled="cliBusy || !ocOverrideChanged" @click="saveOcSelection">保存选择</AppButton>
                </div>
                <p v-if="ocOverrideOptions" class="item-note">同名项默认使用 WSL 全局配置。勾选覆盖后，下次启动使用预设版本。</p>
                <div v-for="entry in ocMcp" :key="entry.name" class="sync-item">
                  <div class="item-main">
                    <div class="item-title">
                      <code>{{ entry.name }}</code>
                      <span v-if="entry.remote" class="badge">远程</span>
                      <span v-else-if="entry.needsAdapt" class="badge badge-warn">需手动适配</span>
                      <span v-else class="badge">stdio {{ entry.nativeCommand }}</span>
                      <span v-if="entry.globalConflict" class="badge badge-warn">与 WSL 全局同名</span>
                    </div>
                    <p v-if="entry.reason" class="item-note">{{ entry.reason }}</p>
                  </div>
                  <label v-if="entry.globalConflict && !entry.needsAdapt" class="item-note override-option">
                    <input type="checkbox" :checked="ocOverride.includes(entry.name)" :disabled="cliBusy" @change="toggleOcOverride(entry.name)" />
                    使用预设覆盖
                  </label>
                </div>
              </template>
              <p v-else class="item-note">暂无预设 MCP。</p>
            </div>
          </details>

          <details v-if="sharedFaces.length" class="sync-group">
            <summary>
              <span class="group-name">视觉与多模态</span>
              <span v-if="visionAwaitingConsent" class="badge badge-warn">视觉清单待授权</span>
              <span v-if="sharedFaceIssue" class="badge" :class="faceBadgeClass(sharedFaceIssue.state)">{{ faceStateText(sharedFaceIssue.state) }}</span>
            </summary>
            <div class="group-content">
              <div v-for="face in sharedFaces" :key="face.key" class="sync-item">
                <div class="item-main">
                  <div class="item-title">
                    <strong>{{ face.label }}</strong>
                    <span class="badge" :class="faceBadgeClass(face.state)">{{ faceStateText(face.state) }}</span>
                  </div>
                  <p v-if="face.key === 'vision-export' && visionAwaitingConsent" class="item-note">
                    视觉清单含 API 密钥副本。授权后，副本将以 0600 权限写入 WSL。
                  </p>
                  <p v-else-if="face.detail !== faceStateText(face.state)" class="item-note">{{ face.detail }}</p>
                  <p v-if="face.lastCheck" class="item-note">检查于 {{ formatTime(face.lastCheck) }}</p>
                </div>
                <AppButton v-if="face.key === 'vision-export' && visionAwaitingConsent" variant="primary" size="small" :disabled="consentBusy" @click="grantConsent">
                  {{ consentBusy ? '授权中…' : '同意共享到 WSL' }}
                </AppButton>
              </div>
            </div>
          </details>
        </div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { getWSLCLIStatus, installCLIToWSL, updateCLIInWSL, type WSLStatus } from '../../api/wslsetup'
import {
  getWSLConfigSupplyStatus,
  grantWSLVisionConsent,
  enableWSLUnifiedSupply,
  disableWSLUnifiedSupply,
  type WSLConfigSupplyStatus,
  ensureWSLSharedSkillsRoot,
  getWSLCliSupplyStatus,
  previewWSLCliConfigSupply,
  importWSLCliConfigKeys,
  applyWSLCliConfigSupply,
  keepWSLCliConfigUnitLocal,
  takeOverWSLCliConfigUnit,
  checkWSLCliResourcesSupply,
  previewWSLCliResourcesSupply,
  applyWSLCliResourcesSupply,
  keepWSLCliResourceLocal,
  takeOverWSLCliResource,
  getWSLOpenCodeMCPPreview,
  setWSLOpenCodeMCPSelection,
  type WSLConfigUnitPreview,
  type WSLSharedSkillsRootStatus,
  type WSLSkillPreview,
  type WSLCliSupplyStatus,
  type WSLOpenCodeMCPEntry,
} from '../../api/wslSupply'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import { cliImportOutcomeMessage } from './wslSupplyMessages'
import { errorMessage } from '../../utils/format'

const props = withDefaults(defineProps<{ refreshRevision?: number }>(), { refreshRevision: 0 })
const { showSuccess, showError, showInfo } = useToast()

// SA-17：卸载后停止扫描轮询链（setTimeout 链不再触发新请求）。
let alive = true
const scanTimers = new Map<string, ReturnType<typeof setTimeout>>()
onUnmounted(() => {
  alive = false
  for (const timer of scanTimers.values()) clearTimeout(timer)
})

const status = ref<WSLStatus | null>(null)
const busyTool = ref<string>('')
// P3-S1「配置状态」区：脱敏快照（只读，不触发同步）。
const supply = ref<WSLConfigSupplyStatus | null>(null)
const consentBusy = ref(false)
// Only show the panel when WSL is relevant (available), or while first loading.
const showPanel = ref(true)

const NAMES: Record<string, string> = {
  claude: 'Claude Code',
  opencode: 'OpenCode',
  codex: 'Codex',
  pi: 'Pi',
  omp: 'Oh My Pi (OMP)',
}
function displayName(tool: string): string {
  return NAMES[tool] || tool
}

// 工具行当前安装态（升级/安装文案与动作分派用）。
function toolInstalled(tool: string): boolean {
  return !!status.value?.tools?.find((t) => t.tool === tool)?.installed
}

async function load() {
  try {
    const st = await getWSLCLIStatus()
    status.value = st
    showPanel.value = st.available || !!st.reason
    if (st.available) {
      await Promise.all([refreshStatus(), loadCliSupply('claude'), loadCliSupply('codex'), loadOcMcp()])
    }
  } catch (err) {
    console.error('load WSL status:', err)
    showPanel.value = false
  }
}

async function install(tool: string) {
  if (busyTool.value) return
  const installed = toolInstalled(tool)
  const action = installed ? '升级' : '安装'
  busyTool.value = tool
  showInfo(installed ? `正在升级 WSL 内的 ${displayName(tool)}…` : `正在将 ${displayName(tool)} 安装到 WSL…`)
  try {
    const res = installed ? await updateCLIInWSL(tool) : await installCLIToWSL(tool)
    if (res.success) {
      if (installed) {
        showSuccess(res.alreadyOK
          ? `${displayName(tool)} 已是 WSL 内最新版本 (${res.version})`
          : `${displayName(tool)} 已在 WSL 内升级 (${res.version})`)
      } else if (res.alreadyOK) {
        showSuccess(`${displayName(tool)} 已在 WSL 中 (${res.version})`)
      } else {
        showSuccess(`${displayName(tool)} 已安装到 WSL (${res.version})`)
      }
      await load()
    } else {
      showError(`${action}失败: ${res.error || '未知错误'}`)
    }
  } catch (err: unknown) {
    showError(`${action}失败: ` + errorMessage(err))
  } finally {
    busyTool.value = ''
  }
}

const sharedFaces = computed(() => supply.value?.faces.filter((face) =>
  face.key === 'vision-export' || face.key === 'modality-kb',
) ?? [])
const visionAwaitingConsent = computed(() => !supply.value?.unified &&
  sharedFaces.value.some((face) => face.key === 'vision-export' && face.state === 'awaiting-consent'),
)
const sharedFaceIssue = computed(() => sharedFaces.value.find((face) =>
  ['failed', 'frozen', 'pending-op'].includes(face.state),
))
const ocLayerFace = computed(() => supply.value?.faces.find((face) => face.key === 'opencode-layer'))

function faceStateText(state: string): string {
  switch (state) {
    case 'synced': return '已同步'
    case 'stale': return '待更新'
    case 'failed': return '失败'
    case 'frozen': return '冻结'
    case 'awaiting-consent': return '待授权'
    case 'consented': return '已授权，待发布'
    case 'scanning': return '扫描中'
    case 'incompatible': return '有不兼容项'
    case 'pending-op': return '待确认'
    case 'host-missing': return '技能目录不可访问'
    default: return '未检查'
  }
}
function faceBadgeClass(state: string): string {
  switch (state) {
    case 'synced': return 'badge-ok'
    case 'failed': return 'badge-error'
    case 'stale': case 'awaiting-consent': case 'consented': case 'scanning':
    case 'pending-op': case 'host-missing': case 'frozen': case 'incompatible': return 'badge-warn'
    default: return ''
  }
}
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString()
}

async function grantConsent(): Promise<void> {
  if (consentBusy.value) return
  consentBusy.value = true
  try {
    await grantWSLVisionConsent()
    showSuccess('已授权向 WSL 共享视觉清单（密钥副本将以 0600 写入）')
    await refreshStatus()
  } catch (err: unknown) {
    showError('授权失败: ' + errorMessage(err))
  } finally {
    consentBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// P2-B：统一配置管理（一次启用 → 启动/保存/对账自动同步）
// ---------------------------------------------------------------------------
const unifiedBusy = ref(false)

async function enableUnified(): Promise<void> {
  if (unifiedBusy.value) return
  unifiedBusy.value = true
  try {
    const outcomes = await enableWSLUnifiedSupply()
    const skipped = outcomes.filter((o) => o.outcome !== 'applied' && o.outcome !== 'fresh')
    if (skipped.length) {
      showInfo(`已启用统一管理：${outcomes.length - skipped.length} 项授权，${skipped.length} 项跳过（保留本地等，详见逐单元状态）`)
    } else {
      showSuccess('已启用统一管理（此后启动/保存/对账自动同步；WSL 手改条目冻结保留）')
    }
    await load()
  } catch (err: unknown) {
    showError('启用失败: ' + errorMessage(err))
  } finally {
    unifiedBusy.value = false
  }
}

async function disableUnified(): Promise<void> {
  if (unifiedBusy.value) return
  unifiedBusy.value = true
  try {
    await disableWSLUnifiedSupply()
    showInfo('已停止自动纳入新项目')
    await refreshStatus()
  } catch (err: unknown) {
    showError('停止失败: ' + errorMessage(err))
  } finally {
    unifiedBusy.value = false
  }
}

function unitStateText(state: string): string {
  switch (state) {
    case 'synced': return '已同步'
    case 'stale': return '待更新'
    case 'frozen': return '冻结'
    case 'partial': return '部分保留'
    case 'detached': return '保留本地'
    case 'awaiting-consent': return '待启用'
    case 'failed': return '失败'
    default: return '未检查'
  }
}
function unitBadgeClass(state: string): string {
  switch (state) {
    case 'synced': return 'badge-ok'
    case 'failed': return 'badge-error'
    case 'stale': case 'partial': return 'badge-warn'
    case 'frozen': case 'detached': case 'awaiting-consent': return 'badge-warn'
    default: return ''
  }
}
function keyBadgeClass(action: string): string {
  switch (action) {
    case 'add': case 'update': case 'remove': return 'badge-warn'
    case 'no-change': return 'badge-ok'
    case 'conflict': case 'keep-frozen': case 'blocked': return 'badge-warn'
    default: return ''
  }
}

function previewActionText(action: string): string {
  switch (action) {
    case 'add': return '新增'
    case 'update': return '更新'
    case 'remove': return '移除'
    case 'write': return '写入 WSL'
    case 'none': case 'no-change': return '无变更'
    case 'keep-frozen': return '保留本地修改'
    case 'conflict': return '有冲突'
    case 'unmanaged-existing': return 'WSL 已有同名项'
    case 'blocked': return '暂不可写入'
    default: return '操作待确认'
  }
}

const previewBlockNotes: Record<string, string> = {
  'source-unavailable': 'Windows 侧配置暂不可用。',
  'unmanaged-existing': 'WSL 侧已有同名文件，请确认是否接管。',
  'local-deletion-frozen': '该配置已在 WSL 本地删除。',
  'local-modification-frozen': '该配置在 WSL 有本地修改。',
  'both-changed-frozen': 'Windows 与 WSL 两侧的配置均有修改。',
  'conversion-blocked': '部分 Windows 配置无法安全转换。',
  'detached': '此配置已选择保留本地。',
}

async function refreshStatus(): Promise<void> {
  try {
    supply.value = await getWSLConfigSupplyStatus()
  } catch {
    // 只读快照失败：保持现状（下次加载重试）
  }
}

const sharedRoot = ref<WSLSharedSkillsRootStatus | null>(null)
const sharedRootBusy = ref(false)
const skillsFace = computed(() => supply.value?.faces.find((f) => f.key === 'skills') ?? null)

async function ensureSharedRoot(): Promise<void> {
  if (sharedRootBusy.value) return
  sharedRootBusy.value = true
  try {
    sharedRoot.value = await ensureWSLSharedSkillsRoot()
    if (sharedRoot.value.state === 'failed') {
      showError('共享技能核验失败：' + (sharedRoot.value.detail || '未知原因'))
    } else if (sharedRoot.value.backup) {
      showSuccess('符号链接已就绪；旧目录备份至 ' + sharedRoot.value.backup)
    }
    await refreshStatus()
  } catch (err: unknown) {
    showError('检查失败: ' + errorMessage(err))
  } finally {
    sharedRootBusy.value = false
  }
}

function skillStateText(state: string): string {
  switch (state) {
    case 'importable': return '可导入'
    case 'supplied': return '已同步'
    case 'stale': return '待更新'
    case 'frozen': return '冻结'
    case 'incompatible': return '不兼容'
    case 'scan-incomplete': return '扫描未完成'
    case 'empty': return '空目录'
    case 'detached': return '保留本地'
    case 'source-deleted': return '源已删除'
    case 'awaiting-consent': return '待写入'
    case 'failed': return '失败'
    default: return '未检查'
  }
}
function skillBadgeClass(state: string): string {
  switch (state) {
    case 'supplied': return 'badge-ok'
    case 'failed': return 'badge-error'
    case 'stale': case 'frozen': case 'scan-incomplete':
    case 'incompatible': case 'source-deleted': case 'awaiting-consent': return 'badge-warn'
    case 'importable': return 'badge-wsl2'
    default: return ''
  }
}

function resourceDetail(state: string, detail: string): string {
  if (state === 'supplied') return '已通过静态检查'
  if (state === 'importable') return '通过兼容性检查'
  if (state === 'source-deleted') return 'Windows 来源已移除，WSL 副本仍保留。'
  return detail
}

const cliBusy = ref(false)
const cliStatus = ref<Record<string, WSLCliSupplyStatus | null>>({ claude: null, codex: null })
const cliIssueCounts = computed(() => Object.fromEntries(Object.entries(cliStatus.value).map(([cli, st]) => {
  const units = st?.units.filter((unit) => ['failed', 'frozen', 'partial'].includes(unit.state)).length ?? 0
  const resources = st?.resources.skills?.filter((resource) =>
    ['failed', 'frozen', 'incompatible'].includes(resource.state),
  ).length ?? 0
  return [cli, units + resources]
})))
const cliPreviews = ref<Record<string, WSLConfigUnitPreview | undefined>>({})
const cliKeySelection = ref<Record<string, string[]>>({})
const cliResSelection = ref<Record<string, string[]>>({})
const cliResPreviews = ref<Record<string, WSLSkillPreview | undefined>>({})
const ocMcp = ref<WSLOpenCodeMCPEntry[]>([])
const ocOverride = ref<string[]>([])
const ocOverrideOptions = computed(() => ocMcp.value.some((entry) => entry.globalConflict && !entry.needsAdapt))
const ocOverrideChanged = computed(() =>
  ocMcp.value.some((entry) => entry.overrideGlobal !== ocOverride.value.includes(entry.name)),
)

async function loadCliSupply(cli: string): Promise<void> {
  if (!alive) return
  try {
    const st = await getWSLCliSupplyStatus(cli)
    if (!alive) return
    cliStatus.value[cli] = st
    clearTimeout(scanTimers.get(cli))
    scanTimers.delete(cli)
    if (st.resources.scanRunning) {
      scanTimers.set(cli, setTimeout(() => {
        scanTimers.delete(cli)
        void loadCliSupply(cli)
      }, 800))
    }
  } catch {
    // 保留上次成功读取的状态。
  }
}

function toggleCliKey(cli: string, key: string): void {
  const selected = cliKeySelection.value[cli] ?? []
  cliKeySelection.value[cli] = selected.includes(key) ? selected.filter((item) => item !== key) : [...selected, key]
}

async function previewCliUnit(cli: string, unit: string): Promise<void> {
  if (cliBusy.value) return
  if (cliPreviews.value[cli]?.unit === unit) {
    delete cliPreviews.value[cli]
    return
  }
  cliBusy.value = true
  try {
    const rows = await previewWSLCliConfigSupply(cli)
    const row = rows.find((item) => item.unit === unit)
    if (!row) {
      showError('暂时无法预览此配置文件')
      return
    }
    cliPreviews.value[cli] = row
    cliKeySelection.value[cli] = (row.keys ?? []).filter((key) => key.selected).map((key) => key.key)
  } catch (err: unknown) {
    showError('预览失败：' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function importCliKeys(cli: string, unit: string): Promise<void> {
  // SA-03：空选择是合法的显式撤回入口（后端按来源删除语义移除托管条目）；
  // 零写入不报导入成功（提示逻辑见 wslSupplyMessages，单测覆盖）。
  const keys = cliKeySelection.value[cli] ?? []
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const outcomes = await importWSLCliConfigKeys(cli, unit, keys)
    if (outcomes.some((outcome) => outcome.outcome === 'applied')) delete cliPreviews.value[cli]
    const msg = cliImportOutcomeMessage(keys.length, outcomes)
    if (msg.kind === 'success') {
      showSuccess(msg.text)
    } else {
      showInfo(msg.text)
    }
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('导入失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function applyCli(cli: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const outcomes = await applyWSLCliConfigSupply(cli)
    delete cliPreviews.value[cli]
    const failed = outcomes.filter((o) => o.outcome === 'failed' || o.outcome === 'blocked')
    if (failed.length) {
      showInfo(`安全更新完成：${failed.length} 个单元保持现状（详见预览）`)
    } else {
      showSuccess('安全更新完成（冻结/未托管条目保持 WSL 现状）')
    }
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('应用失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function keepCliUnit(cli: string, unit: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await keepWSLCliConfigUnitLocal(cli, unit)
    if (cliPreviews.value[cli]?.unit === unit) delete cliPreviews.value[cli]
    showInfo('已保留 WSL 本地配置（停跟随）')
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('停跟随失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function takeOverCliUnit(cli: string, unit: string, token: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await takeOverWSLCliConfigUnit(cli, unit, token)
    delete cliPreviews.value[cli]
    showSuccess('已按预览确认接管（托管域已按 Windows 当前内容重建基准）')
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('接管失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function checkCliResources(cli: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await checkWSLCliResourcesSupply(cli)
    delete cliResPreviews.value[cli]
    // 重拉完整状态（后台扫描分批推进，轮询至收敛）。
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('检查失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function previewCliResource(cli: string, name: string): Promise<void> {
  if (cliBusy.value) return
  if (cliResPreviews.value[cli]?.name === name) {
    delete cliResPreviews.value[cli]
    return
  }
  cliBusy.value = true
  try {
    const rows = await previewWSLCliResourcesSupply(cli)
    const row = rows.find((item) => item.name === name)
    if (!row) {
      showError('暂时无法预览此资源')
      return
    }
    cliResPreviews.value[cli] = row
  } catch (err: unknown) {
    showError('预览失败：' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

function selectedResources(cli: string): string[] {
  const selected = cliResSelection.value[cli]
  if (!selected?.length) return []
  const resources = cliStatus.value[cli]?.resources.skills ?? []
  return resources.filter((resource) => resource.selectable && selected.includes(resource.name)).map((resource) => resource.name)
}

function toggleCliRes(cli: string, name: string): void {
  const selected = cliResSelection.value[cli] ?? []
  cliResSelection.value[cli] = selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name]
}

async function applyCliResources(cli: string, only?: string[]): Promise<void> {
  const rels = only ?? selectedResources(cli)
  if (!rels.length) {
    showInfo('请先勾选要导入的资源')
    return
  }
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const outcomes = await applyWSLCliResourcesSupply(cli, rels)
    const failed = outcomes.filter((o) => o.outcome !== 'applied' && o.outcome !== 'fresh')
    if (failed.length) {
      showInfo(`导入完成：${outcomes.length - failed.length} 项成功，${failed.length} 项保持现状（不兼容/冻结项见预览）`)
    } else {
      showSuccess(`已导入 ${rels.length} 项资源（静态检查通过；未执行内容）`)
    }
    if (rels.includes(cliResPreviews.value[cli]?.name ?? '')) delete cliResPreviews.value[cli]
    if (!only) delete cliResSelection.value[cli]
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('导入失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function keepCliResource(cli: string, rel: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await keepWSLCliResourceLocal(cli, rel)
    if (cliResPreviews.value[cli]?.name === rel) delete cliResPreviews.value[cli]
    showInfo(`已保留 WSL 本地资源（停跟随）：${rel}`)
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('停跟随失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function takeOverCliResource(cli: string, rel: string, token: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await takeOverWSLCliResource(cli, rel, token)
    delete cliResPreviews.value[cli]
    showSuccess(`已按预览确认接管资源：${rel}`)
    await loadCliSupply(cli)
  } catch (err: unknown) {
    showError('接管失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

async function loadOcMcp(): Promise<void> {
  try {
    const rows = await getWSLOpenCodeMCPPreview()
    ocMcp.value = rows
    ocOverride.value = rows.filter((e) => e.overrideGlobal).map((e) => e.name)
  } catch {
    // 静默缺省
  }
}

function toggleOcOverride(name: string): void {
  ocOverride.value = ocOverride.value.includes(name)
    ? ocOverride.value.filter((n) => n !== name)
    : [...ocOverride.value, name]
}

async function saveOcSelection(): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await setWSLOpenCodeMCPSelection(ocOverride.value)
    showSuccess('已保存 OpenCode 附加层 MCP 覆盖选择（下次启动生效）')
    await loadOcMcp()
  } catch (err: unknown) {
    showError('保存失败: ' + errorMessage(err))
  } finally {
    cliBusy.value = false
  }
}

watch(() => props.refreshRevision, () => { if (alive) void load() })
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
  color: var(--success-strong);
  background: color-mix(in srgb, var(--success) 14%, transparent);
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

.supply-section {
  margin-top: 20px;
  padding-top: 18px;
  border-top: 1px solid var(--separator);
}

.supply-section h3 {
  margin: 0 0 12px;
  font-size: 15px;
  font-weight: 650;
  color: var(--label);
}

.sync-mode {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px 20px;
  padding: 14px 16px;
  border: 1px solid var(--separator);
  border-radius: var(--radius-md);
  background: var(--window);
}

.sync-mode-active {
  border-color: color-mix(in srgb, var(--success) 24%, var(--separator));
  background: color-mix(in srgb, var(--success) 5%, var(--card));
}

.sync-copy {
  flex: 1 1 24rem;
  min-width: 0;
}

.sync-copy strong {
  font-size: 14px;
}

.sync-pending {
  margin: 10px 0 0;
  padding-inline: 12px;
  border-inline-start: 2px solid var(--warning);
  color: var(--warning-strong);
  font-size: 12px;
  line-height: 1.6;
}

.sync-list {
  margin-top: 12px;
  border: 1px solid var(--separator);
  border-radius: var(--radius-md);
}

.sync-group + .sync-group {
  border-top: 1px solid var(--separator);
}

.sync-group > summary,
.resource-group > summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  min-height: 46px;
  padding: 10px 16px;
  border-radius: var(--radius-sm);
  list-style: none;
  cursor: pointer;
  transition: background 150ms;
}

.sync-group > summary::-webkit-details-marker,
.resource-group > summary::-webkit-details-marker {
  display: none;
}

.sync-group > summary::after,
.resource-group > summary::after {
  content: '';
  width: 6px;
  height: 6px;
  margin: -3px 2px 0 3px;
  border-right: 1.5px solid currentColor;
  border-bottom: 1.5px solid currentColor;
  color: var(--secondary);
  transform: rotate(45deg);
}

.sync-group[open] > summary::after,
.resource-group[open] > summary::after {
  margin-top: 3px;
  transform: rotate(225deg);
}

.sync-group > summary:hover,
.resource-group > summary:hover {
  background: var(--control);
}

.supply-section summary:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}

.group-name {
  margin-right: auto;
  color: var(--label);
  font-size: 13px;
  font-weight: 600;
}

.group-meta {
  color: var(--secondary);
  font-size: 12px;
}

.group-content {
  padding: 0 16px 14px;
}

.group-action {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 16px;
}

.group-action > div {
  flex: 1 1 18rem;
  min-width: 0;
}

.cli-content {
  display: grid;
  gap: 12px;
}

.cli-settings,
.resource-group {
  min-width: 0;
  border: 1px solid var(--separator);
  border-radius: var(--radius-md);
  background: var(--window);
}

.cli-settings,
.resource-content {
  padding: 0 12px;
}

.resource-group > summary {
  min-height: 38px;
  padding: 8px 12px;
}

.section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 12px;
  min-height: 42px;
  padding: 6px 0;
}

.section-heading h4 {
  margin: 0;
  color: var(--label);
  font-size: 13px;
  font-weight: 600;
}

.open-code-heading {
  margin-top: 6px;
}

.sync-item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  padding: 10px 0;
  border-top: 1px solid var(--separator);
}

.group-content > .sync-item:first-child {
  border-top: 0;
  padding-top: 0;
}

.item-main {
  flex: 1 1 16rem;
  min-width: 0;
}

.item-title,
.item-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
}

.item-title {
  color: var(--label);
  font-size: 13px;
}

.item-title label,
.override-option {
  display: inline-flex;
  align-items: flex-start;
  gap: 6px;
  min-width: 0;
}

.item-title input,
.override-option input {
  flex: none;
  margin: 2px 0 0;
  accent-color: var(--accent);
}

.item-note {
  margin: 0;
  color: var(--secondary);
  font-size: 12px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}

.item-title + .item-note,
.item-note + .item-note {
  margin-top: 4px;
}

.supply-section code {
  color: var(--label);
  font-family: var(--mono);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.supply-section .badge {
  color: var(--secondary);
  background: var(--control);
  font-size: 11px;
}

.supply-section .badge-ok {
  color: var(--success-strong);
  background: color-mix(in srgb, var(--success) 14%, var(--card));
}

.supply-section .badge-warn {
  color: var(--warning-strong);
  background: var(--warning-tint);
}

.supply-section .badge-error {
  color: var(--danger-strong);
  background: color-mix(in srgb, var(--danger) 10%, var(--card));
}

.supply-section .badge-wsl2 {
  color: var(--accent-strong);
  background: color-mix(in srgb, var(--accent) 10%, var(--card));
}

.item-preview {
  flex: 1 1 100%;
  min-width: 0;
  display: grid;
  gap: 6px;
  padding: 10px 12px;
  border-inline-start: 2px solid var(--separator);
  border-radius: var(--radius-sm);
  background: var(--card);
}

.key-list,
.compat-details ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

.key-list li + li {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--separator);
}

.preview-actions {
  margin-top: 4px;
}

.preview-actions:empty {
  display: none;
}

.compat-details {
  margin-top: 4px;
  font-size: 12px;
  color: var(--secondary);
  overflow-wrap: anywhere;
}

.compat-details > summary {
  color: var(--accent-strong);
  cursor: pointer;
}

.compat-details li {
  margin: 4px 0 0 12px;
}

.resource-toolbar > .item-note {
  flex: 1 1 12rem;
}

.resource-content > .item-note {
  padding-bottom: 10px;
}
</style>
