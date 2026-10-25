<!--
  PiAmagiConfig - pi (amagi-pi) amagi.json 可视化配置组件。
  挂载于 Provider Center → CLI 独立配置 → Pi → Agent 配置。
  布局（provider-center-layout-redesign §4.3）：导航即标题——不再有 h2 / subtitle；
  一行 EditorToolbar（路径胶囊 / 合法状态 / 模式切换 / 保存）；卡片顺序
  角色模型（含分层策略）→ 执行中咨询 → 上下文压缩 → MCP 路由 → 并发限制；常显说明只留「当前状态语义 /
  操作后果」，其余收进 InfoTip（文案与处置见 design §5.2 T07–T29）。
  可视化模式：profile 选择、agents.{role}.model 三级下拉（provider → model →
  thinking level，数据来自 pi 模型目录）、统一压缩模型、MCP 路由编辑（含 v2.8.2 mcp.leader）。
  mcp.leader / mcp.default：名单编辑器常显（all/none 态添加服务器即切白名单）；
  UI 编辑态与数据推断态解耦（点「白名单」进入编辑视图，空列表中间态不跳回「不加载」）。
  源码模式：JSON 文本直接编辑。保存走后端原子写入。
-->
<template>
  <div class="pac-root" :class="{ 'editor-local-layout': navigation === 'local' }">
    <SectionNav v-if="navigation === 'local'" label="配置分区" :groups="localGroups" :current-id="mode === 'visual' ? activeSection : ''" controls-id="pac-editor-detail" :notice="mode !== 'visual' ? '源码模式：返回可视化后切换分区' : ''" @select="selectSection" />
    <div id="pac-editor-detail" class="editor-detail">
    <LoadingState v-if="loading" message="加载配置中..." />

    <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />

    <template v-else>
      <!-- 工具栏：路径胶囊 + JSON 合法状态 ····· 模式切换 + 保存。导航即标题，不再有 h2 / subtitle -->
      <EditorToolbar
        :path="configPath"
        path-label="配置文件"
        :valid-label="mode === 'json' && !jsonError ? 'JSON 合法' : ''"
        @copy-path="copyPath"
      >
        <template #status>
          <button v-if="compactionError && mode === 'visual' && activeSection !== 'compaction'" type="button" class="pac-error-link" @click="selectSection('compaction')">上下文压缩待修正</button>
          <span v-else-if="compactionError && mode !== 'visual'" class="pac-error-link">上下文压缩待修正</span>
        </template>
        <template #actions>
          <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" />
          <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">
            {{ saving ? '保存中...' : '保存配置' }}
          </AppButton>
        </template>
      </EditorToolbar>

      <StatusBanner v-if="jsonError" compact type="error" role="alert" class="pac-json-error" :message="jsonError" />
      <StatusBanner
        v-if="catalogError"
        compact
        type="warning"
        class="pac-catalog-warn"
        :message="`模型目录加载失败，下拉不可用：${catalogError}`"
      />

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="pac-visual">
        <!-- ① 角色模型（含分层策略 profile） -->
        <EditorSection v-if="visited.agents" v-show="activeSection === 'agents'" :active="activeSection === 'agents'"
          section-id="agents" title="角色模型" code="agents" :badge="agentRows.length"
        >
          <div class="pac-field">
            <FieldLabel text="分层策略" code="profile" :items="PROFILE_ITEMS" />
            <Dropdown
              class="pac-profile-dd"
              :model-value="profileValue"
              :options="PROFILE_OPTIONS"
              @update:model-value="updateProfile"
            />
          </div>
          <div class="pac-field">
            <FieldLabel text="角色覆盖" code="agents.<角色>.model" :tip="AGENTS_TIP" />
            <div class="pac-agent-rows">
              <div v-for="row in agentRows" :key="row.role" class="pac-agent-row">
                <div class="pac-agent-role">
                  <TextInput
                    :model-value="row.role"
                    placeholder="角色名（如 baize）"
                    mono
                    class="pac-role-input"
                    @update:model-value="renameRole(row.role, $event)"
                  />
                </div>
                <ModelSpecSelector
                  :model-value="row.model"
                  :catalog="catalog"
                  class="pac-agent-spec"
                  @update:model-value="updateAgentModel(row.role, $event)"
                />
                <AppButton variant="icon" size="small" aria-label="删除角色" @click="removeRole(row.role)">
                  <span class="pac-remove">×</span>
                </AppButton>
              </div>
            </div>
            <div class="pac-actions">
              <AppButton variant="ghost" size="small" @click="addRole('')">+ 添加角色</AppButton>
              <AppButton
                v-for="s in unconfiguredRoleSuggestions"
                :key="s"
                variant="ghost"
                size="small"
                @click="addRole(s)"
              >
                + {{ s }}
              </AppButton>
            </div>
          </div>
        </EditorSection>

        <EditorSection v-if="visited.advisor" v-show="activeSection === 'advisor'" :active="activeSection === 'advisor'" section-id="advisor" title="执行中咨询" code="advisor" meta="异步建议返回原会话">
          <PiAdvisorConfig :value="configData.advisor" :catalog="catalog" @change="updateAdvisor" />
        </EditorSection>

        <!--
          ② 上下文压缩（e2e 锚点卡：标题须含「上下文压缩」；
          卡内第一个 Dropdown 必须是 ModelSpecSelector 的，且只允许它的 1 个 input；
          InfoTip 浮层 Teleport 出卡片且 role=tooltip，不会混入卡内 role=alert）
        -->
        <EditorSection v-if="visited.compaction" v-show="activeSection === 'compaction'" :active="activeSection === 'compaction'"
          section-id="compaction" title="上下文压缩" code="compaction" meta="下次压缩生效"
          :tip="COMPACTION_TIP" :badge="compactionMode === 'unset' ? null : 1"
        >
          <div class="pac-field">
            <FieldLabel text="压缩模型" code="compaction.model" :items="COMPACTION_MODE_ITEMS" />
            <Segmented
              v-model="compactionModeModel"
              :options="COMPACTION_MODE_OPTIONS"
              variant="pill"
              class="pac-inline-seg"
            />
            <p class="pac-caption">{{ COMPACTION_MODE_CAPTIONS[compactionMode] }}</p>
            <template v-if="compactionMode === 'custom'">
              <ModelSpecSelector
                :model-value="compactionModel"
                :catalog="catalog"
                @update:model-value="updateCompactionModel"
              />
              <p class="pac-caption is-consequence">窗口不足或调用失败时取消本次压缩，历史保留</p>
            </template>
            <div class="pac-actions pac-actions-inline">
              <AppButton variant="ghost" size="small" :disabled="!lubanModel" @click="copyLubanCompactionModel">
                复制 luban 当前模型
              </AppButton>
              <span class="pac-caption" :class="{ mono: lubanModel }">
                {{ lubanModel ? `luban：${lubanModel}` : 'luban 未配置模型' }}
              </span>
            </div>
            <StatusBanner
              v-if="compactionError"
              compact
              type="error"
              role="alert"
              class="pac-json-error"
              :message="compactionError"
            />
          </div>
        </EditorSection>

        <!-- ③ MCP 路由 -->
        <EditorSection v-if="visited.mcp" v-show="activeSection === 'mcp'" :active="activeSection === 'mcp'"
          section-id="mcp" title="MCP 路由" code="mcp" :badge="mcpBadge"
        >
          <div class="pac-content-groups">
          <ConfigContentGroup icon="mcp" label="Leader 服务器">
            <template #header>
            <FieldLabel
              text="Leader 服务器"
              code="mcp.leader"
              effect="重启 pi 生效"
              :tip="MCP_TIPS.leader"
              :items="MCP_MODE_ITEMS.leader"
            />
            </template>
            <Segmented
              v-model="mcpLeaderModeModel"
              :options="MCP_MODE_OPTIONS"
              variant="pill"
              class="pac-inline-seg"
            />
            <StringListEditor
              :model-value="mcpLeader"
              item-placeholder="服务器名（如 web-search-prime）"
              add-label="添加服务器"
              :empty-text="MCP_EMPTY_TEXT[mcpLeaderUiMode]"
              mono
              @update:model-value="updateMcpLeader"
            />
          </ConfigContentGroup>
          <ConfigContentGroup icon="mcp" label="默认服务器">
            <template #header>
            <FieldLabel
              text="默认服务器"
              code="mcp.default"
              effect="即时生效"
              :tip="MCP_TIPS.default"
              :items="MCP_MODE_ITEMS.default"
            />
            </template>
            <Segmented
              v-model="mcpDefaultModeModel"
              :options="MCP_MODE_OPTIONS"
              variant="pill"
              class="pac-inline-seg"
            />
            <StringListEditor
              :model-value="mcpDefault"
              item-placeholder="服务器名（如 web-search-prime）"
              add-label="添加服务器"
              :empty-text="MCP_EMPTY_TEXT[mcpDefaultUiMode]"
              mono
              @update:model-value="updateMcpDefault"
            />
          </ConfigContentGroup>
          <ConfigContentGroup icon="agent" label="角色附加服务器">
            <template #header>
              <FieldLabel text="角色附加服务器" code="mcp.agents" :tip="MCP_AGENTS_TIP" />
            </template>
            <div class="pac-mcp-agents">
              <div v-for="row in mcpAgentRows" :key="row.role" class="pac-mcp-agent-row">
                <TextInput
                  :model-value="row.role"
                  placeholder="角色名"
                  mono
                  class="pac-role-input"
                  @update:model-value="renameMcpAgent(row.role, $event)"
                />
                <TextInput
                  :model-value="row.servers.join(', ')"
                  placeholder="逗号分隔服务器名"
                  mono
                  class="pac-mcp-servers-input"
                  @update:model-value="updateMcpAgentServers(row.role, $event)"
                />
                <AppButton variant="icon" size="small" aria-label="删除" @click="removeMcpAgent(row.role)">
                  <span class="pac-remove">×</span>
                </AppButton>
              </div>
              <div class="pac-actions">
                <AppButton variant="ghost" size="small" @click="addMcpAgent">+ 添加角色路由</AppButton>
              </div>
            </div>
          </ConfigContentGroup>
          </div>
        </EditorSection>

        <!-- ④ 并发限制 -->
        <EditorSection v-if="visited.concurrency" v-show="activeSection === 'concurrency'" :active="activeSection === 'concurrency'"
          section-id="concurrency" title="并发限制" code="concurrency" :tip="CONCURRENCY_TIP" :badge="concurrencyBadge"
        >
          <div class="pac-content-groups">
          <ConfigContentGroup icon="concurrency" label="默认并发">
            <template #header>
              <FieldLabel text="默认并发" code="default" :tip="CONCURRENCY_DEFAULT_TIP" />
            </template>
            <TextInput
              :model-value="concurrencyDefault"
              type="number"
              placeholder="留空 = 内置默认 4"
              mono
              class="pac-limit-input pac-limit-default"
              @update:model-value="updateConcurrencyDefault"
            />
          </ConfigContentGroup>

          <ConfigContentGroup icon="provider" label="服务商并发">
            <template #header><FieldLabel text="服务商并发" code="providers" /></template>
            <div v-if="concurrencyProviderRows.length" class="pac-concurrency-rows">
              <div v-for="row in concurrencyProviderRows" :key="row.id" class="pac-concurrency-row">
                <div v-if="row.isCustom" class="pac-concurrency-custom-key">
                  <TextInput
                    :model-value="row.key"
                    placeholder="服务商名（如 openrouter）"
                    mono
                    class="pac-concurrency-key-input"
                    @update:model-value="renameConcurrencyProvider(row.key, $event)"
                  />
                  <AppButton
                    variant="ghost"
                    size="small"
                    class="pac-switch-dd-btn"
                    title="切换为列表下拉选择"
                    @click="switchToPresetProvider(row.key)"
                  >
                    从列表选择
                  </AppButton>
                </div>
                <Dropdown
                  v-else
                  :model-value="row.key"
                  :options="providerDropdownOptions"
                  placeholder="选择服务商"
                  class="pac-concurrency-dd"
                  @update:model-value="onProviderDropdownSelect(row.key, $event)"
                />
                <TextInput
                  :model-value="String(row.limit ?? '')"
                  type="number"
                  placeholder="并发数"
                  mono
                  class="pac-limit-input"
                  @update:model-value="updateConcurrencyProviderLimit(row.key, $event)"
                />
                <AppButton variant="icon" size="small" aria-label="删除服务商限制" @click="removeConcurrencyProvider(row.key)">
                  <span class="pac-remove">×</span>
                </AppButton>
              </div>
            </div>
            <div class="pac-actions">
              <AppButton variant="ghost" size="small" @click="addConcurrencyProvider()">+ 添加服务商限制</AppButton>
            </div>
          </ConfigContentGroup>

          <ConfigContentGroup icon="model" label="模型并发">
            <template #header><FieldLabel text="模型并发" code="models" /></template>
            <div v-if="concurrencyModelRows.length" class="pac-concurrency-rows">
              <div v-for="row in concurrencyModelRows" :key="row.id" class="pac-concurrency-row">
                <div v-if="row.isCustom" class="pac-concurrency-custom-key pac-concurrency-custom-model">
                  <TextInput
                    :model-value="row.key"
                    placeholder="provider/model（如 anthropic/claude-3-7-sonnet）"
                    mono
                    class="pac-concurrency-model-input"
                    @update:model-value="renameConcurrencyModel(row.key, $event)"
                  />
                  <AppButton
                    variant="ghost"
                    size="small"
                    class="pac-switch-dd-btn"
                    title="切换为列表下拉选择"
                    @click="switchToPresetModel(row.key)"
                  >
                    从列表选择
                  </AppButton>
                </div>
                <Dropdown
                  v-else
                  :model-value="row.key"
                  :options="modelDropdownOptions"
                  placeholder="选择模型（provider/model）"
                  class="pac-concurrency-model-dd"
                  @update:model-value="onModelDropdownSelect(row.key, $event)"
                />
                <TextInput
                  :model-value="String(row.limit ?? '')"
                  type="number"
                  placeholder="并发数"
                  mono
                  class="pac-limit-input"
                  @update:model-value="updateConcurrencyModelLimit(row.key, $event)"
                />
                <AppButton variant="icon" size="small" aria-label="删除模型限制" @click="removeConcurrencyModel(row.key)">
                  <span class="pac-remove">×</span>
                </AppButton>
              </div>
            </div>
            <div class="pac-actions">
              <AppButton variant="ghost" size="small" @click="addConcurrencyModel()">+ 添加模型限制</AppButton>
            </div>
          </ConfigContentGroup>
          </div>
        </EditorSection>
      </div>

      <!-- 源码模式 -->
      <div v-else class="pac-json">
        <textarea v-model="jsonContent" class="pac-json-editor" spellcheck="false" @input="parseJsonToConfig" />
      </div>
    </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import { getAmagiConfig, saveAmagiConfig, getAmagiConfigPath, getPiModelCatalog } from '../../api/piConfig';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import Dropdown, { type DropdownOption } from '../ui/Dropdown.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import EditorToolbar from '../ui/EditorToolbar.vue';
