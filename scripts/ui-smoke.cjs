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
 if(pathname===base+'api/create'&&req.method==='POST'){
  let body='';for await(const chunk of req)body+=chunk;
  const input=JSON.parse(body);
  requests.push({path:pathname,headers:req.headers,body:input});
  const record={id:'browser-mock-id',source:input.source,target:input.parent+'/'+input.name,mode:input.mode,enabled:true,owned:true,status:'mounted'};
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
  await page.locator('#pick-source').click();
  const src=new URL(await page.evaluate(()=>window.__pickedURL));
  assert.equal(src.pathname,'/app-auth/pick-file');
  assert.equal(src.searchParams.get('directory'),'true');
  assert(src.searchParams.get('sidebarGroup').split(',').includes('remote'));
  await sendPicker(page,src,'/vol02/1000-0-test/Photos');
  await page.locator('#source-label').getByText('/vol02/1000-0-test/Photos').waitFor();
  await page.locator('#pick-target').click();
  const dst=new URL(await page.evaluate(()=>window.__pickedURL));
  assert(!dst.searchParams.get('sidebarGroup').split(',').includes('remote'));
  await sendPicker(page,dst,'/vol1/1000/share');
  await page.locator('#alias').fill('爸爸相册');
  await page.getByRole('button',{name:'读写'}).click();
  assert.equal(await page.locator('#preview').innerText(),'/vol1/1000/share/爸爸相册');
  await page.locator('#create').click();
  await page.locator('.record-title strong').getByText('爸爸相册').waitFor();
  assert.equal(requests.length,1);
  assert.deepEqual(requests[0].body,{source:'/vol02/1000-0-test/Photos',parent:'/vol1/1000/share',name:'爸爸相册',mode:'rw'});
  assert.equal(requests[0].headers['x-fnos-mount-ui'],'1');
  if(errors.length)throw Error('Browser errors: '+errors.join(';'));
  if(process.env.CAPTURE_SCREENSHOT==='1'){
    const screenshot=path.join(root,'dist','ui-mobile-v1.1.2.png');
    fs.mkdirSync(path.dirname(screenshot),{recursive:true});
    await page.screenshot({path:screenshot,fullPage:true});
  }
  console.log('UI SMOKE PASSED: mobile layout, native source/target picker, callback, Chinese path, mode, create JSON, record rendering');
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
