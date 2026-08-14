<!--
  ProviderRegistryEditor - pi/omp 模型提供商注册表可视化编辑器。
  pi 编辑 ~/.pi/agent/models.json（JSON），omp 编辑 ~/.omp/agent/models.yml（YAML），
  两者 providers 结构一致：{ api, apiKey?, baseUrl?, models: [{id, ...元数据}] }。
  可视化模式完全可视化：provider 折叠卡片（api 下拉/回退输入、baseUrl、apiKey
  密文输入、模型列表增删改）；高级字段用专用编辑器（thinkingLevelMap 级别映射、
  thinking 模式+级别列表、input 能力列表、cost 四项费用），其余未知字段走
  VisualValueEditor 递归可视化编辑（字符串/数字/布尔/列表/嵌套对象，零 JSON）。
  源码模式完全源码：JSON/YAML 文本直接编辑。
  注意：amagi-* 前缀的 provider 由 CodeBox 提供商中心同步管理，手工修改可能被覆盖。
-->
<template>
  <div class="pre-root">
    <div class="pre-header">
      <h2 class="pre-title">{{ isPi ? 'Pi 模型提供商（models.json）' : 'OMP 模型提供商（models.yml）' }}</h2>
      <p class="pre-subtitle">
        编辑模型注册表：提供商连接信息（api / baseUrl / apiKey）与模型元数据。
        Agent 配置中的 provider/model 下拉即来自此文件，保存后重新进入配置页生效。
      </p>
    </div>

    <LoadingState v-if="loading" message="加载注册表中..." />

    <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />

    <template v-else>
      <div class="pre-toolbar">
        <Segmented v-model="modeModel" :options="modeOptions" variant="pill" class="pre-mode-tabs" />
        <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">
          {{ saving ? '保存中...' : '保存配置' }}
        </AppButton>
      </div>

      <div class="pre-path-row">
        <span class="pre-path-label">注册表文件：</span>
        <code class="pre-path-value">{{ configPath || '加载中...' }}</code>
        <AppButton v-if="configPath" variant="ghost" size="small" class="pre-copy-btn" @click="copyPath">
          复制路径
        </AppButton>
      </div>

      <div v-if="parseError" class="pre-parse-error">{{ parseError }}（可视化不可用，请切到源码模式修复）</div>
      <div v-else-if="mode === 'source'" class="pre-parse-valid">{{ isPi ? 'JSON' : 'YAML' }} 合法</div>

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="pre-visual">
        <div v-if="providerNames.length === 0" class="pre-empty">
          <EmptyState title="注册表为空" description="尚无提供商，添加一个开始配置模型。" />
        </div>

        <ConfigCategoryCard
          v-for="name in providerNames"
          :key="name"
          :title="name"
          :category="'provider'"
          :expanded="expandedProviders[name] ?? false"
          :badge="modelCount(name)"
          @toggle="toggleProvider(name)"
        >
          <div v-if="name.startsWith('amagi-')" class="pre-managed-warn">
            amagi-* 提供商由 CodeBox 提供商中心同步管理，手工修改可能在下次同步时被覆盖。
          </div>
          <div class="pre-fields">
            <div class="pre-field">
              <label class="pre-label">api（协议类型）</label>
              <Dropdown
                v-if="!providerApiRaw(name) || API_OPTIONS.some((o) => o.value === providerApiRaw(name))"
                :model-value="providerApiRaw(name)"
                :options="API_OPTIONS"
                placeholder="选择协议"
                @update:model-value="setProviderField(name, 'api', $event)"
              />
              <TextInput
                v-else
                :model-value="providerApiRaw(name)"
                mono
                placeholder="api 类型"
                @update:model-value="setProviderField(name, 'api', $event)"
              />
            </div>
            <div class="pre-field">
              <label class="pre-label">baseUrl</label>
              <TextInput
                :model-value="providerFieldString(name, 'baseUrl')"
                mono
                placeholder="https://..."
                @update:model-value="setProviderField(name, 'baseUrl', $event)"
              />
            </div>
            <div class="pre-field">
              <label class="pre-label">apiKey（留空表示未设置）</label>
              <TextInput
                :model-value="providerFieldString(name, 'apiKey')"
                type="password"
                mono
                placeholder="sk-..."
                @update:model-value="setProviderField(name, 'apiKey', $event)"
              />
            </div>
          </div>

          <!-- provider 级其他字段（auth: none / authHeader 等），完全可视化 -->
          <div class="pre-unknown">
            <div class="pre-unknown-title">其他字段</div>
            <div v-if="providerUnknownKeys(name).length === 0" class="pre-unknown-empty">暂无其他字段</div>
            <div v-for="k in providerUnknownKeys(name)" :key="k" class="pre-unknown-item">
              <div class="pre-unknown-head">
                <span class="pre-unknown-key">{{ k }}</span>
                <AppButton variant="icon" size="small" aria-label="删除字段" @click="deleteProviderField(name, k)">
                  <span class="pre-remove">×</span>
                </AppButton>
              </div>
              <VisualValueEditor
                :model-value="providersMap[name]?.[k]"
                @update:model-value="setProviderField(name, k, $event)"
              />
            </div>
            <div class="pre-actions">
              <AppButton variant="ghost" size="small" @click="addProviderField(name, '')">+ 字符串字段</AppButton>
              <AppButton variant="ghost" size="small" @click="addProviderField(name, false)">+ 布尔字段</AppButton>
              <AppButton variant="ghost" size="small" @click="addProviderField(name, 0)">+ 数字字段</AppButton>
              <AppButton variant="ghost" size="small" @click="addProviderField(name, {})">+ 对象字段</AppButton>
            </div>
          </div>

          <!-- 模型列表 -->
          <div class="pre-models">
            <div class="pre-models-head">
              <span class="pre-label">模型（{{ modelCount(name) }}）</span>
            </div>
            <div v-for="(m, idx) in providerModels(name)" :key="idx" class="pre-model">
              <div class="pre-model-head">
                <TextInput
                  :model-value="modelFieldString(name, idx, 'id')"
                  mono
                  placeholder="模型 id（如 glm-5.2）"
                  class="pre-model-id"
                  @update:model-value="setModelField(name, idx, 'id', $event)"
                />
                <Badge v-if="modelFieldString(name, idx, 'id') === ''" type="warning" text="id 不能为空" />
                <div class="pre-model-reasoning">
                  <span class="pre-label">推理</span>
                  <Switch
                    :model-value="!!providersMap[name]?.models?.[idx]?.reasoning"
                    @update:model-value="setModelField(name, idx, 'reasoning', $event)"
                  />
                </div>
                <AppButton variant="icon" size="small" aria-label="删除模型" @click="removeModel(name, idx)">
                  <span class="pre-remove">×</span>
                </AppButton>
              </div>
              <div class="pre-model-grid">
                <div class="pre-field">
                  <label class="pre-label">显示名（name）</label>
                  <TextInput
                    :model-value="modelFieldString(name, idx, 'name')"
                    placeholder="展示名称，可留空"
                    @update:model-value="setModelField(name, idx, 'name', $event)"
                  />
                </div>
                <div class="pre-field">
                  <label class="pre-label">上下文窗口（contextWindow）</label>
                  <TextInput
                    :model-value="modelFieldString(name, idx, 'contextWindow')"
                    type="number"
                    placeholder="如 128000"
                    @update:model-value="setModelNumber(name, idx, 'contextWindow', $event)"
                  />
                </div>
                <div class="pre-field">
                  <label class="pre-label">最大输出（maxTokens）</label>
                  <TextInput
                    :model-value="modelFieldString(name, idx, 'maxTokens')"
                    type="number"
                    placeholder="如 8192"
                    @update:model-value="setModelNumber(name, idx, 'maxTokens', $event)"
                  />
                </div>
              </div>
              <!-- 模型高级字段：专用可视化编辑器，完全可视化 -->
              <div class="pre-unknown">
                <div class="pre-unknown-title">高级字段</div>
                <div
                  v-if="modelAdvancedPresent(name, idx).length === 0 && modelExtraKeys(name, idx).length === 0"
                  class="pre-unknown-empty"
                >
                  暂无高级字段
                </div>

                <!-- thinkingLevelMap：级别映射 -->
                <div v-if="hasModelField(name, idx, 'thinkingLevelMap')" class="pre-unknown-item">
                  <div class="pre-unknown-head">
                    <span class="pre-unknown-key">thinkingLevelMap（级别映射）</span>
                    <AppButton variant="icon" size="small" aria-label="删除" @click="deleteModelField(name, idx, 'thinkingLevelMap')">
                      <span class="pre-remove">×</span>
                    </AppButton>
                  </div>
                  <ThinkingLevelMapEditor
                    :model-value="modelFieldObject(name, idx, 'thinkingLevelMap')"
                    @update:model-value="setModelField(name, idx, 'thinkingLevelMap', $event)"
                  />
                </div>

                <!-- thinking：mode + levels -->
                <div v-if="hasModelField(name, idx, 'thinking')" class="pre-unknown-item">
                  <div class="pre-unknown-head">
                    <span class="pre-unknown-key">thinking（思考模式）</span>
                    <AppButton variant="icon" size="small" aria-label="删除" @click="deleteModelField(name, idx, 'thinking')">
                      <span class="pre-remove">×</span>
                    </AppButton>
                  </div>
                  <div class="pre-thinking">
                    <div class="pre-field">
                      <label class="pre-label">mode</label>
                      <TextInput
                        :model-value="thinkingMode(name, idx)"
                        mono
                        placeholder="如 effort"
                        @update:model-value="updateThinkingMode(name, idx, $event)"
                      />
                    </div>
                    <div class="pre-field">
                      <label class="pre-label">levels</label>
                      <StringListEditor
                        :model-value="thinkingLevels(name, idx)"
                        item-placeholder="级别（如 high）"
                        add-label="添加级别"
                        empty-text="未设置级别"
                        mono
                        @update:model-value="updateThinkingLevels(name, idx, $event)"
                      />
                    </div>
                  </div>
                </div>

                <!-- input：输入能力 -->
                <div v-if="hasModelField(name, idx, 'input')" class="pre-unknown-item">
                  <div class="pre-unknown-head">
                    <span class="pre-unknown-key">input（输入能力）</span>
                    <AppButton variant="icon" size="small" aria-label="删除" @click="deleteModelField(name, idx, 'input')">
                      <span class="pre-remove">×</span>
                    </AppButton>
                  </div>
                  <StringListEditor
                    :model-value="modelFieldStringArray(name, idx, 'input')"
                    item-placeholder="能力（如 text / image）"
                    add-label="添加能力"
                    empty-text="未设置"
                    mono
                    @update:model-value="setModelField(name, idx, 'input', $event)"
                  />
                </div>

                <!-- cost：四项费用 -->
                <div v-if="hasModelField(name, idx, 'cost')" class="pre-unknown-item">
                  <div class="pre-unknown-head">
                    <span class="pre-unknown-key">cost（USD / 百万 token）</span>
                    <AppButton variant="icon" size="small" aria-label="删除" @click="deleteModelField(name, idx, 'cost')">
                      <span class="pre-remove">×</span>
                    </AppButton>
                  </div>
                  <div class="pre-cost-grid">
                    <div v-for="f in COST_FIELDS" :key="f.key" class="pre-field">
                      <label class="pre-label">{{ f.label }}</label>
                      <TextInput
                        :model-value="costFieldString(name, idx, f.key)"
                        type="number"
                        :placeholder="f.placeholder"
                        @update:model-value="updateCostField(name, idx, f.key, $event)"
                      />
                    </div>
                  </div>
                </div>

                <!-- 其余未知字段：递归可视化编辑 -->
                <div v-for="k in modelExtraKeys(name, idx)" :key="k" class="pre-unknown-item">
                  <div class="pre-unknown-head">
                    <span class="pre-unknown-key">{{ k }}</span>
                    <AppButton variant="icon" size="small" aria-label="删除字段" @click="deleteModelField(name, idx, k)">
                      <span class="pre-remove">×</span>
                    </AppButton>
                  </div>
                  <VisualValueEditor
                    :model-value="providersMap[name]?.models?.[idx]?.[k]"
                    @update:model-value="setModelField(name, idx, k, $event)"
                  />
                </div>

                <div class="pre-actions">
                  <AppButton variant="ghost" size="small" @click="addModelAdvanced(name, idx, 'thinkingLevelMap')">+ 级别映射</AppButton>
                  <AppButton variant="ghost" size="small" @click="addModelAdvanced(name, idx, 'thinking')">+ thinking</AppButton>
                  <AppButton variant="ghost" size="small" @click="addModelAdvanced(name, idx, 'input')">+ 输入能力</AppButton>
                  <AppButton variant="ghost" size="small" @click="addModelAdvanced(name, idx, 'cost')">+ 费用</AppButton>
                  <AppButton variant="ghost" size="small" @click="addModelExtraField(name, idx, {})">+ 对象字段</AppButton>
                  <AppButton variant="ghost" size="small" @click="addModelExtraField(name, idx, '')">+ 字符串字段</AppButton>
                </div>
              </div>
            </div>
            <div class="pre-actions">
              <AppButton variant="ghost" size="small" @click="addModel(name)">+ 添加模型</AppButton>
            </div>
          </div>

          <div class="pre-actions pre-provider-actions">
            <AppButton variant="ghost" size="small" @click="removeProvider(name)">删除提供商 {{ name }}</AppButton>
          </div>
        </ConfigCategoryCard>

        <!-- 添加提供商 -->
        <div class="pre-add-provider">
          <TextInput
            v-model="newProviderName"
            mono
            placeholder="新提供商名（如 amagi-my-relay）"
            class="pre-add-input"
          />
          <AppButton variant="ghost" size="small" :disabled="!newProviderName.trim()" @click="addProvider">
            + 添加提供商
          </AppButton>
        </div>

        <!-- 根级其他键（$schema 等），完全可视化 -->
        <ConfigCategoryCard
          v-if="rootUnknownKeys.length"
          title="其他顶层键"
          category="unknown"
          :expanded="expandedRoot"
          :badge="rootUnknownKeys.length"
          @toggle="expandedRoot = !expandedRoot"
        >
          <div class="pre-unknown">
            <div v-for="k in rootUnknownKeys" :key="k" class="pre-unknown-item">
              <div class="pre-unknown-head">
                <span class="pre-unknown-key">{{ k }}</span>
                <AppButton variant="icon" size="small" aria-label="删除键" @click="deleteRootField(k)">
                  <span class="pre-remove">×</span>
                </AppButton>
              </div>
              <VisualValueEditor :model-value="data[k]" @update:model-value="setRootField(k, $event)" />
            </div>
          </div>
        </ConfigCategoryCard>
      </div>

      <!-- 源码模式 -->
      <div v-else class="pre-source">
        <textarea v-model="sourceContent" class="pre-source-editor" spellcheck="false" @input="parseSource" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { load as yamlLoad, dump as yamlDump } from 'js-yaml';