import FieldLabel from '../ui/FieldLabel.vue';
import EditorSection from '../ui/EditorSection.vue';
import SectionNav from '../ui/SectionNav.vue';
import { PI_SECTIONS, type EditorNavigationProps, type EditorNavigationSnapshot } from './configNavigation';
import ConfigContentGroup from './ConfigContentGroup.vue';
import StringListEditor from './StringListEditor.vue';
import ModelSpecSelector from './ModelSpecSelector.vue';
import PiAdvisorConfig from './PiAdvisorConfig.vue';
import { validateAdvisorConfig } from './piAdvisor';
import { useModelCatalog } from './useModelCatalog';
import { applyMcpListMode, mcpListModeOf, type McpListMode } from './mcpListMode';
import {
  applyCompactionMode, compactionModeOf, compactionModelOf, validateCompactionConfig, type CompactionMode,
} from './piCompaction';
import {
  normalizeLimitInput,
  cleanConcurrencyConfig,
  buildProviderDropdownOptions,
  buildModelDropdownOptions,
} from './piConcurrency';

const { showSuccess, showError } = useToast();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
];

const PROFILE_OPTIONS = [
  { value: 'tiered', label: 'tiered（分层默认）' },
  { value: 'inherit', label: 'inherit（继承 pi 默认）' },
];

