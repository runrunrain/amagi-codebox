<template>
  <div class="pi-plugin-panel">
    <!-- 首屏门控（store 派生，契约 §6.1/§6.4）：加载中且无数据无错误 → Loading；
         loadError 且无数据 → 整页 ErrorState（重试仍走 refresh）。 -->
    <LoadingState v-if="showInitialLoading" message="加载 Pi 包中..." />
    <ErrorState
      v-else-if="showInitialError"
      :message="loadError"
      :on-retry="reload"
    />

    <template v-else>
      <!-- ① 诊断横幅：errors=store.loadError（有数据后的刷新失败），warnings=store.warnings -->
      <DiagnosticBanner :errors="diagnosticErrors" :warnings="warnings" />

      <!-- ② 资源计数标签：包 N | 技能 | 扩展 | 提示词 | 主题（Σ=0 隐藏）+ 行尾搜索 -->
      <CountTabs
        v-model="resourceTabModel"
        v-model:search-value="searchText"
        :tabs="countTabs"
        :search-placeholder="searchPlaceholder"
      />

      <!-- ③ 工具栏：标题+计数徽章 | 目标 Segmented（Windows/WSL/同时应用，保留）+ 刷新 + 安装包 -->
      <ListToolbar :title="toolbarTitle" :count="toolbarCount" :loading="loading">
        <template #actions>
          <Segmented
            v-model="opTargetModel"
            :options="targetOptions"
            class="target-seg"
          />
          <AppButton variant="ghost" size="small" :disabled="loading" @click="reload">
            {{ loading ? '刷新中…' : '刷新' }}
          </AppButton>
          <AppButton variant="primary" size="small" @click="openInstallDialog">
            安装包
          </AppButton>
        </template>
      </ListToolbar>

      <!-- ④ 内容区 -->
      <!-- 包详情态：居中窄单列二级详情页（← 返回列表 + 「Pi › 包名」面包屑 +
           DetailHeader + 描述 + 只读资源列表 + MetaGrid + 切换源卡片），替换内容区 -->
      <div v-if="resourceTab === 'packages' && activePackage" class="detail-page">
        <div class="detail-column">
          <button type="button" class="detail-back" @click="closeDetail">← 返回列表</button>
          <DetailHeader
            :breadcrumb="[{ label: 'Pi', clickable: true }, { label: activePackage.name }]"
            :title="activePackage.name"
            :badges="detailBadges"
            @back="closeDetail"
          >
            <template #actions>
              <AppButton
                variant="primary"
                size="small"
                :disabled="mutating || activePackage.sourceType === 'local'"
                @click="handleUpdate"
              >
                {{ activePackage.sourceType === 'local' ? '本地直载' : (mutating ? '处理中…' : '更新') }}
              </AppButton>
              <AppButton variant="danger" size="small" :disabled="mutating" @click="showRemoveDialog = true">
                移除
              </AppButton>
            </template>
          </DetailHeader>

          <p v-if="activePackage.description" class="detail-description">
            {{ activePackage.description }}
          </p>

          <div v-if="loadingDetail" class="detail-loading">正在读取包资源…</div>
          <div v-else-if="isDetail(activePackage)" class="detail-section">
            <div class="detail-section-title">包含资源</div>
            <div v-if="activePackage.resources.length" class="resource-rows">
              <SubItemRow
                v-for="(item, index) in activePackage.resources"
                :key="`${item.type}:${item.name}:${index}`"
                :name="item.name"
                :type-badge="resourceTypeLabel(item.type)"
              />
            </div>
            <p v-else class="detail-hint">该包没有可展示的资源。</p>
          </div>

          <MetaGrid :items="detailMetaItems" />

          <!-- 源类型切换：git ⇄ npm ⇄ 本地（remove+install 原子事务，失败自动回滚）。
               功能与交互保留，样式 token 化（契约 §5.4/§6.4）。 -->
          <div class="switch-source-card">
            <div class="switch-source-head">
              <span>切换源类型</span>
              <span class="switch-source-tip">git ⇄ npm ⇄ 本地互转；旧实体保留，可随时切回</span>
            </div>
            <div class="switch-type-row">
              <AppButton
                v-for="t in switchTypes"
                :key="t.value"
                :variant="switchTargetType === t.value ? 'primary' : 'ghost'"
                size="small"
                :disabled="mutating || t.value === activePackage.sourceType"
                @click="pickSwitchType(t.value)"
              >
                {{ t.label }}{{ activePackage.sourceType === t.value ? '（当前）' : '' }}
              </AppButton>
            </div>
            <div v-if="switchTargetType" class="switch-input-row">
              <label :for="`switch-source-${activePackage.sourceType}`">新源地址</label>
              <input
                :id="`switch-source-${activePackage.sourceType}`"
                v-model="switchTargetSource"
                class="module-input"
                :placeholder="switchPlaceholder"
                @keydown.enter="handleSwitchSource"
              />
              <div v-if="switchHistory.length" class="switch-history">
                <span>历史：</span>
                <code
                  v-for="h in switchHistory"
                  :key="h"
                  class="switch-history-item"
                  :title="`使用 ${h}`"
                  @click="switchTargetSource = h"
                >{{ shortenSource(h) }}</code>
              </div>
            </div>
            <div v-if="switchTargetType" class="switch-actions">
              <AppButton variant="ghost" size="small" :disabled="mutating" @click="cancelSwitch">
                取消
              </AppButton>
              <AppButton
                variant="primary"
                size="small"
                :disabled="mutating || !switchTargetSource.trim()"
                @click="handleSwitchSource"
              >
                {{ mutating ? '切换中…' : '切换' }}
              </AppButton>
            </div>
          </div>
        </div>
      </div>

      <!-- 包列表态：行式列表（无开关），不分组 -->
      <template v-else-if="resourceTab === 'packages'">
        <div v-if="filteredPackages.length" class="package-rows">
          <PluginRow
            v-for="pkg in filteredPackages"
            :key="pkg.source"
            :name="pkg.name"
            :description="pkg.description"
            :badges="rowBadges(pkg)"
            @click="select(pkg.source)"
          />
        </div>
        <EmptyStateCard
          v-else-if="!installed.length"
          title="暂无 Pi 全局包"
          description="点击『安装包』，输入 npm 包名、git 地址或本地路径"
        >
          <template #primary>
            <AppButton variant="primary" size="small" @click="openInstallDialog">安装包</AppButton>
          </template>
        </EmptyStateCard>
        <p v-else class="list-empty-hint">未找到匹配「{{ searchText }}」的包。</p>
      </template>

      <!-- 资源标签页：按所属包分组的行式条目（名称=资源名，描述缺省不显示） -->
      <template v-else>
        <template v-for="group in resourceGroups" :key="group.source">
          <GroupHeader :title="group.name" :count="group.items.length" />
          <div class="package-rows">
            <PluginRow
              v-for="item in group.items"
              :key="`${group.source}:${item}`"
              :name="item"
              @click="jumpToPackage(group.source)"
            />
          </div>
        </template>
        <p v-if="!resourceGroups.length" class="list-empty-hint">未找到匹配的资源。</p>
      </template>
    </template>

    <!-- P3-S5：WSL 侧插件面板（原生重放）——与 Windows 列表各自反映自己的
         实体；目标选择器为 wsl/both 时操作会同步作用到本区。整体保留（契约 §6.4）。 -->
    <PiPluginWSLSection ref="wslSection" />

    <Dialog
      v-model:open="showInstallDialog"
      title="安装 Pi 包"
      description="使用 pi 官方包安装命令（写入 settings.json 并拉取实体）"
    >
      <div class="install-form">
        <label for="pi-package-source">包源 Package source</label>
        <input
          id="pi-package-source"
          v-model="installSource"
          class="module-input"
          placeholder="例：npm:@earendil-works/pi-web-access、git:github.com/owner/repo 或 /path/to/pkg"
          @keydown.enter="handleInstall"
        />
        <p>npm 包需加 npm: 前缀（如 npm:@earendil-works/pi-web-access）；git 源用 git:&lt;url&gt;；本地源直接给绝对路径。</p>
        <p class="install-target-note">
          应用目标：{{ targetLabel }}（在工具栏目标选择器切换 Windows / WSL / 同时应用；WSL 侧重放固定版本/ref）
        </p>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showInstallDialog = false">取消</AppButton>
        <AppButton
          variant="primary"
          :disabled="mutating || !installSource.trim()"
          @click="handleInstall"
        >
          {{ mutating ? '安装中…' : '安装' }}
        </AppButton>
      </template>
    </Dialog>

    <ConfirmDialog
      v-model:open="showRemoveDialog"
      title="移除 Pi 包"
      :message="removeConfirmMessage"
      danger
      confirm-text="移除"
      @confirm="handleRemove"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { usePiPluginStore } from '../../stores/piPlugin';