import {
  getPiModelsConfig, savePiModelsConfig, getPiModelsConfigPath,
} from '../../api/piConfig';
import {
  getOmpModelsConfig, saveOmpModelsConfig, getOmpModelsConfigPath,
} from '../../api/ompConfig';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import Dropdown from '../ui/Dropdown.vue';
import Switch from '../ui/Switch.vue';
import Badge from '../ui/Badge.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import EmptyState from '../ui/EmptyState.vue';
import ConfigCategoryCard from './ConfigCategoryCard.vue';
import StringListEditor from './StringListEditor.vue';
import ThinkingLevelMapEditor from './ThinkingLevelMapEditor.vue';
import VisualValueEditor from './VisualValueEditor.vue';

const props = defineProps<{ engine: 'pi' | 'omp' }>();

const { showSuccess, showError } = useToast();

const isPi = computed(() => props.engine === 'pi');

const modeOptions = computed(() => [
  { value: 'visual', label: '可视化' },
  { value: 'source', label: isPi.value ? 'JSON' : 'YAML' },
]);

/** 已知的 api 协议枚举（pi 常见 + omp 扩展），不在列表中的值回退文本输入 */
const API_OPTIONS = [
  { value: 'openai-completions', label: 'openai-completions' },
  { value: 'anthropic-messages', label: 'anthropic-messages' },
  { value: 'openai-responses', label: 'openai-responses' },
  { value: 'google-generative-ai', label: 'google-generative-ai' },
  { value: 'google-vertex', label: 'google-vertex' },
];