const COMPACTION_MODE_OPTIONS = [
  { value: 'unset', label: '未指定' },
  { value: 'custom', label: '统一模型' },
  { value: 'native', label: 'pi 原生模型' },
];

/** 常显短句：只显示当前态一条（P3 · 当前选项决定写入什么），全部状态的对照见 COMPACTION_MODE_ITEMS */
const COMPACTION_MODE_CAPTIONS: Record<CompactionMode, string> = {
  unset: '删除该键，由 pi 使用当前会话模型',
  custom: '所有 Agent 用此模型生成压缩摘要',
  native: '写入 null，各会话使用当前模型',
};

/** 三态对照（InfoTip）：term 与 COMPACTION_MODE_OPTIONS 的选项名一一对应 */
const COMPACTION_MODE_ITEMS = [
  { term: '未指定', desc: '删除 compaction.model；全局未指定时 pi 使用当前会话模型，项目仍可单独指定' },
  { term: '统一模型', desc: '写入 provider/model[:thinking]，所有 Agent 用该模型生成压缩摘要' },
  { term: 'pi 原生模型', desc: '写入显式 null，使用各会话当前模型；项目设为 null 可禁用全局统一模型' },
];

/** 上下文压缩卡片 InfoTip：用途 / 优先级 / 生效细节 / 后果 / luban 副本语义 */
const COMPACTION_TIP = [
  'Leader 与 JSON/RPC 子 Agent 共用压缩模型，用于 /compact、自动压缩与上下文溢出恢复。',
  '项目 amagi.json 优先于此全局配置；子 Agent 沿用派发者的配置目录。',
  '保存后下次压缩读取；新增服务商环境凭据引用后需重新派发子 Agent。模型认证由 pi 管理。',
  '请选上下文窗口足够的已注册模型：配置、认证、摘要失败或窗口不足都会取消本次压缩并保留历史。',
  '「复制 luban 当前模型」写入独立副本，之后修改 luban 不会联动。',
];

