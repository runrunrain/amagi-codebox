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

      <!-- P3-S1 最小「配置状态」区：托管面同步状态 + 视觉清单共享同意入口 -->
      <div v-if="supply" class="supply-section">
        <h3>配置状态</h3>
        <p class="set-sub">
          Windows 面板保存后，托管配置按需同步到 WSL（{{ supply.distro }}）；
          已安装不等于已同步，安装成功也不改写 WSL 内的用户自有文件。
        </p>
        <!-- P2-B：统一配置管理（一次启用 → 启动/保存/对账自动同步） -->
        <div class="unified-row">
          <template v-if="!supply.unified">
            <span class="supply-detail">
              统一管理：一次启用后，Windows 侧配置自动同步到 WSL，无需重复预览/勾选——
              Claude MCP 服务器与设置白名单、Codex MCP/profile、共享技能与
              CLAUDE.md/commands/agents/AGENTS.md、视觉清单；新条目随下次对账自动纳入。
              MCP env 与视觉清单含密钥副本，按 0600 写入；WSL 手改与已有同名项不会被覆盖。
            </span>
            <AppButton variant="primary" size="small" :disabled="unifiedBusy" @click="enableUnified">
              {{ unifiedBusy ? '启用中…' : '启用统一管理' }}
            </AppButton>
          </template>
          <template v-else>
            <span class="badge badge-ok">已由 Windows 统一管理（{{ supply.distro }}）</span>
            <AppButton variant="ghost" size="small" :disabled="unifiedBusy" @click="disableUnified">
              {{ unifiedBusy ? '处理中…' : '停止自动纳入新项' }}
            </AppButton>
            <span class="supply-detail">停止后已授权单元与副本保持现状；撤回单个单元用各面板的「保留本地」。</span>
          </template>
        </div>
        <div class="supply-list">
          <div v-for="f in supply.faces" :key="f.key" class="supply-row">
            <label>{{ f.label }}</label>
            <div class="row-value">
              <span class="badge" :class="faceBadgeClass(f.state)">{{ faceStateText(f.state) }}</span>
              <span class="supply-detail">{{ f.detail }}</span>
              <span v-if="f.lastCheck" class="supply-time">检查于 {{ formatTime(f.lastCheck) }}</span>
            </div>
          </div>
        </div>
        <div v-if="visionAwaitingConsent && !supply.unified" class="consent-row">
          <span class="supply-detail">
            视觉清单含 API 密钥副本；向 WSL 共享前需要你的明确同意（文件将以 0600 权限写入，
            仅在 distro 内可读）。
          </span>
          <AppButton
            variant="primary"
            size="small"
            :disabled="consentBusy"
            @click="grantConsent"
          >
            {{ consentBusy ? '授权中…' : '同意共享到 WSL' }}
          </AppButton>
        </div>
        <!-- 共享技能根（1.3.130：受管符号链接直连 Windows ~/.agents/skills） -->
        <div class="pi-units skills-section">
          <h4>共享技能（Windows ~/.agents/skills → WSL）</h4>
          <p class="set-sub">
            WSL 内 ~/.agents/skills 以受管符号链接直连 Windows 技能根（与 pi 共享 agent 根同一
            机制）：无需逐项导入，WSL 侧始终读到 Windows 最新内容；既有 WSL 目录会在链接前自动
            备份为 skills.pre-shared.*。会话启动时自动核验，也可在此手动检查/修复。
          </p>
          <div class="skills-toolbar">
            <span class="badge" :class="faceBadgeClass(skillsFace?.state || 'unchecked')">{{ faceStateText(skillsFace?.state || 'unchecked') }}</span>
            <span class="supply-detail">{{ skillsFace?.detail || '尚未核验（启动 WSL pi 会话或点检查）' }}</span>
            <AppButton variant="ghost" size="small" :disabled="sharedRootBusy" @click="ensureSharedRoot">
              {{ sharedRootBusy ? '核验中…' : '检查/修复' }}
            </AppButton>
          </div>
          <p v-if="sharedRoot?.backup" class="set-hint">旧目录已备份迁移：<span class="mono">{{ sharedRoot.backup }}</span></p>
        </div>
        <!-- P3-S4：Claude / Codex 用户配置导入（键级选择导入 + 资源目录单元） -->
        <div v-if="cliStatus['claude'] || cliStatus['codex']" class="pi-units s4-section">
          <h4>用户配置导入（Claude / Codex → WSL）</h4>
          <p class="set-sub">
            仅导入你勾选的条目：MCP 服务器（远程保留 URL；npx.cmd 等仅目标实测到原生命令才映射，
            Windows 专属形态保持 WSL 现状并逐项报告）、设置白名单字段与 Codex profile（TOML 节，
            非目标节与注释逐字节保留）。信任/凭据/历史/未知字段永不跨界；WSL 手改条目冻结保留。
          </p>
          <div v-for="cli in ['claude', 'codex']" :key="cli" class="s4-cli-block">
            <h5>{{ cli === 'claude' ? 'Claude Code' : 'Codex' }}</h5>
            <div v-if="!cliStatus[cli]?.available" class="supply-detail">{{ cliStatus[cli]?.reason || '未启用' }}</div>
            <template v-else>
              <div v-for="u in cliStatus[cli]?.units ?? []" :key="u.unit" class="pi-unit-row">
                <label class="mono">{{ u.relPath }}</label>
                <span class="badge" :class="unitBadgeClass(u.state)">{{ unitStateText(u.state) }}</span>
                <span class="supply-detail">{{ u.detail }}</span>
                <div class="unit-actions">
                  <AppButton v-if="u.actions.includes('preview')" variant="ghost" size="small" :disabled="cliBusy" @click="previewCliUnit(cli, u.unit)">预览</AppButton>
                  <AppButton v-if="u.actions.includes('apply')" variant="ghost" size="small" :disabled="cliBusy" @click="applyCli(cli)">应用安全更新</AppButton>
                  <AppButton v-if="u.actions.includes('keep-local')" variant="ghost" size="small" :disabled="cliBusy" @click="keepCliUnit(cli, u.unit)">保留本地</AppButton>
                  <AppButton
                    v-if="u.actions.includes('takeover') && cliPreviewHash(cli, u.unit)"
                    variant="primary" size="small" :disabled="cliBusy"
                    @click="takeOverCliUnit(cli, u.unit)"
                  >确认接管</AppButton>
                </div>
                <!-- 预览：逐键选择面（转换前→转换后对照；脱敏） -->
                <div v-if="cliPreviewFor(cli, u.unit)" class="unit-preview">
                  <div v-for="k in cliPreviewFor(cli, u.unit)?.keys ?? []" :key="k.key" class="key-row">
                    <label class="mono" v-if="k.importable">
                      <input
                        type="checkbox"
                        :checked="k.selected || cliKeySelected(cli, u.unit, k.key)"
                        :disabled="cliBusy"
                        @change="toggleCliKey(cli, u.unit, k.key)"
                      />
                      {{ k.key }}
                    </label>
                    <span v-else class="mono">{{ k.key }}</span>
                    <span class="badge" :class="keyBadgeClass(k.action)">{{ k.action }}</span>
                    <span v-if="k.before || k.after" class="supply-detail">{{ k.before || '—' }} → {{ k.after || '—' }}</span>
                    <span class="supply-detail">{{ k.reason }}</span>
                  </div>
                  <div v-if="cliPreviewFor(cli, u.unit)">
                    <AppButton variant="primary" size="small" :disabled="cliBusy" @click="importCliKeys(cli, u.unit)">
                      {{
                        anyCliKeySelected(cli, u.unit)
                          ? `导入所选（${cliKeySelection[cli + '|' + u.unit]?.length ?? 0}）`
                          : '撤回全部导入（空选择移除托管条目）'
                      }}
                    </AppButton>
                  </div>
                  <div v-if="(cliPreviewFor(cli, u.unit)?.notes ?? []).length" class="supply-detail">
                    {{ (cliPreviewFor(cli, u.unit)?.notes ?? []).join('；') }}
                  </div>
                </div>
              </div>
              <!-- 资源目录单元（CLAUDE.md/commands/agents/skills、AGENTS.md/skills） -->
              <div class="skills-toolbar">
                <span v-if="cliStatus[cli]?.resources.scanRunning" class="badge badge-warn">扫描中 {{ cliStatus[cli]?.resources.scanScanned }}/{{ cliStatus[cli]?.resources.scanTotal }}</span>
                <AppButton variant="ghost" size="small" :disabled="cliBusy" @click="checkCliResources(cli)">检查资源</AppButton>
                <AppButton variant="ghost" size="small" :disabled="cliBusy" @click="previewCliResources(cli)">预览资源</AppButton>
                <AppButton variant="primary" size="small" :disabled="cliBusy || !cliResSelection[cli]?.length" @click="importCliResources(cli)">
                  导入所选资源（{{ cliResSelection[cli]?.length ?? 0 }}）
                </AppButton>
              </div>
              <div v-for="s in cliStatus[cli]?.resources.skills ?? []" :key="cli + '/' + s.name" class="pi-unit-row skill-row">
                <label class="mono">
                  <input v-if="s.selectable" type="checkbox" :checked="cliResSelection[cli]?.includes(s.name)" :disabled="cliBusy" @change="toggleCliRes(cli, s.name)" />
                  {{ s.name }}
                </label>
                <span class="badge" :class="skillBadgeClass(s.state)">{{ skillStateText(s.state) }}</span>
                <span class="supply-detail">{{ s.detail }}<template v-if="s.files">（{{ s.files }} 个文件）</template></span>
                <div class="unit-actions">
                  <AppButton v-if="s.actions.includes('apply')" variant="ghost" size="small" :disabled="cliBusy" @click="applyCliResources(cli, [s.name])">应用安全更新</AppButton>
                  <AppButton v-if="s.actions.includes('keep-local')" variant="ghost" size="small" :disabled="cliBusy" @click="keepCliResource(cli, s.name)">保留本地</AppButton>
                  <AppButton
                    v-if="s.actions.includes('takeover') && cliResPreviewHash(cli, s.name)"
                    variant="primary" size="small" :disabled="cliBusy"
                    @click="takeOverCliResource(cli, s.name)"
                  >确认接管</AppButton>
                </div>
                <div v-if="s.incompatible?.length" class="unit-preview">
                  <div v-for="it in s.incompatible" :key="it" class="key-row">
                    <span class="badge badge-missing">不兼容</span>
                    <span class="supply-detail">{{ it }}</span>
                  </div>
                </div>
              </div>
            </template>
          </div>
        </div>
        <!-- P3-S4：OpenCode 附加层 MCP 选择（全局同名冲突默认保留本地；勾选覆盖） -->
        <div v-if="ocMcp.length" class="pi-units s4-section">
          <h4>OpenCode 附加层 MCP（preset → WSL）</h4>
          <p class="set-sub">
            preset 中的 MCP 条目经形态检查后写入附加层（不碰 WSL 全局配置）；与 WSL 全局
            opencode.json 同名的条目默认保留本地，勾选覆盖后才写入。
          </p>
          <div v-for="e in ocMcp" :key="e.name" class="pi-unit-row">
            <label class="mono">{{ e.name }}</label>
            <span v-if="e.remote" class="badge badge-ok">远程</span>
            <span v-else-if="e.needsAdapt" class="badge badge-missing">需手动适配</span>
            <span v-else class="badge badge-ok">stdio {{ e.nativeCommand }}</span>
            <span v-if="e.globalConflict" class="badge badge-warn">与 WSL 全局同名</span>
            <span v-if="e.reason" class="supply-detail">{{ e.reason }}</span>
            <div class="unit-actions" v-if="e.globalConflict && !e.needsAdapt">
              <label class="supply-detail">
                <input type="checkbox" :checked="ocOverride.includes(e.name)" :disabled="cliBusy" @change="toggleOcOverride(e.name)" />
                覆盖全局
              </label>
            </div>
          </div>
          <div class="consent-row">
            <AppButton variant="primary" size="small" :disabled="cliBusy" @click="saveOcSelection">保存选择</AppButton>
            <span class="supply-detail">覆盖选择保存在 Windows 侧（下次启动生效）；未勾选的同名条目继续保留 WSL 全局版本。</span>
          </div>
        </div>
        <p v-if="supply.pendingPush" class="set-hint">
          有保存后的变更待同步：将在 WSL 空闲时自动推送，或于下次启动会话时补齐。
        </p>
      </div>
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
  type WSLConfigSupplyFace,
  type WSLConfigUnitPreview,
} from '../../api/wslSupply'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import { cliImportOutcomeMessage } from './wslSupplyMessages'

