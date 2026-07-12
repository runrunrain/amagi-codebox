<!--
  OpenCodeGlobalConfig - OpenCode 全局配置(opencode.json)可视化编辑组件
  与 OpenCodePresets 预设管理独立，作为 Provider Center 中的"全局配置"区块

  设计依据：fuxi/20260620-opencode-config-editor-redesign/design.md
  关键点：
  - 真实结构：mcp（非 mcp_servers）、provider/agent/mcp/permission 是 object map、instructions 是 array
  - 顶层无 model 键（OpenCode 用 agent-level model）
  - 所有顶层键完整加载 + 可视化可编辑
  - 统一 v-model（modelValue + update:modelValue）双向绑定
  - 类型保持：boolean/number/object/array 不退化为 string
  - 安全：apiKey/Authorization 用 MaskedValue，env 占位符原样保留
-->
<template>
  <div class="oc-global">
    <!-- 标题区 -->
    <div class="ocg-header">
      <h2 class="ocg-title">OpenCode 全局配置</h2>
      <p class="ocg-subtitle">编辑全局 opencode.json 配置文件。修改后保存立即生效。</p>
    </div>

    <!-- Loading state -->
    <LoadingState v-if="loading" message="加载配置中..." />

    <!-- Error state -->
    <ErrorState
      v-else-if="error"
      title="加载失败"
      :message="error"
      :on-retry="initialLoad"
    />

    <!-- Main content -->
    <template v-else>
      <!-- 工具栏：可视化/JSON 切换 + 保存按钮 -->
      <div class="ocg-toolbar">
        <Segmented
          v-model="modeModel"
          :options="MODE_OPTIONS"
          variant="pill"
          class="ocg-mode-tabs"
        />
        <AppButton
          variant="primary"
          size="small"
          :disabled="saving"
          @click="handleSave"
        >
          {{ saving ? '保存中...' : '保存配置' }}
        </AppButton>
      </div>

      <!-- 配置文件路径区 -->
      <div class="ocg-path-row">
        <span class="ocg-path-label">配置文件：</span>
        <code class="ocg-path-value">{{ configPath || '加载中...' }}</code>
        <AppButton
          v-if="configPath"
          variant="ghost"
          size="small"
          @click="copyPath"
          class="ocg-copy-btn"
        >
          复制路径
        </AppButton>
      </div>

      <!-- JSON 合法性提示 -->
      <div v-if="jsonError" class="ocg-json-error">
        {{ jsonError }}
      </div>
      <div v-else-if="!saving && mode === 'json'" class="ocg-json-valid">
        JSON 合法
      </div>

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="ocg-visual">
        <!-- 空配置友好提示 -->
        <div v-if="isConfigEmpty" class="ocg-empty-hint">
          <EmptyState
            icon=" "
            title="全局配置为空"
            description="当前 opencode.json 暂无可视化配置项，可切换到 JSON 模式直接编辑。"
          />
        </div>
        <div class="ocg-cards">
          <!-- Model 说明卡片（OpenCode 无顶层 model，仅真实存在时才显示输入框） -->
          <ConfigCategoryCard
            title="Model（顶层）"
            category="model"
            :expanded="expandedCategories.model"
            :badge="hasTopLevelModel ? 1 : null"
            @toggle="expandedCategories.model = !expandedCategories.model"
          >
            <div class="ocg-info-text">
              OpenCode 模型在 agent 级配置（如 <code>agent.luban.model</code>），顶层通常无 <code>model</code> 字段。
            </div>
            <div v-if="hasTopLevelModel" class="ocg-field">
              <label class="ocg-label">顶层 model</label>
              <TextInput
                :model-value="String(configData.model ?? '')"
                placeholder="未设置"
                @update:model-value="updateField('model', $event)"
              />
            </div>
          </ConfigCategoryCard>

          <!-- Provider 配置（object map） -->
          <ConfigCategoryCard
            title="Provider"
            category="provider"
            :expanded="expandedCategories.provider"
            :badge="providerCount"
            @toggle="expandedCategories.provider = !expandedCategories.provider"
          >
            <ProviderMapEditor
              :model-value="providerMap"
              @update:model-value="updateProvider($event)"
            />
          </ConfigCategoryCard>

          <!-- Agent 配置（object map） -->
          <ConfigCategoryCard
            title="Agent"
            category="agent"
            :expanded="expandedCategories.agent"
            :badge="agentCount"
            @toggle="expandedCategories.agent = !expandedCategories.agent"
          >
            <AgentMapEditor
              :model-value="agentMap"
              @update:model-value="updateAgent($event)"
            />
          </ConfigCategoryCard>

          <!-- MCP 配置（object map，真实键 mcp） -->
          <ConfigCategoryCard
            title="MCP Servers"
            category="mcp"
            :expanded="expandedCategories.mcp"
            :badge="mcpCount"
            @toggle="expandedCategories.mcp = !expandedCategories.mcp"
          >
            <McpMapEditor
              :model-value="mcpMap"
              @update:model-value="updateMcp($event)"
            />
          </ConfigCategoryCard>

          <!-- Permission 配置（object map，值 enum） -->
          <ConfigCategoryCard
            title="Permission"
            category="permission"
            :expanded="expandedCategories.permission"
            :badge="permissionCount"
            @toggle="expandedCategories.permission = !expandedCategories.permission"
          >
            <PermissionMapEditor
              :model-value="permissionMap"
              @update:model-value="updatePermission($event)"
            />
          </ConfigCategoryCard>

          <!-- Instructions 配置（array<string>） -->
          <ConfigCategoryCard
            title="Instructions"
            category="instructions"
            :expanded="expandedCategories.instructions"
            :badge="instructionsCount"
            @toggle="expandedCategories.instructions = !expandedCategories.instructions"
          >
            <StringListEditor
              :model-value="instructionsArray"
              item-placeholder="文件路径（如 resources/core/common/persona.md）"
              add-label="添加文件"
              empty-text="暂无 instructions"
              mono
              @update:model-value="updateInstructions($event)"
            />
          </ConfigCategoryCard>

          <!-- Plugin 配置（array<string>，OpenCode v1.17+ 标准格式） -->
          <ConfigCategoryCard
            title="Plugin"
            category="plugin"
            :expanded="expandedCategories.plugin"
            :badge="pluginCount"
            @toggle="expandedCategories.plugin = !expandedCategories.plugin"
          >
            <StringListEditor
              :model-value="pluginArray"
              item-placeholder="github:owner/repo 或本地路径"
              add-label="添加插件"
              empty-text="暂无 plugin"
              mono
              @update:model-value="updatePlugin($event)"
            />
          </ConfigCategoryCard>

          <!-- Experimental 配置（object，类型保持 boolean/number） -->
          <ConfigCategoryCard
            title="Experimental"
            category="experimental"
            :expanded="expandedCategories.experimental"
            :badge="experimentalCount"
            @toggle="expandedCategories.experimental = !expandedCategories.experimental"
          >
            <TypedKeyValueEditor
              :model-value="experimentalMap"
              empty-text="暂无 experimental"
              @update:model-value="updateExperimental($event)"
            />
          </ConfigCategoryCard>

          <!-- 未识别键（$schema 等，兜底 RawJsonEditor） -->
          <ConfigCategoryCard
            v-if="unknownKeys.length > 0"
            title="其他键（兜底 JSON）"
            category="unknown"
            :expanded="expandedCategories.unknown"
            :badge="unknownKeys.length"
            @toggle="expandedCategories.unknown = !expandedCategories.unknown"
          >
            <div class="ocg-unknown-list">
              <div v-for="k in unknownKeys" :key="k" class="ocg-unknown-item">
                <div class="ocg-unknown-key">{{ k }}</div>
                <RawJsonEditor
                  :model-value="configData[k]"
                  @update:model-value="updateField(k, $event)"
                />
              </div>
            </div>
          </ConfigCategoryCard>
        </div>
      </div>

      <!-- JSON 模式 -->
      <div v-else class="ocg-json">
        <textarea
          v-model="jsonContent"
          class="ocg-json-editor"
          spellcheck="false"
          @input="validateJson"
        />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { GetOpenCodeConfig, SaveOpenCodeConfig, GetOpenCodeConfigPath } from '../../../wailsjs/go/main/App';