/** 分层策略 profile 的术语对照（选项名已在 PROFILE_OPTIONS 的 label 里） */
const PROFILE_ITEMS = [
  { term: 'tiered', desc: '按 leader / fast / work / expert 分层套用 profiles/tiered.json' },
  { term: 'inherit', desc: '全部继承 pi 默认模型' },
];

const AGENTS_TIP = [
  '按角色覆盖模型，优先于分层策略。',
  '角色名对应 amagi-pi agents/*.md；模型候选来自 pi 模型目录。',
];

const MCP_AGENTS_TIP = '为指定角色追加服务器，与默认服务器取并集。';

const CONCURRENCY_TIP = [
  '按服务商 / 模型分池限制并发请求数；未匹配时使用默认并发。',
  '优先级：模型 > 服务商 > 默认。',
];

const CONCURRENCY_DEFAULT_TIP = [
  '未单独指定服务商或模型时的并发池大小，正整数。',
  '应用预填 10；清空后保存将移除该键，回落 amagi-pi 内置默认 4。',
];

/** amagi-pi agents/*.md 的角色名（建议值） */
const ROLE_SUGGESTIONS = [
  'baize', 'cangjie', 'diting', 'diting-quick', 'fuxi', 'hongjun',
  'laojun', 'luban', 'luoshen', 'puti', 'taibai', 'wenqu', 'wukong',
];

const loading = ref(true);
const saving = ref(false);
const error = ref('');
const mode = ref<'visual' | 'json'>('visual');
const jsonContent = ref('');
const jsonError = ref('');
const configPath = ref('');
const configData = ref<Record<string, any>>({});
const props = withDefaults(defineProps<EditorNavigationProps>(), { navigation: 'local', navOwner: '' });
const emit = defineEmits<{ 'navigation-change': [snapshot: EditorNavigationSnapshot] }>();
const activeSection = ref(props.sectionId || 'agents');
const visited = ref<Record<string, boolean>>({ [activeSection.value]: true });
function selectSection(id: string) {
  if (!PI_SECTIONS.some(s => s.id === id) || mode.value !== 'visual') return;
  activeSection.value = id; visited.value[id] = true;
}
watch(() => props.sectionId, id => { if (id) selectSection(id); });

const { catalog, catalogError, loadCatalog } = useModelCatalog();

interface AgentRow { role: string; model: string }
interface McpAgentRow { role: string; servers: string[] }
interface ConcurrencyRow { id: string; key: string; limit: number | string; isCustom: boolean }

const customProviderKeys = ref<Set<string>>(new Set());
const customModelKeys = ref<Set<string>>(new Set());
const providerRowIds = ref<Record<string, string>>({});
const modelRowIds = ref<Record<string, string>>({});

let rowIdSeq = 0;
function genConcurrencyRowId(prefix: string) {
  return `${prefix}-${++rowIdSeq}-${Date.now().toString(36)}`;
}

function getProviderRowId(key: string): string {
  if (!providerRowIds.value[key]) {
    providerRowIds.value[key] = genConcurrencyRowId('p-row');
  }
  return providerRowIds.value[key];
}

function getModelRowId(key: string): string {
  if (!modelRowIds.value[key]) {
    modelRowIds.value[key] = genConcurrencyRowId('m-row');
  }
  return modelRowIds.value[key];
}

const agentRows = computed<AgentRow[]>(() => {
  const agents = configData.value.agents;
  if (!agents || typeof agents !== 'object' || Array.isArray(agents)) return [];
  return Object.keys(agents).map((role) => ({
    role,
    model: typeof agents[role]?.model === 'string' ? agents[role].model : '',
  }));
});

const compactionMode = computed(() => compactionModeOf(configData.value.compaction));
const compactionModel = computed(() => compactionModelOf(configData.value.compaction));
const compactionError = computed(() => validateCompactionConfig(configData.value));
const lubanModel = computed(() => {
  const model = configData.value.agents?.luban?.model;
  return typeof model === 'string' ? model : '';
});
const compactionDraft = ref('');
const compactionModeModel = computed({
  get: () => compactionMode.value,
  set: (mode: string) => {
    if (compactionModel.value) compactionDraft.value = compactionModel.value;
    updateCompaction(mode as CompactionMode, compactionDraft.value);
  },
});

function updateCompaction(mode: CompactionMode, model = '') {
  try {
    applyCompactionMode(configData.value, mode, model);
    serialize();
  } catch (error) {
    showError((error as Error).message);
  }
}

function updateCompactionModel(model: string) {
  compactionDraft.value = model;
  updateCompaction('custom', model);
}

function copyLubanCompactionModel() {
  if (lubanModel.value) updateCompactionModel(lubanModel.value);
}

const mcpLeader = computed<string[]>(() => {
  const v = configData.value.mcp?.leader;
  return Array.isArray(v) ? v : [];
});

/** mcp.leader / mcp.default 三态选项与逐模式提示（语义锚定 amagi-pi：未设置 = 全部；[] = 不加载） */
const MCP_MODE_OPTIONS: { value: McpListMode; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'list', label: '白名单' },
  { value: 'none', label: '不加载' },
];

/** 三态对照（InfoTip）：term 与 MCP_MODE_OPTIONS 的选项名一一对应；当前态语义由 MCP_EMPTY_TEXT 常显承载 */
const MCP_MODE_ITEMS: Record<'leader' | 'default', { term: string; desc: string }[]> = {
  leader: [
    { term: '全部', desc: '未设置该键，加载全部（保存时删除 mcp.leader）' },
    { term: '白名单', desc: '仅加载列表内服务器，* 表示全部' },
    { term: '不加载', desc: '保存为空列表 []，Leader 不加载任何 MCP' },
  ],
  default: [
    { term: '全部', desc: '未设置该键，子 Agent 默认加载全部（保存时删除 mcp.default）' },
    { term: '白名单', desc: '仅加载列表内服务器，作为子 Agent 基线，* 表示全部' },
    { term: '不加载', desc: '保存为空列表 []，子 Agent 默认不加载（角色附加服务器仍生效）' },
  ],
};

