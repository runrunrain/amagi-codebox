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

  布局（provider-center-layout-redesign §4.6）：导航即标题，不再有 h2 / subtitle；EditorToolbar 常显
  「即时生效」；空配置不再放大块 EmptyState（下方分类卡本身就能编辑），只在工具栏保留「空文件」状态词；
  卡片标题统一「中文标题 + code 键名」，和 Pi / OMP 一致。
-->
<template>
  <div class="oc-global" :class="{ 'editor-local-layout': navigation === 'local' }">
    <SectionNav v-if="navigation === 'local'" label="配置分区" :groups="localGroups" :current-id="mode === 'visual' ? activeSection : ''" controls-id="ocg-editor-detail" :notice="mode !== 'visual' ? '源码模式：返回可视化后切换分区' : ''" @select="selectSection" />
    <div id="ocg-editor-detail" class="editor-detail">
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
      <!-- 工具栏：路径胶囊 + 合法 / 空文件状态 ····· 生效标签 + 模式切换 + 保存。导航即标题，不再有 h2 / subtitle -->
      <EditorToolbar
        :path="configPath"
        path-label="配置文件"
        effect="即时生效"
        :valid-label="mode === 'json' && !jsonError && !saving ? 'JSON 合法' : ''"
        @copy-path="copyPath"
      >
        <template #status>
          <span v-if="mode === 'visual' && isConfigEmpty" class="ocg-note">空文件</span>
        </template>
        <template #actions>
          <Segmented
            v-model="modeModel"
            :options="MODE_OPTIONS"
            variant="pill"
          />
          <AppButton
            variant="primary"
            size="small"
            :disabled="saving"
            @click="handleSave"
          >
            {{ saving ? '保存中...' : '保存配置' }}
          </AppButton>
        </template>
      </EditorToolbar>

      <StatusBanner v-if="jsonError" compact type="error" role="alert" class="ocg-json-error" :message="jsonError" />

      <!-- 可视化模式：空配置时下方分类卡本身就能编辑，不再放大块空态 -->
      <div v-if="mode === 'visual'" class="ocg-visual">
        <!-- 顶层模型：OpenCode 无顶层 model，仅真实存在该键时才渲染（编辑能力不变） -->
        <EditorSection v-if="hasTopLevelModel && visited.model" v-show="activeSection === 'model'" :active="activeSection === 'model'"
          section-id="model" title="顶层模型" code="model" :tip="MODEL_TIP" :badge="1">
          <TextInput
            :model-value="String(configData.model ?? '')"
            placeholder="未设置"
            @update:model-value="updateField('model', $event)"
          />
        </EditorSection>

        <!-- Provider 配置（object map） -->
        <EditorSection v-if="visited.provider" v-show="activeSection === 'provider'" :active="activeSection === 'provider'"
          section-id="provider" title="提供商" code="provider" :badge="providerCount">
          <ProviderMapEditor
            :model-value="providerMap"
            @update:model-value="updateProvider($event)"
          />
        </EditorSection>

        <!-- Agent 配置（object map） -->
        <EditorSection v-if="visited.agent" v-show="activeSection === 'agent'" :active="activeSection === 'agent'"
          section-id="agent" title="Agent" code="agent" :badge="agentCount">
          <AgentMapEditor
            :model-value="agentMap"
            @update:model-value="updateAgent($event)"
          />
        </EditorSection>

        <!-- MCP 配置（object map，真实键 mcp） -->
        <EditorSection v-if="visited.mcp" v-show="activeSection === 'mcp'" :active="activeSection === 'mcp'"
          section-id="mcp" title="MCP 服务器" code="mcp" :badge="mcpCount">
          <McpMapEditor
            :model-value="mcpMap"
            @update:model-value="updateMcp($event)"
          />
        </EditorSection>

        <!-- Permission 配置（object map，值 enum） -->
        <EditorSection v-if="visited.permission" v-show="activeSection === 'permission'" :active="activeSection === 'permission'"
          section-id="permission" title="权限" code="permission" :badge="permissionCount">
          <PermissionMapEditor
            :model-value="permissionMap"
            @update:model-value="updatePermission($event)"
          />
        </EditorSection>

        <!-- Instructions 配置（array<string>） -->
        <EditorSection v-if="visited.instructions" v-show="activeSection === 'instructions'" :active="activeSection === 'instructions'"
          section-id="instructions" title="指令文件" code="instructions" :badge="instructionsCount">
          <StringListEditor
            :model-value="instructionsArray"
            item-placeholder="文件路径（如 resources/core/common/persona.md）"
            add-label="添加文件"
            empty-text="暂无 instructions"
            mono
            @update:model-value="updateInstructions($event)"
          />
        </EditorSection>

        <!-- Plugin 配置（array<string>，OpenCode v1.17+ 标准格式） -->
        <EditorSection v-if="visited.plugin" v-show="activeSection === 'plugin'" :active="activeSection === 'plugin'"
          section-id="plugin" title="插件" code="plugin" :badge="pluginCount">
          <StringListEditor
            :model-value="pluginArray"
            item-placeholder="github:owner/repo 或本地路径"
            add-label="添加插件"
            empty-text="暂无 plugin"
            mono
            @update:model-value="updatePlugin($event)"
          />
        </EditorSection>

        <!-- Experimental 配置（object，类型保持 boolean/number） -->
        <EditorSection v-if="visited.experimental" v-show="activeSection === 'experimental'" :active="activeSection === 'experimental'"
          section-id="experimental" title="实验特性" code="experimental" :badge="experimentalCount">
          <TypedKeyValueEditor
            :model-value="experimentalMap"
            empty-text="暂无 experimental"
            @update:model-value="updateExperimental($event)"
          />
        </EditorSection>

        <!-- 未识别键（$schema 等，兜底 RawJsonEditor） -->
        <EditorSection v-if="unknownKeys.length > 0 && visited.unknown" v-show="activeSection === 'unknown'" :active="activeSection === 'unknown'"
          section-id="unknown" title="其他键" :badge="unknownKeys.length">
          <div class="ocg-unknown-list">
            <div v-for="k in unknownKeys" :key="k" class="ocg-unknown-item">
              <div class="ocg-unknown-key">{{ k }}</div>
              <RawJsonEditor
                :model-value="configData[k]"
                @update:model-value="updateField(k, $event)"
              />
            </div>
          </div>
        </EditorSection>
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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import { GetOpenCodeConfig, SaveOpenCodeConfig, GetOpenCodeConfigPath } from '../../../wailsjs/go/main/App';
import { useToast } from '../../composables/useToast';
import { useProviderStore } from '../../stores/provider';

