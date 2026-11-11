<template>
  <ConfigCard>
    <div class="heading"><h2>分流连接日志</h2><AppButton :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '刷新' }}</AppButton></div>
    <p class="hint">按提供商 → 域名 → 实际出口归拢；对照连接发生时已生效的配置。持续保存经过 CodeBox 的连接记录，不采样。此处统计连接次数，不是字节数或请求数。</p>
    <p class="hint">无法确认绕过本代理的流量、上游之后的公网出口；未识别域名或出口显示为“无法确认”。出口记录表示内核选择了该节点，不代表连接成功。</p>
    <form class="filters" @submit.prevent="applyFilters">
      <label>搜索<input v-model="draft.search" placeholder="提供商、域名、实际出口" /></label>
      <label>判断<select v-model="draft.verdict"><option value="">全部</option><option value="mismatch">出口不符</option><option value="unknown">无法确认</option><option value="match">出口符合</option></select></label>
      <label>提供商<input v-model="draft.provider" placeholder="完整名称，可点击汇总填入" /></label>
      <label>实际出口<input v-model="draft.exit" placeholder="完整名称，可点击汇总填入" /></label>
      <label>开始时间（本地）<input v-model="draft.since" type="datetime-local" /></label>
      <label>结束时间（本地）<input v-model="draft.until" type="datetime-local" /></label>
      <AppButton type="submit" :disabled="loading">筛选</AppButton><AppButton type="button" @click="reset">重置</AppButton>
    </form>
    <p v-if="error || page?.error" class="problem" role="alert">{{ error || page?.error }} · 记录可能不完整，请勿据此认定全部流量正常。</p>
    <div class="heading"><strong>筛选结果：{{ page?.total || 0 }} 条连接</strong><label class="toggle"><input v-model="live" type="checkbox" /> 自动刷新</label></div>
    <p v-if="page && !page.total" class="hint">没有符合条件的连接记录。新日志从本版本内核启动后开始记录，旧运行日志不会回填。</p>
    <div v-if="page?.groups.length" class="table-wrap">
      <table><caption>域名出口汇总（最多显示 500 组，异常优先；可继续筛选缩小范围）</caption><thead><tr><th>提供商 / 域名</th><th>指定出口</th><th>实际出口</th><th>判断</th><th>连接数</th></tr></thead>
        <tbody><tr v-for="(g, i) in page.groups" :key="i" :class="g.verdict"><td><button class="link" @click="selectGroup(g.provider, '')">{{ g.provider }}</button><br /><code>{{ g.domain }}</code></td><td>{{ g.expected || '未能确定' }}</td><td><button class="link" @click="selectGroup(g.provider, g.actual)">{{ g.actual || '尚无出口证据' }}</button></td><td>{{ verdictText(g.verdict) }}</td><td>{{ g.count }}</td></tr></tbody>
      </table>
    </div>
    <details v-if="page?.total" open><summary>逐条连接明细</summary><div class="table-wrap"><table><thead><tr><th>时间 / 配置版本</th><th>提供商 / 目标</th><th>入口 / 协议</th><th>指定 → 实际出口</th><th>判断</th></tr></thead><tbody><tr v-for="r in page.records" :key="r.id" :class="r.verdict"><td>{{ new Date(r.time).toLocaleString() }}<br /><small>v{{ r.revision }}</small></td><td>{{ r.provider }}<br /><code>{{ r.destination }}</code></td><td>{{ r.inbound || '未知入口' }}<br />{{ r.network }}</td><td>{{ r.expected || '未能确定' }}<br />→ {{ r.actual || '尚无出口证据' }}</td><td>{{ verdictText(r.verdict) }}</td></tr></tbody></table></div>
      <div class="pagination"><AppButton :disabled="loading || offset === 0" @click="changePage(-100)">上一页</AppButton><span>{{ offset + 1 }}–{{ Math.min(offset + 100, page.total) }} / {{ page.total }}</span><AppButton :disabled="loading || offset + 100 >= page.total" @click="changePage(100)">下一页</AppButton></div>
    </details>
  </ConfigCard>
</template>
<script setup lang="ts">
import { onMounted, onActivated, onDeactivated, onUnmounted, ref } from 'vue'
import { router } from '../../wailsjs/go/models'
import { GetAIProxyTraffic } from '../../wailsjs/go/main/App'
import ConfigCard from './ui/ConfigCard.vue'
import AppButton from './ui/AppButton.vue'
const empty = () => ({search:'',verdict:'',provider:'',exit:'',since:'',until:''})
const draft=ref(empty()), applied=ref(empty()), offset=ref(0), page=ref<router.TrafficPage>(), loading=ref(false), error=ref(''),live=ref(true)
const verdictText=(value:string)=>({match:'出口符合',mismatch:'⚠ 出口不符',unknown:'无法确认'}[value] || '无法确认')
let refreshPending=false
async function refresh(){
 if(loading.value){refreshPending=true;return}
 loading.value=true;error.value=''
 try {const q=applied.value;page.value=await GetAIProxyTraffic(new router.TrafficQuery({...q,offset:offset.value,since:q.since?new Date(q.since).toISOString():'',until:q.until?new Date(q.until).toISOString():''}))}catch(e){error.value=String(e)}finally{loading.value=false;if(refreshPending){refreshPending=false;void refresh()}}
}
function applyFilters(){applied.value={...draft.value};offset.value=0;void refresh()}
function reset(){draft.value=empty();applyFilters()}
function selectGroup(provider:string,exit:string){draft.value.provider=provider;draft.value.exit=exit;applyFilters()}
function changePage(delta:number){offset.value+=delta;void refresh()}
let timer:ReturnType<typeof setInterval>|undefined
function start(){if(timer)return;void refresh();timer=setInterval(()=>{if(live.value && offset.value===0)void refresh()},3000)}
function stop(){clearInterval(timer);timer=undefined}
onMounted(start);onActivated(start);onDeactivated(stop);onUnmounted(stop)
</script>
<style scoped>
h2{font-size:15px;margin:0}.heading,.pagination{display:flex;align-items:center;justify-content:space-between;gap:12px}.hint,caption{font-size:12px;color:var(--secondary);line-height:1.7}.filters{display:flex;flex-wrap:wrap;gap:12px;align-items:end;margin:18px 0}label{font-size:12px;color:var(--secondary)}input,select{display:block;background:var(--control);color:var(--label);border:1px solid var(--separator);border-radius:6px;padding:8px;margin-top:5px;max-width:240px}.toggle{display:flex;align-items:center;gap:8px}.toggle input{margin:0}.table-wrap{overflow:auto;margin:12px 0}table{width:100%;border-collapse:collapse;font-size:12px;text-align:left}caption{text-align:left;padding-bottom:8px}th,td{padding:10px;border-bottom:1px solid var(--separator);vertical-align:top;overflow-wrap:anywhere;min-width:100px}th{color:var(--secondary);font-weight:500}tr.mismatch{background:color-mix(in srgb,var(--danger-strong) 10%,transparent)}tr.mismatch td:last-child,.problem{color:var(--danger-strong);font-weight:600}tr.unknown td:last-child{color:var(--warning-strong)}code,small{color:var(--secondary)}.link{border:0;background:none;color:var(--accent);padding:0;cursor:pointer;font:inherit;text-align:left}.link:hover{text-decoration:underline}summary{cursor:pointer;margin-top:18px}.pagination{justify-content:flex-end;font-size:12px}
</style>