import type { PiPackage, PiPackageDetail, PiPluginOpOutcome, PiPluginTarget } from '../../api/piPlugin';
import {
  installPiPackageTargeted,
  removePiPackageTargeted,
  updatePiPackageTargeted,
} from '../../api/piPlugin';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import Segmented from '../ui/Segmented.vue';
import PiPluginWSLSection from './PiPluginWSLSection.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import GroupHeader from './ui/GroupHeader.vue';
import ListToolbar from './ui/ListToolbar.vue';
import MetaGrid from './ui/MetaGrid.vue';
import PluginRow from './ui/PluginRow.vue';
import SubItemRow from './ui/SubItemRow.vue';
import type { CountTabItem, PluginBadgeDescriptor } from './pluginViewTypes';
import { useToast } from '../../composables/useToast';
import { SwitchPackageSource } from '../../../wailsjs/go/piplugin/Service';

const store = usePiPluginStore();
const {
  installed,
  warnings,
  loadError,
  activeSource,
  activePackage,
  loading,
  loadingDetail,
  skillCount,
  extensionCount,
  promptCount,
  themeCount,
} = storeToRefs(store);
const { showSuccess, showError } = useToast();

// ---- 首屏门控（store 派生，契约 §6.1/§6.4）----
const showInitialLoading = computed(() => loading.value && installed.value.length === 0 && !loadError.value);
const showInitialError = computed(() => !!loadError.value && installed.value.length === 0);
// 有数据后的刷新失败 → loadError 并入诊断横幅（整页 ErrorState 只在无数据时出现）
const diagnosticErrors = computed(() => (loadError.value ? [loadError.value] : []));