const PROVIDER_KNOWN_KEYS = new Set(['api', 'apiKey', 'baseUrl', 'models']);
const MODEL_KNOWN_KEYS = new Set(['id', 'name', 'contextWindow', 'maxTokens', 'reasoning']);
/** 模型高级字段（有专用可视化编辑器） */
const MODEL_ADVANCED_KEYS = new Set(['thinkingLevelMap', 'thinking', 'input', 'cost']);
/** cost 对象的四个已知费用字段 */
const COST_FIELDS = [
  { key: 'input', label: '输入', placeholder: '如 1.0' },
  { key: 'output', label: '输出', placeholder: '如 2.0' },
  { key: 'cacheRead', label: '缓存读', placeholder: '如 0.1' },
  { key: 'cacheWrite', label: '缓存写', placeholder: '如 0.2' },
] as const;

const loading = ref(true);
const saving = ref(false);
const error = ref('');
const mode = ref<'visual' | 'source'>('visual');
const sourceContent = ref('');
const parseError = ref('');
const configPath = ref('');
const data = ref<Record<string, any>>({});
const expandedProviders = ref<Record<string, boolean>>({});
const expandedRoot = ref(false);
const newProviderName = ref('');

const providersMap = computed<Record<string, any>>(() => {
  const v = data.value.providers;
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
});