/** mcp.leader / mcp.default 字段 InfoTip：来源、读取时机、与其它路由的关系（生效时机已由字段标签常显） */
const MCP_TIPS = {
  leader: [
    'Leader 主进程加载的 MCP 服务器名单（amagi-pi v2.8.2+）。',
    '装配时读取一次，改动需重启 pi 生效；与子 Agent 路由相互独立。',
  ],
  default: [
    '所有子 Agent 的基线名单，与 mcp.agents 各角色名单取并集。',
    '每次派发时读盘，改动即时生效。',
  ],
};

/** 名单编辑器空态引导（常显编辑器：all/none 态也可直接添加，输入即切白名单）。
 *  三态下名单必然为空时才出现，因此这是「当前态语义」的常显承载（T21）。 */
const MCP_EMPTY_TEXT: Record<McpListMode, string> = {
  all: '未设置 = 加载全部；添加服务器即切换为白名单',
  none: '[] = 不加载任何 MCP；添加服务器即切换为白名单',
  list: '名单为空，保存后等同「不加载」',
};

/** UI 编辑态与数据推断态解耦：点「白名单」进入编辑视图（数据可保持 []），
 *  避免空列表中间态把高亮跳回「不加载」、编辑器随 v-if 消失（不可达入口根因）。 */
const mcpLeaderUiMode = ref<McpListMode>('all');
const mcpLeaderModeModel = computed<McpListMode>({
  get: () => mcpLeaderUiMode.value,
  set: (m) => switchMcpListMode('leader', m),
});

const mcpDefault = computed<string[]>(() => {
  const v = configData.value.mcp?.default;
  return Array.isArray(v) ? v : [];
});

const mcpDefaultUiMode = ref<McpListMode>('all');
const mcpDefaultModeModel = computed<McpListMode>({
  get: () => mcpDefaultUiMode.value,
  set: (m) => switchMcpListMode('default', m),
});

/** badge：all=0；none=1（有显式配置）；list=条数（跟随 UI 编辑态） */
const mcpBadge = computed<number>(() =>
  (mcpLeaderUiMode.value === 'all' ? 0 : mcpLeaderUiMode.value === 'none' ? 1 : mcpLeader.value.length) +
  (mcpDefaultUiMode.value === 'all' ? 0 : mcpDefaultUiMode.value === 'none' ? 1 : mcpDefault.value.length)
);

const mcpAgentRows = computed<McpAgentRow[]>(() => {
  const agents = configData.value.mcp?.agents;
  if (!agents || typeof agents !== 'object' || Array.isArray(agents)) return [];
  return Object.keys(agents).map((role) => ({
    role,
    servers: Array.isArray(agents[role]) ? agents[role] : [],
  }));
});

const profileValue = computed(() => {
  const v = configData.value.profile;
  return v === 'inherit' ? 'inherit' : 'tiered';
});

const unconfiguredRoleSuggestions = computed(() =>
  ROLE_SUGGESTIONS.filter((s) => !agentRows.value.some((r) => r.role === s)).slice(0, 5)
);

const providerDropdownOptions = computed<DropdownOption[]>(() => {
  const agentModels = agentRows.value.map((r) => r.model).filter(Boolean);
  return buildProviderDropdownOptions(
    catalog.value,
    agentModels,
    configData.value.concurrency?.providers
  );
});

const modelDropdownOptions = computed<DropdownOption[]>(() => {
  const agentModels = agentRows.value.map((r) => r.model).filter(Boolean);
  return buildModelDropdownOptions(
    catalog.value,
    agentModels,
    configData.value.concurrency?.models
  );
});

const concurrencyBadge = computed<number | null>(() => {
  const c = configData.value.concurrency;
  if (!c || typeof c !== 'object') return null;
  let count = 0;
  const defRaw = String(c.default ?? '').trim();
  const defNum = parseInt(defRaw, 10);
  if (defRaw && !isNaN(defNum) && defNum > 0) count += 1;

  if (c.providers && typeof c.providers === 'object' && !Array.isArray(c.providers)) {
    count += Object.keys(c.providers).length;
  }
  if (c.models && typeof c.models === 'object' && !Array.isArray(c.models)) {
    count += Object.keys(c.models).length;
  }
  return count > 0 ? count : null;
});

const concurrencyDefault = computed<string>(() => {
  const def = configData.value.concurrency?.default;
  if (def !== undefined && def !== null) {
    return String(def);
  }
  return '';
});

const concurrencyProviderRows = computed<ConcurrencyRow[]>(() => {
  const providers = configData.value.concurrency?.providers;
  if (!providers || typeof providers !== 'object' || Array.isArray(providers)) return [];
  return Object.keys(providers).map((key) => ({
    id: getProviderRowId(key),
    key,
    limit: providers[key] ?? '',
    isCustom: customProviderKeys.value.has(key),
  }));
});

const concurrencyModelRows = computed<ConcurrencyRow[]>(() => {
  const models = configData.value.concurrency?.models;
  if (!models || typeof models !== 'object' || Array.isArray(models)) return [];
  return Object.keys(models).map((key) => ({
    id: getModelRowId(key),
    key,
    limit: models[key] ?? '',
    isCustom: customModelKeys.value.has(key),
  }));
});

async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    const [content, path] = await Promise.all([getAmagiConfig(), getAmagiConfigPath()]);
    jsonContent.value = content || '';
    configPath.value = path || '';
    parseJsonToConfig();
    // 应用级默认：并发 default 未设置时预填 10（保存落盘；用户可清空保存移除键回落 amagi-pi 内置 4）。
    if (jsonError.value) mode.value = 'json';
    else ensureConcurrencyDefault10();
    void loadCatalog(getPiModelCatalog);
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

function parseJsonToConfig() {
  const trimmed = (jsonContent.value || '').trim();
  if (!trimmed) {
    configData.value = {};
    compactionDraft.value = '';
    jsonError.value = '';
    syncMcpUiModesFromConfig();
    return;
  }
  try {
    const parsed = JSON.parse(trimmed);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      jsonError.value = '配置根必须是 JSON 对象';
      return;
    }
    configData.value = parsed;
    compactionDraft.value = compactionModelOf(parsed.compaction);
    jsonError.value = '';
    syncMcpUiModesFromConfig();
  } catch (e) {
    jsonError.value = 'JSON 格式错误：' + (e as Error).message;
  }
}

