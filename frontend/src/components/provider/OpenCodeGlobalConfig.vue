<!--
  OpenCodeGlobalConfig - OpenCode 全局配置(opencode.json)可视化编辑组件
  与 OpenCodePresets 预设管理独立，作为 Provider Center 中的"全局配置"区块

  功能特性：
  - 可视化/JSON 双模式切换
  - 可折叠配置类别卡片（8类：Model/Provider/MCP/Agent/Permission/Instructions/Plugin/Experimental）
  - 配置文件路径展示与复制
  - JSON 合法性实时验证
  - 真实 API 集成（GetOpenCodeConfig/SaveOpenCodeConfig）
  - 苹果 HIG 风格
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
        v-model="mode"
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
      <!-- 空配置友好提示：区分"加载成功但配置为空"与"加载失败" -->
      <div v-if="isConfigEmpty" class="ocg-empty-hint">
        <EmptyState
          icon=" "
          title="全局配置为空"
          description="当前 opencode.json 暂无可视化配置项，可切换到 JSON 模式直接编辑。"
        />
      </div>
      <div class="ocg-cards">
        <!-- Model 配置（单项） -->
        <ConfigCategoryCard
          title="Model"
          :expanded="expandedCategories.model"
          :badge="null"
          @toggle="expandedCategories.model = !expandedCategories.model"
        >
          <div v-if="configData.model !== undefined" class="ocg-field">
            <label class="ocg-label">默认模型</label>
            <TextInput
              :model-value="String(configData.model || '')"
              placeholder="未设置"
              @update:model-value="updateField('model', $event)"
            />
          </div>
          <EmptyState v-else icon="—" title="未配置 Model 字段" description="当前 opencode.json 中无 model 配置" />
        </ConfigCategoryCard>

        <!-- Provider 配置（数组） -->
        <ConfigCategoryCard
          title="Provider"
          :expanded="expandedCategories.provider"
          :badge="providerCount"
          @toggle="expandedCategories.provider = !expandedCategories.provider"
        >
          <ProviderListEditor
            :providers="configData.provider || []"
            @update="updateProvider"
          />
        </ConfigCategoryCard>

        <!-- MCP Servers 配置（对象/数组） -->
        <ConfigCategoryCard
          title="MCP Servers"
          :expanded="expandedCategories.mcp"
          :badge="mcpCount"
          @toggle="expandedCategories.mcp = !expandedCategories.mcp"
        >
          <MCPListEditor
            :mcp-servers="configData.mcp_servers || configData.mcpServers || {}"
            @update="updateMcpServers"
          />
        </ConfigCategoryCard>

        <!-- Agent 配置（对象/数组） -->
        <ConfigCategoryCard
          title="Agent"
          :expanded="expandedCategories.agent"
          :badge="agentCount"
          @toggle="expandedCategories.agent = !expandedCategories.agent"
        >
          <AgentListEditor
            :agents="configData.agent || []"
            @update="updateAgent"
          />
        </ConfigCategoryCard>

        <!-- Permission 配置（对象/数组） -->
        <ConfigCategoryCard
          title="Permission"
          :expanded="expandedCategories.permission"
          :badge="permissionCount"
          @toggle="expandedCategories.permission = !expandedCategories.permission"
        >
          <PermissionListEditor
            :permissions="configData.permission || []"
            @update="updatePermission"
          />
        </ConfigCategoryCard>

        <!-- Instructions 配置（对象/键值对） -->
        <ConfigCategoryCard
          title="Instructions"
          :expanded="expandedCategories.instructions"
          :badge="instructionsCount"
          @toggle="expandedCategories.instructions = !expandedCategories.instructions"
        >
          <KeyValueEditor
            :data="configData.instructions || {}"
            @update="updateInstructions"
          />
        </ConfigCategoryCard>

        <!-- Plugin 配置（对象/键值对） -->
        <ConfigCategoryCard
          title="Plugin"
          :expanded="expandedCategories.plugin"
          :badge="pluginCount"
          @toggle="expandedCategories.plugin = !expandedCategories.plugin"
        >
          <KeyValueEditor
            :data="configData.plugin || {}"
            @update="updatePlugin"
          />
        </ConfigCategoryCard>

        <!-- Experimental 配置（对象/键值对） -->
        <ConfigCategoryCard
          title="Experimental"
          :expanded="expandedCategories.experimental"
          :badge="experimentalCount"
          @toggle="expandedCategories.experimental = !expandedCategories.experimental"
        >
          <KeyValueEditor
            :data="configData.experimental || {}"
            @update="updateExperimental"
          />
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
import ProviderListEditor from './ProviderListEditor.vue';
import MCPListEditor from './MCPListEditor.vue';
import AgentListEditor from './AgentListEditor.vue';
import PermissionListEditor from './PermissionListEditor.vue';
import KeyValueEditor from './KeyValueEditor.vue';