const props = withDefaults(defineProps<{ refreshRevision?: number }>(), { refreshRevision: 0 })
const { showSuccess, showError, showInfo } = useToast()

// SA-17：卸载后停止扫描轮询链（setTimeout 链不再触发新请求）。
let alive = true
onUnmounted(() => { alive = false })

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
    if (st.available) {
      // 只读状态快照：失败不干扰安装卡片（配置状态区静默缺省）。
      getWSLConfigSupplyStatus()
        .then((s) => { supply.value = s; watchFaces() })
        .catch(() => { supply.value = null })
      // P3-S4：Claude/Codex 用户配置导入面 + OpenCode 附加层 MCP 选择面。
      loadAllCliSupply()
    }
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
  } catch (err: any) {
    showError(`${action}失败: ` + (err?.message || err))
  } finally {
    busyTool.value = ''
  }
}

const visionAwaitingConsent = ref(false)
function refreshConsentFlag(): void {
  const face = supply.value?.faces?.find((f) => f.key === 'vision-export')
  visionAwaitingConsent.value = face?.state === 'awaiting-consent'
}

function faceStateText(state: string): string {
  switch (state) {
    case 'synced': return '已同步'
    case 'stale': return '陈旧'
    case 'failed': return '失败'
    case 'frozen': return '冻结'
    case 'awaiting-consent': return '待授权'
    case 'consented': return '已授权，待发布'
    case 'scanning': return '扫描中'
    case 'incompatible': return '有不兼容项'
    case 'pending-op': return '待确认'
    default: return '未检查'
  }
}
function faceBadgeClass(state: string): string {
  switch (state) {
    case 'synced': return 'badge-ok'
    case 'failed': case 'incompatible': return 'badge-missing'
    case 'stale': case 'awaiting-consent': case 'consented': case 'scanning': case 'pending-op': return 'badge-warn'
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
    await load()
    refreshConsentFlag()
  } catch (err: any) {
    showError('授权失败: ' + (err?.message || err))
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
  } catch (err: any) {
    showError('启用失败: ' + (err?.message || err))
  } finally {
    unifiedBusy.value = false
  }
}

