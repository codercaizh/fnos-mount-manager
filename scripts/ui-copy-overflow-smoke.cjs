const http=require('http'),fs=require('fs'),path=require('path'),assert=require('assert/strict');
const {chromium}=require('/data/fnos-toolbox/node_modules/playwright');
const root=path.resolve(__dirname,'..');
const web=path.join(root,'cmd/mountmgr/web');
const base='/app/fnos-mount-manager/';
const completed={
 id:'history1',sources:['/vol1/1000/pictures/猫.jpg','/vol1/1000/docs/workspace'],
 target:'/vol1/1000/backup',transfers:8,verify:'size',state:'completed',
 created:'2026-10-24T17:38:00+08:00',updated:'2026-10-24T17:38:08+08:00',
 totalBytes:8290304,doneBytes:8290304,doneFiles:2,totalFiles:2,
 items:[
  {source:'/vol1/1000/pictures/猫.jpg',dest:'/vol1/1000/backup/猫.jpg',dir:false},
  {source:'/vol1/1000/docs/workspace',dest:'/vol1/1000/backup/workspace',dir:true}
 ]
};
const active={id:'active1',sources:['/vol1/1000/学习资料/超长网盘资源目录'],target:'/vol1/1000/学习资料',transfers:8,verify:'size',state:'running',created:'2026-10-24T20:22:00+08:00',updated:'2026-10-24T22:03:00+08:00',totalBytes:23459322452,doneBytes:234231244,currentBytes:236622002,speed:99.92*1024*1024,eta:3*3600+34*60+34,totalFiles:999,doneFiles:0,currentFile:'1. Big Muzzy 12 集动画/Muzzy BBC - Unit 1 - American English - MUZZY in GONDOLAND'.repeat(5)};
active.activeTransfers=8;active.currentFiles=Array.from({length:8},(_,i)=>'系列合集/世界名著音频课程/第'+(i+1)+'集-中文 English title.mp4');
active.currentDoneFiles=117;
const jobs=[active,completed];let lastCreate;
const sdk=`export class TrimApp{
 constructor(){this.isStandaloneWeb=false;} async ready(){}
 async pickFile(options){if(options.directory)return ['/vol1/1000/backup'];
 return ['/vol1/1000/pictures/猫.jpg','/vol1/1000/docs/workspace'];}
}`;
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://local'),p=u.pathname;
 const json=(obj,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(obj));};
 if(p===base+'api/status')return json({root:true,records:[],interval:'60 秒'});
 if(p===base+'api/copy/status')return json({cpu:4,defaultParallel:8,jobs:jobs.slice().reverse()});
 if(p===base+'api/copy/detail')return json(jobs.find(j=>j.id===u.searchParams.get('id'))||{},200);
 if(p===base+'api/copy/create'){
   let b='';for await(const c of req)b+=c;lastCreate=JSON.parse(b);
   return json({id:'new',state:'queued'},201);
 }
 if(p===base+'fnos-sdk.js'){res.writeHead(200,{'Content-Type':'text/javascript'});res.end(sdk);return;}
 const name=p===base?'index.html':p.startsWith(base)?p.slice(base.length):null;
 if(!name||!/^(index.html|copy.html|app.js|copy.js|style.css|copy.css|icon.png|auth-callback.html|copy-auth-callback.html|copy-v206.html|copy-v206.js|copy-v206.css)$/.test(name)){res.writeHead(404);res.end('not found');return;}
 res.writeHead(200,{'Content-Type':name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':name.endsWith('.png')?'image/png':'text/html'});
 fs.createReadStream(path.join(web,name)).pipe(res);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let b;
 try{
  b=await chromium.launch({headless:true,args:['--no-sandbox']});
  const page=await b.newPage({viewport:{width:390,height:844},deviceScaleFactor:1});
  const errors=[];page.on('pageerror',e=>{errors.push(e.message);console.error('PAGE_ERROR',e.message)});page.on('console',m=>console.log('CONSOLE',m.type(),m.text()));page.on('requestfailed',r=>console.error('FAILED REQUEST',r.url(),r.failure()));
  const address='http://127.0.0.1:'+server.address().port+base;
  await page.goto(address);
  assert.equal(await page.title(),'NAS 工具箱');
  await page.getByRole('link',{name:'快速复制'}).click();
  await page.getByRole('heading',{name:'最近传输任务'}).waitFor();
  await page.getByText('117 / 999').waitFor();
  assert.equal(await page.locator('#job-count').count(),0);
  for(const width of [320,360,375,390,412,430]){
   await page.setViewportSize({width,height:844});
   await page.locator('.job:not(.completed)').first().waitFor();
   await page.getByText('正在传输 8 / 8 路').waitFor();
   const report=await page.evaluate(()=>{
    const width=document.documentElement.clientWidth;
    return {width,scrollWidth:document.documentElement.scrollWidth,app:document.querySelector('.app').getBoundingClientRect().width, offenders:[...document.querySelectorAll('body *')].map(el=>({tag:el.tagName,classes:typeof el.className==='string'?el.className:'',right:Math.round(el.getBoundingClientRect().right),scroll:el.scrollWidth,client:el.clientWidth})).filter(r=>r.right>width+2).slice(0,22)};
   });
   console.log('OVERFLOW',JSON.stringify(report));
   if(report.scrollWidth>width+2)throw Error('layout overflow at '+width);
  }
  await page.setViewportSize({width:390,height:844});
  await page.locator('.live-transfers summary').click();
  assert.equal(await page.locator('.live-transfer-file:visible').count(),8);
  await page.locator('.live-transfers summary').click();

  await page.waitForTimeout(1200);console.log('BEFORE WAIT: URL',page.url(),'JOBS',await page.locator('#jobs').innerText(),'ERRORS',errors);
  await page.locator('.job.completed').first().waitFor({timeout:5000});
  assert.equal(await page.locator('.job.completed').count(),1);
  assert.equal(await page.locator('.copy-detail-body').first().isVisible(),false);
  await page.locator('.job.completed summary').click();
  await page.getByText('/vol1/1000/backup/猫.jpg').first().waitFor();
  assert.equal(await page.locator('.job.completed .copy-detail-item').count(),2);
  await page.locator('#pick-source').click();
  await page.locator('.source-row').first().waitFor();
  assert.equal(await page.locator('.source-row').count(),2);
  await page.locator('#pick-target').click();
  await page.locator('#parallel').fill('5');
  await page.locator('#create').click();
  await page.waitForTimeout(200);
  assert.equal(lastCreate.parallel,5);
  assert.deepEqual(lastCreate.sources,['/vol1/1000/pictures/猫.jpg','/vol1/1000/docs/workspace']);
  assert.equal(lastCreate.target,'/vol1/1000/backup');
  const layout=await page.evaluate(()=>({width:document.documentElement.scrollWidth,view:innerWidth}));
  assert(layout.width<=layout.view+2,'mobile overflow '+JSON.stringify(layout));
  assert.deepEqual(errors,[]);
  await page.screenshot({path:path.join(root,'dist','nas-toolbox-copy-mobile.png'),fullPage:true});
  await page.getByRole('link',{name:'目录映射'}).click();
  await page.getByRole('heading',{name:'我的挂载'}).waitFor();
  assert.equal(await page.title(),'NAS 工具箱');
  assert.deepEqual(errors,[]);
  console.log('TOOLBOX UI PASS: navigation, history no count, expand sources and destination, copy selector, new task.');
 }finally{if(b)await b.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