const mutating = ref(false);
const showInstallDialog = ref(false);
const showRemoveDialog = ref(false);
const installSource = ref('');

// ---- 资源计数标签与页态（面板本地 resourceTab，契约 §2/§6.4）----
type PiResourceTabKey = 'packages' | 'skills' | 'extensions' | 'prompts' | 'themes';
type PiResourceField = 'skills' | 'extensions' | 'prompts' | 'themes';
const RESOURCE_FIELDS: Record<Exclude<PiResourceTabKey, 'packages'>, PiResourceField> = {
  skills: 'skills',
  extensions: 'extensions',
  prompts: 'prompts',
  themes: 'themes',
};
function isResourceTabKey(value: string): value is Exclude<PiResourceTabKey, 'packages'> {
  return value === 'skills' || value === 'extensions' || value === 'prompts' || value === 'themes';
}
const resourceTab = ref<PiResourceTabKey>('packages');
// CountTabs 的 modelValue 是 string：经 computed 收窄回资源标签联合类型。
const resourceTabModel = computed({
  get: () => resourceTab.value as string,
  set: (value: string) => {
    if (value === 'packages' || isResourceTabKey(value)) {
      resourceTab.value = value;
    }
  },
});
function openResourceTab(key: string): void {
  resourceTabModel.value = key;
}
const searchText = ref('');

const countTabs = computed<CountTabItem[]>(() => [
  { key: 'packages', label: '包', count: installed.value.length },
  { key: 'skills', label: '技能', count: skillCount.value, hidden: skillCount.value === 0 },
  { key: 'extensions', label: '扩展', count: extensionCount.value, hidden: extensionCount.value === 0 },
  { key: 'prompts', label: '提示词', count: promptCount.value, hidden: promptCount.value === 0 },
  { key: 'themes', label: '主题', count: themeCount.value, hidden: themeCount.value === 0 },
]);