const providerNames = computed<string[]>(() => Object.keys(providersMap.value));

const rootUnknownKeys = computed<string[]>(() =>
  Object.keys(data.value).filter((k) => k !== 'providers')
);

function modelCount(name: string): number {
  const models = providersMap.value[name]?.models;
  return Array.isArray(models) ? models.length : 0;
}

function providerModels(name: string): any[] {
  const models = providersMap.value[name]?.models;
  return Array.isArray(models) ? models : [];
}

function toggleProvider(name: string) {
  expandedProviders.value = { ...expandedProviders.value, [name]: !(expandedProviders.value[name] ?? false) };
}

function providerUnknownKeys(name: string): string[] {
  const entry = providersMap.value[name];
  if (!entry || typeof entry !== 'object' || Array.isArray(entry)) return [];
  return Object.keys(entry).filter((k) => !PROVIDER_KNOWN_KEYS.has(k));
}

function modelUnknownKeys(name: string, idx: number): string[] {
  const m = providerModels(name)[idx];
  if (!m || typeof m !== 'object' || Array.isArray(m)) return [];
  return Object.keys(m).filter((k) => !MODEL_KNOWN_KEYS.has(k) && !MODEL_ADVANCED_KEYS.has(k));
}

function hasModelField(name: string, idx: number, key: string): boolean {
  const m = providerModels(name)[idx];
  return !!m && typeof m === 'object' && key in m;
}