import { useToast } from '../../composables/useToast';
import { useProviderStore } from '../../stores/provider';

import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import EmptyState from '../ui/EmptyState.vue';
import ConfigCategoryCard from './ConfigCategoryCard.vue';
import ProviderMapEditor from './ProviderMapEditor.vue';
import AgentMapEditor from './AgentMapEditor.vue';
import McpMapEditor from './McpMapEditor.vue';
import PermissionMapEditor from './PermissionMapEditor.vue';
import StringListEditor from './StringListEditor.vue';
import TypedKeyValueEditor from './TypedKeyValueEditor.vue';
import RawJsonEditor from './RawJsonEditor.vue';

const { showSuccess, showError } = useToast();
const store = useProviderStore();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
];

// 真实键名常量（消除别名魔法字符串，无 fallback）
const CONFIG_KEYS = {
  schema: '$schema',
  agent: 'agent',
  provider: 'provider',
  mcp: 'mcp',
  permission: 'permission',
  instructions: 'instructions',
  plugin: 'plugin',
  experimental: 'experimental',
  model: 'model',
} as const;

const KNOWN_KEYS = new Set<string>(Object.values(CONFIG_KEYS));

// 状态
const loading = ref(true);
const saving = ref(false);
const error = ref('');
const mode = ref<'visual' | 'json'>('visual');
const jsonContent = ref('');
const jsonError = ref('');
const configPath = ref('');