const searchPlaceholder = computed(() =>
  resourceTab.value === 'packages' ? '搜索包名称或描述' : '搜索资源名称或所属包');

// 本地过滤（契约 §7：搜索为面板本地 computed，不进 store、不发后端请求）
const filteredPackages = computed(() => {
  const keyword = searchText.value.trim().toLowerCase();
  if (!keyword) return installed.value;
  return installed.value.filter(pkg =>
    pkg.name.toLowerCase().includes(keyword)
    || (pkg.description ?? '').toLowerCase().includes(keyword));
});

interface ResourceGroup {
  name: string;
  source: string;
  items: string[];
}
const resourceGroups = computed<ResourceGroup[]>(() => {
  if (!isResourceTabKey(resourceTab.value)) return [];
  const field = RESOURCE_FIELDS[resourceTab.value];
  const keyword = searchText.value.trim().toLowerCase();
  const groups: ResourceGroup[] = [];
  for (const pkg of installed.value) {
    const all = pkg[field] ?? [];
    const items = keyword
      ? all.filter(name => name.toLowerCase().includes(keyword) || pkg.name.toLowerCase().includes(keyword))
      : all;
    if (items.length) groups.push({ name: pkg.name, source: pkg.source, items });
  }
  return groups;
});

const toolbarTitle = computed(() =>
  resourceTab.value === 'packages'
    ? '已安装'
    : countTabs.value.find(tab => tab.key === resourceTab.value)?.label ?? '资源');
const toolbarCount = computed(() =>
  resourceTab.value === 'packages'
    ? filteredPackages.value.length
    : resourceGroups.value.reduce((sum, group) => sum + group.items.length, 0));

// ---- P3-S5：操作目标（Windows / WSL / 同时应用）----
// windows（默认）走既有 store 路径；含 WSL 的目标走目标化 wrapper，每目标
// 独立结果（Windows 成功不代 WSL 报喜，单侧失败独立提示）。
const opTarget = ref<PiPluginTarget>('windows');
const targetOptions = [
  { value: 'windows', label: 'Windows' },
  { value: 'wsl', label: 'WSL' },
  { value: 'both', label: '同时应用' },
];
// Segmented 的 modelValue 是 string：经 computed 收窄回目标联合类型。
const opTargetModel = computed({
  get: () => opTarget.value as string,
  set: (value: string) => {
    if (value === 'windows' || value === 'wsl' || value === 'both') {
      opTarget.value = value;
    }
  },
});
const targetLabel = computed(
  () => targetOptions.find(option => option.value === opTarget.value)?.label ?? opTarget.value,
);
const removeConfirmMessage = computed(() => {
  if (!activePackage.value) return '';
  const scope = opTarget.value === 'windows'
    ? 'Pi settings.json'
    : opTarget.value === 'wsl'
      ? 'WSL 侧 pi settings'
      : 'Windows 与 WSL 两侧 pi settings';
  return `将从 ${scope} 移除「${activePackage.value.name}」。实体目录会保留，便于后续重新安装。`;
});

const wslSection = ref<InstanceType<typeof PiPluginWSLSection> | null>(null);

const outcomeTargetNames: Record<string, string> = { windows: 'Windows', wsl: 'WSL' };

function describeOutcome(outcome: PiPluginOpOutcome): string {
  const head = outcomeTargetNames[outcome.target] ?? outcome.target;
  if (outcome.ok) {
    return `${head}：${outcome.detail || outcome.outcome}`;
  }
  return `${head} 未成功（${outcome.outcome}${outcome.errCode ? `/${outcome.errCode}` : ''}）：${outcome.detail || '无详情'}`;
}

// runTargeted 执行目标化操作：每目标一条结果独立提示，完成后两侧列表各
// 刷新自己的实体（Windows store / WSL 快照）。
async function runTargeted(kind: 'install' | 'update' | 'remove', source: string): Promise<boolean> {
  const fn = kind === 'install'
    ? installPiPackageTargeted
    : kind === 'update'
      ? updatePiPackageTargeted
      : removePiPackageTargeted;
  const result = await fn(source, opTarget.value);
  let allOk = true;
  for (const outcome of result.results) {
    if (outcome.ok) {
      showSuccess(describeOutcome(outcome));
    } else {
      allOk = false;
      showError(describeOutcome(outcome));
    }
  }
  try {
    if (opTarget.value !== 'wsl') {
      await store.refresh(true);
    }
    if (opTarget.value !== 'windows') {
      await wslSection.value?.refresh(true);
    }
  } catch {
    /* 刷新失败不掩盖操作结果 */
  }
  return allOk;
}

