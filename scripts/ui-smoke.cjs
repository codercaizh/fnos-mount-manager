#!/usr/bin/env node
// Browser acceptance: real static UI + mocked API and native fnOS picker return.
// This does not require privileged bind-mount operations or a running NAS.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const { chromium }=require('playwright');
const root=path.resolve(__dirname,'..'),web=path.join(root,'cmd/mountmgr/web');
const base='/app/fnos-mount-manager/';
let requests=[];
const mockRecords=[];
const types={'.html':'text/html; charset=utf-8','.css':'text/css; charset=utf-8','.js':'text/javascript; charset=utf-8','.png':'image/png'};
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://127.0.0.1');
 const pathname=u.pathname;
 if(pathname===base+'api/status'){
  res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({root:true,records:mockRecords,interval:'60 秒'}));return;
 }
 if(pathname===base+'api/remote-shares'&&req.method==='GET'){
  res.writeHead(200,{'Content-Type':'application/json'});
  res.end(JSON.stringify({items:[{path:'/vol02/1000-0-test',name:'爸爸相册',subtitle:'caizh@10.0.2.50',labelSource:'fnos'},{path:'/vol02/1000-0-empty',name:'妈妈相册',subtitle:'myp@10.0.2.50',labelSource:'fnos'}]}));return;
 }
 if(pathname===base+'api/toggle'&&req.method==='POST'){
  let body='';for await(const chunk of req)body+=chunk;
  const input=JSON.parse(body);
  const record=mockRecords.find(x=>x.id===input.id);
  if(!record){res.writeHead(404);res.end();return;}
  record.enabled=input.enabled;
  record.status=input.enabled?'mounted':'disabled';
  res.writeHead(200,{'Content-Type':'application/json'});
  res.end(JSON.stringify(record));return;
 }
 if(pathname===base+'api/change-source'&&req.method==='POST'){
  let body='';for await(const chunk of req)body+=chunk;
  const input=JSON.parse(body);
  requests.push({path:pathname,headers:req.headers,body:input});
  const record=mockRecords.find(x=>x.id===input.id);
  if(!record){res.writeHead(404);res.end();return;}
  if(record.enabled){res.writeHead(409,{'Content-Type':'application/json'});res.end(JSON.stringify({error:'Disable first'}));return;}
  record.source=input.source;
  record.status='disabled';
  res.writeHead(200,{'Content-Type':'application/json'});
  res.end(JSON.stringify(record));return;
 }
 if(pathname===base+'api/create'&&req.method==='POST'){
  let body='';for await(const chunk of req)body+=chunk;
  const input=JSON.parse(body);
  requests.push({path:pathname,headers:req.headers,body:input});
  const record={id:'browser-mock-'+(mockRecords.length+1),source:input.source,target:input.parent+'/'+input.name,mode:input.mode,enabled:true,owned:true,status:'mounted'};
  mockRecords.push(record);
  res.writeHead(201,{'Content-Type':'application/json'});res.end(JSON.stringify(record));return;
 }
 if(pathname===base || pathname===base+'index.html')return send('index.html');
 const name=pathname.startsWith(base)?pathname.slice(base.length):null;
 if(name && ['app.js','style.css','fnos-sdk.js','icon.png','auth-callback.html'].includes(name))return send(name);
 res.writeHead(404);res.end('not found');
 function send(name){
  const fp=path.join(web,name);
  const ext=path.extname(fp);
  res.writeHead(200,{'Content-Type':types[ext]||'application/octet-stream','Cache-Control':'no-store'});
  fs.createReadStream(fp).pipe(res);
 }
});
async function run(){
 await new Promise(ok=>server.listen(0,'127.0.0.1',ok));
 let browser;
 try{
  browser=await chromium.launch({headless:true,args:['--no-sandbox']});
  const page=await browser.newPage({viewport:{width:390,height:844},deviceScaleFactor:1});
  const errors=[];page.on('pageerror',e=>{errors.push(e.message);console.error('BROWSER-ERROR',e.message)});page.on('console',m=>{if(m.type()==='error')console.error('CONSOLE-ERROR',m.text())});page.on('response',r=>{if(r.status()>=400)console.error('HTTP',r.status(),r.url())});
  await page.addInitScript(()=>{window.open=url=>{window.__pickedURL=url||'';return {location:{set href(v){window.__pickedURL=v;}}};};});
  await page.goto('http://127.0.0.1:'+server.address().port+base);
  console.log('PAGE STATUS',await page.locator('#health').innerText(), 'url',page.url());
  await page.locator('#health').getByText('运行中').waitFor({timeout:9000});
  assert.equal(await page.locator('html').evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(245, 247, 251)');
  assert.equal(await page.locator('#mkdir-target').count(),0,'No separate local folder creation button');
  await page.locator('#pick-source').click();
  await page.getByRole('button',{name:/选择文件夹/}).first().waitFor();
  await page.getByRole('button',{name:/选择远程目录/}).first().waitFor();
  assert.equal(await page.evaluate(()=>window.__pickedURL||''),'','Source must open choice dialog first');
  await page.locator('.choice-item').first().click();
  const src=new URL(await page.evaluate(()=>window.__pickedURL));
  assert.equal(src.pathname,'/app-auth/pick-file');
  assert.equal(src.searchParams.get('directory'),'true');
  assert(src.searchParams.get('sidebarGroup').split(',').includes('remote'));
  await sendPicker(page,src,'/vol02/1000-0-test/Photos');
  await page.locator('#source-label').getByText('/vol02/1000-0-test/Photos').waitFor();
  // A whole SMB share can be selected directly without selecting any subfolder,
  // including a share with no subdirectories at all.
  await page.locator('#pick-source').click();
  await page.locator('.choice-item').nth(1).click();
  await page.locator('.share-item').nth(1).waitFor();
  assert.equal(await page.locator('.share-item').nth(1).locator('strong').innerText(),'妈妈相册');
  assert.equal(await page.locator('.share-item').nth(1).locator('small').innerText(),'myp@10.0.2.50');
  assert.equal(await page.locator('.share-item').nth(0).locator('small').innerText(),'caizh@10.0.2.50');
  assert.equal(await page.locator('.share-item').nth(1).innerText().then(x=>x.includes('/vol02')),false,'Do not display internal paths in SMB list');
  await page.locator('.share-item').nth(1).click();
  await page.locator('#source-label').getByText('妈妈相册').waitFor();
  assert.equal(await page.locator('#pick-source').getAttribute('data-path'),'/vol02/1000-0-empty');
  await page.locator('#pick-target').click();
  const dst=new URL(await page.evaluate(()=>window.__pickedURL));
  assert(!dst.searchParams.get('sidebarGroup').split(',').includes('remote'));
  await sendPicker(page,dst,'/vol1/1000/share/新建目录');
  // The fnOS native picker owns New Folder. No additional creation endpoint.
  await page.locator('#target-label').getByText('/vol1/1000/share/新建目录').waitFor();
  // A native alias should default the map name, but the user can customize it.
  assert.equal(await page.locator('#alias').inputValue(),'妈妈相册');
  // A misleading local name must not silently hide a different SMB source.
  await page.locator('#alias').fill('爸爸相册');
  let warning='';
  page.once('dialog',async d=>{warning=d.message();await d.dismiss();});
  await page.locator('#create').click();
  assert.match(warning,/来源.*妈妈相册.*映射名称.*爸爸相册/);
  assert.equal(requests.length,0,'Mismatch cancellation must not submit');
  await page.locator('#alias').fill('妈妈相册');
  await page.getByRole('button',{name:'读写'}).click();
  assert.equal(await page.locator('#preview').innerText(),'来源：妈妈相册\n目标：/vol1/1000/share/新建目录/妈妈相册');
  await page.locator('#create').click();
  await page.locator('.record-title strong').getByText('妈妈相册').waitFor();
  assert.equal(await page.locator('#pick-source').getAttribute('data-path'),null,'Must clear source after creating a mapping');
  assert.equal(await page.locator('#target-label').innerText(),'/vol1/1000/share/新建目录','Keep common destination parent');
  assert.equal(await page.locator('#alias').inputValue(),'');
  assert.equal(requests.length,1);
  assert.deepEqual(requests[0].body,{source:'/vol02/1000-0-empty',parent:'/vol1/1000/share/新建目录',name:'妈妈相册',mode:'rw'});
  // Typing a different alias without reselecting a source is forbidden.
  await page.locator('#alias').fill('爸爸相册');
  await page.locator('#create').click();
  await page.locator('#toast').getByText(/请选择来源/).waitFor();
  assert.equal(requests.length,1,'Second create cannot silently reuse previous source');
  await page.locator('#pick-source').click();
  await page.locator('.choice-item').nth(1).click();
  await page.locator('.share-item').first().click(); // 爸爸相册 source
  assert.equal(await page.locator('#pick-source').getAttribute('data-path'),'/vol02/1000-0-test');
  assert.equal(await page.locator('#alias').inputValue(),'爸爸相册');
  await page.locator('#create').click();
  await page.locator('.record-title strong').getByText('爸爸相册').waitFor();
  assert.equal(requests.length,2);
  assert.deepEqual(requests[1].body,{source:'/vol02/1000-0-test',parent:'/vol1/1000/share/新建目录',name:'爸爸相册',mode:'rw'});
  assert.notEqual(requests[0].body.source,requests[1].body.source);
  assert.notEqual(requests[0].body.name,requests[1].body.name);
  assert.equal(requests[0].headers['x-fnos-mount-ui'],'1');
  // Repair workflow: disable -> choose new source -> save (no target changes) -> enable.
  page.on('dialog',d=>d.accept());
  await page.locator('.record').first().getByRole('button',{name:'停用'}).click();
  await page.locator('.record').first().getByRole('button',{name:'更换来源'}).click();
  await page.locator('.choice-item').first().click();
  const changed=new URL(await page.evaluate(()=>window.__pickedURL));
  await sendPicker(page,changed,'/vol02/1000-0-alternate');
  await page.locator('#toast').getByText(/来源已更新/).waitFor();
  assert.deepEqual(requests.at(-1).body,{id:'browser-mock-1',source:'/vol02/1000-0-alternate'});
  assert.equal(mockRecords[0].target,'/vol1/1000/share/新建目录/妈妈相册');
  assert.equal(mockRecords[0].enabled,false);
  assert.equal(mockRecords[0].source,'/vol02/1000-0-alternate');
  await page.locator('.record').first().getByRole('button',{name:'启用'}).click();
  await page.locator('.record').first().getByRole('button',{name:'停用'}).waitFor();

  // Browser confirm dialogs default to dismissed; install a handler for this explicit user action.
  if(errors.length)throw Error('Browser errors: '+errors.join(';'));
  if(process.env.CAPTURE_SCREENSHOT==='1'){
    const screenshot=path.join(root,'dist','ui-mobile-v1.2.5.png');
    fs.mkdirSync(path.dirname(screenshot),{recursive:true});
    await page.screenshot({path:screenshot,fullPage:true});
  }
  console.log('UI SMOKE PASSED: two distinct SMB mappings into one parent, source reset, duplicate guard, edit disabled mapping, restore enable, native chooser');
 }finally{
  if(browser)await browser.close();
  await new Promise(ok=>server.close(ok));
 }
}
async function sendPicker(page,url,value){
 await page.evaluate(({state,value})=>{
  window.postMessage({type:'fnos-mount-manager:picker',result:{state,method:'pickFile',status:'success',path:[value]}},location.origin);
 },{state:url.searchParams.get('state'),value});
}
run().catch(e=>{console.error(e);process.exitCode=1;server.close();});