function modelAdvancedPresent(name: string, idx: number): string[] {
  const m = providerModels(name)[idx];
  if (!m || typeof m !== 'object') return [];
  return Object.keys(m).filter((k) => MODEL_ADVANCED_KEYS.has(k));
}

function modelExtraKeys(name: string, idx: number): string[] {
  return modelUnknownKeys(name, idx);
}

function modelFieldObject(name: string, idx: number, key: string): Record<string, any> {
  const v = providerModels(name)[idx]?.[key];
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
}

function modelFieldStringArray(name: string, idx: number, key: string): string[] {
  const v = providerModels(name)[idx]?.[key];
  return Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [];
}

function thinkingLevels(name: string, idx: number): string[] {
  const thinking = modelFieldObject(name, idx, 'thinking');
  return Array.isArray(thinking.levels) ? thinking.levels.filter((x: any) => typeof x === 'string') : [];
}

function thinkingMode(name: string, idx: number): string {
  const mode = modelFieldObject(name, idx, 'thinking').mode;
  return typeof mode === 'string' ? mode : '';
}

function costFieldString(name: string, idx: number, field: string): string {
  const cost = modelFieldObject(name, idx, 'cost');
  const v = cost[field];
  return v === undefined || v === null ? '' : String(v);
}

function providerApiRaw(name: string): string {
  const v = providersMap.value[name]?.api;
  return typeof v === 'string' ? v : '';
}

function providerFieldString(name: string, key: string): string {
  const v = providersMap.value[name]?.[key];
  return typeof v === 'string' ? v : '';
}

function modelFieldString(name: string, idx: number, key: string): string {
  const v = providerModels(name)[idx]?.[key];
  return v === undefined || v === null ? '' : String(v);
}

async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    const [content, path] = isPi.value
      ? await Promise.all([getPiModelsConfig(), getPiModelsConfigPath()])
      : await Promise.all([getOmpModelsConfig(), getOmpModelsConfigPath()]);
    sourceContent.value = content || '';
    configPath.value = path || '';
    parseSource();
    if (parseError.value) mode.value = 'source';
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