async function disableUnified(): Promise<void> {
  if (unifiedBusy.value) return
  unifiedBusy.value = true
  try {
    await disableWSLUnifiedSupply()
    showInfo('已停止自动纳入新项（已授权单元与副本保持现状；撤回单个单元用「保留本地」）')
    await refreshStatus()
  } catch (err: any) {
    showError('停止失败: ' + (err?.message || err))
  } finally {
    unifiedBusy.value = false
  }
}

// faces 变化时同步同意标记（load 完成后调用）。
function watchFaces(): void {
  refreshConsentFlag()
}

function unitStateText(state: string): string {
  switch (state) {
    case 'synced': return '已同步'
    case 'stale': return '陈旧'
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
    case 'failed': return 'badge-missing'
    case 'stale': case 'partial': return 'badge-warn'
    case 'frozen': case 'detached': case 'awaiting-consent': return 'badge-warn'
    default: return ''
  }
}
function keyBadgeClass(action: string): string {
  switch (action) {
    case 'add': case 'update': case 'remove': return 'badge-warn'
    case 'no-change': return 'badge-ok'
    default: return 'badge-missing'
  }
}
async function refreshStatus(): Promise<void> {
  try {
    supply.value = await getWSLConfigSupplyStatus()
    refreshConsentFlag()
  } catch {
    // 只读快照失败：保持现状（下次加载重试）
  }
}