// 配置数据（可视化模式使用）
const configData = ref<Record<string, any>>({});

// 展开状态：默认全部收起（主上要求"不要默认完全展开"）
const expandedCategories = ref<Record<string, boolean>>({
  model: false,
  provider: false,
  mcp: false,
  agent: false,
  permission: false,
  instructions: false,
  plugin: false,
  experimental: false,
  unknown: false,
});

// ===== 计算属性：各顶层键的强类型视图（直接映射，无别名 fallback） =====

// provider object map
const providerMap = computed<Record<string, any>>(() => {
  const v = configData.value[CONFIG_KEYS.provider];
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
});

// agent object map
const agentMap = computed<Record<string, any>>(() => {
  const v = configData.value[CONFIG_KEYS.agent];
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
});

// mcp object map（真实键，无 mcp_servers fallback）
const mcpMap = computed<Record<string, any>>(() => {
  const v = configData.value[CONFIG_KEYS.mcp];
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
});

// permission object map
const permissionMap = computed<Record<string, string>>(() => {
  const v = configData.value[CONFIG_KEYS.permission];
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
});

// instructions array<string>
const instructionsArray = computed<string[]>(() => {
  const v = configData.value[CONFIG_KEYS.instructions];
  return Array.isArray(v) ? v : [];
});

// plugin array<string>（OpenCode v1.17+ 标准格式，元素如 github:owner/repo）
const pluginArray = computed<string[]>(() => {
  const v = configData.value[CONFIG_KEYS.plugin];
  return Array.isArray(v) ? v : [];
});

// experimental object（类型保持）
const experimentalMap = computed<Record<string, any>>(() => {
  const v = configData.value[CONFIG_KEYS.experimental];
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
});

// 顶层 model 是否真实存在
const hasTopLevelModel = computed(() => {
  return Object.prototype.hasOwnProperty.call(configData.value, CONFIG_KEYS.model);
});

// 未识别键清单
const unknownKeys = computed<string[]>(() => {
  return Object.keys(configData.value).filter((k) => !KNOWN_KEYS.has(k));
});

// 计算数量徽章
const providerCount = computed(() => Object.keys(providerMap.value).length);
const agentCount = computed(() => Object.keys(agentMap.value).length);
const mcpCount = computed(() => Object.keys(mcpMap.value).length);
const permissionCount = computed(() => Object.keys(permissionMap.value).length);
const instructionsCount = computed(() => instructionsArray.value.length);
const pluginCount = computed(() => pluginArray.value.length);
const experimentalCount = computed(() => Object.keys(experimentalMap.value).length);

// 空配置判定
const isConfigEmpty = computed(() => {
  return (
    !jsonContent.value ||
    !jsonContent.value.trim() ||
    !configData.value ||
    Object.keys(configData.value).length === 0
  );
});

// 初始化加载
async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    const [content, path] = await Promise.all([
      GetOpenCodeConfig(),
      GetOpenCodeConfigPath(),
    ]);
    jsonContent.value = content || '';
    configPath.value = path || '';
    parseJsonToConfig();
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

// 将 JSON 解析为配置对象
function parseJsonToConfig() {
  const trimmed = (jsonContent.value || '').trim();
  if (!trimmed) {
    configData.value = {};
    jsonError.value = '';
    return;
  }
  try {
    const parsed = JSON.parse(trimmed);
    configData.value = parsed && typeof parsed === 'object' ? parsed : {};
    jsonError.value = '';
  } catch (e) {
    jsonError.value = 'JSON 格式错误：' + (e as Error).message;
  }
}

function validateJson() {
  parseJsonToConfig();
}

