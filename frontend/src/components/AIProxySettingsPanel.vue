<template>
  <div class="settings-stack">
    <section class="settings-section">
      <div class="section-title">
        <div><h2>节点来源</h2><p class="hint">{{ sourceText }}</p></div>
        <AppButton :disabled="busy" @click="$emit('sync')"><AIProxyIcon name="refresh" />同步 v2rayN</AppButton>
      </div>
      <div v-if="!state.groups.length" class="empty-inline">暂无可用节点，先同步 v2rayN 或手动导入订阅。</div>
      <div v-for="group in state.groups" :key="group.id" class="group-row">
        <span class="group-icon"><AIProxyIcon name="globe" /></span>
        <div class="group-copy"><strong>{{ group.name }}</strong><p class="hint">{{ group.source === 'v2rayn' ? 'v2rayN' : '手动导入' }}<span class="dot-separator">·</span>{{ group.nodes.length }} 个节点</p><details v-if="group.warnings?.length"><summary class="warning-text">{{ group.warnings.length }} 条兼容性提示</summary><p v-for="warning in group.warnings" :key="warning" class="hint">{{ warning }}</p></details></div>
        <div class="actions"><AppButton v-if="group.refreshable" size="small" :disabled="busy" @click="$emit('refresh', group.id)">更新</AppButton><button class="text-button" :disabled="busy" :aria-label="`移除节点分组 ${group.name}`" @click="$emit('remove', group.id)">移除</button></div>
      </div>
      <details class="import disclosure">
        <summary><AIProxyIcon name="plus" />手动导入订阅或节点<AIProxyIcon name="chevron" /></summary>
        <form class="import-form" @submit.prevent="$emit('import')">
          <div class="settings-fields"><label class="field">名称<input v-model="importDraft.name" class="input" required placeholder="订阅名称" :disabled="busy" /></label><label class="field">订阅地址<input v-model="importDraft.url" class="input" type="url" autocomplete="off" placeholder="https://…" :disabled="busy" /></label></div>
          <label class="field">或粘贴节点内容<textarea v-model="importDraft.content" rows="3" autocomplete="off" placeholder="节点链接、Base64、Clash YAML 或 sing-box JSON" :disabled="busy" /></label>
          <AppButton type="submit" variant="primary" :disabled="busy || !importDraft.name.trim()">导入节点</AppButton>
        </form>
      </details>
    </section>
    <section class="settings-section">
      <div class="section-title"><div><h2>网络与出口绑定</h2><p class="hint">指定本功能的物理出口，减少 TUN 路由再次接管。</p></div></div>
      <div class="settings-fields">
        <label class="field">出口方式<select v-model="draft.routing.egress_mode" class="input" aria-label="出口方式" :disabled="busy"><option value="auto">自动绑定默认网卡（推荐）</option><option value="interface">指定物理网卡</option><option value="system">跟随系统路由</option></select></label>
        <label v-if="draft.routing.egress_mode === 'interface'" class="field">物理出口网卡<select v-model="draft.routing.bind_interface" class="input" aria-label="物理出口网卡" :disabled="busy"><option value="">请选择已连接的网卡</option><option v-for="nic in state.interfaces.filter(n => n.selectable)" :key="nic.name" :value="nic.name">{{ nic.name }}</option><option v-if="draft.routing.bind_interface && !state.interfaces.some(n => n.name === draft.routing.bind_interface && n.selectable)" :value="draft.routing.bind_interface">{{ draft.routing.bind_interface }}（不可用）</option></select></label>
        <label class="field">独立 DNS <span class="field-note">可选</span><input v-model.trim="draft.routing.dns_server" class="input" :disabled="busy" placeholder="服务器 IP，留空使用系统解析" /></label>
      </div>
      <p v-if="draft.routing.egress_mode === 'system'" class="hint warning-text">跟随系统路由时，v2rayN TUN 可能再次接管出口。</p>
      <details class="disclosure"><summary><AIProxyIcon name="info" />与 TUN 共存的说明<AIProxyIcon name="chevron" /></summary><div class="help-body"><p>自动模式异常时，可指定已连接的物理网卡。本功能不会修改 v2rayN 设置。</p><p>系统 DNS 仍可能经过 TUN；启用 FakeIP 时，可指定独立 DNS。若节点本身是 localhost / 127.0.0.1 代理，后续路径仍由该代理决定。本地 API 与中继始终回环直连。</p></div></details>
    </section>
    <section class="settings-section">
      <details class="disclosure"><summary><span><strong>高级设置</strong><span class="summary-description">入口端口、默认上游与内核路径</span></span><AIProxyIcon name="chevron" /></summary>
        <div class="settings-fields advanced-fields">
          <label class="field">统一入口端口<input v-model.number="draft.routing.proxy_port" class="input" type="number" min="1024" max="65535" :disabled="busy" /></label>
          <label class="field">其他域名上游<input v-model="draft.routing.fallback_proxy" class="input" autocomplete="off" placeholder="留空，使用直连" :disabled="busy" /></label>
          <label class="field">订阅下载代理<input v-model="draft.routing.fetch_proxy" class="input" autocomplete="off" placeholder="留空，使用直连" :disabled="busy" /></label>
          <label class="field">sing-box 路径<input v-model="draft.corePath" class="input" :placeholder="state.status.core || '留空，自动查找'" :disabled="busy" /></label>
          <label class="field field-wide">v2rayN 数据库<input v-model="draft.v2raynDatabase" class="input" :placeholder="state.source.database || '留空，自动查找 guiNDB.db'" :disabled="busy" /></label>
        </div>
        <p class="hint">统一入口：127.0.0.1:{{ draft.routing.proxy_port }}。所有入口仅监听本机；停止分流后，新会话恢复原有代理策略。</p>
      </details>
    </section>
    <section class="settings-section"><details class="disclosure"><summary><span><strong>内核运行日志</strong><span class="summary-description">启动状态与诊断信息</span></span><AIProxyIcon name="chevron" /></summary><pre>{{ state.status.error || state.status.logs || '暂无运行日志' }}</pre></details></section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { AIProxySettings, AIProxyState } from '../api/aiProxy'
