<!-- 共用插件市场面板（~/.agents/marketplaces）：两级分页，复用 ui/* 共享件。
     一级＝市场列表：ListToolbar（共用市场 N + 添加市场/从 Claude 导入/刷新）+
     全宽市场行（PluginRow：名称 + 「N 个插件」计数徽章 + Missing 警示徽章 +
     描述/来源完整多行；行尾 更新/删除）。
     二级＝市场内插件列表：← 返回 + 面包屑（共用市场 › 市场名）+ DetailHeader
     （N 个插件 + 更新/打开目录）+ 插件行（技能数/已部署徽章 + 行尾
     「更新插件」/「部署技能」/「卸载」）。「已部署」与卸载范围均以来源凭据（provenance
     标记）为准：仅凭据匹配的目录可卸载，同名手动/无凭据条目保留，市场 clone
     不受影响。initialMarket prop 提供无依赖窄定位（外部导航配合）。
     添加/导入/删除均带 Dialog；部署为增量复制（已存在跳过，不覆盖用户改动），
     toast 汇报部署/跳过/卸载明细。首屏 Loading/Error 门控为 store 派生。 -->
<template>
  <div class="shared-market-panel">
    <LoadingState v-if="showInitialLoading" message="加载市场中..." />

    <ErrorState v-else-if="showInitialError" :message="error" :on-retry="load" />

    <template v-else>
      <!-- ═══ 一级：市场列表 ═══ -->
      <template v-if="!activeMarket">
        <DiagnosticBanner :errors="error ? [error] : []" />

        <ListToolbar title="共用市场" :count="marketplaces.length" :loading="isLoading">
          <template #actions>
            <AppButton variant="primary" size="small" @click="openAddDialog">
              <span class="add-plus" aria-hidden="true">＋</span>添加市场
            </AppButton>
            <AppButton variant="ghost" size="small" :disabled="importLoading" @click="openImportDialog">
              {{ importLoading ? '读取中…' : '从 Claude 导入' }}
            </AppButton>
            <AppButton variant="ghost" size="small" :disabled="isLoading" @click="load">
              {{ isLoading ? '刷新中…' : '刷新' }}
            </AppButton>
          </template>
        </ListToolbar>

        <EmptyStateCard
          v-if="marketplaces.length === 0"
          title="暂无共用市场"
          description="添加 Claude Code 插件市场（或任意 git 市场源），统一克隆到 ~/.agents/marketplaces 管理"
        />

        <div v-else class="market-list">
          <PluginRow
            v-for="market in marketplaces"
            :key="market.name"
            :name="market.name"
            :description="marketDescription(market)"
            :badges="marketBadges(market)"
            @click="selectMarket(market)"
          >
            <template #trailing>
              <div class="row-actions">
                <AppButton
                  variant="ghost"
                  size="small"
                  :disabled="marketUpdateBusy(market.name)"
                  @click.stop="handleUpdate(market)"
                >
                  {{ isUpdating(market.name) ? '更新中…' : '更新' }}
                </AppButton>
                <AppButton
                  variant="danger"
                  size="small"
                  :disabled="marketUpdateBusy(market.name)"
                  @click.stop="requestRemove(market)"
                >
                  删除
                </AppButton>
              </div>
            </template>
          </PluginRow>
        </div>
      </template>

      <!-- ═══ 二级：市场内插件列表 ═══ -->
      <div v-else class="market-plugin-page">
        <button type="button" class="back-btn" @click="backToMarkets">← 返回市场列表</button>

        <DetailHeader
          :breadcrumb="[{ label: '共用市场', clickable: true }, { label: activeMarket.name }]"
          :title="activeMarket.name"
          :badges="[{ text: `${activeMarket.plugins.length} 个插件`, type: 'scope', variant: 'muted' }]"
          @back="backToMarkets"
        >
          <template #actions>
            <AppButton variant="ghost" size="small" @click="handleOpenFolder">
              打开目录
            </AppButton>
            <AppButton
              variant="ghost"
              size="small"
              :disabled="marketUpdateBusy(activeMarket.name)"
              @click="handleUpdate(activeMarket)"
            >
              {{ isUpdating(activeMarket.name) ? '更新中…' : '更新' }}
            </AppButton>
          </template>
        </DetailHeader>

        <EmptyStateCard
          v-if="activeMarket.missing"
          title="市场目录不可读"
          description="本地 clone 缺失或 marketplace.json 解析失败；点击「更新」可从源重新克隆恢复"
        />

        <template v-if="!activeMarket.missing || activeMarket.plugins.length > 0">
          <ListToolbar title="市场插件" :count="activeMarket.plugins.length" />

          <EmptyStateCard
            v-if="activeMarket.plugins.length === 0"
            title="市场无插件条目"
            description="marketplace.json 中没有任何插件"
          />

          <div v-else class="plugin-list">
            <PluginRow
              v-for="plugin in activeMarket.plugins"
              :key="plugin.name"
              :name="plugin.name"
              :description="pluginDescription(plugin)"
              :badges="pluginBadges(plugin)"
            >
              <template #trailing>
                <div class="row-actions">
                  <AppButton
                    v-if="plugin.updatable"
                    variant="ghost"
                    size="small"
                    :disabled="pluginUpdateBusy(activeMarket.name, plugin.name)"
                    title="从市场源更新此插件"
                    @click="handleUpdatePlugin(plugin)"
                  >
                    {{ isUpdatingPlugin(plugin.name) ? '更新中…' : '更新插件' }}
                  </AppButton>
                  <AppButton
                    variant="primary"
                    size="small"
                    :disabled="plugin.externalSource || plugin.skills.length === 0 || isDeploying(plugin.name) || isUpdatingPlugin(plugin.name)"
                    :title="deployButtonTitle(plugin)"
                    @click="handleDeploy(plugin)"
                  >
                    {{ deployButtonLabel(plugin) }}
                  </AppButton>
                  <AppButton
                    v-if="deployedCount(plugin) > 0"
                    variant="danger"
                    size="small"
                    :disabled="isUninstalling(plugin.name) || isUpdatingPlugin(plugin.name)"
                    :title="uninstallButtonTitle(plugin)"
                    @click="requestUninstall(plugin)"
                  >
                    {{ isUninstalling(plugin.name) ? '卸载中…' : '卸载' }}
                  </AppButton>
                </div>
              </template>
            </PluginRow>
          </div>
        </template>
      </div>

      <!-- 添加市场 Dialog -->
      <Dialog
        v-model:open="showAddDialog"
        title="添加共用市场"
        description="输入 Claude Code 插件市场源（git URL 或本地路径），将克隆到 ~/.agents/marketplaces"
      >
        <div class="add-market-form">
          <div class="form-group">
            <label>市场源</label>
            <input
              v-model="addSource"
              type="text"
              placeholder="例: https://github.com/user/plugins-marketplace.git"
              class="form-input"
              @keydown.enter="submitAdd"
            />
          </div>
          <p class="dialog-hint">
            市场须含 .claude-plugin/marketplace.json；市场内插件的技能可一键部署为 ~/.agents/skills 共用技能。
          </p>
        </div>
        <template #footer>
          <AppButton variant="ghost" @click="showAddDialog = false">取消</AppButton>
          <AppButton variant="primary" :disabled="addSubmitting || !addSource.trim()" @click="submitAdd">
            {{ addSubmitting ? '克隆中…' : '添加' }}
          </AppButton>
        </template>
      </Dialog>

      <!-- 从 Claude Code 导入 Dialog -->
      <Dialog
        v-model:open="showImportDialog"
        title="从 Claude Code 导入市场"
        description="选择 Claude Code 已注册的市场，克隆为共用市场（保持同名）"
      >
        <div class="import-list">
          <EmptyState v-if="claudeMarkets.length === 0" icon="◇" title="Claude Code 暂无已注册市场" />
          <div
            v-for="market in claudeMarkets"
            :key="market.name"
            :class="['import-row', { selected: importSelected.has(market.name), taken: market.imported }]"
            role="button"
            tabindex="0"
            @click="toggleImport(market)"
          >
            <span class="import-check" aria-hidden="true">{{ importSelected.has(market.name) ? '☑' : '☐' }}</span>
            <div class="import-main">
              <span class="import-name">{{ market.name }}</span>
              <span class="import-source">{{ market.source || '（来源不可解析）' }}</span>
            </div>
            <span v-if="market.imported" class="import-taken">已导入</span>
          </div>
        </div>
        <template #footer>
          <AppButton variant="ghost" @click="showImportDialog = false">取消</AppButton>
          <AppButton
            variant="primary"
            :disabled="importSubmitting || importSelected.size === 0"
            @click="confirmImport"
          >
            {{ importSubmitting ? '导入中…' : `导入（${importSelected.size}）` }}
          </AppButton>
        </template>
      </Dialog>

      <!-- 删除市场确认（danger） -->
      <ConfirmDialog
        v-model:open="showRemoveDialog"
        title="删除共用市场"
        :message="removeConfirmMessage"
        danger
        confirm-text="删除"
        @confirm="confirmRemove"
      />

      <!-- 卸载插件已部署技能确认（danger；仅移除凭据匹配目录） -->
      <ConfirmDialog
        v-model:open="showUninstallDialog"
        title="卸载已部署技能"
        :message="uninstallConfirmMessage"
        danger
        confirm-text="卸载"
        @confirm="confirmUninstall"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useSharedMarketStore } from '../../stores/sharedMarket';
import { useSharedSkillsStore } from '../../stores/sharedSkills';
import * as sharedMarketApi from '../../api/sharedMarket';
import type { SharedMarketplace, SharedMarketPlugin, ClaudeMarketplaceForImport } from '../../api/sharedMarket';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import ListToolbar from './ui/ListToolbar.vue';
import PluginRow from './ui/PluginRow.vue';
import type { PluginBadgeDescriptor } from './pluginViewTypes';
import { useToast } from '../../composables/useToast';

const store = useSharedMarketStore();
// F06：部署写入 ~/.agents/skills 后，同步刷新共用技能 store——否则返回
// 共用技能子页时 loaded=true 的缓存不重读，新技能不可见无法立即管理
const sharedSkillsStore = useSharedSkillsStore();
const {
  marketplaces,
  loading: isLoading,
  error,
  loaded,
} = storeToRefs(store);
const { load, add, update, updatePlugin, remove } = store;
const { showSuccess, showError, showWarn } = useToast();

// --- 无依赖窄定位：initialMarket prop ---
// 仅按名称在已加载市场列表中选中市场详情页（不走路由/store；供外部导航如
// 共用技能页「查看来源市场」配合）。列表就绪前挂起等待；同名 prop 不重复
// 拉回（用户手动返回后不再次跳转），prop 变更则重新定位。
const props = defineProps<{ initialMarket?: string }>();

// --- 两级分页状态 ---
const activeMarketName = ref<string | null>(null);
const activeMarket = computed<SharedMarketplace | null>(
  () => marketplaces.value.find((m) => m.name === activeMarketName.value) || null,
);

// initialMarket 定位（声明于 activeMarketName 之后，immediate 回调即时执行）
const appliedInitialMarket = ref<string | null>(null);
watch(
  [() => props.initialMarket, marketplaces],
  () => {
    const target = props.initialMarket;
    if (!target || appliedInitialMarket.value === target) return;
    if (marketplaces.value.some((m) => m.name === target)) {
      activeMarketName.value = target;
      appliedInitialMarket.value = target;
    }
  },
  { immediate: true },
);

// --- 行级操作中态 ---
const updatingNames = ref<Set<string>>(new Set());
const updatingPlugins = ref(new Map<string, string>());
const deployingKeys = ref<Record<string, boolean>>({});

// --- 添加市场 Dialog ---
const showAddDialog = ref(false);
const addSource = ref('');
const addSubmitting = ref(false);

// --- 从 Claude 导入 Dialog ---
const showImportDialog = ref(false);
const importLoading = ref(false);
const importSubmitting = ref(false);
const claudeMarkets = ref<ClaudeMarketplaceForImport[]>([]);
const importSelected = ref<Set<string>>(new Set());

// --- 删除确认 ---
const showRemoveDialog = ref(false);
const pendingRemove = ref<SharedMarketplace | null>(null);

// --- 首屏门控（store 派生，SSR 测试不执行 onMounted） ---
const showInitialLoading = computed(
  () => isLoading.value && marketplaces.value.length === 0 && !error.value,
);
const showInitialError = computed(() => !!error.value && marketplaces.value.length === 0);

// --- 行组装 ---
function marketDescription(market: SharedMarketplace): string {
  if (market.missing) return `本地 clone 不可读（${market.source}）；点击「更新」可恢复`;
  return market.description || market.source;
}

function marketBadges(market: SharedMarketplace): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [
    { text: `${market.plugins.length} 个插件`, type: 'scope', variant: 'muted' },
  ];
  if (market.missing) {
    badges.push({ text: '目录缺失', type: 'type', color: 'warning' });
  }
  return badges;
}

function pluginDescription(plugin: SharedMarketPlugin): string {
  return plugin.description || (plugin.externalSource ? '外部 git 引用，内容不在市场仓库内' : '');
}

function pluginBadges(plugin: SharedMarketPlugin): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [];
  if (plugin.version) badges.push({ text: `v${plugin.version}`, type: 'ver' });
  if (plugin.author) badges.push({ text: plugin.author, type: 'source', variant: 'muted' });
  if (plugin.externalSource) {
    badges.push({ text: '外部引用', type: 'type', color: 'warning' });
  } else {
    badges.push({ text: `${plugin.skills.length} 个技能`, type: 'scope', variant: 'muted' });
  }
  // 已部署徽章（凭据制）：只有来源凭据仍指向本插件的目录才计入，
  // 同名手动目录不虚报
  const deployed = deployedCount(plugin);
  if (deployed > 0) badges.push({ text: `已部署 ${deployed}`, type: 'scope' });
  return badges;
}

function deployedCount(plugin: SharedMarketPlugin): number {
  return plugin.deployedSkills?.length ?? 0;
}

function deployButtonLabel(plugin: SharedMarketPlugin): string {
  if (plugin.externalSource) return '不可部署';
  if (plugin.skills.length === 0) return '无技能';
  return '部署技能';
}

function deployButtonTitle(plugin: SharedMarketPlugin): string {
  if (plugin.externalSource) return '插件内容不在市场仓库内（外部 git 引用）';
  if (plugin.skills.length === 0) return '未发现 SKILL.md，无可部署技能';
  return `复制技能到 ~/.agents/skills：${plugin.skills.join('、')}`;
}

// --- 导航 ---
function selectMarket(market: SharedMarketplace) {
  activeMarketName.value = market.name;
}

function backToMarkets() {
  activeMarketName.value = null;
}

// --- 动作 ---
function isUpdating(name: string): boolean {
  return updatingNames.value.has(name);
}

function marketUpdateBusy(name: string): boolean {
  return isUpdating(name) || updatingPlugins.value.has(name);
}

function isUpdatingPlugin(name: string): boolean {
  const marketName = activeMarketName.value;
  return marketName !== null && updatingPlugins.value.get(marketName) === name;
}

function pluginUpdateBusy(marketName: string, pluginName: string): boolean {
  return marketUpdateBusy(marketName) || isDeploying(pluginName) || isUninstalling(pluginName);
}

async function handleUpdate(market: SharedMarketplace) {
  if (marketUpdateBusy(market.name)) return;
  updatingNames.value = new Set(updatingNames.value).add(market.name);
  try {
    await update(market.name);
    showSuccess(`已更新「${market.name}」`);
  } catch (err) {
    console.error('[SharedMarketPanel] Update failed:', err);
    showError(`更新市场失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    const next = new Set(updatingNames.value);
    next.delete(market.name);
    updatingNames.value = next;
  }
}

async function handleUpdatePlugin(plugin: SharedMarketPlugin) {
  const market = activeMarket.value;
  if (!market || !plugin.updatable || pluginUpdateBusy(market.name, plugin.name)) return;

  updatingPlugins.value.set(market.name, plugin.name);
  try {
    await updatePlugin(market.name, plugin.name);
    showSuccess(`已更新插件「${plugin.name}」`);
  } catch (err) {
    console.error('[SharedMarketPanel] Plugin update failed:', err);
    showError(`更新插件失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    updatingPlugins.value.delete(market.name);
  }
}

// --- 部署忙态 key（F09）：模板与 handler 共用同一构造，保证按钮禁用态真实 ---
function deployKey(pluginName: string): string {
  return activeMarket.value ? `${activeMarket.value.name}/${pluginName}` : pluginName;
}

function isDeploying(pluginName: string): boolean {
  return !!deployingKeys.value[deployKey(pluginName)];
}

async function handleDeploy(plugin: SharedMarketPlugin) {
  if (!activeMarket.value) return;
  const key = deployKey(plugin.name);
  if (isDeploying(plugin.name)) return;

  deployingKeys.value = { ...deployingKeys.value, [key]: true };
  try {
    const report = await sharedMarketApi.deploySharedMarketplacePlugin(
      activeMarket.value.name,
      plugin.name,
    );
    const parts: string[] = [];
    if (report.deployed.length > 0) parts.push(`已部署 ${report.deployed.length} 个技能`);
    if (report.skipped.length > 0) parts.push(`跳过 ${report.skipped.length} 个（已存在）`);
    if (parts.length === 0) {
      showWarn('没有可部署的技能');
    } else {
      showSuccess(`「${plugin.name}」${parts.join('，')}；在「共用技能」子页管理启停`);
    }
    // F06：刷新共用技能缓存，返回技能页立即可见
    void sharedSkillsStore.load();
  } catch (err) {
    console.error('[SharedMarketPanel] Deploy failed:', err);
    showError(`部署失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    deployingKeys.value = { ...deployingKeys.value, [key]: false };
  }
}

async function handleOpenFolder() {
  if (!activeMarket.value) return;
  try {
    await sharedMarketApi.openSharedMarketplaceFolder(activeMarket.value.name);
  } catch (err) {
    console.error('[SharedMarketPanel] Open folder failed:', err);
    showError(`打开目录失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

// --- 卸载插件已部署技能（凭据制：仅移除来源凭据匹配目录） ---
const uninstallingKeys = ref<Record<string, boolean>>({});
const showUninstallDialog = ref(false);
const pendingUninstall = ref<SharedMarketPlugin | null>(null);

function uninstallKey(pluginName: string): string {
  return activeMarket.value ? `uninstall:${activeMarket.value.name}/${pluginName}` : `uninstall:${pluginName}`;
}

function isUninstalling(pluginName: string): boolean {
  return !!uninstallingKeys.value[uninstallKey(pluginName)];
}

function uninstallButtonTitle(plugin: SharedMarketPlugin): string {
  return `仅移除有来源凭据的已部署技能：${(plugin.deployedSkills ?? []).join('、')}；同名手动目录与市场克隆不受影响`;
}

function requestUninstall(plugin: SharedMarketPlugin) {
  pendingUninstall.value = plugin;
  showUninstallDialog.value = true;
}

const uninstallConfirmMessage = computed(() => {
  const plugin = pendingUninstall.value;
  if (!plugin || !activeMarket.value) return '';
  const skills = plugin.deployedSkills ?? [];
  return `卸载「${activeMarket.value.name} / ${plugin.name}」的已部署共用技能？将删除 ${skills.length} 个凭据匹配目录：${skills.join('、')}（含停用态）；同名手动目录与无凭据目录会保留，市场克隆不受影响。此操作不可恢复。`;
});

async function confirmUninstall() {
  const plugin = pendingUninstall.value;
  if (!plugin || !activeMarket.value) return;
  const key = uninstallKey(plugin.name);
  if (uninstallingKeys.value[key]) return;

  uninstallingKeys.value = { ...uninstallingKeys.value, [key]: true };
  showUninstallDialog.value = false;
  try {
    const report = await sharedMarketApi.uninstallSharedMarketplacePlugin(
      activeMarket.value.name,
      plugin.name,
    );
    if (report.removed.length > 0) {
      showSuccess(`「${plugin.name}」已卸载 ${report.removed.length} 个技能：${report.removed.join('、')}；手动/无凭据目录已保留`);
    } else {
      showWarn('没有可卸载的已部署技能（无来源凭据记录）');
    }
  } catch (err) {
    console.error('[SharedMarketPanel] Uninstall failed:', err);
    showError(`卸载失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    // 无论成败都刷新：卸载改变了 ~/.agents/skills 现场，市场列表的
    // deployedSkills 与共用技能缓存须同步（F06 同款联动）
    await load();
    void sharedSkillsStore.load();
    uninstallingKeys.value = { ...uninstallingKeys.value, [key]: false };
    pendingUninstall.value = null;
  }
}

// --- 添加市场 ---
function openAddDialog() {
  addSource.value = '';
  showAddDialog.value = true;
}

async function submitAdd() {
  const source = addSource.value.trim();
  if (!source || addSubmitting.value) return;

  addSubmitting.value = true;
  try {
    await add(source);
    showSuccess('市场已添加');
    showAddDialog.value = false;
    addSource.value = '';
  } catch (err) {
    console.error('[SharedMarketPanel] Add failed:', err);
    showError(`添加市场失败：${err instanceof Error ? err.message : String(err)}`);
    // 失败保留弹窗与输入，便于修正后重试
  } finally {
    addSubmitting.value = false;
  }
}

// --- 从 Claude Code 导入 ---
async function openImportDialog() {
  showImportDialog.value = true;
  importLoading.value = true;
  importSelected.value = new Set();
  try {
    claudeMarkets.value = await sharedMarketApi.listClaudeMarketplacesForImport();
  } catch (err) {
    console.error('[SharedMarketPanel] List claude marketplaces failed:', err);
    showError(`读取 Claude 市场列表失败：${err instanceof Error ? err.message : String(err)}`);
    showImportDialog.value = false;
  } finally {
    importLoading.value = false;
  }
}

function toggleImport(market: ClaudeMarketplaceForImport) {
  if (market.imported) return; // 已导入的不可再选
  const next = new Set(importSelected.value);
  if (next.has(market.name)) {
    next.delete(market.name);
  } else {
    next.add(market.name);
  }
  importSelected.value = next;
}

async function confirmImport() {
  if (importSelected.value.size === 0 || importSubmitting.value) return;

  importSubmitting.value = true;
  try {
    await sharedMarketApi.importSharedMarketplacesFromClaude([...importSelected.value]);
    marketplaces.value = await sharedMarketApi.listSharedMarkets();
    loaded.value = true;
    showSuccess(`已导入 ${importSelected.value.size} 个市场`);
    showImportDialog.value = false;
  } catch (err) {
    console.error('[SharedMarketPanel] Import failed:', err);
    showError(`导入失败：${err instanceof Error ? err.message : String(err)}`);
    // F07：后端批量导入不回滚，部分成功时同步真实状态——刷新共用列表与导入
    // 视图，剔除已成功项、保留失败项供重试；弹窗保留。刷新失败不掩盖原错误。
    try {
      marketplaces.value = await sharedMarketApi.listSharedMarkets();
      loaded.value = true;
      claudeMarkets.value = await sharedMarketApi.listClaudeMarketplacesForImport();
      const importedNow = new Set(marketplaces.value.map((m) => m.name));
      importSelected.value = new Set(
        [...importSelected.value].filter((name) => !importedNow.has(name)),
      );
    } catch (refreshErr) {
      console.error('[SharedMarketPanel] Refresh after partial import failed:', refreshErr);
    }
  } finally {
    importSubmitting.value = false;
  }
}

// --- 删除市场 ---
function requestRemove(market: SharedMarketplace) {
  pendingRemove.value = market;
  showRemoveDialog.value = true;
}

const removeConfirmMessage = computed(() => {
  const market = pendingRemove.value;
  if (!market) return '';
  return `删除共用市场「${market.name}」？将删除本地克隆目录 ${market.clonePath} 与登记状态；若还有带来源记录的部署，请先卸载对应插件。此操作不可恢复。`;
});

async function confirmRemove() {
  const market = pendingRemove.value;
  if (!market) return;
  try {
    await remove(market.name);
    showSuccess(`已删除「${market.name}」`);
    if (activeMarketName.value === market.name) {
      activeMarketName.value = null;
    }
  } catch (err) {
    console.error('[SharedMarketPanel] Remove failed:', err);
    showError(`删除市场失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    showRemoveDialog.value = false;
    pendingRemove.value = null;
  }
}

// 首载：store 已加载过则不重复
onMounted(() => {
  if (!loaded.value) {
    void load();
  }
});

// 测试契约（面板范式）：expose 触发器
defineExpose({
  selectMarket,
  backToMarkets,
  handleUpdate,
  handleUpdatePlugin,
  handleDeploy,
  handleOpenFolder,
  requestUninstall,
  confirmUninstall,
  openAddDialog,
  submitAdd,
  addSource,
  openImportDialog,
  toggleImport,
  confirmImport,
  requestRemove,
  confirmRemove,
});
</script>

<style scoped>
.shared-market-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.market-list,
.plugin-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.market-list :deep(.plugin-row),
.plugin-list :deep(.plugin-row) {
  align-items: flex-start;
}

.market-list :deep(.plugin-row-desc),
.plugin-list :deep(.plugin-row-desc) {
  white-space: normal;
  overflow: visible;
  word-break: break-word;
}

.row-actions {
  display: flex;
  gap: 6px;
  flex: 0 0 auto;
}

.market-plugin-page {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.back-btn {
  align-self: flex-start;
  border: none;
  background: none;
  padding: 2px 0;
  font-size: 13px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
}

.back-btn:hover {
  text-decoration: underline;
}

.add-plus {
  font-size: 13px;
  font-weight: 600;
  line-height: 1;
}

/* 添加市场表单（与 ExtensionsView add-market form 同构） */
.add-market-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input {
  padding: 8px 12px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
}

.form-input:focus {
  outline: none;
  border-color: var(--accent);
}

.dialog-hint {
  font-size: 12px;
  color: var(--secondary);
  margin: 0;
}

/* 导入选择器列表 */
.import-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 320px;
  overflow-y: auto;
}

.import-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.12s;
}

.import-row:hover {
  border-color: var(--accent);
}

.import-row.selected {
  border-color: var(--accent);
  background: var(--control);
}

.import-row.taken {
  cursor: default;
  opacity: 0.55;
}

.import-check {
  font-size: 15px;
  color: var(--accent);
  flex: 0 0 auto;
}

.import-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.import-name {
  font-size: 13px;
  color: var(--label);
  font-weight: 500;
}

.import-source {
  font-size: 11px;
  color: var(--secondary);
  font-family: var(--mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.import-taken {
  font-size: 11px;
  color: var(--secondary);
  flex: 0 0 auto;
}
</style>