function parseSource() {
  const trimmed = (sourceContent.value || '').trim();
  if (!trimmed) {
    data.value = {};
    parseError.value = '';
    return;
  }
  try {
    const parsed = isPi.value ? JSON.parse(trimmed) : yamlLoad(trimmed);
    data.value = parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, any>)
      : {};
    if (!parsed || typeof parsed !== 'object') throw new Error('root must be an object/mapping');
    parseError.value = '';
  } catch (e) {
    parseError.value = (isPi.value ? 'JSON 格式错误：' : 'YAML 格式错误：') + (e as Error).message;
  }
}

function serialize() {
  try {
    sourceContent.value = isPi.value
      ? JSON.stringify(data.value, null, 2) + '\n'
      : yamlDump(data.value, { indent: 2, lineWidth: 120 });
    parseError.value = '';
  } catch (e) {
    parseError.value = '序列化失败：' + (e as Error).message;
  }
}

function ensureProviders(): Record<string, any> {
  if (!data.value.providers || typeof data.value.providers !== 'object' || Array.isArray(data.value.providers)) {
    data.value.providers = {};
  }
  return data.value.providers;
}

function setProviderField(name: string, key: string, value: any) {
  const providers = ensureProviders();
  const entry = providers[name];
  if (!entry || typeof entry !== 'object' || Array.isArray(entry)) return;
  if (key === 'apiKey' || key === 'baseUrl' || key === 'api') {
    if (value === '' && key !== 'api') {
      delete entry[key]; // 留空 = 移除该字段
    } else {
      entry[key] = value;
    }
  } else {
    entry[key] = value;
  }
  data.value.providers = { ...providers };
  serialize();
}

function setRootField(key: string, value: any) {
  data.value[key] = value;
  serialize();
}

function deleteRootField(key: string) {
  delete data.value[key];
  serialize();
}

