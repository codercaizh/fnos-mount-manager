#!/usr/bin/env node
const fs=require('fs'), path=require('path');
const {Resvg}=require('@resvg/resvg-js');
const root=path.resolve(__dirname,'..');
const fpk=path.join(root,'packaging/fpk'), web=path.join(root,'cmd/mountmgr/web');
const svg=fs.readFileSync(path.join(__dirname,'icon.svg'),'utf8');
for(const size of [64,256]){
 const image=new Resvg(svg,{fitTo:{mode:'width',value:size}});
 const data=image.render().asPng();
 const name=size===64?'ICON.PNG':'ICON_256.PNG';
 fs.writeFileSync(path.join(fpk,name),data);
 fs.writeFileSync(path.join(fpk,'app/ui/images','icon_'+size+'.png'),data);
 if(size===256) fs.writeFileSync(path.join(web,'icon.png'),data);
 console.log(name,size+'px',data.length+'B');
}
