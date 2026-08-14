<!--
  PiAmagiConfig - pi (amagi-pi) amagi.json 可视化配置组件。
  挂载于 Provider Center → 预设 → Pi 引擎标签。
  可视化模式：profile 选择、agents.{role}.model 三级下拉（provider → model →
  thinking level，数据来自 ~/.pi/agent/models.json 目录）、MCP 路由编辑。
  源码模式：JSON 文本直接编辑。保存走后端原子写入。
-->
<template>
  <div class="pac-root">
    <div class="pac-header">
      <h2 class="pac-title">Pi 配置（amagi-pi）</h2>
      <p class="pac-subtitle">
        编辑 ~/.pi/agent/amagi.json：多 agent 角色的模型分配与 MCP 路由。
        模型下拉来自 models.json 注册表，保存后新会话生效。
      </p>
    </div>

    <LoadingState v-if="loading" message="加载配置中..." />

    <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />

    <template v-else>
      <div class="pac-toolbar">
        <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" class="pac-mode-tabs" />
        <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">
          {{ saving ? '保存中...' : '保存配置' }}
        </AppButton>
      </div>

      <div class="pac-path-row">
        <span class="pac-path-label">配置文件：</span>
        <code class="pac-path-value">{{ configPath || '加载中...' }}</code>
        <AppButton v-if="configPath" variant="ghost" size="small" class="pac-copy-btn" @click="copyPath">
          复制路径
        </AppButton>
      </div>

      <div v-if="jsonError" class="pac-json-error">{{ jsonError }}</div>
      <div v-else-if="mode === 'json'" class="pac-json-valid">JSON 合法</div>
      <div v-if="catalogError" class="pac-catalog-warn">
        模型目录加载失败，下拉不可用：{{ catalogError }}
      </div>

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="pac-visual">
        <ConfigCategoryCard
          title="Profile（分层策略）"
          category="profile"
          :expanded="expanded.profile"
          @toggle="expanded.profile = !expanded.profile"
        >
          <div class="pac-field">
            <label class="pac-label">profile</label>
            <Dropdown
              :model-value="profileValue"
              :options="PROFILE_OPTIONS"
              @update:model-value="updateProfile"
            />
            <p class="pac-hint">
              tiered：按 leader/fast/work/expert 分层套用 profiles/tiered.json；inherit：全部继承 pi 默认模型。
            </p>
          </div>
        </ConfigCategoryCard>

        <ConfigCategoryCard
          title="角色模型（agents）"
          category="agents"
          :expanded="expanded.agents"
          :badge="agentRows.length"
          @toggle="expanded.agents = !expanded.agents"
        >
          <p class="pac-hint">
            按角色覆盖模型，优先级高于 profile。角色名与 amagi-pi agents/*.md 对应。
          </p>
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
        </ConfigCategoryCard>

        <ConfigCategoryCard
          title="MCP 路由"
          category="mcp"
          :expanded="expanded.mcp"
          :badge="mcpDefault.length"
          @toggle="expanded.mcp = !expanded.mcp"
        >
          <div class="pac-field">
            <label class="pac-label">默认服务器（mcp.default，* 表示全部）</label>
            <StringListEditor
              :model-value="mcpDefault"
              item-placeholder="服务器名（如 web-search-prime）"
              add-label="添加服务器"
              empty-text="未设置（默认不启用任何 MCP）"
              mono
              @update:model-value="updateMcpDefault"
            />
          </div>
          <div class="pac-field">
            <label class="pac-label">角色附加服务器（mcp.agents）</label>
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
          </div>
        </ConfigCategoryCard>
      </div>

      <!-- 源码模式 -->
      <div v-else class="pac-json">
        <textarea v-model="jsonContent" class="pac-json-editor" spellcheck="false" @input="parseJsonToConfig" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { getAmagiConfig, saveAmagiConfig, getAmagiConfigPath, getPiModelCatalog } from '../../api/piConfig';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import Dropdown from '../ui/Dropdown.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import ConfigCategoryCard from './ConfigCategoryCard.vue';
import StringListEditor from './StringListEditor.vue';
import ModelSpecSelector from './ModelSpecSelector.vue';
import { useModelCatalog } from './useModelCatalog';

const { showSuccess, showError } = useToast();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
];

const PROFILE_OPTIONS = [
  { value: 'tiered', label: 'tiered（分层默认）' },
  { value: 'inherit', label: 'inherit（继承 pi 默认）' },
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
const expanded = ref({ profile: true, agents: true, mcp: false });

const { catalog, catalogError, loadCatalog } = useModelCatalog();

interface AgentRow { role: string; model: string }
interface McpAgentRow { role: string; servers: string[] }

const agentRows = computed<AgentRow[]>(() => {
  const agents = configData.value.agents;
  if (!agents || typeof agents !== 'object' || Array.isArray(agents)) return [];
  return Object.keys(agents).map((role) => ({
    role,
    model: typeof agents[role]?.model === 'string' ? agents[role].model : '',
  }));
});

const mcpDefault = computed<string[]>(() => {
  const v = configData.value.mcp?.default;
  return Array.isArray(v) ? v : [];
});

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

async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    const [content, path] = await Promise.all([getAmagiConfig(), getAmagiConfigPath()]);
    jsonContent.value = content || '';
    configPath.value = path || '';
    parseJsonToConfig();
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
    jsonError.value = '';
    return;
  }
  try {
    const parsed = JSON.parse(trimmed);
    configData.value = parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
    jsonError.value = '';
  } catch (e) {
    jsonError.value = 'JSON 格式错误：' + (e as Error).message;
  }
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

function updateMcpDefault(v: string[]) {
  if (!configData.value.mcp || typeof configData.value.mcp !== 'object') {
    configData.value.mcp = {};
  }
  configData.value.mcp.default = v;
  serialize();
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

async function handleSave() {
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

function handleModeChange(newMode: 'visual' | 'json') {
  if (newMode === 'visual') {
    parseJsonToConfig();
  } else {
    serialize();
  }
}

const modeModel = computed({
  get: () => mode.value,
  set: (v: string) => {
    mode.value = v as 'visual' | 'json';
    handleModeChange(mode.value);
  },
});

onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.pac-root {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.pac-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.pac-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.pac-subtitle {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
  line-height: 1.6;
}

.pac-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.pac-mode-tabs {
  display: inline-flex;
}

.pac-path-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.pac-path-label {
  font-size: 12px;
  color: var(--tertiary);
}

.pac-path-value {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
  background: var(--control);
  padding: 4px 8px;
  border-radius: 6px;
}

.pac-copy-btn {
  font-size: 11px;
  padding: 4px 10px;
}

.pac-json-error {
  font-size: 12px;
  color: var(--danger);
  background: rgba(255, 59, 48, 0.1);
  padding: 8px 12px;
  border-radius: 8px;
}

.pac-json-valid {
  font-size: 12px;
  color: var(--success);
  padding: 4px 0;
}

.pac-catalog-warn {
  font-size: 12px;
  color: var(--warning, #b8860b);
  background: var(--control);
  padding: 8px 12px;
  border-radius: 8px;
}

.pac-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pac-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pac-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
}

.pac-hint {
  font-size: 12px;
  color: var(--tertiary);
  margin: 4px 0 0;
  line-height: 1.6;
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
</style>
