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
        编辑 ~/.pi/agent/amagi.json：多 agent 角色的模型分配、MCP 路由与并发限制。
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

        <ConfigCategoryCard
          title="并发限制（concurrency）"
          category="concurrency"
          :expanded="expanded.concurrency"
          :badge="concurrencyBadge"
          @toggle="expanded.concurrency = !expanded.concurrency"
        >
          <p class="pac-hint">
            按 provider/model 分池限制并发请求数。三项全部可选，未匹配时回退默认容量。
          </p>

          <div class="pac-field">
            <label class="pac-label">默认并发数（default）</label>
            <div class="pac-concurrency-default-row">
              <TextInput
                :model-value="concurrencyDefault"
                type="number"
                placeholder="4（留空使用内置默认）"
                mono
                class="pac-limit-input"
                @update:model-value="updateConcurrencyDefault"
              />
              <span class="pac-hint-inline">未单独指定 provider 或 model 时的默认并发池大小（正整数）</span>
            </div>
          </div>

          <div class="pac-field">
            <label class="pac-label">服务商并发（providers）</label>
            <p class="pac-subhint">按服务商限制并发容量，覆盖默认并发数。</p>
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
          </div>

          <div class="pac-field">
            <label class="pac-label">模型并发（models）</label>
            <p class="pac-subhint">按精确 provider/model 限制并发容量，优先级高于服务商限制。</p>
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
import Dropdown, { type DropdownOption } from '../ui/Dropdown.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import ConfigCategoryCard from './ConfigCategoryCard.vue';
import StringListEditor from './StringListEditor.vue';
import ModelSpecSelector from './ModelSpecSelector.vue';
import { useModelCatalog } from './useModelCatalog';
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
const expanded = ref({ profile: true, agents: true, mcp: false, concurrency: false });

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

async function handleSave() {
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

.pac-concurrency-default-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.pac-limit-input {
  width: 110px;
}

.pac-hint-inline {
  font-size: 12px;
  color: var(--tertiary);
}

.pac-subhint {
  font-size: 12px;
  color: var(--tertiary);
  margin: 0 0 4px;
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
</style>