const { showSuccess, showError, showInfo } = useToast();
const store = useProviderStore();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
];

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

// 展开状态
const expandedCategories = ref<Record<string, boolean>>({
  model: false,
  provider: true,
  mcp: false,
  agent: false,
  permission: false,
  instructions: false,
  plugin: false,
  experimental: false,
});

// 计算数量徽章
const providerCount = computed(() => {
  const providers = configData.value.provider || configData.value.providers || [];
  return Array.isArray(providers) ? providers.length : Object.keys(providers).length;
});

const mcpCount = computed(() => {
  const mcp = configData.value.mcp_servers || configData.value.mcpServers || {};
  if (Array.isArray(mcp)) return mcp.length;
  return Object.keys(mcp).length;
});

const agentCount = computed(() => {
  const agent = configData.value.agent || [];
  return Array.isArray(agent) ? agent.length : Object.keys(agent).length;
});

const permissionCount = computed(() => {
  const perm = configData.value.permission || [];
  return Array.isArray(perm) ? perm.length : Object.keys(perm).length;
});

const instructionsCount = computed(() => {
  const inst = configData.value.instructions || {};
  return Object.keys(inst).length;
});

const pluginCount = computed(() => {
  const plugin = configData.value.plugin || {};
  return Object.keys(plugin).length;
});

const experimentalCount = computed(() => {
  const exp = configData.value.experimental || {};
  return Object.keys(exp).length;
});

// 空配置判定：用于区分"加载成功但配置为空"与"加载失败"
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
  // 空配置：视为合法的空对象，不报 JSON 错误
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

// 验证 JSON
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

// 更新字段
function updateField(key: string, value: any) {
  configData.value[key] = value;
  serializeConfigToJson();
}

// 更新 Provider
function updateProvider(providers: any[]) {
  configData.value.provider = providers;
  serializeConfigToJson();
}

// 更新 MCP Servers
function updateMcpServers(mcp: Record<string, any> | any[]) {
  configData.value.mcp_servers = mcp;
  serializeConfigToJson();
}

// 更新 Agent
function updateAgent(agents: any[]) {
  configData.value.agent = agents;
  serializeConfigToJson();
}

// 更新 Permission
function updatePermission(permissions: any[] | Record<string, any>) {
  configData.value.permission = permissions;
  serializeConfigToJson();
}

// 更新 Instructions
function updateInstructions(instructions: Record<string, any>) {
  configData.value.instructions = instructions;
  serializeConfigToJson();
}

// 更新 Plugin
function updatePlugin(plugin: Record<string, any>) {
  configData.value.plugin = plugin;
  serializeConfigToJson();
}

// 更新 Experimental
function updateExperimental(experimental: Record<string, any>) {
  configData.value.experimental = experimental;
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
    // 回退方案
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

// 切换模式时同步数据
function handleModeChange(newMode: 'visual' | 'json') {
  if (newMode === 'visual' && jsonContent.value) {
    parseJsonToConfig();
  } else if (newMode === 'json' && configData.value) {
    serializeConfigToJson();
  }
}

// 监听模式切换
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

.ocg-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ocg-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
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