// ---------------------------------------------------------------------------
// 共享技能根（1.3.130：受管符号链接直连 Windows ~/.agents/skills；原 P3-S3
// 同意制投影退役——状态面来自配置状态 faces，显式检查走 ensureWSLSharedSkillsRoot）
// ---------------------------------------------------------------------------
import { ensureWSLSharedSkillsRoot, type WSLSharedSkillsRootStatus, type WSLSkillPreview } from '../../api/wslSupply'

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
    // 配置状态 faces 同步刷新（skills face 读进程内缓存）。
    getWSLConfigSupplyStatus().then((s) => { supply.value = s; watchFaces() }).catch(() => {})
  } catch (err: any) {
    showError('检查失败: ' + (err?.message || err))
  } finally {
    sharedRootBusy.value = false
  }
}

function skillStateText(state: string): string {
  switch (state) {
    case 'importable': return '可导入'
    case 'supplied': return '已供给'
    case 'stale': return '陈旧'
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
    case 'incompatible': case 'failed': case 'source-deleted': return 'badge-missing'
    case 'stale': case 'frozen': case 'scan-incomplete': case 'empty': case 'awaiting-consent': return 'badge-warn'
    case 'importable': return 'badge-wsl2'
    default: return ''
  }
}

// ---------------------------------------------------------------------------
// P3-S4：Claude / Codex 用户配置导入 + OpenCode 附加层 MCP 选择面
// ---------------------------------------------------------------------------
import {
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
  type WSLCliSupplyStatus,
  type WSLOpenCodeMCPEntry,
} from '../../api/wslSupply'