import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import EditorToolbar from '../ui/EditorToolbar.vue';
import EditorSection from '../ui/EditorSection.vue';
import SectionNav from '../ui/SectionNav.vue';
import { OPENCODE_SECTIONS, type EditorNavigationProps, type EditorNavigationSnapshot } from './configNavigation';
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

/** 顶层模型卡片 InfoTip（design §5.2 T43）：原「Model 说明卡」的正文收进这里，卡片仅在键存在时渲染 */
const MODEL_TIP = 'OpenCode 通常在 agent 级配置模型（agent.<名称>.model），顶层 model 较少使用。';

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

const props = withDefaults(defineProps<EditorNavigationProps>(), { navigation: 'local', navOwner: '' });
const emit = defineEmits<{ 'navigation-change': [snapshot: EditorNavigationSnapshot] }>();
const activeSection = ref(props.sectionId || 'provider');
const visited = ref<Record<string, boolean>>({ [activeSection.value]: true });

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

const navigationItems = computed(() => OPENCODE_SECTIONS.filter(s => s.id === 'model' ? hasTopLevelModel.value : s.id === 'unknown' ? unknownKeys.value.length > 0 : true));
function selectSection(id: string) {
  if (mode.value !== 'visual' || !navigationItems.value.some(s => s.id === id)) return;
  activeSection.value = id; visited.value[id] = true;
}
watch(() => props.sectionId, id => { if (id) selectSection(id); });
watch([navigationItems, loading, mode], () => {
  if (loading.value || error.value || mode.value !== 'visual') return;
  selectSection(navigationItems.value.some(s => s.id === activeSection.value) ? activeSection.value : 'provider');
});
const localGroups = computed(() => [{ id: 'opencode', items: navigationItems.value.map(s => ({ ...s, disabled: loading.value || !!error.value || mode.value !== 'visual' })) }]);
const navigationSnapshot = computed<EditorNavigationSnapshot>(() => ({ owner: props.navOwner, phase: loading.value ? 'loading' : error.value ? 'error' : 'ready', mode: mode.value === 'visual' ? 'visual' : 'source', activeId: activeSection.value, items: navigationItems.value }));
watch(navigationSnapshot, snapshot => emit('navigation-change', snapshot), { immediate: true });
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
  gap: 12px;
}

