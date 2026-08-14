<!--
  OmpGlobalConfig - OMP (oh-my-pi) config.yml 可视化配置组件。
  挂载于 Provider Center → 预设 → OMP 引擎标签。
  可视化模式：modelRoles.{role}（provider → model → thinking level 三级下拉，
  数据来自 ~/.omp/agent/models.yml 目录）、task.agentModelOverrides（@role
  引用或直接 spec）。其余键原样保留。源码模式：YAML 文本直接编辑。
-->
<template>
  <div class="ogc-root">
    <div class="ogc-header">
      <h2 class="ogc-title">OMP 配置（oh-my-pi）</h2>
      <p class="ogc-subtitle">
        编辑 ~/.omp/agent/config.yml：modelRoles 角色模型与 task 子代理覆盖。
        模型下拉来自 models.yml 注册表，保存后新会话生效。
      </p>
    </div>

    <LoadingState v-if="loading" message="加载配置中..." />

    <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />

    <template v-else>
      <div class="ogc-toolbar">
        <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" class="ogc-mode-tabs" />
        <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">
          {{ saving ? '保存中...' : '保存配置' }}
        </AppButton>
      </div>

      <div class="ogc-path-row">
        <span class="ogc-path-label">配置文件：</span>
        <code class="ogc-path-value">{{ configPath || '加载中...' }}</code>
        <AppButton v-if="configPath" variant="ghost" size="small" class="ogc-copy-btn" @click="copyPath">
          复制路径
        </AppButton>
      </div>

      <div v-if="yamlError" class="ogc-yaml-error">{{ yamlError }}</div>
      <div v-else-if="mode === 'source'" class="ogc-yaml-valid">YAML 合法</div>
      <div v-if="catalogError" class="ogc-catalog-warn">
        模型目录加载失败，下拉不可用：{{ catalogError }}
      </div>

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="ogc-visual">
        <ConfigCategoryCard
          title="角色模型（modelRoles）"
          category="modelRoles"
          :expanded="expanded.modelRoles"
          :badge="roleRows.length"
          @toggle="expanded.modelRoles = !expanded.modelRoles"
        >
          <p class="ogc-hint">
            每个 worker 角色绑定一个 provider/model:level spec，供 task 子代理通过 @role 引用。
          </p>
          <div class="ogc-rows">
            <div v-for="row in roleRows" :key="row.role" class="ogc-row">
              <TextInput
                :model-value="row.role"
                placeholder="角色名（如 coding_worker）"
                mono
                class="ogc-role-input"
                @update:model-value="renameRole(row.role, $event)"
              />
              <ModelSpecSelector
                :model-value="row.spec"
                :catalog="catalog"
                class="ogc-spec"
                @update:model-value="updateRoleSpec(row.role, $event)"
              />
              <AppButton variant="icon" size="small" aria-label="删除角色" @click="removeRole(row.role)">
                <span class="ogc-remove">×</span>
              </AppButton>
            </div>
          </div>
          <div class="ogc-actions">
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
          title="子代理模型覆盖（task.agentModelOverrides）"
          category="overrides"
          :expanded="expanded.overrides"
          :badge="overrideRows.length"
          @toggle="expanded.overrides = !expanded.overrides"
        >
          <p class="ogc-hint">
            将子代理映射到 modelRoles 角色（@role 引用）或直接绑定模型 spec。
          </p>
          <div class="ogc-rows">
            <div v-for="row in overrideRows" :key="row.agent" class="ogc-row">
              <TextInput
                :model-value="row.agent"
                placeholder="子代理名（如 scout）"
                mono
                class="ogc-role-input"
                @update:model-value="renameOverride(row.agent, $event)"
              />
              <!-- @role 引用：下拉选择 modelRoles 中的角色 -->
              <Dropdown
                v-if="isRoleRef(row.value)"
                :model-value="row.value.slice(1)"
                :options="roleRefOptions"
                class="ogc-roleref-dd"
                @update:model-value="updateOverride(row.agent, '@' + $event)"
              />
              <!-- 直接 spec：三级下拉 -->
              <ModelSpecSelector
                v-else
                :model-value="row.value"
                :catalog="catalog"
                class="ogc-spec"
                @update:model-value="updateOverride(row.agent, $event)"
              />
              <!-- 形态切换 -->
              <div class="ogc-toggle-kind">
                <AppButton
                  variant="ghost"
                  size="small"
                  @click="toggleOverrideKind(row.agent, row.value)"
                >
                  {{ isRoleRef(row.value) ? '改为直接模型' : '改为 @role 引用' }}
                </AppButton>
              </div>
              <AppButton variant="icon" size="small" aria-label="删除" @click="removeOverride(row.agent)">
                <span class="ogc-remove">×</span>
              </AppButton>
            </div>
          </div>
          <div class="ogc-actions">
            <AppButton variant="ghost" size="small" @click="addOverride('')">+ 添加子代理</AppButton>
            <AppButton
              v-for="s in OVERRIDE_SUGGESTIONS.filter((x) => !overrideRows.some((r) => r.agent === x))"
              :key="s"
              variant="ghost"
              size="small"
              @click="addOverride(s)"
            >
              + {{ s }}
            </AppButton>
          </div>
        </ConfigCategoryCard>
      </div>

      <!-- 源码模式 -->
      <div v-else class="ogc-source">
        <textarea v-model="sourceContent" class="ogc-source-editor" spellcheck="false" @input="parseYamlToConfig" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { load as yamlLoad, dump as yamlDump } from 'js-yaml';