/** configData 重新解析后同步 MCP 编辑态（initialLoad 与 JSON→可视化模式切换两条路径共用）。 */
function syncMcpUiModesFromConfig() {
  mcpLeaderUiMode.value = mcpListModeOf(configData.value.mcp?.leader);
  mcpDefaultUiMode.value = mcpListModeOf(configData.value.mcp?.default);
}

function serialize() {
  try {
    jsonContent.value = JSON.stringify(configData.value, null, 2) + '\n';
    jsonError.value = '';
  } catch (e) {
    jsonError.value = '序列化失败：' + (e as Error).message;
  }
}

function updateProfile(v: string) {
  configData.value.profile = v;
  serialize();
}

function addRole(role: string) {
  if (!role) {
    const base = 'role';
    let i = 1;
    while (configData.value.agents?.[`${base}${i}`]) i++;
    role = `${base}${i}`;
  }
  if (!configData.value.agents || typeof configData.value.agents !== 'object') {
    configData.value.agents = {};
  }
  configData.value.agents[role] = { ...(configData.value.agents[role] || {}), model: '' };
  serialize();
}

function removeRole(role: string) {
  if (configData.value.agents) {
    delete configData.value.agents[role];
    serialize();
  }
}

function renameRole(oldRole: string, newRole: string) {
  const trimmed = newRole.trim();
  if (!trimmed || trimmed === oldRole) return;
  const agents = configData.value.agents || {};
  if (agents[trimmed]) return; // 目标已存在，避免覆盖
  const entries = Object.keys(agents).map((k) => [k === oldRole ? trimmed : k, agents[k]] as const);
  configData.value.agents = Object.fromEntries(entries);
  serialize();
}

function updateAgentModel(role: string, spec: string) {
  const agents = configData.value.agents || {};
  agents[role] = { ...(agents[role] || {}), model: spec };
  configData.value.agents = { ...agents };
  serialize();
}

/** 三态切换：UI 态立即切换；all 删键（= 全部）/ none 写 [] / list 进入编辑视图（保留已有名单，从 all/none 切入给空列表） */
function switchMcpListMode(field: 'leader' | 'default', mode: McpListMode) {
  if (field === 'leader') mcpLeaderUiMode.value = mode;
  else mcpDefaultUiMode.value = mode;
  const cur = configData.value.mcp?.[field];
  const keepList = mode === 'list' && Array.isArray(cur) && cur.length > 0 ? (cur as string[]) : [];
  applyMcpListMode(configData.value, field, mode, keepList);
  serialize();
}

function updateMcpDefault(v: string[]) {
  mcpDefaultUiMode.value = 'list';
  applyMcpListMode(configData.value, 'default', 'list', v);
  serialize();
}

function updateMcpLeader(v: string[]) {
  mcpLeaderUiMode.value = 'list';
  applyMcpListMode(configData.value, 'leader', 'list', v);
  serialize();
}

/** 并发 default 未设置时预填 10（仅 initialLoad 一次；JSON 模式编辑/模式切换不重复注入） */
function ensureConcurrencyDefault10() {
  const c = configData.value.concurrency;
  if (!c || typeof c !== 'object' || c.default === undefined || c.default === null) {
    ensureConcurrency().default = 10;
    serialize();
  }
}

function addMcpAgent() {
  if (!configData.value.mcp || typeof configData.value.mcp !== 'object') {
    configData.value.mcp = {};
  }
  if (!configData.value.mcp.agents || typeof configData.value.mcp.agents !== 'object') {
    configData.value.mcp.agents = {};
  }
  const existing = new Set(Object.keys(configData.value.mcp.agents));
  let role = 'role';
  let i = 1;
  while (existing.has(`${role}${i}`)) i++;
  configData.value.mcp.agents[`${role}${i}`] = [];
  serialize();
}

function renameMcpAgent(oldRole: string, newRole: string) {
  const trimmed = newRole.trim();
  const agents = configData.value.mcp?.agents;
  if (!trimmed || trimmed === oldRole || !agents || agents[trimmed]) return;
  const entries = Object.keys(agents).map((k) => [k === oldRole ? trimmed : k, agents[k]] as const);
  configData.value.mcp.agents = Object.fromEntries(entries);
  serialize();
}

function updateMcpAgentServers(role: string, raw: string) {
  const servers = raw.split(',').map((s) => s.trim()).filter(Boolean);
  configData.value.mcp.agents[role] = servers;
  serialize();
}

function removeMcpAgent(role: string) {
  if (configData.value.mcp?.agents) {
    delete configData.value.mcp.agents[role];
    serialize();
  }
}

function ensureConcurrency() {
  if (!configData.value.concurrency || typeof configData.value.concurrency !== 'object') {
    configData.value.concurrency = {};
  }
  return configData.value.concurrency;
}

function ensureConcurrencyProviders() {
  const c = ensureConcurrency();
  if (!c.providers || typeof c.providers !== 'object' || Array.isArray(c.providers)) {
    c.providers = {};
  }
  return c.providers;
}

function ensureConcurrencyModels() {
  const c = ensureConcurrency();
  if (!c.models || typeof c.models !== 'object' || Array.isArray(c.models)) {
    c.models = {};
  }
  return c.models;
}

function cleanupConcurrency() {
  const cleaned = cleanConcurrencyConfig(configData.value.concurrency);
  if (cleaned) {
    configData.value.concurrency = cleaned;
  } else {
    delete configData.value.concurrency;
  }
}

function cleanEmptyLimits() {
  const cleaned = cleanConcurrencyConfig(configData.value.concurrency);
  if (cleaned) {
    configData.value.concurrency = cleaned;
  } else {
    delete configData.value.concurrency;
  }
}

function updateConcurrencyDefault(v: any) {
  const c = ensureConcurrency();
  c.default = normalizeLimitInput(v);
  serialize();
}

function addConcurrencyProvider(providerName?: string) {
  const providers = ensureConcurrencyProviders();
  let key = (providerName || '').trim();
  let isCustom = false;
  if (!key) {
    const standardOptions = providerDropdownOptions.value.filter((o) => o.value !== '__custom__');
    const available = standardOptions.find((o) => providers[o.value] === undefined);
    if (available) {
      key = available.value;
    } else {
      const base = 'provider';
      let i = 1;
      while (providers[`${base}${i}`] !== undefined) i++;
      key = `${base}${i}`;
      isCustom = true;
    }
  }
  if (providers[key] === undefined) {
    providers[key] = 4;
  }
  if (isCustom) {
    customProviderKeys.value.add(key);
  }
  serialize();
}

