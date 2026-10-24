import {TrimApp} from './fnos-sdk.js';
const $ = id => document.getElementById(id);
const app = new TrimApp();
const base = new URL('.', import.meta.url).pathname;
const sources=new Map();
let target='', pendingPicker=null,refreshBusy=false, lastStatus=null,toastTimer;
function node(tag,cls,text){const el=document.createElement(tag);if(cls)el.className=cls;if(text!==undefined)el.textContent=text;return el;}
function toast(msg){const el=$('toast');el.textContent=msg;el.classList.remove('hidden');clearTimeout(toastTimer);toastTimer=setTimeout(()=>el.classList.add('hidden'),4200);}
async function api(path,opts={}){
 const resp=await fetch(base+path,{cache:'no-store',...opts});
 const body=await resp.text();
 let result;
 try{result=JSON.parse(body);}catch{
   const error=(resp.status===404?'请求地址不存在':resp.status>=500?'飞牛网关或服务暂时不可用':'服务返回了非 JSON 响应');
   throw Error(error+'（HTTP '+resp.status+'）');
 }
 if(!resp.ok)throw Error(result.error||('操作失败（HTTP '+resp.status+'）'));
 return result;
}
async function action(route,payload){return api(route,{method:'POST',headers:{'Content-Type':'application/json','X-Fnos-Quickcopy-UI':'1'},body:JSON.stringify(payload)});}
const clean = x => typeof x==='string'?('/'+x.trim().replace(/^\/+/,'' )).replace(/\/+$/,''):'';
function short(s){const p=s.split('/');return p[p.length-1]||s;}
function human(n){if(!Number.isFinite(n)||n<0)return '—';for(const u of ['B','KB','MB','GB','TB','PB']){if(n<1024||u==='PB')return (u==='B'?n.toFixed(0):n.toFixed(1))+' '+u;n/=1024;}}
function duration(sec){if(!Number.isFinite(sec)||sec<0)return '计算中';const s=Math.floor(sec%60).toString().padStart(2,'0'),m=Math.floor(sec/60%60).toString().padStart(2,'0'),h=Math.floor(sec/3600);return h+'小时 '+m+'分 '+s+'秒';}
function addSources(values,type='auto'){let count=0;for(const original of values){const p=clean(original);if(!p||p==='/'||sources.has(p))continue;sources.set(p,{path:p,type});count++;}renderSources();if(count===0)toast('没有新增的来源');}
function renderSources(){const wrap=$('sources');wrap.replaceChildren();$('source-count').textContent=sources.size?sources.size+' 项':'未选择';wrap.classList.toggle('hidden',!sources.size);if(!sources.size)return;
 for(const [path,item] of sources){
  const line=node('div','source-row');
  line.title=path;
  const type=node('span','type','▣');
  const name=node('span','source-name',short(path));
  const detail=node('small','','');
  const del=node('button','remove','×');del.type='button';del.title='移除';del.addEventListener('click',()=>{sources.delete(path);renderSources();});
  line.append(type,name,detail,del);wrap.append(line);
 }
}
function updateTarget(v){const p=clean(v);if(!p||p==='/')return;target=p;$('target-text').textContent=p;$('target-text').classList.add('selected');$('target-text').title=p;}
function consume(result,side,type){const values=Array.isArray(result)?result:result?.code===0&&Array.isArray(result.data)?result.data:result?.path;if(!values?.length){if(result?.code!==undefined&&result.code!==0)toast(result.msg||'选择失败');return;}if(side==='target')updateTarget(values[0]);else addSources(values,type);}
function onAuth(result){if(!pendingPicker||result?.state!==pendingPicker.state||result?.method!=='pickFile')return;const pending=pendingPicker;pendingPicker=null;if(result.status==='success')consume(result.path,pending.side,pending.type);else if(result.status==='error')toast('文件选择失败');}
window.addEventListener('message',ev=>{if(ev.origin===location.origin&&ev.data?.type==='fnos-mount-manager:copy-picker')onAuth(ev.data.result);});
window.addEventListener('storage',ev=>{if(ev.key==='fnos-mount-manager:copy-picker-result'&&ev.newValue){try{onAuth(JSON.parse(ev.newValue));}catch{}}});
async function pick(side){
 const button=$(side==='target'?'pick-target':'pick-source');
 button.disabled=true;
 const isTarget=side==='target';
 const sidebarGroup=['myFiles','otherShare','remote','favorites'];
 const options={sidebarGroup,title:isTarget?'选择目标目录':'选择文件或文件夹',
   okText:'选择',...(isTarget?{directory:true,creatable:true}:{multiple:true,multiSelect:true})};
 try{
  await app.ready();
  if(app.isStandaloneWeb){
   const state=crypto.randomUUID();pendingPicker={state,side,type:'auto'};
   await app.openAppAuth('pickFile',{
      appName:'fnos-mount-manager',...(isTarget?{directory:true}:{}),
      sidebarGroup,redirectUri:'/app/fnos-mount-manager/copy-auth-callback.html',state
   },{target:'_blank',features:'width=880,height=710'});
   return;
  }
  consume(await app.pickFile(options),side,'auto');
 }catch(e){toast('选择失败：'+(e.message||e));}
 finally{button.disabled=false;}
}
$('pick-source').addEventListener('click',()=>pick('source'));
$('pick-target').addEventListener('click',()=>pick('target'));
const parallelInput=$('parallel');
$('create').addEventListener('click',async()=>{
 if(sources.size===0){toast('请先选择文件或文件夹');return;}
 if(!target){toast('请选择目标文件夹');return;}
 const parallel=Number.parseInt(parallelInput.value,10);
 if(!Number.isInteger(parallel)||parallel<1||parallel>256){toast('并行数需在 1～256 之间');return;}
 const verify=document.querySelector('input[name="verify"]:checked').value;
 const btn=$('create');btn.disabled=true;
 try{
  await action('api/copy/create',{sources:[...sources.keys()],target,parallel,verify});
  sources.clear();renderSources();
  toast('已提交');
  await refresh();
 }catch(e){toast(e.message);}
 finally{btn.disabled=false;}
});
const states={queued:'排队中',scanning:'正在扫描',running:'复制中',verifying:'内容校验中',paused:'已暂停',error:'出错',completed:'已完成'};
function taskButton(label,style,fn){const b=node('button','action'+(style?' '+style:''),label);b.type='button';b.addEventListener('click',fn);return b;}
async function changeTask(route,id){try{await action('api/copy/'+route,{id});await refresh();}catch(e){toast(e.message);}}
function formatSubmitted(value){
 const d=new Date(value);if(!Number.isFinite(d.getTime()))return '—';
 const two=n=>String(n).padStart(2,'0');
 const prefix=d.getFullYear()===new Date().getFullYear()?'':d.getFullYear()+'-';
 return prefix+two(d.getMonth()+1)+'-'+two(d.getDate())+' '+two(d.getHours())+':'+two(d.getMinutes());
}
function formatElapsed(start,end){
 const a=new Date(start).getTime(),b=new Date(end).getTime();
 if(!Number.isFinite(a)||!Number.isFinite(b)||b<a)return '—';
 const seconds=Math.floor((b-a)/1000),h=Math.floor(seconds/3600),m=Math.floor(seconds/60)%60,s=seconds%60;
 return (h?h+'时':'')+(m?m+'分':'')+s+'秒';
}
const expandedJobs=new Set();
const expandedTransfers=new Set();
const detailCache=new Map();
// Preserve the horizontal offset of the *whole* active-filename list across
// periodic status refreshes (which reconstruct the task card).
const liveScrollPositions=new Map();
let liveTouching=false,liveMouseDragging=false;
function bindLiveHorizontalScroll(el,id){
 el.dataset.liveScrollId=id;
 el.addEventListener('scroll',()=>liveScrollPositions.set(id,el.scrollLeft),{passive:true});
 el.addEventListener('touchstart',()=>{liveTouching=true;},{passive:true});
 el.addEventListener('touchend',()=>{liveTouching=false;},{passive:true});
 el.addEventListener('touchcancel',()=>{liveTouching=false;},{passive:true});
 let startX=0,startLeft=0,dragging=false;
 const stopDrag=()=>{dragging=false;liveMouseDragging=false;};
 el.addEventListener('pointerdown',e=>{
  if(e.pointerType!=='mouse'||e.button!==0)return;
  dragging=true;liveMouseDragging=true;startX=e.clientX;startLeft=el.scrollLeft;
  el.setPointerCapture(e.pointerId);
 });
 el.addEventListener('pointermove',e=>{
  if(!dragging)return;
  el.scrollLeft=startLeft-(e.clientX-startX);
 });
 el.addEventListener('pointerup',stopDrag);
 el.addEventListener('pointercancel',stopDrag);
 el.addEventListener('lostpointercapture',stopDrag);
}