import { getOmpConfig, saveOmpConfig, getOmpConfigPath, getOmpModelCatalog } from '../../api/ompConfig';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import Dropdown from '../ui/Dropdown.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import ConfigCategoryCard from './ConfigCategoryCard.vue';
import ModelSpecSelector from './ModelSpecSelector.vue';
import { useModelCatalog } from './useModelCatalog';

const { showSuccess, showError } = useToast();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'source', label: 'YAML' },
];

/** modelRoles 常见角色建议值 */
const ROLE_SUGGESTIONS = [
  'coding_worker', 'review_worker', 'research_worker',
  'cheap_worker', 'designer', 'frontend_visual_worker', 'default',
];

/** task.agentModelOverrides 常见子代理建议值 */
const OVERRIDE_SUGGESTIONS = ['scout', 'security-reviewer', 'librarian', 'task', 'sonic'];

const loading = ref(true);
const saving = ref(false);
const error = ref('');
const mode = ref<'visual' | 'source'>('visual');
const sourceContent = ref('');
const yamlError = ref('');
const configPath = ref('');
const configData = ref<Record<string, any>>({});
const expanded = ref({ modelRoles: true, overrides: false });

const { catalog, catalogError, loadCatalog } = useModelCatalog();

interface RoleRow { role: string; spec: string }
interface OverrideRow { agent: string; value: string }

const roleRows = computed<RoleRow[]>(() => {
  const roles = configData.value.modelRoles;
  if (!roles || typeof roles !== 'object' || Array.isArray(roles)) return [];
  return Object.keys(roles).map((role) => ({
    role,
    spec: typeof roles[role] === 'string' ? roles[role] : '',
  }));
});

const overrideRows = computed<OverrideRow[]>(() => {
  const overrides = configData.value.task?.agentModelOverrides;
  if (!overrides || typeof overrides !== 'object' || Array.isArray(overrides)) return [];
  return Object.keys(overrides).map((agent) => ({
    agent,
    value: typeof overrides[agent] === 'string' ? overrides[agent] : '',
  }));
});

const roleRefOptions = computed(() =>
  roleRows.value.map((r) => ({ value: r.role, label: '@' + r.role }))
);

const unconfiguredRoleSuggestions = computed(() =>
  ROLE_SUGGESTIONS.filter((s) => !roleRows.value.some((r) => r.role === s)).slice(0, 4)
);

function isRoleRef(v: string): boolean {
  return v.startsWith('@') && v.length > 1;
}

async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    const [content, path] = await Promise.all([getOmpConfig(), getOmpConfigPath()]);
    sourceContent.value = content || '';
    configPath.value = path || '';
    parseYamlToConfig();
    void loadCatalog(getOmpModelCatalog);
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

function parseYamlToConfig() {
  const trimmed = (sourceContent.value || '').trim();
  if (!trimmed) {
    configData.value = {};
    yamlError.value = '';
    return;
  }
  try {
    const parsed = yamlLoad(trimmed);
    configData.value = parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, any>)
      : {};
    yamlError.value = '';
  } catch (e) {
    yamlError.value = 'YAML 格式错误：' + (e as Error).message;
  }
}

function serialize() {
  try {
    sourceContent.value = yamlDump(configData.value, { indent: 2, lineWidth: 120 });
    yamlError.value = '';
  } catch (e) {
    yamlError.value = '序列化失败：' + (e as Error).message;
  }
}

// ---- modelRoles 编辑 ----

function addRole(role: string) {
  if (!role) {
    let i = 1;
    while (configData.value.modelRoles?.[`role${i}`]) i++;
    role = `role${i}`;
  }
  if (!configData.value.modelRoles || typeof configData.value.modelRoles !== 'object') {
    configData.value.modelRoles = {};
  }
  configData.value.modelRoles[role] = '';
  serialize();
}

function removeRole(role: string) {
  if (configData.value.modelRoles) {
    delete configData.value.modelRoles[role];
    serialize();
  }
}

