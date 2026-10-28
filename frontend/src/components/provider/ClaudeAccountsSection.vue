<!--
  ClaudeAccountsSection — Provider center「Claude 订阅账号」区（Phase 1c，design §7.1）。
  状态机：hidden/absent/managed/enrolling/needs-reconcile/blocked/error（见 claudeAccountsView.ts）。
  交互契约：
   - 账号卡：别名、默认遮罩邮箱（显隐切换只显示后端遮罩值，完整邮箱不出后端）、
     套餐、双端登录健康、当前活动标记；额度区为 Phase 2 占位，不放假数据。
   - 切换：expectedGeneration 乐观锁（store）；SESSIONS_ACTIVE → 等待会话退出，
     会话退出后自动完成（本组件轮询 getSessions 触发重试）。
   - 与既有卡片：「登录认证 · Pi」保持原样；切换成功后 loadLoginProviders() 刷新发现。
-->
<template>
  <section v-if="state !== 'hidden'" class="ca-section" data-testid="claude-accounts-section">
    <div class="pc-zone-label">
      <span>Claude 订阅账号</span>
      <span class="zn-sep">·</span>
      <span>双端登录池</span>
      <span v-if="state === 'managed'" class="zn-count">· {{ accounts.length }} 个账号</span>
    </div>
    <p class="ca-note">
      保存 Claude Code 与 Pi 的原生订阅登录，在一键切换时同步写入两端，免去反复登录。
      此池同步影响 Claude OAuth provider 与 Pi anthropic 登录卡；进行中的旧会话不受切换影响，新会话使用目标账号。
    </p>

    <!-- 加载中 -->
    <div v-if="state === 'loading'" class="ca-empty">加载 Claude 订阅账号…</div>

    <!-- 池不可用（读取失败且无上次数据） -->
    <template v-else-if="state === 'blocked'">
      <StatusBanner type="error" :message="`账号池不可用：${store.loadError}`" />
      <div class="ca-actions">
        <AppButton variant="ghost" size="small" :disabled="store.loading" @click="reload">重试加载</AppButton>
      </div>
    </template>

    <!-- 未录入：首次 capture 引导（design §4.1 步骤 1） -->
    <div v-else-if="state === 'absent'" class="ca-absent">
      <p class="ca-absent-tip">
        尚未保存任何 Claude 订阅账号。请在原生 Claude Code 与 Pi 中登录<strong>同一订阅账号</strong>，
        并退出所有正在使用它的会话（包括 CodeBox 外部的终端），再保存当前双端登录。
      </p>
      <div class="ca-form">
        <TextInput
          v-model="firstAlias"
          class="ca-alias-input"
          placeholder="账号别名（如：工作号）"
          :disabled="busy"
        />
        <AppButton
          variant="primary"
          size="small"
          :disabled="busy || !!firstAliasError"
          :title="busyText"
          @click="saveFirst"
        >{{ busy ? busyText : '保存当前双端登录' }}</AppButton>
      </div>
      <p v-if="firstAliasError" class="ca-error">{{ firstAliasError }}</p>
    </div>

    <!-- 录入向导（design §4.1 四步） -->
    <div v-else-if="state === 'enrolling'" class="ca-enroll">
      <ol class="ca-steps">
        <li
          v-for="(s, i) in ENROLLMENT_STEPS"
          :key="s.key"
          :class="stepClass(s.key)"
        >
          <span class="ca-step-title">{{ i + 1 }}. {{ s.title }}</span>
          <span class="ca-step-detail">{{ s.detail }}</span>
        </li>
      </ol>
      <div v-if="enrollStep === 'login'" class="ca-form">
        <AppButton variant="primary" size="small" @click="store.advanceEnrollmentStep()">
          已在两端登录新账号，下一步
        </AppButton>
        <AppButton variant="ghost" size="small" :disabled="busy" @click="cancelAskOpen = true">取消录入</AppButton>
      </div>
      <div v-else class="ca-form">
        <TextInput
          v-model="enrollAlias"
          class="ca-alias-input"
          placeholder="新账号别名（如：个人号）"
          :disabled="busy"
        />
        <AppButton
          variant="primary"
          size="small"
          :disabled="busy || !!enrollAliasError"
          :title="busyText"
          @click="captureEnrolled"
        >{{ busy ? busyText : '核对并保存' }}</AppButton>
        <AppButton variant="ghost" size="small" :disabled="busy" @click="cancelAskOpen = true">取消录入</AppButton>
      </div>
      <p v-if="enrollAliasError" class="ca-error">{{ enrollAliasError }}</p>
    </div>

    <!-- 需要恢复（needs-reconcile：inspect 报告 + 三选一） -->
    <div v-else-if="state === 'needs-reconcile'" class="ca-reconcile">
      <StatusBanner
        type="warning"
        :message="`存在未完成的事务（${store.pool?.pauseReason || '未结切换事务'}），恢复完成前受影响的 Claude/Pi 启动入口保持关闭。`"
      />
      <div class="ca-actions">
        <AppButton variant="ghost" size="small" :disabled="busy" @click="runInspect">重新检查</AppButton>
      </div>
      <ul class="ca-inspect">
        <li v-for="(line, i) in inspectLines" :key="i">{{ line }}</li>
      </ul>
      <div v-for="opt in reconcileOptions" :key="opt.action" class="ca-reconcile-opt">
        <div class="ca-reconcile-text">
          <strong>{{ opt.title }}</strong>
          <span>{{ opt.description }}</span>
        </div>
        <AppButton
          variant="primary"
          size="small"
          :disabled="busy"
          :title="busyText"
          @click="applyReconcile(opt.action)"
        >执行</AppButton>
      </div>
    </div>

    <!-- 已管理（managed）／读取失败但保留上次值（error） -->
    <template v-else>
      <StatusBanner
        v-if="state === 'error'"
        type="warning"
        :message="`刷新失败，正在显示上次保存的账号状态：${store.loadError}`"
        action-text="重试"
        @action="reload"
      />
      <StatusBanner
        v-if="activeNeedsLogin"
        type="warning"
        message="当前活动账号凭据已失效（需重新登录）。请先在原生客户端重新登录该账号，或切换到另一账号。"
      />

      <!-- 顶部操作 -->
      <div class="ca-toolbar">
        <AppButton
          variant="primary"
          size="small"
          :disabled="busy || accounts.length >= 2"
          :title="accounts.length >= 2 ? '当前版本支持双账号，如需更换请先移除' : busyText"
          @click="beginEnroll"
        >添加另一个账号</AppButton>
        <div class="ca-rotation">
          <Switch :model-value="rotationEnabled" :disabled="busy" @update:model-value="onRotationToggle" />
          <span class="ca-rotation-label">自动轮换</span>
          <span class="ca-mono-note">自动切换引擎于后续版本生效，当前仅保存开关</span>
        </div>
      </div>

      <!-- 账号卡 -->
      <div class="ca-grid">
        <article
          v-for="acc in accounts"
          :key="acc.id"
          class="ca-card"
          :class="{ active: isActive(acc), pending: pendingFor(acc) }"
        >
          <header class="ca-card-head">
            <h4 class="ca-alias">{{ acc.alias }}</h4>
            <span v-if="isActive(acc)" class="ca-badge">当前活动</span>
            <span v-else-if="pendingFor(acc)" class="ca-badge pending">等待切换</span>
          </header>
          <div class="ca-row">
            <span class="ca-label">邮箱</span>
            <span class="ca-value mono">{{ emailDisplay(acc) }}</span>
            <button
              v-if="acc.emailMasked"
              class="ca-eye"
              type="button"
              @click="toggleEmail(acc.id)"
            >{{ revealedEmails.has(acc.id) ? '隐藏' : '显示' }}</button>
            <span v-else class="ca-value placeholder">—</span>
          </div>
          <div class="ca-row">
            <span class="ca-label">套餐</span>
            <span class="ca-value">{{ seatTierLabel(acc.seatTier) }}</span>
          </div>
          <div class="ca-row">
            <span class="ca-label">双端登录</span>
            <span class="ca-health">
              <span class="ca-health-item" :data-tone="grantHealthView(acc.health.claudeGrant).tone">CC {{ grantHealthView(acc.health.claudeGrant).label }}</span>
              <span class="ca-health-item" :data-tone="grantHealthView(acc.health.piGrant).tone">Pi {{ grantHealthView(acc.health.piGrant).label }}</span>
              <span class="ca-health-item" :data-tone="identityStatusView(acc.health.identityStatus).tone">{{ identityStatusView(acc.health.identityStatus).label }}</span>
            </span>
          </div>
          <div class="ca-row">
            <span class="ca-label">额度</span>
            <span class="ca-value placeholder">额度显示将于后续版本提供</span>
          </div>

          <!-- 等待会话退出 / 等待外部进程确认 -->
          <div v-if="pendingFor(acc) && pendingInfo" class="ca-pending">
            <strong>{{ pendingInfo.title }}</strong>
            <span>{{ pendingInfo.detail }}</span>
            <span v-if="pendingInfo.autoRetry" class="ca-mono-note">会话退出后自动完成，无需手动操作</span>
            <div class="ca-actions">
              <AppButton
                v-if="!pendingInfo.autoRetry"
                variant="ghost"
                size="small"
                :disabled="busy"
                @click="retryPending"
              >重试切换</AppButton>
              <AppButton variant="ghost" size="small" :disabled="busy" @click="store.clearPendingSwitch()">取消等待</AppButton>
            </div>
          </div>

          <footer class="ca-card-ops">
            <AppButton
              v-if="!isActive(acc)"
              variant="primary"
              size="small"
              :disabled="!actionsFor(acc).canSwitch"
              :title="actionsFor(acc).canSwitch ? '' : busyText"
              @click="switchTo(acc)"
            >切换到此账号</AppButton>
            <AppButton
              v-else
              variant="ghost"
              size="small"
              :disabled="!actionsFor(acc).canUpdateCurrent"
              :title="actionsFor(acc).canUpdateCurrent ? '核对并回收当前原生登录' : busyText"
              @click="updateOpen = true"
            >更新当前登录</AppButton>
            <AppButton variant="ghost" size="small" :disabled="busy" @click="openRename(acc)">重命名</AppButton>
            <button
              class="ca-remove"
              type="button"
              :disabled="!actionsFor(acc).canRemove"
              :title="actionsFor(acc).removeHint || '从账号池移除保存的快照'"
              @click="askRemove(acc)"
            >移除</button>
          </footer>
        </article>
      </div>

      <!-- operation 反馈 -->
      <div v-if="operationRunning" class="ca-opbanner" data-testid="ca-operation-running">{{ operationText }}</div>
      <div v-else-if="errorText" class="ca-opbanner error" data-testid="ca-operation-error">{{ errorText }}</div>
    </template>

    <!-- 重命名弹窗 -->
    <Dialog :open="renameOpen" title="重命名账号" @update:open="renameOpen = $event">
      <div class="ca-form column">
        <span class="ca-dialog-label">账号「{{ renameTarget?.alias }}」的新别名（1..64 个字符）</span>
        <TextInput v-model="renameValue" placeholder="新别名" />
        <p v-if="renameError" class="ca-error">{{ renameError }}</p>
      </div>
      <template #footer>
        <div class="ca-actions">
          <AppButton variant="ghost" @click="renameOpen = false">取消</AppButton>
          <AppButton variant="primary" :disabled="!!renameError" @click="confirmRename">保存</AppButton>
        </div>
      </template>
    </Dialog>

    <!-- 移除确认 -->
    <ConfirmDialog
      v-model:open="removeOpen"
      :danger="true"
      title="移除保存的账号"
      :message="removeMessage"
      confirm-text="移除"
      cancel-text="取消"
      @confirm="confirmRemove"
    />

    <!-- 更新当前登录确认 -->
    <ConfirmDialog
      v-model:open="updateOpen"
      title="更新当前登录"
      :message="UPDATE_CONFIRM_TEXT"
      confirm-text="核对并回收"
      cancel-text="取消"
      @confirm="updateCurrent"
    />

    <!-- 取消录入确认 -->
    <ConfirmDialog
      v-model:open="cancelAskOpen"
      title="取消录入"
      :message="CANCEL_ENROLL_TEXT"
      confirm-text="取消录入"
      cancel-text="继续录入"
      @confirm="cancelEnroll"
    />

    <!-- 自动轮换风险确认（首次开启） -->
    <Dialog :open="riskOpen" title="开启自动轮换" @update:open="riskOpen = $event">
      <div class="ca-risk">
        <p>开启前请确认以下使用约束与风险：</p>
        <ol>
          <li>切换要求所有 CodeBox 内的 Claude/Pi 会话退出，且需你确认外部进程（其他终端、编辑器等）也已退出；CodeBox 无法获得操作系统级的全局原子性。</li>
          <li>账号轮换属于非官方机制，可能违反服务条款、触发限制或要求重新授权；请自行确认届时条款。</li>
          <li>外部刷新可能使已保存的 refresh token 失效，届时需要重新登录对应账号。</li>
        </ol>
        <p class="ca-mono-note">自动切换引擎将于后续版本生效，本次开启仅保存设置。</p>
      </div>
      <template #footer>
        <div class="ca-actions">
          <AppButton variant="ghost" @click="riskOpen = false">暂不开启</AppButton>
          <AppButton variant="primary" @click="confirmEnableRotation">我已了解风险，开启</AppButton>
        </div>
      </template>
    </Dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, onMounted, ref, watch } from 'vue';