function onProviderDropdownSelect(oldKey: string, newKey: string) {
  if (newKey === '__custom__') {
    customProviderKeys.value.add(oldKey);
    return;
  }
  if (newKey === oldKey) return;
  const providers = ensureConcurrencyProviders();
  if (providers[newKey] !== undefined) {
    showError(`服务商「${newKey}」已在并发限制列表中`);
    return;
  }
  renameConcurrencyProvider(oldKey, newKey);
}

function switchToPresetProvider(key: string) {
  customProviderKeys.value.delete(key);
  const standardOptions = providerDropdownOptions.value.filter((o) => o.value !== '__custom__');
  if (!standardOptions.some((o) => o.value === key)) {
    const providers = ensureConcurrencyProviders();
    const available = standardOptions.find((o) => providers[o.value] === undefined);
    if (available) {
      renameConcurrencyProvider(key, available.value);
    }
  }
}

function renameConcurrencyProvider(oldKey: string, newKey: string) {
  const trimmed = newKey.trim();
  if (!trimmed || trimmed === oldKey) return;
  const providers = ensureConcurrencyProviders();
  if (providers[trimmed] !== undefined) {
    showError(`服务商「${trimmed}」已存在，请换一个`);
    return;
  }
  const entries = Object.keys(providers).map((k) => [k === oldKey ? trimmed : k, providers[k]] as const);
  configData.value.concurrency.providers = Object.fromEntries(entries);
  if (customProviderKeys.value.has(oldKey)) {
    customProviderKeys.value.delete(oldKey);
    customProviderKeys.value.add(trimmed);
  }
  const oldId = providerRowIds.value[oldKey];
  if (oldId) {
    delete providerRowIds.value[oldKey];
    providerRowIds.value[trimmed] = oldId;
  }
  serialize();
}

function updateConcurrencyProviderLimit(key: string, val: any) {
  if (!configData.value.concurrency?.providers) return;
  configData.value.concurrency.providers[key] = normalizeLimitInput(val);
  // 中间态输入过程中不调用 cleanEmptyLimits/cleanupConcurrency，绝不删键不删行
  serialize();
}

function removeConcurrencyProvider(key: string) {
  if (configData.value.concurrency?.providers) {
    delete configData.value.concurrency.providers[key];
    customProviderKeys.value.delete(key);
    delete providerRowIds.value[key];
    cleanupConcurrency();
    serialize();
  }
}

function addConcurrencyModel(modelSpec?: string) {
  const models = ensureConcurrencyModels();
  let key = (modelSpec || '').trim();
  let isCustom = false;
  if (!key) {
    const standardOptions = modelDropdownOptions.value.filter((o) => o.value !== '__custom__');
    const available = standardOptions.find((o) => models[o.value] === undefined);
    if (available) {
      key = available.value;
    } else {
      const base = 'provider/model';
      let i = 1;
      while (models[`${base}-${i}`] !== undefined) i++;
      key = `${base}-${i}`;
      isCustom = true;
    }
  }
  if (models[key] === undefined) {
    models[key] = 2;
  }
  if (isCustom) {
    customModelKeys.value.add(key);
  }
  serialize();
}

function onModelDropdownSelect(oldKey: string, newKey: string) {
  if (newKey === '__custom__') {
    customModelKeys.value.add(oldKey);
    return;
  }
  if (newKey === oldKey) return;
  const models = ensureConcurrencyModels();
  if (models[newKey] !== undefined) {
    showError(`模型「${newKey}」已在并发限制列表中`);
    return;
  }
  renameConcurrencyModel(oldKey, newKey);
}

function switchToPresetModel(key: string) {
  customModelKeys.value.delete(key);
  const standardOptions = modelDropdownOptions.value.filter((o) => o.value !== '__custom__');
  if (!standardOptions.some((o) => o.value === key)) {
    const models = ensureConcurrencyModels();
    const available = standardOptions.find((o) => models[o.value] === undefined);
    if (available) {
      renameConcurrencyModel(key, available.value);
    }
  }
}

function renameConcurrencyModel(oldKey: string, newKey: string) {
  const trimmed = newKey.trim();
  if (!trimmed || trimmed === oldKey) return;
  const models = ensureConcurrencyModels();
  if (models[trimmed] !== undefined) {
    showError(`模型「${trimmed}」已存在，请换一个`);
    return;
  }
  const entries = Object.keys(models).map((k) => [k === oldKey ? trimmed : k, models[k]] as const);
  configData.value.concurrency.models = Object.fromEntries(entries);
  if (customModelKeys.value.has(oldKey)) {
    customModelKeys.value.delete(oldKey);
    customModelKeys.value.add(trimmed);
  }
  const oldId = modelRowIds.value[oldKey];
  if (oldId) {
    delete modelRowIds.value[oldKey];
    modelRowIds.value[trimmed] = oldId;
  }
  serialize();
}

function updateConcurrencyModelLimit(key: string, val: any) {
  if (!configData.value.concurrency?.models) return;
  configData.value.concurrency.models[key] = normalizeLimitInput(val);
  // 中间态输入过程中不调用 cleanEmptyLimits/cleanupConcurrency，绝不删键不删行
  serialize();
}

function removeConcurrencyModel(key: string) {
  if (configData.value.concurrency?.models) {
    delete configData.value.concurrency.models[key];
    customModelKeys.value.delete(key);
    delete modelRowIds.value[key];
    cleanupConcurrency();
    serialize();
  }
}

function updateAdvisor(value: any) { if (value === undefined) delete configData.value.advisor; else configData.value.advisor = value; serialize(); }

async function handleSave() {
  const advisorError = validateAdvisorConfig(configData.value);
  if (advisorError) { showError(advisorError); return; }

  if (jsonError.value) {
    showError('JSON 格式错误，无法保存');
    return;
  }
  if (compactionError.value) {
    showError(compactionError.value);
    return;
  }
  if (mode.value === 'visual') {
    cleanEmptyLimits();
    cleanupConcurrency();
    serialize();
  }
  if (jsonError.value) {
    showError('JSON 格式错误，无法保存');
    return;
  }
  saving.value = true;
  try {
    await saveAmagiConfig(jsonContent.value);
    showSuccess('配置已保存');
  } catch (err) {
    showError('保存失败：' + (err as Error).message);
  } finally {
    saving.value = false;
  }
}