import AIProxyIcon from './AIProxyIcon.vue'
import AppButton from './ui/AppButton.vue'

const props = defineProps<{ state: AIProxyState; form: AIProxySettings; busy: boolean; importForm: { name: string; url: string; content: string } }>()
defineEmits<{ sync: []; refresh: [id: string]; remove: [id: string]; import: [] }>()
// These are the parent's editable drafts, not the saved state. Replacements after save stay reactive.
const draft = computed(() => props.form)
const importDraft = computed(() => props.importForm)
const sourceText = computed(() => props.state.source.error || (props.state.source.available ? `已同步 ${props.state.source.groups} 个分组、${props.state.source.nodes} 个节点；保留 v2rayN 原始数据。` : '从本机 v2rayN 同步，或导入自己的订阅。'))
</script>

<style scoped src="../styles/ai-proxy-controls.css"></style>
<style scoped>
.settings-stack { display: flex; flex-direction: column; gap: 20px; }
.settings-section { padding: 24px; border: 1px solid var(--proxy-border); border-radius: 14px; background: var(--card); }
.settings-section .section-title { margin-bottom: 20px; }
.settings-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px 24px; }
.settings-fields + .hint { margin-top: 14px; }
.group-row { display: flex; align-items: center; gap: 14px; padding: 17px 0; border-top: 1px solid var(--proxy-border); }
.group-icon { display: grid; place-items: center; width: 38px; height: 38px; border-radius: 10px; background: var(--control); color: var(--secondary); flex-shrink: 0; }
.group-copy { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.group-copy .hint { margin-top: 3px; }
.dot-separator { margin: 0 8px; }
.empty-inline { padding: 12px 0 24px; color: var(--secondary); font-size: 13px; }
.import { padding-top: 16px; border-top: 1px solid var(--proxy-border); }
.import-form { display: flex; flex-direction: column; gap: 18px; margin-top: 20px; }
.import-form > .btn { align-self: flex-start; }
.settings-section > .disclosure:not(.import) { margin-top: 20px; }
.settings-section > .disclosure:first-child { margin-top: 0; }
.advanced-fields { margin: 24px 0 16px; }
.summary-description { display: block; margin-top: 5px; color: var(--secondary); font-size: 12px; font-weight: 400; }
pre { margin-top: 20px; padding: 16px; max-height: 280px; overflow: auto; background: var(--control); border-radius: 8px; color: var(--secondary); font: 12px/1.7 var(--mono); white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 760px) { .settings-fields { grid-template-columns: minmax(0, 1fr); } .settings-section { padding: 18px; } }
</style>