import {
  claudeAccountErrorMessage,
  type ClaudeAccount,
} from '../../api/claudeAccounts';
import { getSessions } from '../../api/session';
import { useClaudeAccountsStore, validateAlias } from '../../stores/claudeAccounts';
import { useProviderStore } from '../../stores/provider';
import { useToast } from '../../composables/useToast';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import Switch from '../ui/Switch.vue';
import TextInput from '../ui/TextInput.vue';
import {
  ENROLLMENT_STEPS,
  accountCardActions,
  accountNeedsLogin,
  busyReasonText,
  emailMaskDisplay,
  enrollmentStepIndex,
  grantHealthView,
  identityStatusView,
  inspectSummary,
  operationStatusText,
  pendingSwitchText,
  reconcileActionOptions,
  seatTierLabel,
} from './claudeAccountsView';

const store = useClaudeAccountsStore();
const providerStore = useProviderStore();
const { showSuccess, showError } = useToast();

const state = computed(() => store.sectionState);
const busy = computed(() => store.busy);
const busyText = computed(() => busyReasonText(busy.value, store.operation, '处理'));
const accounts = computed(() => store.accounts);
const rotationEnabled = computed(() => !!store.pool?.rotationEnabled);
const activeNeedsLogin = computed(() => {
  const active = store.activeAccount;
  return !!active && accountNeedsLogin(active);
});

