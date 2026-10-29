<template>
  <div class="advisor-fields">
    <Segmented :model-value="mode" :options="modes" @update:model-value="changeMode" />
    <p class="advisor-note">默认异步咨询，原 Agent 继续执行；建议完成后回到原会话。首次启用后重载 Leader，新子任务按派发时配置授权。</p>
    <template v-if="mode === 'pi' || mode === 'codex-chat'">
      <template v-if="mode === 'pi'">
        <FieldLabel text="咨询模型" code="advisor.model" />
        <ModelSpecSelector :model-value="value?.model || ''" :catalog="catalog" @update:model-value="patch({ model: $event })" />
      </template>
      <template v-else>
        <FieldLabel text="咨询会话" code="advisor.codex.mode" />
        <Segmented :model-value="target.mode || (target.threadId ? 'existing' : 'managed')" :options="[{value:'managed',label:'新建并自动续接'},{value:'existing',label:'指定已有聊天'}]" @update:model-value="changeSessionMode" />
        <template v-if="(target.mode || (target.threadId ? 'existing' : 'managed')) === 'managed'">
          <p class="advisor-note">首次创建专用 Codex Work 咨询会话（不是普通 ChatGPT Chat），后续自动续接。不同调用者及授权分别保存，保留本机 Codex 登录和咨询上下文。</p>
          <FieldLabel text="Codex 模型（可选）" code="advisor.codex.model" />
          <TextInput :model-value="target.model || ''" placeholder="默认 gpt-6-astra" mono @update:model-value="patchTarget({ model: $event || undefined })" />
          <FieldLabel text="思考档位" code="advisor.codex.reasoningEffort" />
          <Dropdown :model-value="target.reasoningEffort || 'high'" :options="['low','medium','high','xhigh','max'].map(v => ({value:v,label:v}))" @update:model-value="patchTarget({ reasoningEffort: $event })" />
        </template>
        <template v-else>
        <FieldLabel text="Codex 桌面共享连接" code="advisor.codex.socketPath" />
        <TextInput :model-value="target.socketPath || ''" placeholder="桌面端同一 app-server 的本机 socket 绝对路径" mono @update:model-value="patchTarget({ socketPath: $event })" />
        <p class="advisor-note">auto 使用 Codex 默认共享连接，也可填写桌面端 socket 绝对路径。连接已有聊天；服务不可用时明确失败。</p>
        <AppButton size="small" variant="ghost" :disabled="probing || !target.socketPath" @click="loadChats">{{ probing ? '检测中…' : '检测连接并读取最近 50 个聊天' }}</AppButton>
        <StatusBanner v-if="probeError" role="alert" type="error" :message="probeError" compact />
        <p v-else-if="connected" role="status" class="advisor-note">已连接；只读检测，没有发送消息</p>
        <FieldLabel text="咨询聊天" code="advisor.codex.threadId" />
        <Dropdown v-if="chats.length" :model-value="target.threadId || ''" :options="chats" @update:model-value="patchTarget({ threadId: $event })" />
        <TextInput :model-value="target.threadId || ''" placeholder="目标聊天 ID（可粘贴或从上方选择）" mono @update:model-value="patchTarget({ threadId: $event })" />
        <p class="advisor-note">仅向已载入且空闲的目标发起咨询；忙碌时不打断。咨询使用该聊天的模型和上下文。Codex 不报告金额时显示未知；输出预算通过提示与可见用量中断约束。</p>
        </template>
      </template>
      <FieldLabel text="Leader 咨询" code="advisor.leader" />
      <Segmented :model-value="value?.leader === false ? 'off' : 'on'" :options="[{value:'on',label:'允许'},{value:'off',label:'关闭'}]" @update:model-value="patch({ leader: $event === 'on' })" />
      <FieldLabel text="可申请咨询的子角色" code="advisor.roles" />
      <StringListEditor :model-value="value?.roles || []" placeholder="如 luban" @update:model-value="patch({ roles: $event })" />
      <p class="advisor-note">角色配置仅给出上限；每次派发仍须显式 scope.advisor=consult。已有任务不自动扩权。</p>
      <div class="advisor-budgets">
        <label>会话尝试次数<TextInput type="number" :model-value="String(value?.maxCalls ?? 5)" @update:model-value="patch({ maxCalls: Number($event) })" /></label>
        <label>单次输出 tokens<TextInput type="number" :model-value="String(value?.maxTokens ?? 4096)" @update:model-value="patch({ maxTokens: Number($event) })" /></label>
        <label>单次时限（毫秒）<TextInput type="number" :model-value="String(value?.timeoutMs ?? 120000)" @update:model-value="patch({ timeoutMs: Number($event) })" /></label>
      </div>
    </template>
    <StatusBanner v-if="error" role="alert" type="error" :message="error" compact />
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue';
import Segmented from '../ui/Segmented.vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import Dropdown from '../ui/Dropdown.vue';
import FieldLabel from '../ui/FieldLabel.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import ModelSpecSelector from './ModelSpecSelector.vue';
import StringListEditor from './StringListEditor.vue';
import { type ModelCatalog } from './useModelCatalog';
import { advisorMode, validateAdvisorConfig } from './piAdvisor';
import { listCodexAdvisorChats } from '../../api/piConfig';
const props = defineProps<{ value: any; catalog: ModelCatalog }>();
const emit = defineEmits<{ change: [value: any] }>();
const mode = computed(() => advisorMode(props.value));
const target = computed(() => props.value?.codex || {});
const error = computed(() => validateAdvisorConfig({ advisor: props.value }));
const modes = [{value:'unset',label:'未配置'},{value:'off',label:'关闭'},{value:'pi',label:'pi 内模型'},{value:'codex-chat',label:'Codex Work 咨询'}];
function patch(value: Record<string, unknown>) { emit('change', { ...props.value, ...value }); }
function patchTarget(value: Record<string, unknown>) { patch({ codex: { ...target.value, ...value } }); connected.value = false; probeError.value = ''; }
function changeSessionMode(mode: string) { patch({ codex: mode === 'managed' ? { mode: 'managed', model: 'gpt-6-astra', reasoningEffort: 'high' } : { mode: 'existing', socketPath: 'auto', threadId: '' } }); chats.value = []; connected.value = false; probeError.value = ''; }
function changeMode(value: string) {
  if (value === 'unset') { emit('change', undefined); return; }
  const next = { ...props.value, backend: value === 'codex-chat' ? 'codex-chat' : 'pi' };
  if (value === 'codex-chat') next.codex = Object.keys(target.value).length ? { ...target.value } : { mode: 'managed', model: 'gpt-6-astra', reasoningEffort: 'high' };
  else { delete next.codex; next.model = value === 'off' ? null : typeof next.model === 'string' ? next.model : ''; }
  emit('change', next);
}
const probing = ref(false), connected = ref(false), probeError = ref('');
const chats = ref<Array<{value:string;label:string}>>([]);
async function loadChats() {
  probing.value = true; probeError.value = ''; connected.value = false;
  const socket = target.value.socketPath;
  try { const result = await listCodexAdvisorChats(socket); if (target.value.socketPath !== socket) return; chats.value = result.data.map(t => ({value:t.id,label:`${t.name || t.id} · ${t.status?.type || '未知'}`})); connected.value = true; }
  catch (e) { probeError.value = String(e); }
  finally { probing.value = false; }
}
</script>
<style scoped>
.advisor-fields{display:flex;flex-direction:column;gap:12px}.advisor-note{font-size:12px;color:var(--text-secondary);line-height:1.6;margin:0}.advisor-budgets{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px}.advisor-budgets label{font-size:12px;display:flex;flex-direction:column;gap:6px}
</style>