const cliBusy = ref(false)
const cliStatus = ref<Record<string, WSLCliSupplyStatus | null>>({ claude: null, codex: null })
const cliPreviews = ref<Record<string, Record<string, WSLConfigUnitPreview>>>({})
const cliKeySelection = ref<Record<string, string[]>>({})
const cliResSelection = ref<Record<string, string[]>>({})
const cliResPreviews = ref<Record<string, Record<string, WSLSkillPreview>>>({})
const ocMcp = ref<WSLOpenCodeMCPEntry[]>([])
const ocOverride = ref<string[]>([])

async function loadCliSupply(cli: string): Promise<void> {
  if (!alive) return
  try {
    const st = await getWSLCliSupplyStatus(cli)
    cliStatus.value = { ...cliStatus.value, [cli]: st }
    if (alive && st.resources.scanRunning) {
      setTimeout(() => loadCliSupply(cli), 800)
    }
  } catch {
    // 保持现状（未启用面静默缺省）
  }
}

function loadAllCliSupply(): void {
  loadCliSupply('claude')
  loadCliSupply('codex')
  loadOcMcp()
}

async function refreshCli(cli: string): Promise<void> {
  await loadCliSupply(cli)
}

function cliPreviewFor(cli: string, unit: string): WSLConfigUnitPreview | undefined {
  return cliPreviews.value[cli]?.[unit]
}

function cliPreviewHash(cli: string, unit: string): string {
  return cliPreviewFor(cli, unit)?.targetHash ?? ''
}

function cliKeySelected(cli: string, unit: string, key: string): boolean {
  return (cliKeySelection.value[cli + '|' + unit] ?? []).includes(key)
}

function anyCliKeySelected(cli: string, unit: string): boolean {
  return (cliKeySelection.value[cli + '|' + unit] ?? []).length > 0
}

function toggleCliKey(cli: string, unit: string, key: string): void {
  const id = cli + '|' + unit
  const cur = cliKeySelection.value[id] ?? []
  cliKeySelection.value = {
    ...cliKeySelection.value,
    [id]: cur.includes(key) ? cur.filter((k) => k !== key) : [...cur, key],
  }
}

