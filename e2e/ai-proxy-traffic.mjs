// Run with the existing ai-proxy Vite fixture on :5188.
import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';
const browser=await chromium.launch({headless:true,channel:process.env.PLAYWRIGHT_CHANNEL || "chrome"});
try {
 const page=await browser.newPage({viewport:{width:1440,height:1000}});
 page.setDefaultTimeout(10000);
 const errors=[];page.on('pageerror',e=>errors.push(String(e)));
 const records=Array.from({length:105},(_,i)=>({id:String(i),time:new Date().toISOString(),revision:7,provider:'OpenAI 团队',domain:'api.openai.com',destination:'api.openai.com:443',network:'TCP',inbound:'auto-in',expected:'东京出口 [tokyo]',actual:i===0?'香港出口 [hk]':'东京出口 [tokyo]',verdict:i===0?'mismatch':'match'}));
 await page.route('http://127.0.0.1:59999/rpc',async route=>{
 if(route.request().method()==='OPTIONS'){await route.fulfill({status:204,headers:{'Access-Control-Allow-Origin':'*','Access-Control-Allow-Headers':'Content-Type','Access-Control-Allow-Methods':'POST'}});return}
 const {method,args}=route.request().postDataJSON();let result;
 if(method==='GetAIProxyState')result={providers:[],interfaces:[],issues:[],enabled:true,pending:false,settings:{routing:{proxy_port:18890,rules:[],fallback_proxy:'',fetch_proxy:'',egress_mode:'auto',bind_interface:'',dns_server:''},corePath:'',v2raynDatabase:''},groups:[],status:{running:true,pid:1,core:'sing-box',error:'',revision:7,logs:''},source:{available:false,database:'',groups:0,nodes:0,skipped:0,error:''},error:''};
 else if(method==='GetAIProxyTraffic'){
 const q=args[0];const filtered=records.filter(r=>(!q.verdict||r.verdict===q.verdict)&&(!q.provider||r.provider===q.provider)&&(!q.exit||r.actual===q.exit)&&(!q.search||`${r.provider} ${r.domain} ${r.actual}`.includes(q.search)));
 const groups=[];for(const r of filtered){let g=groups.find(g=>g.actual===r.actual);if(!g){g={...r,count:0};groups.push(g)}g.count++}
 result={records:filtered.slice(q.offset,q.offset+100),groups,total:filtered.length,error:''};
 }else throw Error(method);
 await route.fulfill({json:result,headers:{'Access-Control-Allow-Origin':'*'}});
 });
 await page.goto('http://127.0.0.1:5188/e2e/ai-proxy.html?bridge=http://127.0.0.1:59999');
 await page.getByText('筛选结果：105 条连接').waitFor();
 await page.getByRole('heading',{name:'分流连接日志'}).scrollIntoViewIfNeeded();
 assert.equal(await page.locator('tr.mismatch').count(),2);
 await page.getByRole('button',{name:'下一页'}).click();
 await page.getByText('101–105 / 105').waitFor();
 await page.locator('.filters select').selectOption('mismatch');
 await page.getByRole('button',{name:'筛选',exact:true}).click();
 await page.getByText('筛选结果：1 条连接').waitFor();
 assert.equal(await page.getByRole('button',{name:'下一页'}).isDisabled(),true);
 await page.getByText('逐条连接明细').scrollIntoViewIfNeeded();
 await page.screenshot({path:'/tmp/amagi-traffic-desktop.png'});
 await page.setViewportSize({width:720,height:900});
 await page.getByRole('heading',{name:'分流连接日志'}).scrollIntoViewIfNeeded();
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth));
 assert.deepEqual(errors,[]);
 console.log('traffic UI: grouping, highlighting, paging, filtering, narrow layout passed');
}finally{await browser.close()}