function renameRole(oldRole: string, newRole: string) {
  const trimmed = newRole.trim();
  if (!trimmed || trimmed === oldRole) return;
  const roles = configData.value.modelRoles || {};
  if (roles[trimmed]) return;
  const entries = Object.keys(roles).map((k) => [k === oldRole ? trimmed : k, roles[k]] as const);
  configData.value.modelRoles = Object.fromEntries(entries);
  serialize();
}

function updateRoleSpec(role: string, spec: string) {
  const roles = configData.value.modelRoles || {};
  roles[role] = spec;
  configData.value.modelRoles = { ...roles };
  serialize();
}

// ---- task.agentModelOverrides 编辑 ----

function addOverride(agent: string) {
  if (!agent) {
    let i = 1;
    while (configData.value.task?.agentModelOverrides?.[`agent${i}`]) i++;
    agent = `agent${i}`;
  }
  if (!configData.value.task || typeof configData.value.task !== 'object') {
    configData.value.task = {};
  }
  if (!configData.value.task.agentModelOverrides || typeof configData.value.task.agentModelOverrides !== 'object') {
    configData.value.task.agentModelOverrides = {};
  }
  configData.value.task.agentModelOverrides[agent] = '';
  serialize();
}

function removeOverride(agent: string) {
  if (configData.value.task?.agentModelOverrides) {
    delete configData.value.task.agentModelOverrides[agent];
    serialize();
  }
}

function renameOverride(oldAgent: string, newAgent: string) {
  const trimmed = newAgent.trim();
  const overrides = configData.value.task?.agentModelOverrides;
  if (!trimmed || trimmed === oldAgent || !overrides || overrides[trimmed]) return;
  const entries = Object.keys(overrides).map((k) => [k === oldAgent ? trimmed : k, overrides[k]] as const);
  configData.value.task.agentModelOverrides = Object.fromEntries(entries);
  serialize();
}

function updateOverride(agent: string, value: string) {
  const overrides = configData.value.task?.agentModelOverrides || {};
  overrides[agent] = value;
  configData.value.task.agentModelOverrides = { ...overrides };
  serialize();
}

function toggleOverrideKind(agent: string, value: string) {
  if (isRoleRef(value)) {
    // 改为直接模型：默认取引用角色的 spec
    const role = value.slice(1);
    const spec = configData.value.modelRoles?.[role];
    updateOverride(agent, typeof spec === 'string' ? spec : '');
  } else {
    // 改为 @role 引用：默认取第一个角色
    const first = Object.keys(configData.value.modelRoles || {})[0] || 'coding_worker';
    updateOverride(agent, '@' + first);
  }
}

async function handleSave() {
  if (yamlError.value) {
    showError('YAML 格式错误，无法保存');
    return;
  }
  saving.value = true;
  try {
    await saveOmpConfig(sourceContent.value);
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

function handleModeChange(newMode: 'visual' | 'source') {
  if (newMode === 'visual') {
    parseYamlToConfig();
  } else {
    serialize();
  }
}

const modeModel = computed({
  get: () => mode.value,
  set: (v: string) => {
    mode.value = v as 'visual' | 'source';
    handleModeChange(mode.value);
  },
});

onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.ogc-root {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ogc-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ogc-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.ogc-subtitle {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
  line-height: 1.6;
}

.ogc-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.ogc-mode-tabs {
  display: inline-flex;
}

.ogc-path-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.ogc-path-label {
  font-size: 12px;
  color: var(--tertiary);
}

.ogc-path-value {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
  background: var(--control);
  padding: 4px 8px;
  border-radius: 6px;
}

.ogc-copy-btn {
  font-size: 11px;
  padding: 4px 10px;
}

.ogc-yaml-error {
  font-size: 12px;
  color: var(--danger);
  background: rgba(255, 59, 48, 0.1);
  padding: 8px 12px;
  border-radius: 8px;
}

.ogc-yaml-valid {
  font-size: 12px;
  color: var(--success);
  padding: 4px 0;
}

.ogc-catalog-warn {
  font-size: 12px;
  color: var(--warning, #b8860b);
  background: var(--control);
  padding: 8px 12px;
  border-radius: 8px;
}

.ogc-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ogc-hint {
  font-size: 12px;
  color: var(--tertiary);
  margin: 4px 0 0;
  line-height: 1.6;
}

.ogc-rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ogc-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  flex-wrap: wrap;
}

.ogc-role-input {
  width: 170px;
}

.ogc-roleref-dd {
  min-width: 180px;
  max-width: 240px;
}

.ogc-spec {
  flex: 1;
  min-width: 280px;
}

.ogc-toggle-kind {
  display: flex;
  align-items: center;
}

.ogc-toggle-kind button {
  font-size: 11px;
}

.ogc-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
}

.ogc-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 8px;
}

.ogc-source-editor {
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

.ogc-source-editor:focus {
  border-color: var(--accent);
}
</style>
