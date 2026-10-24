import { TrimApp } from './fnos-sdk.js';
const $ = id => document.getElementById(id);
const base = new URL('.', import.meta.url).pathname;
const app = new TrimApp();
let selectedMode = 'ro';
let busy = false;
let pendingPicker = null;
let editSourceFor = null;
let recordCache = [];
const shareLabelCache = new Map();
let toastTimer;
const ICONS = {
  folder:'<path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3z"/>',
  cloud:'<path d="M6.5 18a4.6 4.6 0 0 1-1-9.1A7 7 0 0 1 18.3 10 4 4 0 0 1 18 18z"/>',
  chevron:'<path d="m9 6 6 6-6 6"/>',
  arrowdown:'<path d="M12 3v17m-6-6 6 6 6-6"/>',
  plus:'<path d="M12 5v14M5 12h14"/>',
  refresh:'<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6 8a8 8 0 0 1 13-1l1 5M4 12l1 5a8 8 0 0 0 13-1"/>',
  info:'<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  pause:'<rect x="4" y="4" width="16" height="16" rx="5"/><path d="M10 8v8M14 8v8"/>',
  play:'<path d="M8 5v14l11-7z"/>',
  pen:'<path d="m4 20 4-.8L20 7l-3-3L5 16zM15 6l3 3"/>',
  shuffle:'<path d="m18 14 4 4-4 4M2 6h5a5 5 0 0 1 4 3l2 5a5 5 0 0 0 5 4h4M18 2l4 4-4 4M2 18h5a5 5 0 0 0 4-3l2-5a5 5 0 0 1 5-4h4"/>',
  trash:'<path d="M4 7h16M10 7V5h4v2M6 7l1 13h10l1-13M10 11v5m4-5v5"/>',
  check:'<path d="m5 12 4 4L19 6"/>',
  empty:'<rect x="3" y="4" width="18" height="17" rx="4"/><path d="M8 11h8M12 7v8"/>',
  link:'<path d="M9 15l6-6M10 7l1-1a5 5 0 0 1 7 7l-1 1M14 17l-1 1a5 5 0 0 1-7-7l1-1"/>'
};
function icon(name){return '<svg aria-hidden="true" viewBox="0 0 24 24">'+(ICONS[name]||ICONS.folder)+'</svg>';}
function setupIcons(){document.querySelectorAll('[data-icon]').forEach(e=>{e.innerHTML=icon(e.dataset.icon);});$('refresh').innerHTML=icon('refresh');$('info-btn').innerHTML=icon('info');}
setupIcons();
function node(tag,cls,text){
  const e=document.createElement(tag); if(cls)e.className=cls; if(text!==undefined)e.textContent=text;return e;
}
function notify(message){const e=$('toast');e.textContent=message;e.classList.remove('hidden');clearTimeout(toastTimer);toastTimer=setTimeout(()=>e.classList.add('hidden'),4000);}
async function api(path, opts={}){
 const response=await fetch(base+path,{cache:'no-store',...opts});
 let data;
 try {data=await response.json();}catch{throw Error('无法读取服务响应');}
 if(!response.ok) throw Error(data.error||'操作失败');
 return data;
}
const action=(path,value)=>api(path,{method:'POST',headers:{'Content-Type':'application/json','X-Fnos-Mount-UI':'1'},body:JSON.stringify(value)});
function withIcon(label,name,handler,danger=false){
 const b=node('button','action'+(danger?' danger':''));b.type='button';b.title=label;b.setAttribute('aria-label',label);
 b.innerHTML=icon(name);b.addEventListener('click',handler);return b;
}
const statuses={mounted:'已挂载',source_offline:'来源离线',disabled:'已停用',conflict:'目录冲突',error:'异常',checking:'检测中'};
async function refresh(){
 if(busy)return;busy=true;
 try{
  const data=await api('api/status');
  recordCache=data.records||[];
  $('health').classList.toggle('offline',!data.root);
  $('health').querySelector('span').textContent=data.root?'运行中':'权限不足';
  $('total').textContent=data.records.length;
  const list=$('records');list.replaceChildren();
  if(!data.records.length){
   const empty=node('div','empty');
   const visual=node('div','empty-icon');visual.innerHTML=icon('link');
   empty.append(visual,node('div','','暂无挂载，创建第一条映射吧'));list.append(empty);return;
  }
  for(const r of data.records){
   const item=node('article','record'+(r.error?' error':''));
   const visual=node('div','list-icon');visual.innerHTML=icon('folder');
   const main=node('div','record-body');
   const line=node('div','record-title');
   line.append(node('strong','',r.target.split('/').pop()),node('span','chip '+r.status,statuses[r.status]||r.status));
   const friendlySource=r.sourceLabel||shareLabelCache.get(r.source)||r.source;
   const p=node('div','record-path');
   p.append(node('div','','来源：'+friendlySource),node('div','','目标：'+r.target));
   p.title=r.source+' → '+r.target;
   const summary=node('div','record-meta',(r.mode==='rw'?'读写':'只读')+' · '+(r.error||'自动维护'));
   main.append(line,p,summary);
   const actions=node('div','record-actions');
   actions.append(withIcon('检查挂载','refresh',async()=>{try{await action('api/retry',{id:r.id});await refresh();}catch(e){notify(e.message);}}));
   actions.append(withIcon('更换来源','shuffle',()=>{
     if(r.enabled){notify('请先停止备份任务并停用此映射，再更换来源');return;}
     showSourceChoices({id:r.id,name:r.target.split('/').pop()});
   }));
   actions.append(withIcon('重命名','pen',async()=>{
    const name=prompt('映射名称',r.target.split('/').pop());
    if(name===null||!name.trim())return;
    try{await action('api/rename',{id:r.id,name:name.trim()});await refresh();notify('名称已更新');}catch(e){notify(e.message);}
   }));
   actions.append(withIcon(r.enabled?'停用':'启用',r.enabled?'pause':'play',async()=>{
    if(r.enabled&&!confirm('确定停用这个映射？请先停止正在使用它的备份任务。'))return;
    try{await action('api/toggle',{id:r.id,enabled:!r.enabled});await refresh();}catch(e){notify(e.message);}
   }));
   actions.append(withIcon('删除记录','trash',async()=>{
    if(!confirm('卸载并删除映射记录？源文件不会被删除。'))return;
    try{await action('api/remove',{id:r.id});await refresh();notify('映射已删除');}catch(e){notify(e.message);}
   },true));
   item.append(visual,main,actions);list.append(item);
  }
 }catch(e){$('health').classList.add('offline');$('health').querySelector('span').textContent='连接失败';notify(e.message);}
 finally{busy=false;}
}
$('refresh').addEventListener('click',refresh);
function normalizePath(x){
 if(typeof x!=='string')return '';
 const trimmed=x.trim();
 return ('/'+trimmed.replace(/^\/+/,'' )).replace(/\/+$/,'');
}
function updateSelected(side,value){
 const path=normalizePath(value);
 if(!path||path==='/')return;
 const isSource=side==='source';
 $(isSource?'source-label':'target-label').textContent=path;
 $(isSource?'source-label':'target-label').classList.add('has-path');
 $(isSource?'pick-source':'pick-target').dataset.path=path;
 preview();
}
function preview(){
 const target=$('pick-target').dataset.path;
 const name=$('alias').value.trim();
 const box=$('preview');
 if(!target||!name){box.classList.add('hidden');return;}
 box.classList.remove('hidden');
 box.replaceChildren(
   node('div','','来源：'+($('source-label').textContent||'未选择')),
   node('div','','目标：'+target+'/'+name)
 );
}
$('alias').addEventListener('input',()=>{ $('alias').dataset.suggested='';preview(); });
async function handleChosenSource(source,displayLabel='',autoSuggest=false){
 const path=normalizePath(source);
 if(!path||path==='/')return;
 const pendingEdit=editSourceFor;
 editSourceFor=null;
 if(pendingEdit){
  try{
   await action('api/change-source',{id:pendingEdit.id,source:path});
   await refresh();
   notify('「'+pendingEdit.name+'」来源已更新，点击启用后生效');
  }catch(e){notify(e.message);}
  return;
 }
 const other=recordCache.find(r=>r.source===path);
 if(other){
  notify('该来源已映射到「'+other.target.split('/').pop()+'」，请选择其他 SMB 来源');
  return;
 }
 updateSelected('source',path);
 if(displayLabel){
  $('source-label').textContent=displayLabel;
  $('source-label').title=path;
 }
 if(autoSuggest){$('source-label').dataset.nativeAlias=displayLabel;}
 else{delete $('source-label').dataset.nativeAlias;}
 if(autoSuggest&&displayLabel&&(!$('alias').value.trim()||$('alias').dataset.suggested==='1')){
  $('alias').value=displayLabel;
  $('alias').dataset.suggested='1';
 }
 preview();
}
document.querySelectorAll('[data-mode]').forEach(b=>b.addEventListener('click',()=>{
 selectedMode=b.dataset.mode;
 document.querySelectorAll('[data-mode]').forEach(x=>{x.classList.toggle('selected',x===b);x.setAttribute('aria-pressed',String(x===b));});
}));
function selectFromResult(result,side){
 const arr=Array.isArray(result)?result:result?.code===0&&Array.isArray(result.data)?result.data:undefined;
 if(arr?.length){
  if(side==='source'){void handleChosenSource(arr[0]);}
  else {updateSelected(side,arr[0]);}
  return true;
 }
 if(result?.code!==undefined&&result.code!==0)throw Error(result.msg||'选择目录失败');
 return false;
}
function receivedAuth(data){
 if(!pendingPicker||data?.state!==pendingPicker.state||data?.method!=='pickFile')return;
 const current=pendingPicker;pendingPicker=null;
 try{
  if(data.status==='success'&&data.path?.length){
    if(current.side==='source'){void handleChosenSource(data.path[0]);}
    else {updateSelected(current.side,data.path[0]);}
  } else {
    editSourceFor=null;
    if(data.status==='error')notify('系统目录选择失败');
  }
 }catch(e){notify(e.message);}
}
window.addEventListener('message',event=>{
 if(event.origin===location.origin&&event.data?.type==='fnos-mount-manager:picker'){receivedAuth(event.data.result);}
});
window.addEventListener('storage',event=>{
 if(event.key==='fnos-mount-manager:picker-result'&&event.newValue){
  try{receivedAuth(JSON.parse(event.newValue));}catch{}
 }
});
async function chooseFolder(side){
 const sidebarGroup=side==='source'?['remote','myFiles','otherShare','favorites']:['myFiles','otherShare'];
 const button=$(side==='source'?'pick-source':'pick-target');button.disabled=true;
 try{
  await app.ready();
  if(app.isStandaloneWeb){
   const state=crypto.randomUUID();
   pendingPicker={state,side};
   await app.openAppAuth('pickFile',{
     appName:'fnos-mount-manager',directory:true,creatable:side==='target',sidebarGroup,
     redirectUri:'/app/fnos-mount-manager/auth-callback.html',state,
   },{target:'_blank',features:'width=860,height=690'});
   return;
  }
  const result=await app.pickFile({
   directory:true,creatable:side==='target',sidebarGroup,
   title:side==='source'?'选择来源文件夹':'选择本地父目录',
   okText:'选择目录'
  });
  selectFromResult(result,side);
 }catch(e){notify('系统目录选择器无法使用：'+(e.message||String(e)));}
 finally{button.disabled=false;}
}
function showExtraDialog(title,description){
 $('dialog-title').textContent=title;
 $('dialog-description').textContent=description;
 $('dialog-content').replaceChildren();

 $('extra-dialog').classList.remove('hidden');
}
function hideExtraDialog(){ $('extra-dialog').classList.add('hidden'); }
$('dialog-close').addEventListener('click',hideExtraDialog);
$('extra-dialog').addEventListener('click',e=>{if(e.target===$('extra-dialog'))hideExtraDialog();});
document.addEventListener('keydown',e=>{if(e.key==='Escape')hideExtraDialog();});