function deleteProviderField(name: string, key: string) {
  const entry = providersMap.value[name];
  if (!entry || typeof entry !== 'object') return;
  delete entry[key];
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function addProviderField(name: string, defaultValue: any) {
  const entry = providersMap.value[name];
  if (!entry || typeof entry !== 'object') return;
  let k = 'new_field';
  let i = 1;
  while (k in entry) k = 'new_field_' + i++;
  entry[k] = defaultValue;
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function deleteModelField(name: string, idx: number, key: string) {
  const m = providerModelsRef(name)[idx];
  if (!m || typeof m !== 'object') return;
  delete m[key];
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function addModelAdvanced(name: string, idx: number, key: string) {
  const defaults: Record<string, any> = {
    thinkingLevelMap: { high: 'high', max: 'max' },
    thinking: { mode: 'effort', levels: ['low', 'high'] },
    input: ['text'],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  };
  const m = providerModelsRef(name)[idx];
  if (!m || typeof m !== 'object' || !(key in defaults)) return;
  m[key] = defaults[key];
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function addModelExtraField(name: string, idx: number, defaultValue: any) {
  const m = providerModelsRef(name)[idx];
  if (!m || typeof m !== 'object') return;
  let k = 'new_field';
  let i = 1;
  while (k in m) k = 'new_field_' + i++;
  m[k] = defaultValue;
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function updateThinkingMode(name: string, idx: number, mode: string) {
  const m = providerModelsRef(name)[idx];
  if (!m || typeof m !== 'object') return;
  const thinking = { ...(m.thinking || {}) };
  if (mode === '') delete thinking.mode;
  else thinking.mode = mode;
  m.thinking = thinking;
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function updateThinkingLevels(name: string, idx: number, levels: string[]) {
  const m = providerModelsRef(name)[idx];
  if (!m || typeof m !== 'object') return;
  m.thinking = { ...(m.thinking || {}), levels };
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function updateCostField(name: string, idx: number, field: string, raw: string) {
  const m = providerModelsRef(name)[idx];
  if (!m || typeof m !== 'object') return;
  const cost = { ...(m.cost || {}) };
  const trimmed = String(raw).trim();
  if (!trimmed) {
    delete cost[field];
  } else {
    const num = Number(trimmed);
    if (Number.isNaN(num)) return;
    cost[field] = num;
  }
  if (Object.keys(cost).length === 0) {
    delete m.cost; // 四项全空则移除 cost 字段
  } else {
    m.cost = cost;
  }
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function addProvider() {
  const name = newProviderName.value.trim();
  if (!name) return;
  const providers = ensureProviders();
  if (providers[name]) {
    showError(`提供商 ${name} 已存在`);
    return;
  }
  providers[name] = { api: 'openai-completions', models: [] };
  data.value.providers = { ...providers };
  newProviderName.value = '';
  expandedProviders.value = { ...expandedProviders.value, [name]: true };
  serialize();
}

function removeProvider(name: string) {
  if (!confirm(`确认删除提供商 ${name} 及其全部模型配置？`)) return;
  const providers = ensureProviders();
  delete providers[name];
  data.value.providers = { ...providers };
  serialize();
}

function providerModelsRef(name: string): any[] {
  const providers = ensureProviders();
  const entry = providers[name];
  if (!entry || typeof entry !== 'object' || Array.isArray(entry)) return [];
  if (!Array.isArray(entry.models)) entry.models = [];
  return entry.models;
}

function addModel(name: string) {
  const models = providerModelsRef(name);
  models.push({ id: '', name: '', reasoning: false });
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function removeModel(name: string, idx: number) {
  const models = providerModelsRef(name);
  models.splice(idx, 1);
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function setModelField(name: string, idx: number, key: string, value: any) {
  const models = providerModelsRef(name);
  const m = models[idx];
  if (!m || typeof m !== 'object') return;
  if ((key === 'name') && value === '') {
    delete m[key];
  } else {
    m[key] = value;
  }
  data.value.providers = { ...ensureProviders() };
  serialize();
}

function setModelNumber(name: string, idx: number, key: string, raw: string) {
  const trimmed = String(raw).trim();
  if (!trimmed) {
    const models = providerModelsRef(name);
    if (models[idx]) delete models[idx][key];
  } else {
    const num = Number(trimmed);
    if (Number.isNaN(num)) return; // 非数值不写入
    setModelField(name, idx, key, num);
    return;
  }
  data.value.providers = { ...ensureProviders() };
  serialize();
}

async function handleSave() {
  if (parseError.value) {
    showError('内容格式错误，无法保存');
    return;
  }
  saving.value = true;
  try {
    if (isPi.value) await savePiModelsConfig(sourceContent.value);
    else await saveOmpModelsConfig(sourceContent.value);
    showSuccess('注册表已保存');
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
    parseSource();
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
.pre-root {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.pre-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.pre-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.pre-subtitle {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
  line-height: 1.6;
}

.pre-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.pre-mode-tabs {
  display: inline-flex;
}

.pre-path-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.pre-path-label {
  font-size: 12px;
  color: var(--tertiary);
}

.pre-path-value {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
  background: var(--control);
  padding: 4px 8px;
  border-radius: 6px;
}

.pre-copy-btn {
  font-size: 11px;
  padding: 4px 10px;
}

.pre-parse-error {
  font-size: 12px;
  color: var(--danger);
  background: rgba(255, 59, 48, 0.1);
  padding: 8px 12px;
  border-radius: 8px;
}

.pre-parse-valid {
  font-size: 12px;
  color: var(--success);
  padding: 4px 0;
}

.pre-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pre-empty {
  background: var(--control);
  border: 1px dashed var(--separator);
  border-radius: 12px;
  padding: 8px 16px;
}

.pre-managed-warn {
  font-size: 12px;
  color: var(--warning, #b8860b);
  background: var(--control);
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 10px;
  line-height: 1.6;
}

.pre-fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pre-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pre-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
}

.pre-unknown {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--separator);
}

.pre-unknown-title {
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  letter-spacing: 0.3px;
}

.pre-unknown-empty {
  font-size: 12px;
  color: var(--tertiary);
  padding: 2px 0;
}

.pre-unknown-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.pre-unknown-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pre-thinking {
  display: grid;
  grid-template-columns: minmax(140px, 220px) 1fr;
  gap: 10px;
  align-items: start;
}

.pre-cost-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
}

.pre-unknown-key {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
}

.pre-models {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 12px;
}

.pre-models-head {
  display: flex;
  align-items: center;
}

.pre-model {
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pre-model-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.pre-model-id {
  width: 220px;
}

.pre-model-reasoning {
  display: flex;
  align-items: center;
  gap: 6px;
}

.pre-model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px;
}

.pre-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
}

.pre-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.pre-provider-actions {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--separator);
}

.pre-add-provider {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.pre-add-input {
  max-width: 320px;
}

.pre-source-editor {
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

.pre-source-editor:focus {
  border-color: var(--accent);
}
</style>