// 将配置对象序列化为 JSON
function serializeConfigToJson() {
  try {
    jsonContent.value = JSON.stringify(configData.value, null, 2) + '\n';
    jsonError.value = '';
  } catch (e) {
    jsonError.value = '序列化失败：' + (e as Error).message;
  }
}

// ===== 各顶层键的更新函数（直接写回 configData[key]，保持类型） =====

function updateField(key: string, value: any) {
  configData.value[key] = value;
  serializeConfigToJson();
}

function updateProvider(v: Record<string, any>) {
  configData.value[CONFIG_KEYS.provider] = v;
  serializeConfigToJson();
}

function updateAgent(v: Record<string, any>) {
  configData.value[CONFIG_KEYS.agent] = v;
  serializeConfigToJson();
}

function updateMcp(v: Record<string, any>) {
  configData.value[CONFIG_KEYS.mcp] = v;
  serializeConfigToJson();
}

function updatePermission(v: Record<string, string>) {
  configData.value[CONFIG_KEYS.permission] = v;
  serializeConfigToJson();
}

function updateInstructions(v: string[]) {
  configData.value[CONFIG_KEYS.instructions] = v;
  serializeConfigToJson();
}

function updatePlugin(v: string[]) {
  configData.value[CONFIG_KEYS.plugin] = v;
  serializeConfigToJson();
}

function updateExperimental(v: Record<string, any>) {
  configData.value[CONFIG_KEYS.experimental] = v;
  serializeConfigToJson();
}

// 保存配置
async function handleSave() {
  if (jsonError.value) {
    showError('JSON 格式错误，无法保存');
    return;
  }
  saving.value = true;
  try {
    await SaveOpenCodeConfig(jsonContent.value);
    showSuccess('配置已保存');
  } catch (err) {
    showError('保存失败：' + (err as Error).message);
  } finally {
    saving.value = false;
  }
}

// 复制路径
async function copyPath() {
  if (!configPath.value) return;
  try {
    await navigator.clipboard.writeText(configPath.value);
    showSuccess('路径已复制到剪贴板');
  } catch {
    const textarea = document.createElement('textarea');
    textarea.value = configPath.value;
    document.body.appendChild(textarea);
    textarea.select();
    try {
      document.execCommand('copy');
      showSuccess('路径已复制到剪贴板');
    } catch {
      showError('复制失败');
    }
    document.body.removeChild(textarea);
  }
}

// 模式切换时同步数据
function handleModeChange(newMode: 'visual' | 'json') {
  if (newMode === 'visual' && jsonContent.value) {
    parseJsonToConfig();
  } else if (newMode === 'json' && configData.value) {
    serializeConfigToJson();
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
  initialLoad();
});

// 避免 store 未使用告警（保留兼容性，未来可能用于其他用途）
void store;
</script>

<style scoped>
.oc-global {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ocg-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ocg-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.ocg-subtitle {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
}

.ocg-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.ocg-mode-tabs {
  display: inline-flex;
}

.ocg-path-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.ocg-path-label {
  font-size: 12px;
  color: var(--tertiary);
}

.ocg-path-value {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
  background: var(--control);
  padding: 4px 8px;
  border-radius: 6px;
}

.ocg-copy-btn {
  font-size: 11px;
  padding: 4px 10px;
}

.ocg-json-error {
  font-size: 12px;
  color: var(--danger);
  background: rgba(255, 59, 48, 0.1);
  padding: 8px 12px;
  border-radius: 8px;
}

.ocg-json-valid {
  font-size: 12px;
  color: var(--success);
  padding: 4px 0;
}

.ocg-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ocg-empty-hint {
  background: var(--control);
  border: 1px dashed var(--separator);
  border-radius: 12px;
  padding: 8px 16px;
  margin-bottom: 4px;
}

.ocg-empty-hint :deep(.empty-state) {
  padding: 28px 16px;
}

.ocg-cards {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ocg-info-text {
  font-size: 12px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 8px;
  padding: 10px 12px;
  line-height: 1.6;
  margin-bottom: 10px;
}

.ocg-info-text code {
  font-family: var(--mono);
  font-size: 11px;
  background: var(--termBg);
  color: var(--termText);
  padding: 1px 5px;
  border-radius: 4px;
}

.ocg-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 8px;
}

.ocg-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
}

.ocg-unknown-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ocg-unknown-key {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--secondary);
  margin-bottom: 4px;
}

.ocg-json {
  display: flex;
  flex-direction: column;
}

.ocg-json-editor {
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--label);
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
}

.ocg-json-editor:focus {
  border-color: var(--accent);
}
</style>