function showSourceChoices(edit=null){
 editSourceFor=edit;
 showExtraDialog(edit?'更换映射来源':'选择来源',edit?
   '为「'+edit.name+'」选择新的 SMB 来源，修改后仍需启用映射':'从文件夹或已连接的 SMB 共享中选择');
 const list=$('dialog-content');
 const chooseFolderButton=node('button','choice-item');
 const folderIcon=node('span','choice-icon');folderIcon.innerHTML=icon('folder');
 const folderTexts=node('span','choice-copy');
 folderTexts.append(node('strong','','选择文件夹'),node('small','','使用飞牛文件夹选择器'));
 chooseFolderButton.append(folderIcon,folderTexts);
 chooseFolderButton.addEventListener('click',()=>{hideExtraDialog();chooseFolder('source');});
 const chooseSMBButton=node('button','choice-item');
 const smbIcon=node('span','choice-icon remote-icon');smbIcon.innerHTML=icon('cloud');
 const smbTexts=node('span','choice-copy');
 smbTexts.append(node('strong','','选择远程目录'),node('small','','直接挂载整个 SMB 共享'));
 chooseSMBButton.append(smbIcon,smbTexts);
 chooseSMBButton.addEventListener('click',()=>{void showRemoteShares();});
 list.append(chooseFolderButton,chooseSMBButton);
}
async function showRemoteShares(){
 showExtraDialog('选择远程目录','优先显示飞牛挂载别名，无法获取时显示共享地址');
 const list=$('dialog-content');
 list.append(node('div','empty','正在读取远程挂载…'));
 try{
  const response=await api('api/remote-shares');
  if($('extra-dialog').classList.contains('hidden'))return;
  list.replaceChildren();
  const back=node('button','back-action','← 返回');back.addEventListener('click',()=>showSourceChoices(editSourceFor));list.append(back);
  if(!response.items?.length){
   list.append(node('p','empty','暂无已连接的 SMB 共享'));return;
  }
  for(const share of response.items){
   shareLabelCache.set(share.path,share.name);
   const button=node('button','share-item');
   button.type='button';
   const label=node('strong','',share.name);
   const subtitle=node('small','',share.subtitle||(share.labelSource==='fnos'?'飞牛挂载别名':share.labelSource==='semantic'?'飞牛文件路径名称':'SMB 共享地址（未获取系统别名）'));
   button.append(label,subtitle);
   // The real mount path is not a human-facing label; use it only as value.
   button.addEventListener('click',()=>{
     const edit=editSourceFor;
     hideExtraDialog();
     void handleChosenSource(share.path,share.name,share.labelSource==='fnos');
   });
   list.append(button);
  }
 }catch(e){list.replaceChildren(node('p','empty',e.message));}
}
$('pick-source').addEventListener('click',()=>showSourceChoices());
$('pick-target').addEventListener('click',()=>chooseFolder('target'));
$('info-btn').addEventListener('click',()=>notify('把远程挂载目录映射到本地共享目录，支持自动恢复'));
$('create').addEventListener('click',async()=>{
 const source=$('pick-source').dataset.path||'';
 const parent=$('pick-target').dataset.path||'';
 const name=$('alias').value.trim();
 if(!source||!parent||!name){notify('请选择来源、本地目录并填写名称');return;}
 const same=recordCache.find(r=>r.source===source);
 if(same){notify('该来源已映射到「'+same.target.split('/').pop()+'」，请重新选择 SMB 来源');return;}
 const selectedAlias=$('source-label').dataset.nativeAlias;
 if(selectedAlias&&selectedAlias!==name){
  if(!confirm('来源是「'+selectedAlias+'」，映射名称是「'+name+'」。确定继续吗？'))return;
 }
 const b=$('create');b.disabled=true;
 try{
  const result=await action('api/create',{source,parent,name,mode:selectedMode});
  await refresh();
  notify(result.status==='mounted'?'挂载成功':'记录已保存：'+(result.error||result.status));
  $('alias').value='';
  $('alias').dataset.suggested='';
  delete $('pick-source').dataset.path;
  $('source-label').textContent='选择文件夹';
  $('source-label').title='';
  $('source-label').classList.remove('has-path');
  delete $('source-label').dataset.nativeAlias;
  preview();
 }catch(e){notify(e.message);}
 finally{b.disabled=false;}
});
refresh();
setInterval(()=>{if(document.visibilityState==='visible')refresh();},15000);
