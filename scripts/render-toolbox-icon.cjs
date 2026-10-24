const fs=require('fs'),path=require('path');
const {Resvg}=require('@resvg/resvg-js');
const root=path.resolve(__dirname,'..');
const svg=fs.readFileSync(path.join(root,'scripts','toolbox-icon.svg'),'utf8');
const signature=Buffer.from('89504e470d0a1a0a','hex');
const table=Array.from({length:256},(_,n)=>{
 for(let i=0;i<8;i++)n=(n&1)?(0xedb88320^(n>>>1)):(n>>>1);
 return n>>>0;
});
function crc(buf) {
 let value=0xffffffff;
 for(const byte of buf)value=table[(value^byte)&255]^(value>>>8);
 return (value^0xffffffff)>>>0;
}
function chunk(type,data){
 const name=Buffer.from(type,'ascii'),len=Buffer.alloc(4),checksum=Buffer.alloc(4);
 len.writeUInt32BE(data.length);
 checksum.writeUInt32BE(crc(Buffer.concat([name,data])));
 return Buffer.concat([len,name,data,checksum]);
}
function addSrgb(buffer){
 if(!buffer.subarray(0,8).equals(signature)||buffer.subarray(12,16).toString()!=='IHDR')throw Error('not a valid PNG');
 const gamma=Buffer.alloc(4);gamma.writeUInt32BE(45455);
 // Keep chunks before IDAT and include explicit sRGB profile rendering intent.
 return Buffer.concat([buffer.subarray(0,33),chunk('sRGB',Buffer.from([0])),chunk('gAMA',gamma),buffer.subarray(33)]);
}
const aliasNames=size=>[
 'packaging/fpk/app/ui/images/toolbox_v202_'+size+'.png',
 'packaging/fpk/app/ui/images/toolbox_v201_'+size+'.png'
];
for(const [size,names] of [
 [64,['packaging/fpk/ICON.PNG','packaging/fpk/app/ui/images/icon_64.png',...aliasNames(64)]],
 [128,[...aliasNames(128)]],
 [192,[...aliasNames(192)]],
 [256,['packaging/fpk/ICON_256.PNG','packaging/fpk/app/ui/images/icon_256.png',
       ...aliasNames(256),'cmd/mountmgr/web/icon.png']],
 [512,[...aliasNames(512)]]
]){
 const png=addSrgb(new Resvg(svg,{fitTo:{mode:'width',value:size}}).render().asPng());
 for(const name of names)fs.writeFileSync(path.join(root,name),png);
}
console.log('NAS Toolbox v2.0.2 icons generated for 64, 128, 192, 256 and 512px');