async function previewCliUnit(cli: string, unit: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const rows = await previewWSLCliConfigSupply(cli)
    const next: Record<string, WSLConfigUnitPreview> = {}
    for (const row of rows) next[row.unit] = row
    cliPreviews.value = { ...cliPreviews.value, [cli]: next }
    // 初始化选择集：当前已选键 + 预览中标记 selected 的键。
    const row = next[unit]
    if (row) {
      const selected = (row.keys ?? []).filter((k) => k.selected).map((k) => k.key)
      cliKeySelection.value = { ...cliKeySelection.value, [cli + '|' + unit]: selected }
    }
    showInfo(`预览完成：${row?.blockedReason || row?.action || ''}`)
  } catch (err: any) {
    showError('预览失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function importCliKeys(cli: string, unit: string): Promise<void> {
  // SA-03：空选择是合法的显式撤回入口（后端按来源删除语义移除托管条目）；
  // 零写入不报导入成功（提示逻辑见 wslSupplyMessages，单测覆盖）。
  const keys = cliKeySelection.value[cli + '|' + unit] ?? []
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const outcomes = await importWSLCliConfigKeys(cli, unit, keys)
    const msg = cliImportOutcomeMessage(keys.length, outcomes)
    if (msg.kind === 'success') {
      showSuccess(msg.text)
    } else {
      showInfo(msg.text)
    }
    await refreshCli(cli)
  } catch (err: any) {
    showError('导入失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function applyCli(cli: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const outcomes = await applyWSLCliConfigSupply(cli)
    const failed = outcomes.filter((o) => o.outcome === 'failed' || o.outcome === 'blocked')
    if (failed.length) {
      showInfo(`安全更新完成：${failed.length} 个单元保持现状（详见预览）`)
    } else {
      showSuccess('安全更新完成（冻结/未托管条目保持 WSL 现状）')
    }
    await refreshCli(cli)
  } catch (err: any) {
    showError('应用失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function keepCliUnit(cli: string, unit: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await keepWSLCliConfigUnitLocal(cli, unit)
    showInfo('已保留 WSL 本地配置（停跟随）')
    await refreshCli(cli)
  } catch (err: any) {
    showError('停跟随失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function takeOverCliUnit(cli: string, unit: string): Promise<void> {
  const token = cliPreviewHash(cli, unit)
  if (!token && token !== '') {
    showError('请先预览该单元再确认接管')
    return
  }
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await takeOverWSLCliConfigUnit(cli, unit, token)
    showSuccess('已按预览确认接管（托管域已按 Windows 当前内容重建基准）')
    await refreshCli(cli)
  } catch (err: any) {
    showError('接管失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function checkCliResources(cli: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await checkWSLCliResourcesSupply(cli)
    // 重拉完整状态（后台扫描分批推进，轮询至收敛）。
    await loadCliSupply(cli)
  } catch (err: any) {
    showError('检查失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function previewCliResources(cli: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    const rows = await previewWSLCliResourcesSupply(cli)
    const next: Record<string, WSLSkillPreview> = {}
    for (const row of rows) next[row.name] = row
    cliResPreviews.value = { ...cliResPreviews.value, [cli]: next }
    showInfo(`资源预览完成：${rows.length} 项（不兼容项已逐项列出）`)
  } catch (err: any) {
    showError('预览失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

function cliResPreviewHash(cli: string, name: string): string {
  return cliResPreviews.value[cli]?.[name]?.targetHash ?? ''
}

function toggleCliRes(cli: string, name: string): void {
  const cur = cliResSelection.value[cli] ?? []
  cliResSelection.value = {
    ...cliResSelection.value,
    [cli]: cur.includes(name) ? cur.filter((n) => n !== name) : [...cur, name],
  }
}

async function importCliResources(cli: string): Promise<void> {
  await applyCliResources(cli)
}

async function applyCliResources(cli: string, only?: string[]): Promise<void> {
  const rels = only ?? cliResSelection.value[cli] ?? []
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
    cliResSelection.value = { ...cliResSelection.value, [cli]: [] }
    await refreshCli(cli)
  } catch (err: any) {
    showError('导入失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function keepCliResource(cli: string, rel: string): Promise<void> {
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await keepWSLCliResourceLocal(cli, rel)
    showInfo(`已保留 WSL 本地资源（停跟随）：${rel}`)
    await refreshCli(cli)
  } catch (err: any) {
    showError('停跟随失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

async function takeOverCliResource(cli: string, rel: string): Promise<void> {
  const token = cliResPreviewHash(cli, rel)
  if (!token && token !== '') {
    showError('请先预览该资源再确认接管')
    return
  }
  if (cliBusy.value) return
  cliBusy.value = true
  try {
    await takeOverWSLCliResource(cli, rel, token)
    showSuccess(`已按预览确认接管资源：${rel}`)
    await refreshCli(cli)
  } catch (err: any) {
    showError('接管失败: ' + (err?.message || err))
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
  } catch (err: any) {
    showError('保存失败: ' + (err?.message || err))
  } finally {
    cliBusy.value = false
  }
}

watch(() => props.refreshRevision, () => { if (alive) void load() })
onMounted(load)
</script>

<style scoped>
.supply-section {
  margin-top: 18px;
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}
.supply-section h3 {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 6px;
}
.supply-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 8px;
}
.supply-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
}
.supply-row label {
  color: var(--label);
  min-width: 140px;
}
.supply-row .row-value {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}
.supply-detail {
  color: var(--tertiary);
  font-size: 12px;
}
.supply-time {
  color: var(--tertiary);
  font-size: 11px;
}
.badge-warn {
  background: color-mix(in srgb, #d08770 22%, transparent);
  color: #d08770;
}
.consent-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin: 8px 0 4px;
}
.unified-row {
  display: flex;
  align-items: baseline;
  gap: 12px;
  flex-wrap: wrap;
  padding: 8px 10px;
  background: var(--control);
  border-radius: 8px;
  margin-bottom: 8px;
}

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

.pi-units {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--separator);
}
.pi-units h4 {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 6px;
}
.pi-unit-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
  padding: 6px 0;
  font-size: 13px;
}
.pi-unit-row > label {
  min-width: 110px;
  color: var(--secondary);
}
.unit-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.unit-preview {
  width: 100%;
  padding: 6px 10px;
  background: var(--control);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.key-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
}
.key-row .mono {
  min-width: 180px;
}
.skills-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.skill-row {
  border-top: 1px dashed var(--separator);
  padding-top: 6px;
}
.skill-row input[type='checkbox'] {
  margin-right: 6px;
}
.s4-cli-block {
  border-top: 1px dashed var(--separator);
  padding-top: 8px;
  margin-top: 8px;
}
.s4-cli-block h5 {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  margin: 4px 0;
}
.s4-section {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--separator);
}
</style>