// ---- operation 反馈（成功 toast / 失败保留文案） ----

const operationRunning = computed(() => store.operation?.state === 'running');
const operationText = computed(() => (store.operation ? operationStatusText(store.operation) : ''));
const actionError = ref('');
/** 失败文案：operation 错误优先（等待中场景除外），否则同步错误。 */
const errorText = computed(() => {
  if (store.operation?.state === 'error' && !store.pendingSwitch) {
    return operationStatusText(store.operation);
  }
  return actionError.value;
});

watch(operationRunning, (running, was) => {
  if (!running && was && store.operation) {
    if (store.operation.state === 'done') {
      showSuccess(`${store.operation.kind === 'switch' ? '切换' : '操作'}完成`);
      actionError.value = '';
    }
  }
});

function resetActionError() {
  actionError.value = '';
}

// ---- 邮箱显隐（只切换显示后端遮罩值；完整邮箱从未跨越后端边界） ----

const revealedEmails = ref(new Set<string>());

function toggleEmail(id: string) {
  const next = new Set(revealedEmails.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  revealedEmails.value = next;
}

function emailDisplay(acc: ClaudeAccount): string {
  return emailMaskDisplay(acc.emailMasked, revealedEmails.value.has(acc.id));
}

// ---- 首次录入（absent） ----

const firstAlias = ref('');
const firstAliasError = computed(() => validateAlias(firstAlias.value));

async function saveFirst() {
  const err = validateAlias(firstAlias.value);
  if (err) return;
  resetActionError();
  try {
    await store.saveCurrentLogin(firstAlias.value.trim());
    showSuccess('正在核对并保存当前双端登录…');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 录入向导（enrolling） ----

const enrollAlias = ref('');
const enrollAliasError = computed(() => validateAlias(enrollAlias.value));
const enrollStep = computed(() => store.enrollment?.step ?? 'login');
const cancelAskOpen = ref(false);

function stepClass(key: 'begin' | 'login' | 'capture'): string[] {
  const current = enrollmentStepIndex(enrollStep.value === 'login' ? 'login' : 'capture');
  const idx = enrollmentStepIndex(key);
  const beginDone = !!store.enrollment; // begin 步随 BeginEnrollment 自动完成
  if (key === 'begin') return beginDone ? ['done'] : ['todo'];
  return idx < current ? ['done'] : idx === current ? ['current'] : ['todo'];
}

async function beginEnroll() {
  resetActionError();
  try {
    await store.beginEnrollment();
    showSuccess('已保存当前账号快照，请在两端原生客户端登录新账号');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

async function captureEnrolled() {
  const err = validateAlias(enrollAlias.value);
  if (err) return;
  resetActionError();
  try {
    await store.captureEnrolled(enrollAlias.value.trim());
    showSuccess('正在核对新账号双端登录…');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

async function cancelEnroll() {
  cancelAskOpen.value = false;
  resetActionError();
  try {
    await store.cancelEnrollment();
    showSuccess('已取消录入');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 账号卡操作 ----

function isActive(acc: ClaudeAccount): boolean {
  return store.pool?.activeId === acc.id;
}

function actionsFor(acc: ClaudeAccount) {
  return accountCardActions(acc, store.pool, busy.value, state.value === 'needs-reconcile');
}

async function switchTo(acc: ClaudeAccount) {
  resetActionError();
  try {
    await store.switchTo(acc.id, acc.alias);
    showSuccess(`正在切换到「${acc.alias}」…`);
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

const updateOpen = ref(false);
const UPDATE_CONFIRM_TEXT =
  '将核对当前原生 Claude Code 与 Pi 登录，并把最新凭据回收到账号池快照。' +
  '若检测到原生登录已换成池内另一账号，将采用该账号为活动账号。操作要求相关会话已退出。';
const CANCEL_ENROLL_TEXT =
  '确定取消录入吗？已保存的当前账号快照会保留；若你在录入期间更换了原生登录，' +
  'CodeBox 会保持「需要恢复」状态，由你选择保存当前配对或恢复原账号。';

async function updateCurrent() {
  updateOpen.value = false;
  resetActionError();
  try {
    const opId = await store.updateCurrentLogin();
    if (opId) showSuccess('正在核对并回收当前登录…');
    else showSuccess('当前登录已与快照一致');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 重命名 ----

const renameOpen = ref(false);
const renameTarget = ref<ClaudeAccount | null>(null);
const renameValue = ref('');
const renameError = computed(() => (renameOpen.value ? validateAlias(renameValue.value) : null));

function openRename(acc: ClaudeAccount) {
  renameTarget.value = acc;
  renameValue.value = acc.alias;
  renameOpen.value = true;
}

async function confirmRename() {
  if (!renameTarget.value) return;
  const err = validateAlias(renameValue.value);
  if (err) return;
  resetActionError();
  try {
    await store.renameAccount(renameTarget.value.id, renameValue.value.trim());
    renameOpen.value = false;
    showSuccess('已重命名');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 移除 ----

const removeOpen = ref(false);
const removeTarget = ref<ClaudeAccount | null>(null);
const removeMessage = computed(() =>
  `确定移除保存的账号「${removeTarget.value?.alias ?? ''}」吗？` +
  '仅从 CodeBox 账号池删除其快照，不会登出原生客户端，也不影响当前正在使用的登录。');

function askRemove(acc: ClaudeAccount) {
  const actions = actionsFor(acc);
  if (!actions.canRemove) return;
  removeTarget.value = acc;
  removeOpen.value = true;
}

async function confirmRemove() {
  if (!removeTarget.value) return;
  resetActionError();
  try {
    await store.removeAccount(removeTarget.value.id);
    showSuccess('已移除保存的账号');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  } finally {
    removeTarget.value = null;
  }
}

// ---- 等待会话退出：自动完成（SESSIONS_ACTIVE 时轮询会话，退出后重试） ----

const pendingInfo = computed(() => (store.pendingSwitch ? pendingSwitchText(store.pendingSwitch.code) : null));

function pendingFor(acc: ClaudeAccount): boolean {
  return store.pendingSwitch?.targetId === acc.id;
}

let pendingPollTimer: ReturnType<typeof setTimeout> | null = null;

function stopPendingPoller() {
  if (pendingPollTimer !== null) {
    clearTimeout(pendingPollTimer);
    pendingPollTimer = null;
  }
}

async function pollSessionsForRetry(): Promise<void> {
  try {
    const list = await getSessions();
    const running = list.filter((s) => s.status === 'running' || s.status === 'stopping');
    if (running.length === 0 && store.pendingSwitch) {
      const info = pendingSwitchText(store.pendingSwitch.code);
      if (info.autoRetry) {
        await store.retryPendingSwitch();
        showSuccess('会话已全部退出，正在自动完成切换…');
        return;
      }
    }
  } catch {
    // 单次查询失败不改变状态，下轮重试。
  }
  pendingPollTimer = setTimeout(() => {
    void pollSessionsForRetry();
  }, 2000);
}

watch(
  () => store.pendingSwitch,
  (pending) => {
    stopPendingPoller();
    if (pending && pendingSwitchText(pending.code).autoRetry) {
      pendingPollTimer = setTimeout(() => {
        void pollSessionsForRetry();
      }, 1000);
    }
  },
);

async function retryPending() {
  resetActionError();
  try {
    await store.retryPendingSwitch();
    showSuccess('正在重试切换…');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 恢复（needs-reconcile） ----

const inspectLines = computed(() => inspectSummary(store.lastInspect));
const reconcileOptions = computed(() => reconcileActionOptions(store.lastInspect));

async function runInspect() {
  resetActionError();
  try {
    await store.reconcileInspect();
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

async function applyReconcile(action: 'complete-target' | 'adopt-current' | 'restore-source') {
  resetActionError();
  try {
    const opId = await store.reconcileApply(action);
    if (opId) showSuccess('恢复操作已开始…');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 自动轮换开关（riskAck 确认弹层） ----

const riskOpen = ref(false);

function onRotationToggle(next: boolean) {
  if (next) {
    riskOpen.value = true;
    return;
  }
  void disableRotation();
}

async function disableRotation() {
  resetActionError();
  try {
    await store.setRotation(false);
    showSuccess('已关闭自动轮换');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

async function confirmEnableRotation() {
  riskOpen.value = false;
  resetActionError();
  try {
    await store.setRotation(true);
    showSuccess('已开启自动轮换（自动切换引擎于后续版本生效）');
  } catch (e) {
    actionError.value = claudeAccountErrorMessage(e);
    showError(actionError.value);
  }
}

// ---- 生命周期与联动刷新 ----

async function reload() {
  await store.refresh();
}

/** 切换/恢复提交后（代际变化）刷新既有登录发现（design §7.1）。 */
let lastSeenGeneration: number | undefined;

watch(
  () => store.pool?.activeGeneration,
  (gen) => {
    if (gen === undefined) {
      lastSeenGeneration = undefined;
      return;
    }
    if (lastSeenGeneration !== undefined && gen !== lastSeenGeneration) {
      void providerStore.loadLoginProviders();
    }
    lastSeenGeneration = gen;
  },
);

/** 进入 needs-reconcile 自动做一次零写检查。 */
watch(
  state,
  (s) => {
    if (s === 'needs-reconcile') void runInspect();
  },
  { immediate: false },
);

onMounted(() => {
  store.ensureEventBridge();
  void store.refresh().then(() => {
    if (state.value === 'needs-reconcile') void runInspect();
  });
});

onUnmounted(() => {
  stopPendingPoller();
});
</script>

<style scoped>
/* 复用 ProviderGrid 的视觉 token（--card/--separator/--label/--tertiary 等）与卡片语言 */
.ca-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--separator);
}

.ca-note {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--tertiary);
}

.ca-empty {
  padding: 24px 16px;
  text-align: center;
  color: var(--tertiary);
  font-size: 13px;
}

.ca-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
}

.ca-absent-tip {
  margin: 0 0 10px;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--secondary);
}

.ca-form {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.ca-form.column {
  flex-direction: column;
  align-items: stretch;
}

.ca-alias-input {
  width: 240px;
}

.ca-error {
  margin: 6px 0 0;
  font-size: 12px;
  color: #FF3B30;
}

/* 录入向导步骤 */
.ca-steps {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.ca-steps li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--card);
}

.ca-steps li.current {
  border-color: var(--accent);
}

.ca-steps li.done {
  opacity: 0.75;
}

.ca-step-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.ca-steps li.done .ca-step-title::after {
  content: ' ✓';
  color: var(--success);
}

.ca-step-detail {
  font-size: 12px;
  line-height: 1.6;
  color: var(--tertiary);
}

/* 恢复引导 */
.ca-reconcile {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ca-inspect {
  margin: 0;
  padding: 10px 14px;
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--card);
  font-size: 12px;
  line-height: 1.7;
  color: var(--secondary);
  list-style: none;
}

.ca-reconcile-opt {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--card);
}

.ca-reconcile-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12px;
  line-height: 1.6;
}

.ca-reconcile-text strong {
  font-size: 12.5px;
  color: var(--label);
}

.ca-reconcile-text span {
  color: var(--tertiary);
}

/* 工具栏 */
.ca-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.ca-rotation {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.ca-rotation-label {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--label);
}

.ca-mono-note {
  font-size: 11px;
  color: var(--tertiary);
}

/* 账号卡网格 */
.ca-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 14px;
}

.ca-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  border: 1px solid var(--separator);
  border-radius: 12px;
  background: var(--card);
  transition: border-color 0.15s;
}

.ca-card.active {
  border-color: var(--accent);
}

.ca-card.pending {
  border-style: dashed;
}

.ca-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.ca-alias {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--label);
}

.ca-badge {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--accent);
  color: #fff;
}

.ca-badge.pending {
  background: var(--warning, #FF9500);
  color: #fff;
}

.ca-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
}

.ca-label {
  color: var(--tertiary);
  flex-shrink: 0;
  min-width: 56px;
}

.ca-value {
  color: var(--secondary);
  flex: 1;
  word-break: break-all;
}

.ca-value.mono {
  font-family: var(--mono);
  font-size: 11px;
}

.ca-value.placeholder {
  color: var(--tertiary);
}

.ca-eye {
  border: none;
  background: transparent;
  color: var(--tertiary);
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 5px;
  cursor: pointer;
  font-family: inherit;
}

.ca-eye:hover {
  color: var(--label);
  background: var(--controlHover);
}

/* 双端健康：按 tone 着色 */
.ca-health {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  flex: 1;
}

.ca-health-item {
  font-size: 11px;
}

.ca-health-item[data-tone='ok'] { color: var(--success); }
.ca-health-item[data-tone='warn'] { color: #B25000; }
.ca-health-item[data-tone='error'] { color: #FF3B30; }
.ca-health-item[data-tone='muted'] { color: var(--tertiary); }

/* 等待会话退出面板 */
.ca-pending {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-radius: 10px;
  background: rgba(255, 149, 0, 0.08);
  border: 1px solid rgba(255, 149, 0, 0.35);
  font-size: 12px;
  line-height: 1.6;
  color: var(--secondary);
}

.ca-pending strong {
  color: #B25000;
}

/* 卡片操作 */
.ca-card-ops {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  margin-top: auto;
}

.ca-remove {
  border: none;
  background: transparent;
  color: var(--tertiary);
  font-size: 11px;
  padding: 3px 8px;
  border-radius: 5px;
  cursor: pointer;
  font-family: inherit;
}

.ca-remove:hover:not(:disabled) {
  color: #FF3B30;
  background: rgba(255, 59, 48, 0.08);
}

.ca-remove:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* operation 反馈条 */
.ca-opbanner {
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 12px;
  color: var(--secondary);
  background: var(--control);
}

.ca-opbanner.error {
  color: #FF3B30;
  background: rgba(255, 59, 48, 0.08);
}

/* 风险确认弹层 */
.ca-risk {
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--secondary);
}

.ca-risk ol {
  margin: 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ca-dialog-label {
  font-size: 12px;
  color: var(--tertiary);
}
</style>