function transferPaths(j) {
 const block=node('div','copy-detail-body');
 const paths=(j.items?.length?j.items:(j.sources||[]).map(p=>({source:p,dest:(j.target.endsWith('/')?j.target.slice(0,-1):j.target)+'/'+short(p)})));
 for(const it of paths){
  const el=node('div','copy-detail-item');
  el.append(node('small','','来源'),node('span','',it.source),node('small','','目标'),node('span','',it.dest));
  block.append(el);
 }
 const meta=node('div','copy-total','并行 '+j.transfers+' · '+(j.verify==='content'?'严格校验':'快速校验'));
 block.append(meta);return block;
}
async function openDetails(id,details,j){
 const holder=details.querySelector('.copy-detail-body');
 if(details.open && holder && !details.dataset.loaded){
  details.dataset.loaded='loading';
  try{
   const full=detailCache.has(id)?detailCache.get(id):await api('api/copy/detail?id='+encodeURIComponent(id));
   detailCache.set(id,full);
   if(!details.isConnected)return;
   holder.replaceWith(transferPaths(full));
   details.dataset.loaded='1';
  }catch(e){details.dataset.loaded='';console.warn(e);}
 }
}
function wireExpanded(details,j){
 if(expandedJobs.has(j.id))details.open=true;
 details.addEventListener('click',e=>{
  if(e.target.closest('summary')){
   e.preventDefault();
   details.open=!details.open;
   if(details.open)expandedJobs.add(j.id);else expandedJobs.delete(j.id);
   if(details.open)void openDetails(j.id,details,j);
  }
 });
 if(details.open)void openDetails(j.id,details,j);
}
function makeCompletedJob(j){
 const card=node('details','job completed');
 const line=node('summary','completed-line');
 const time=node('time','completed-time',formatSubmitted(j.created));time.dateTime=j.created;
 const size=node('span','completed-size',human(Number(j.totalBytes)||Number(j.doneBytes)||0));
 const state=node('span','completed-state','已完成');
 const elapsed=node('span','completed-elapsed',formatElapsed(j.created,j.updated));
 line.append(time,size,state,elapsed);
 card.append(line,transferPaths(j));wireExpanded(card,j);return card;
}
function makeJob(j){
 if(j.state==='completed')return makeCompletedJob(j);
 const card=node('article','job');
 const title=node('div','job-title');
 const name=j.sources?.length===1?short(j.sources[0]):(j.sources?.length||0)+' 个来源';
 const tag=node('span','tag '+j.state,states[j.state]||j.state);
 title.append(node('strong','',name),tag);
 const path=node('div','paths',(j.sources?.length||0)+' 个来源 → '+j.target);
 path.title=(j.sources||[]).join('\n')+'\n目标：'+j.target;
 const current=Math.max(0,Number(j.doneBytes||0)+Number(j.currentBytes||0));
 const total=Number(j.totalBytes)||0;
 let percent=j.state==='completed'?100:total>0?Math.min(99.9,current/total*100):0;
 if(j.state==='verifying')percent=99.9;
 const stats=node('div','stat-line');
 stats.append(node('span','percent',percent.toFixed(1)+'%'),node('span','meter-count',human(current)+' / '+(total?human(total):j.state==='scanning'?'扫描中':human(0))));
 const track=node('div','track');const bar=node('div','bar');bar.style.width=percent+'%';track.append(bar);
 const metrics=node('div','metrics');
 const values=[['实时速度',j.state==='running'?human(j.speed||0)+'/s':'—'],['预计剩余',j.state==='running'?duration(j.eta):j.state==='completed'?'00小时 00分 00秒':'—'],['文件数',Math.min(Number(j.totalFiles)||Infinity,(Number(j.doneFiles)||0)+(Number(j.currentDoneFiles)||0))+' / '+(j.totalFiles||'—')]];
 for(const [label,value] of values){const col=node('div','metric');col.append(node('small','',label),node('strong','',value));metrics.append(col);}
 const foot=node('div','job-bottom');const filename=node('div','filename',j.currentFile||('并行 '+j.transfers+' · '+(j.verify==='content'?'严格':'快速')));filename.title=j.currentFile||'';
 foot.append(filename);
 if(['queued','running','scanning','verifying'].includes(j.state))foot.append(taskButton('暂停','',()=>changeTask('pause',j.id)));
 // Use explicit actions, never rely on button labels to infer state.
 if(['paused','error'].includes(j.state)){foot.append(taskButton('继续','',()=>changeTask('resume',j.id)));}
 let live=null;
 if(j.state==='running'){
  live=node('details','live-transfers');
  const names=Array.isArray(j.currentFiles)&&j.currentFiles.length?j.currentFiles:(j.currentFile?[j.currentFile]:[]);
  const active=Number(j.activeTransfers)||0;
  const summary=node('summary','live-summary','正在传输 '+active+' / '+j.transfers+' 路');
  live.append(summary);
  const body=node('div','live-transfer-list');
  bindLiveHorizontalScroll(body,j.id);
  if(names.length){
   for(const name of names){const el=node('div','live-transfer-file',name);el.title=name;body.append(el);}
   if(active>names.length)body.append(node('div','live-transfer-more','还有 '+(active-names.length)+' 路正在传输'));
  }else{
   body.append(node('div','live-transfer-file','正在等待传输统计…'));
  }
  live.append(body);
  if(names.some(n=>n.length>44))live.append(node('div','live-transfer-hint','← 左右滑动查看完整文件名 →'));
  live.open=expandedTransfers.has(j.id);
  live.addEventListener('toggle',()=>{if(live.open)expandedTransfers.add(j.id);else expandedTransfers.delete(j.id);});
 }
 const detail=node('details','copy-details');detail.append(node('summary','','查看来源和目标'),transferPaths(j));wireExpanded(detail,j);
 card.append(title,path,stats,track,metrics);
 if(live)card.append(live);
 card.append(foot,detail);
 if(j.error)card.append(node('div','job-error',j.error));
 return card;
}
async function refresh(){
 if(refreshBusy)return;refreshBusy=true;
 try{
  const data=await api('api/copy/status');lastStatus=data;
  // Do not destroy the scrolling DOM while a finger or mouse is dragging it.
  if(liveTouching||liveMouseDragging)return;
  $('connection').classList.remove('offline');$('connection').lastElementChild.textContent='服务运行中';
  if(!parallelInput.dataset.touched)parallelInput.value=data.defaultParallel||Math.max(1,Number(data.cpu)||2)*2;
  const jobs=data.jobs||[];
  const wrap=$('jobs');wrap.replaceChildren();
  if(!jobs.length){const empty=node('div','empty-queue');empty.append(node('span','','◴'),node('p','','暂无任务'));wrap.append(empty);}
  for(const job of jobs)wrap.append(makeJob(job));
  for(const list of wrap.querySelectorAll('.live-transfer-list[data-live-scroll-id]')){
   const saved=liveScrollPositions.get(list.dataset.liveScrollId);
   if(saved>0)list.scrollLeft=saved;
  }
 }catch(e){$('connection').classList.add('offline');$('connection').lastElementChild.textContent='服务离线';console.warn(e);}
 finally{refreshBusy=false;}
}
parallelInput.addEventListener('input',()=>parallelInput.dataset.touched='1');
$('refresh').addEventListener('click',refresh);
refresh();setInterval(()=>{if(!document.hidden)refresh();},1400);