// ---- 源类型切换（git ⇄ npm ⇄ 本地）----
type SwitchType = 'git' | 'npm' | 'local';
const switchTypes: { value: SwitchType; label: string }[] = [
  { value: 'git', label: 'Git 仓库' },
  { value: 'npm', label: 'npm 包' },
  { value: 'local', label: '本地路径' },
];
const switchTargetType = ref<SwitchType | ''>('');
const switchTargetSource = ref('');
// 历史源记忆（按包名分组持久化 localStorage；主上 git↔本地往返场景的一键切回）
const switchHistoryKey = (name: string) => `amagi-codebox.pi.switch-history.${name}`;
function loadSwitchHistory(name: string): string[] {
  try {
    const raw = localStorage.getItem(switchHistoryKey(name));
    const arr = raw ? (JSON.parse(raw) as unknown) : [];
    return Array.isArray(arr) ? (arr as string[]).slice(0, 5) : [];
  } catch {
    return [];
  }
}
function appendSwitchHistory(name: string, source: string): void {
  if (!name || !source) return;
  try {
    const list = loadSwitchHistory(name);
    const next = [source, ...list.filter(x => x !== source)].slice(0, 5);
    localStorage.setItem(switchHistoryKey(name), JSON.stringify(next));
  } catch {
    /* localStorage 不可用时静默降级（仅失去记忆） */
  }
}
const switchHistory = computed(() =>
  activePackage.value ? loadSwitchHistory(activePackage.value.name) : [],
);
const switchPlaceholder = computed(() => {
  switch (switchTargetType.value) {
    case 'git':
      return '例：git:github.com/runrunrain/amagi-pi';
    case 'npm':
      return '例：npm:amagi-pi';
    case 'local':
      return '例：/c/Users/name/dev/amagi-pi 或 ~/dev/amagi-pi';
    default:
      return '';
  }
});
function pickSwitchType(t: SwitchType): void {
  switchTargetType.value = t;
  // 本地/历史一键预填：历史里找同类型源
  const hist = activePackage.value ? loadSwitchHistory(activePackage.value.name) : [];
  const typed = hist.find(h =>
    t === 'git' ? h.startsWith('git:') :
    t === 'npm' ? h.startsWith('npm:') :
    !h.startsWith('git:') && !h.startsWith('npm:'),
  );
  switchTargetSource.value = typed ?? '';
}
function cancelSwitch(): void {
  switchTargetType.value = '';
  switchTargetSource.value = '';
}
function shortenSource(src: string): string {
  if (src.length <= 42) return src;
  return `${src.slice(0, 24)}…${src.slice(-14)}`;
}
async function handleSwitchSource(): Promise<void> {
  const pkg = activePackage.value;
  const target = switchTargetSource.value.trim();
  if (!pkg || !target || mutating.value) return;
  mutating.value = true;
  try {
    const result = await SwitchPackageSource(pkg.source, target);
    if (result && result.success) {
      appendSwitchHistory(pkg.name, target);
      appendSwitchHistory(pkg.name, pkg.source);
      showSuccess(result.output || `已切换到 ${target}；重启 pi 会话后生效`);
      cancelSwitch();
      await store.selectPackage(target);
      await store.refresh(true);
    } else {
      showError(`切换失败：${result?.error || '未知错误（配置已回滚）'}`);
    }
  } catch (error) {
    showError(`切换失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

// ---- 视图字段映射（契约 §5）----
const RESOURCE_TYPE_LABELS: Record<string, string> = {
  extension: '扩展',
  skill: '技能',
  prompt: '提示词',
  theme: '主题',
};
function resourceTypeLabel(type: string): string {
  return RESOURCE_TYPE_LABELS[type] ?? type;
}

function isDetail(pkg: PiPackage | PiPackageDetail): pkg is PiPackageDetail {
  return Array.isArray((pkg as PiPackageDetail).resources);
}

function sourceLabel(sourceType: string) {
  if (sourceType === 'git') return 'Git';
  if (sourceType === 'local') return '本地';
  return 'npm';
}

function scopeLabel(scope: string) {
  return scope === 'user' ? '用户级' : scope;
}

function formatDate(value: string) {
  return new Date(value).toLocaleString('zh-CN');
}

// 行徽章（契约 §5.1 Pi 列）：来源类型 + v{version} + pinned + 包源（mono，超长截断）。
// pinned 用 scope（--control 底 + --secondary 字）：tag 为白字无底色，浅色背景上不可见
function rowBadges(pkg: PiPackage): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [
    { text: sourceLabel(pkg.sourceType), type: 'source', variant: 'muted' },
  ];
  if (pkg.version) badges.push({ text: `v${pkg.version}`, type: 'ver' });
  if (pkg.pinned) badges.push({ text: 'pinned', type: 'scope', variant: 'muted' });
  badges.push({ text: shortenSource(pkg.source), type: 'source', variant: 'mono' });
  return badges;
}

const detailBadges = computed<PluginBadgeDescriptor[]>(() => {
  const pkg = activePackage.value;
  if (!pkg) return [];
  const badges: PluginBadgeDescriptor[] = [
    { text: sourceLabel(pkg.sourceType), type: 'source', variant: 'muted' },
  ];
  if (pkg.version) badges.push({ text: `v${pkg.version}`, type: 'ver' });
  if (pkg.pinned) badges.push({ text: 'pinned', type: 'scope', variant: 'muted' });
  return badges;
});

interface MetaItem {
  label: string;
  value: string;
  mono?: boolean;
}
// MetaGrid 行（契约 §5.2 Pi 列）；空 value 项在此过滤，不造占位假值。
const detailMetaItems = computed<MetaItem[]>(() => {
  const pkg = activePackage.value;
  if (!pkg) return [];
  const items: MetaItem[] = [
    { label: '包源', value: pkg.source, mono: true },
    { label: '来源类型', value: sourceLabel(pkg.sourceType) },
    { label: '作用域', value: scopeLabel(pkg.scope) },
  ];
  if (pkg.installPath) items.push({ label: '安装路径', value: pkg.installPath });
  if (pkg.author) items.push({ label: '开发者', value: pkg.author });
  if (pkg.repository) items.push({ label: '仓库', value: pkg.repository });
  if (pkg.lastUpdated) items.push({ label: '更新时间', value: formatDate(pkg.lastUpdated) });
  if (isDetail(pkg)) {
    items.push({ label: 'Manifest', value: pkg.manifestDeclared ? '已声明' : '未声明（按目录扫描）' });
  }
  return items;
});

// ---- 加载 / 导航 / 操作 ----
async function reload(): Promise<void> {
  // refresh 失败由 store 写入 loadError 并 rethrow；此处吞掉避免未处理拒绝，
  // 失败展示统一走 store.loadError（诊断横幅 / 整页 ErrorState）。
  try {
    await store.refresh(true);
  } catch {
    /* 展示见 store.loadError */
  }
}

async function select(source: string) {
  cancelSwitch();
  try {
    await store.selectPackage(source);
  } catch (error) {
    showError(`读取包详情失败：${error instanceof Error ? error.message : String(error)}`);
  }
}

function jumpToPackage(source: string): void {
  resourceTab.value = 'packages';
  void select(source);
}

function closeDetail(): void {
  cancelSwitch();
  activeSource.value = null;
}

function openInstallDialog() {
  installSource.value = '';
  showInstallDialog.value = true;
}

async function handleInstall() {
  const source = installSource.value.trim();
  if (!source || mutating.value) return;
  mutating.value = true;
  try {
    if (opTarget.value === 'windows') {
      await store.install(source);
      showInstallDialog.value = false;
      showSuccess('Pi 包安装成功');
    } else {
      // 目标含 WSL：每目标独立结果；有失败时保留对话框便于修改重试。
      const allOk = await runTargeted('install', source);
      if (allOk) {
        showInstallDialog.value = false;
      }
    }
  } catch (error) {
    showError(`安装失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleUpdate() {
  if (!activePackage.value || mutating.value) return;
  mutating.value = true;
  try {
    if (opTarget.value === 'windows') {
      const result = await store.update(activePackage.value.source);
      showSuccess(result.output || 'Pi 包已更新');
    } else {
      await runTargeted('update', activePackage.value.source);
    }
  } catch (error) {
    showError(`更新失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

async function handleRemove() {
  if (!activePackage.value || mutating.value) return;
  const source = activePackage.value.source;
  mutating.value = true;
  try {
    if (opTarget.value === 'windows') {
      await store.remove(source);
      showSuccess('Pi 包已移除');
    } else {
      await runTargeted('remove', source);
    }
  } catch (error) {
    showError(`移除失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    mutating.value = false;
  }
}

// 可测试性契约（§8）：行为断言经 expose 的 handler 触发
defineExpose({
  openInstallDialog,
  showInstallDialog,
  handleInstall,
  handleUpdate,
  handleRemove,
  select,
  openResourceTab,
});

onMounted(() => {
  void reload();
});
</script>

<style scoped>
/* 根间距与 PluginInstalledPanel/PluginMarketPanel 对齐（14px，跨面板一致） */
.pi-plugin-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.target-seg {
  font-size: 12px;
}

.package-rows {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: var(--radius-card);
  padding: 6px;
}

/* 描述完整多行显示（不截断、不加省略号）：ui/PluginRow 不可改，面板 scoped :deep 覆盖 */
.package-rows :deep(.plugin-row) {
  align-items: flex-start;
}

.package-rows :deep(.plugin-row-desc) {
  white-space: normal;
  overflow: visible;
  text-overflow: clip;
}

/* 详情页：居中窄单列（约 720px，左右留白） */
.detail-page {
  display: flex;
  flex-direction: column;
}

.detail-column {
  width: 100%;
  max-width: 720px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.detail-back {
  align-self: flex-start;
  border: none;
  background: none;
  padding: 0;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  color: var(--accent);
}

.detail-back:hover {
  text-decoration: underline;
}

.detail-description {
  margin: 0;
  color: var(--secondary);
  line-height: 1.65;
  font-size: 13px;
}

.detail-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.detail-section-title {
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.resource-rows {
  display: flex;
  flex-direction: column;
}

.detail-loading {
  color: var(--secondary);
  font-size: 12px;
}

.detail-hint {
  margin: 0;
  color: var(--tertiary);
  font-size: 12px;
}

.list-empty-hint {
  margin: 0;
  padding: 24px 0;
  text-align: center;
  color: var(--tertiary);
  font-size: 13px;
}

.install-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.install-form label {
  font-size: 13px;
  font-weight: 600;
}

.install-form p {
  margin: 5px 0 0;
  font-size: 12px;
  color: var(--secondary);
}

.install-target-note {
  color: var(--tertiary);
}

.module-input {
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  padding: 9px 11px;
}

.module-input:focus {
  outline: none;
  border-color: var(--accent);
}

/* 源类型切换卡片（token 化，契约 §5.4/风险 11：原 --border/--bg-elevated/--text-muted
   全前端无定义、渲染为深色 fallback，替换为 --separator/--control/--secondary） */
.switch-source-card {
  padding: 12px 14px;
  border: 1px solid var(--separator);
  border-radius: var(--radius-md);
  background: var(--control);
}

.switch-source-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.switch-source-tip {
  font-size: 11px;
  font-weight: 400;
  color: var(--secondary);
}

.switch-type-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.switch-input-row {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.switch-input-row label {
  font-size: 11px;
  color: var(--secondary);
}

.switch-history {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 11px;
  color: var(--secondary);
}

.switch-history-item {
  cursor: pointer;
  padding: 2px 8px;
  border-radius: 6px;
  border: 1px solid var(--separator);
  transition: border-color 0.15s ease;
}

.switch-history-item:hover {
  border-color: var(--accent);
}

.switch-actions {
  margin-top: 10px;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
