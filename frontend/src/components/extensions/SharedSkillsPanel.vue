<!-- 共用技能面板（契约 §2.6）：~/.agents/skills 的四段式管理面板，复用 ui/* 共享件。
     ① DiagnosticBanner（errors=store.error）→ ② CountTabs（全部/已启用/已禁用 + 搜索）→
     ③ ListToolbar（「共用技能 N」+ 打开技能根目录/刷新）→ ④ 内容区
     （列表态：PluginRow + 启停 Switch + 版本/托管徽章；详情态：DetailHeader+MetaGrid+描述；
      空态：EmptyStateCard）。
     启停/删除为目录移动/永久删除（对读取 ~/.agents/skills 的 CLI 立即生效），均带确认
     弹窗（文案契约 §2.6）；managed（amagi-codebox 自带）不弹窗不调 API，toast 引导到
     设置 → 命令行访问。首屏 Loading/Error 门控照抄 PluginInstalledPanel（store 派生，
     SSR 测试不执行 onMounted）。 -->
<template>
  <div class="shared-skills-panel">
    <LoadingState v-if="showInitialLoading" message="加载技能中..." />

    <ErrorState
      v-else-if="showInitialError"
      :message="error"
      :on-retry="load"
    />

    <template v-else>
      <!-- ① 诊断横幅：errors=store 加载错误（有数据后的降级形态） -->
      <DiagnosticBanner :errors="error ? [error] : []" />

      <!-- ② 计数标签（已禁用为 0 时仍显示，计数真实）+ 搜索框 -->
      <CountTabs
        :tabs="countTabs"
        :model-value="activeTab"
        search-placeholder="搜索技能名或描述…"
        :search-value="searchText"
        @update:model-value="selectTab"
        @update:search-value="onSearch"
      />

      <!-- ③ 工具栏 -->
      <ListToolbar title="共用技能" :count="visibleSkills.length" :loading="isLoading">
        <template #actions>
          <AppButton variant="ghost" size="small" @click="handleOpenRoot">
            打开技能根目录
          </AppButton>
          <AppButton variant="ghost" size="small" :disabled="isLoading" @click="load">
            {{ isLoading ? '刷新中…' : '刷新' }}
          </AppButton>
        </template>
      </ListToolbar>

      <!-- ④ 内容区：空态 / 列表+详情两列 -->
      <EmptyStateCard
        v-if="skills.length === 0"
        title="暂无共用技能"
        description="~/.agents/skills 下的技能目录会自动出现在这里"
      />

      <div v-else class="ex-split">
        <!-- 左列：技能行（图标/名称+徽章/描述/启停 Switch） -->
        <div class="skill-list">
          <PluginRow
            v-for="skill in visibleSkills"
            :key="skill.name"
            :name="skill.displayName"
            :description="skill.description"
            :badges="rowBadges(skill)"
            :enabled="skill.enabled"
            switch-visible
            :active="activeSkillName === skill.name"
            @click="selectSkill(skill)"
            @toggle="(value) => handleToggle(skill, value)"
          />
          <EmptyState
            v-if="visibleSkills.length === 0"
            icon="◇"
            title="无匹配技能"
            description="换个关键词或切换计数标签试试"
          />
        </div>

        <!-- 右列：详情 -->
        <div v-if="activeSkill" class="skill-detail">
          <DetailHeader
            :breadcrumb="[{ label: '共用技能', clickable: true }, { label: activeSkill.displayName }]"
            :title="activeSkill.displayName"
            :badges="rowBadges(activeSkill)"
            @back="clearActive"
          >
            <template #actions>
              <AppButton
                variant="primary"
                size="small"
                @click="handleToggle(activeSkill, !activeSkill.enabled)"
              >
                {{ activeSkill.enabled ? '停用' : '启用' }}
              </AppButton>
              <AppButton variant="ghost" size="small" @click="handleOpenFolder(activeSkill)">
                打开目录
              </AppButton>
              <AppButton variant="danger" size="small" @click="requestDelete(activeSkill)">
                删除
              </AppButton>
            </template>
          </DetailHeader>

          <p v-if="activeSkill.description" class="detail-desc">{{ activeSkill.description }}</p>
          <p v-else class="detail-desc">该技能暂无描述。</p>

          <MetaGrid v-if="metaItems.length > 0" :items="metaItems" class="detail-meta" />
        </div>

        <div v-else class="skill-detail-empty">
          <EmptyState icon="◇" title="选择技能" description="点击左侧技能查看详细信息" />
        </div>
      </div>
    </template>

    <!-- 启停确认：目录移动真实生效（文案契约 §2.6） -->
    <ConfirmDialog
      v-model:open="showToggleDialog"
      :title="pendingToggle?.value ? '启用技能' : '停用技能'"
      :message="toggleConfirmMessage"
      :confirm-text="pendingToggle?.value ? '启用' : '停用'"
      @confirm="confirmToggle"
    />

    <!-- 删除确认（danger）：永久删除目录 -->
    <ConfirmDialog
      v-model:open="showDeleteDialog"
      title="删除技能"
      :message="deleteConfirmMessage"
      danger
      confirm-text="删除"
      @confirm="confirmDelete"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { useSharedSkillsStore } from '../../stores/sharedSkills';
import * as sharedSkillsApi from '../../api/sharedSkills';
import type { SharedSkillView } from '../../api/sharedSkills';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import ListToolbar from './ui/ListToolbar.vue';
import MetaGrid from './ui/MetaGrid.vue';
import PluginRow from './ui/PluginRow.vue';
import type {
  CountTabItem,
  MetaItemView,
  PluginBadgeDescriptor,
} from './pluginViewTypes';
import { useToast } from '../../composables/useToast';

// managed 技能引导文案（契约 §2.4 行为裁定：与 App 门面错误文案一致）
const MANAGED_TOAST = 'amagi-codebox 为应用托管技能，请在 设置 → 命令行访问 中管理';

// install 五态 → 中文标签（managed 技能详情用，未知值原样展示）
const MANAGED_STATUS_LABELS: Record<string, string> = {
  'not-installed': '未安装',
  'up-to-date': '已是最新',
  outdated: '有更新',
  'modified-by-user': '用户已修改',
  invalid: '无效',
};

const store = useSharedSkillsStore();
const {
  skills,
  loading: isLoading,
  error,
  loaded,
  enabledCount,
  disabledCount,
} = storeToRefs(store);
const { load, setEnabled, remove } = store;
const { showSuccess, showError, showWarn } = useToast();

// --- 本地视图状态（门控类状态一律 store 派生，契约 §2.6） ---
type SkillTab = 'all' | 'enabled' | 'disabled';
const activeTab = ref<SkillTab>('all');
const searchText = ref('');
const activeSkillName = ref<string | null>(null);
const showToggleDialog = ref(false);
const showDeleteDialog = ref(false);
const pendingToggle = ref<{ skill: SharedSkillView; value: boolean } | null>(null);
const pendingDelete = ref<SharedSkillView | null>(null);

// --- 首屏门控（store 派生，SSR 测试不执行 onMounted） ---
const showInitialLoading = computed(
  () => isLoading.value && skills.value.length === 0 && !error.value,
);

const showInitialError = computed(() => !!error.value && skills.value.length === 0);

// --- ② 计数标签：计数来自 store 真实数据 ---
const countTabs = computed<CountTabItem[]>(() => [
  { key: 'all', label: '全部', count: skills.value.length },
  { key: 'enabled', label: '已启用', count: enabledCount.value },
  { key: 'disabled', label: '已禁用', count: disabledCount.value },
]);

function selectTab(key: string) {
  activeTab.value = key as SkillTab;
  searchText.value = '';
}

function onSearch(value: string) {
  searchText.value = value;
}

// --- ④ 列表态：标签过滤 + 搜索过滤 ---
const visibleSkills = computed<SharedSkillView[]>(() => {
  const query = searchText.value.trim().toLowerCase();
  return skills.value
    .filter((s) =>
      activeTab.value === 'all'
        ? true
        : activeTab.value === 'enabled'
          ? s.enabled
          : !s.enabled,
    )
    .filter(
      (s) =>
        !query ||
        s.name.toLowerCase().includes(query) ||
        s.displayName.toLowerCase().includes(query) ||
        s.description.toLowerCase().includes(query),
    );
});

// --- ④ 详情态 ---
const activeSkill = computed<SharedSkillView | null>(
  () => skills.value.find((s) => s.name === activeSkillName.value) || null,
);

function selectSkill(skill: SharedSkillView) {
  activeSkillName.value = skill.name;
}

function clearActive() {
  activeSkillName.value = null;
}

// 行/详情徽章：托管徽章（warning 受管范式）+ 版本（缺失不伪造）
function rowBadges(skill: SharedSkillView): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [];
  if (skill.managed) {
    badges.push({ text: '应用托管', type: 'type', color: 'warning' });
  }
  if (skill.version) {
    badges.push({ text: `v${skill.version}`, type: 'ver' });
  }
  return badges;
}

const metaItems = computed<MetaItemView[]>(() => {
  const skill = activeSkill.value;
  if (!skill) return [];
  const items: MetaItemView[] = [
    { label: '名称', value: skill.displayName },
    { label: '目录名', value: skill.name, mono: true },
    { label: '状态', value: skill.enabled ? '已启用' : '已禁用' },
  ];
  if (skill.version) items.push({ label: '版本', value: skill.version });
  if (skill.author) items.push({ label: '作者', value: skill.author });
  items.push({ label: '路径', value: skill.path, mono: true });
  if (!skill.hasSkillMd) {
    items.push({ label: 'SKILL.md', value: '缺失（以上为回退信息）' });
  }
  if (skill.managed && skill.managedStatus) {
    items.push({
      label: '托管状态',
      value: MANAGED_STATUS_LABELS[skill.managedStatus] ?? skill.managedStatus,
    });
  }
  return items.filter((item) => item.value !== '');
});

// --- 启停：目录移动真实生效，先确认（文案契约 §2.6）；managed 直接 toast 引导 ---
function handleToggle(skill: SharedSkillView, value: boolean) {
  if (skill.managed) {
    showWarn(MANAGED_TOAST);
    return;
  }
  pendingToggle.value = { skill, value };
  showToggleDialog.value = true;
}

const toggleConfirmMessage = computed(() => {
  const pending = pendingToggle.value;
  if (!pending) return '';
  return pending.value
    ? `启用「${pending.skill.displayName}」？将把目录移回 ~/.agents/skills/，各 CLI 即可重新发现并加载该技能。`
    : `停用「${pending.skill.displayName}」？将把目录移动到 ~/.agents/skills-disabled/，所有读取 ~/.agents/skills 的 CLI 将立即不再加载该技能。可随时重新启用恢复。`;
});

async function confirmToggle() {
  const pending = pendingToggle.value;
  if (!pending) return;
  try {
    await setEnabled(pending.skill.name, pending.value);
    showSuccess(pending.value ? `已启用「${pending.skill.displayName}」` : `已停用「${pending.skill.displayName}」`);
  } catch (err) {
    console.error('[SharedSkillsPanel] Toggle failed:', err);
    showError(`状态切换失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    showToggleDialog.value = false;
    pendingToggle.value = null;
  }
}

// --- 删除：永久删除目录，danger 确认（文案契约 §2.6）；managed 直接 toast 引导 ---
function requestDelete(skill: SharedSkillView) {
  if (skill.managed) {
    showWarn(MANAGED_TOAST);
    return;
  }
  pendingDelete.value = skill;
  showDeleteDialog.value = true;
}

const deleteConfirmMessage = computed(() => {
  const skill = pendingDelete.value;
  if (!skill) return '';
  return `删除「${skill.displayName}」？将永久删除目录 ${skill.path} 及其全部内容，此操作不可恢复。`;
});

async function confirmDelete() {
  const skill = pendingDelete.value;
  if (!skill) return;
  try {
    await remove(skill.name);
    showSuccess(`已删除「${skill.displayName}」`);
    if (activeSkillName.value === skill.name) {
      activeSkillName.value = null;
    }
  } catch (err) {
    console.error('[SharedSkillsPanel] Delete failed:', err);
    showError(`删除失败：${err instanceof Error ? err.message : String(err)}`);
  } finally {
    showDeleteDialog.value = false;
    pendingDelete.value = null;
  }
}

// --- 打开目录（单技能 / 根目录） ---
async function handleOpenFolder(skill: SharedSkillView) {
  try {
    await sharedSkillsApi.openSharedSkillFolder(skill.name);
  } catch (err) {
    console.error('[SharedSkillsPanel] Open folder failed:', err);
    showError(`打开目录失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

async function handleOpenRoot() {
  try {
    await sharedSkillsApi.openSharedSkillsRoot();
  } catch (err) {
    console.error('[SharedSkillsPanel] Open root failed:', err);
    showError(`打开技能根目录失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

// 首载：store 已加载过则不重复（契约 §2.6：切走再切回不重复加载，靠 store 状态）
onMounted(() => {
  if (!loaded.value) {
    void load();
  }
});

// 测试契约（面板范式）：expose 触发器
defineExpose({
  selectSkill,
  clearActive,
  selectTab,
  onSearch,
  handleToggle,
  confirmToggle,
  requestDelete,
  confirmDelete,
  handleOpenFolder,
  handleOpenRoot,
  handleRefresh: load,
});
</script>

<style scoped>
.shared-skills-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.ex-split {
  display: grid;
  grid-template-columns: 320px 1fr;
  gap: 14px;
  align-items: start;
}

.skill-list {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 320px);
  overflow-y: auto;
}

/* 详情列 */
.skill-detail {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 16px 18px;
  max-height: calc(100vh - 320px);
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.skill-detail-empty {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  min-height: 200px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.detail-desc {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.6;
}

.detail-meta {
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}

/* Scrollbar styling */
.skill-list::-webkit-scrollbar,
.skill-detail::-webkit-scrollbar {
  width: 8px;
}

.skill-list::-webkit-scrollbar-track,
.skill-detail::-webkit-scrollbar-track {
  background: transparent;
}

.skill-list::-webkit-scrollbar-thumb,
.skill-detail::-webkit-scrollbar-thumb {
  background: var(--separator);
  border-radius: 4px;
}

.skill-list::-webkit-scrollbar-thumb:hover,
.skill-detail::-webkit-scrollbar-thumb:hover {
  background: var(--tertiary);
}
</style>