/* .ocg-json-error 仅作样式锚点：背景与内边距交给 StatusBanner compact */

/* 工具栏状态槽：空文件 */
.ocg-note {
  font-size: 12px;
  color: var(--tertiary);
  white-space: nowrap;
}

.ocg-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
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
.oc-global { min-width:0; }
.oc-global.editor-local-layout { --section-nav-width:184px; display:grid; grid-template-columns:var(--section-nav-width) minmax(0, 1fr); gap:16px; }
.editor-detail { min-width:0; display:flex; flex-direction:column; gap:12px; container:editor-detail / inline-size; }
@media (max-width:1199px) { .oc-global.editor-local-layout { --section-nav-width:144px; gap:12px; } }
@media (max-width:1023px) { .oc-global.editor-local-layout { --section-nav-width:112px; } }
/* 仅复杂记录恢复轻矩形；权限与 provider 内层模型继续无框，原展开行为不变。 */
.oc-global :deep(:is(.provider-editor, .agent-editor, .mcp-editor)) { gap:10px; }
.oc-global :deep(:is(.provider-editor > .pe-card, .agent-editor > .ae-card, .mcp-editor > .me-card)) {
  background:var(--card); border:1px solid var(--separator); border-radius:8px; box-shadow:none; min-width:0; overflow:visible;
}
.oc-global :deep(:is(.provider-editor > .pe-card, .agent-editor > .ae-card, .mcp-editor > .me-card):hover) { border-color:var(--separator); }
.oc-global :deep(:is(.provider-editor .pe-card > .pe-thumb-head, .agent-editor .ae-card > .ae-thumb-head, .mcp-editor .me-card > .me-thumb-head)) { padding:8px 12px; border-radius:7px; min-width:0; }
.oc-global :deep(:is(.provider-editor .pe-thumb-head, .agent-editor .ae-thumb-head, .mcp-editor .me-thumb-head):hover) { background:var(--controlHover); }
.oc-global :deep(:is(.provider-editor .pe-thumb, .agent-editor .ae-thumb, .mcp-editor .me-thumb)) { flex:0 0 24px; width:24px; height:24px; border-radius:6px; transform:none; }
.oc-global :deep(:is(.provider-editor .pe-thumb-icon, .agent-editor .ae-thumb-icon, .mcp-editor .me-thumb-icon)),
.oc-global :deep(:is(.provider-editor .pe-thumb-icon, .agent-editor .ae-thumb-icon, .mcp-editor .me-thumb-icon) svg) { width:16px; height:16px; }
.oc-global :deep(:is(.provider-editor .pe-expanded, .agent-editor .ae-expanded, .mcp-editor .me-expanded)) { padding:10px 12px 12px; min-width:0; }
.oc-global :deep(:is(.provider-editor .pe-thumb-meta, .agent-editor .ae-thumb-meta, .mcp-editor .me-thumb-meta)) { flex-wrap:wrap; }
.oc-global :deep(:is(.provider-editor .pe-thumb-name, .agent-editor .ae-thumb-name, .mcp-editor .me-thumb-name)) { white-space:normal; overflow-wrap:anywhere; }
.oc-global :deep(.perm-editor) { gap:0; }
.oc-global :deep(.perm-editor > .pe-card), .oc-global :deep(.provider-editor .pe-model-card) { background:transparent; border:0; border-radius:0; box-shadow:none; }
.oc-global :deep(.perm-editor > .pe-card + .pe-card), .oc-global :deep(.provider-editor .pe-model-card + .pe-model-card) { border-top:1px solid var(--separator); }
.oc-global :deep(.perm-editor .pe-thumb-head), .oc-global :deep(.perm-editor .pe-expanded) { padding-inline:0; }
.oc-global :deep(.provider-editor .pe-model-card) { padding:10px 0; }
.oc-global :deep(.variants-editor .vme-row) { background:transparent; border-radius:0; }
@container editor-detail (max-width:479px) {
  .oc-global :deep(:is(.provider-editor .pe-expanded, .agent-editor .ae-expanded, .mcp-editor .me-expanded)) { padding-inline:8px; }
  .oc-global :deep(.ae-field-row) { flex-direction:column; }
  .oc-global :deep(.ae-tool-row), .oc-global :deep(.pe-model-head) { flex-wrap:wrap; }
  .oc-global :deep(input), .oc-global :deep(.ae-field) { min-width:0; max-width:100%; }
  .oc-global :deep(.me-reveal-btn) { flex-shrink:0; white-space:nowrap; }
  :deep(.pe-card-head), :deep(.ae-card-head), :deep(.me-card-head), :deep(.pe-row), :deep(.tke-row), :deep(.vme-row-head), :deep(.me-header-row) { flex-wrap:wrap; }
  :deep(.tke-key), :deep(.me-header-key), :deep(.pe-key) { max-width:100%; min-width:0; }
  :deep(.tke-value), :deep(.pe-value), :deep(.me-header-val), :deep(.pe-option-val) { flex-basis:100%; min-width:0; }
  :deep(.masked-value .value) { min-width:0; overflow-wrap:anywhere; }
  :deep(.masked-value .toggle-btn), :deep(.pe-reveal-btn) { flex-shrink:0; white-space:nowrap; }
  :deep(textarea) { max-width:100%; min-width:0; }
}
@supports not (container-type: inline-size) {
  .oc-global :deep(:is(.provider-editor .pe-expanded, .agent-editor .ae-expanded, .mcp-editor .me-expanded)) { padding-inline:8px; }
  .oc-global :deep(.ae-field-row) { flex-direction:column; }
  .oc-global :deep(.ae-tool-row), .oc-global :deep(.pe-model-head) { flex-wrap:wrap; }
  .oc-global :deep(input), .oc-global :deep(.ae-field) { min-width:0; max-width:100%; }
  .oc-global :deep(.me-reveal-btn) { flex-shrink:0; white-space:nowrap; }
  :deep(.pe-card-head), :deep(.ae-card-head), :deep(.me-card-head), :deep(.pe-row), :deep(.tke-row), :deep(.vme-row-head), :deep(.me-header-row) { flex-wrap:wrap; }
  :deep(.tke-key), :deep(.me-header-key), :deep(.pe-key) { max-width:100%; min-width:0; }
  :deep(.tke-value), :deep(.pe-value), :deep(.me-header-val), :deep(.pe-option-val) { flex-basis:100%; min-width:0; }
  :deep(.masked-value .value) { min-width:0; overflow-wrap:anywhere; }
  :deep(.masked-value .toggle-btn), :deep(.pe-reveal-btn) { flex-shrink:0; white-space:nowrap; }
  :deep(textarea) { max-width:100%; min-width:0; }
}
</style>