async function copyPath() {
  if (!configPath.value) return;
  try {
    await navigator.clipboard.writeText(configPath.value);
    showSuccess('路径已复制到剪贴板');
  } catch {
    showError('复制失败');
  }
}

const modeModel = computed({
  get: () => mode.value,
  set: (v: string) => {
    // 非法源码不能被旧的可视化状态序列化覆盖；允许留在 JSON 模式修复。
    if (v === 'visual') {
      parseJsonToConfig();
      if (jsonError.value) return;
    } else if (!jsonError.value) {
      serialize();
    }
    mode.value = v as 'visual' | 'json';
  },
});

const navigationItems = computed(() => PI_SECTIONS.map(s => ({ ...s, invalid: s.id === 'compaction' && !!compactionError.value })));
const localGroups = computed(() => [{ id: 'pi', items: navigationItems.value.map(s => ({ ...s, disabled: loading.value || !!error.value || mode.value !== 'visual' })) }]);
const navigationSnapshot = computed<EditorNavigationSnapshot>(() => ({ owner: props.navOwner, phase: loading.value ? 'loading' : error.value ? 'error' : 'ready', mode: mode.value === 'visual' ? 'visual' : 'source', activeId: activeSection.value, items: navigationItems.value }));
watch(navigationSnapshot, snapshot => emit('navigation-change', snapshot), { immediate: true });
onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.pac-root {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* .pac-json-error / .pac-catalog-warn 仅作测试锚点：背景与内边距交给 StatusBanner compact */

.pac-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pac-content-groups { display:flex; flex-direction:column; gap:12px; min-width:0; }

.pac-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pac-field + .pac-field {
  margin-top: 14px;
}

/* 常显微文案：状态语义用 tertiary，后果用 secondary；每块 ≤ 24 字、单行 */
.pac-caption {
  margin: -2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--tertiary);
}

.pac-caption.is-consequence {
  color: var(--secondary);
}

.pac-caption.mono {
  font-family: var(--mono);
  font-size: 11.5px;
}

/* 字段内的三态 / 压缩模式 pill：收缩为内容宽（Segmented 的 class 落在其根节点上） */
.pac-inline-seg {
  align-self: flex-start;
}

.pac-inline-seg :deep(.seg) {
  flex: 0 0 auto;
  padding: 5px 14px;
  white-space: nowrap;
}

.pac-profile-dd {
  max-width: 280px;
}

.pac-agent-rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pac-agent-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  flex-wrap: wrap;
}

.pac-role-input {
  width: 150px;
}

.pac-agent-spec {
  flex: 1;
  min-width: 280px;
}

.pac-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
}

.pac-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 8px;
}

.pac-actions-inline {
  align-items: center;
  margin-top: 4px;
}

.pac-mcp-agents {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.pac-mcp-agent-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.pac-mcp-servers-input {
  flex: 1;
  min-width: 220px;
}

.pac-limit-input {
  width: 110px;
}

/* 默认并发的 placeholder「留空 = 内置默认 4」比 110px 宽，单独放宽避免被截断 */
.pac-limit-input.pac-limit-default {
  width: 180px;
}

.pac-concurrency-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.pac-concurrency-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.pac-concurrency-dd {
  min-width: 180px;
  max-width: 260px;
}

.pac-concurrency-model-dd {
  flex: 1;
  min-width: 260px;
}

.pac-concurrency-custom-key {
  display: flex;
  align-items: center;
  gap: 6px;
}

.pac-concurrency-key-input {
  width: 160px;
}

.pac-concurrency-custom-model {
  flex: 1;
  min-width: 260px;
}

.pac-concurrency-model-input {
  flex: 1;
  min-width: 200px;
}

.pac-switch-dd-btn {
  font-size: 11px;
  padding: 4px 8px;
  white-space: nowrap;
}

.pac-json-editor {
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.6;
  background: var(--termBg);
  color: var(--termText);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 14px 16px;
  min-height: 400px;
  max-height: 600px;
  resize: vertical;
  outline: none;
  white-space: pre;
  overflow: auto;
  width: 100%;
}

.pac-json-editor:focus {
  border-color: var(--accent);
}
.pac-root { min-width:0; }
.pac-root.editor-local-layout { --section-nav-width:184px; display:grid; grid-template-columns:var(--section-nav-width) minmax(0, 1fr); gap:16px; }
.editor-detail { min-width:0; display:flex; flex-direction:column; gap:12px; container:editor-detail / inline-size; }
.pac-error-link { color:var(--danger-strong); background:none; border:0; font:inherit; font-size:12px; padding:0; cursor:pointer; }
.pac-caption { overflow-wrap:anywhere; }
@media (max-width:1199px) { .pac-root.editor-local-layout { --section-nav-width:144px; gap:12px; } }
@media (max-width:1023px) { .pac-root.editor-local-layout { --section-nav-width:112px; } }
@container editor-detail (max-width:479px) {
  .pac-agent-spec, .pac-concurrency-model-dd, .pac-concurrency-custom-model, .pac-mcp-servers-input { min-width:0; flex-basis:100%; }
  .pac-concurrency-custom-key { flex-wrap:wrap; min-width:0; }
  :deep(input) { max-width:100%; min-width:0; }
  :deep(.mss-row) { flex-direction:column; }
  :deep(.mss-dd), :deep(.mss-dd-level) { width:100%; max-width:100%; min-width:0; }
}
@supports not (container-type: inline-size) {
  .pac-agent-spec, .pac-concurrency-model-dd, .pac-concurrency-custom-model, .pac-mcp-servers-input { min-width:0; flex-basis:100%; }
  .pac-concurrency-custom-key { flex-wrap:wrap; min-width:0; }
  :deep(input) { max-width:100%; min-width:0; }
  :deep(.mss-row) { flex-direction:column; }
  :deep(.mss-dd), :deep(.mss-dd-level) { width:100%; max-width:100%; min-width:0; }
}
</style>
