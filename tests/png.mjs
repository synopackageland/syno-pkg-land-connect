import assert from 'node:assert/strict';
import {inflateSync} from 'node:zlib';

/** Decodes an 8-bit RGB or RGBA PNG into raw pixels. */
export function decodePng(buf){
 assert.equal(buf.subarray(0,8).toString('hex'),'89504e470d0a1a0a');
 let width,height,colorType;const idat=[];
 for(let at=8;at<buf.length;){
  const len=buf.readUInt32BE(at),type=buf.toString('ascii',at+4,at+8),data=buf.subarray(at+8,at+8+len);
  if(type==='IHDR'){width=data.readUInt32BE(0);height=data.readUInt32BE(4);assert.equal(data[8],8);colorType=data[9];}
  if(type==='IDAT')idat.push(data);
  at+=12+len;
 }
 const bpp={2:3,6:4}[colorType];assert.ok(bpp,'8-bit RGB or RGBA');
 const raw=inflateSync(Buffer.concat(idat)),stride=width*bpp,px=Buffer.alloc(stride*height);
 for(let y=0;y<height;y++){
  const filter=raw[y*(stride+1)],line=raw.subarray(y*(stride+1)+1,(y+1)*(stride+1));
  for(let x=0;x<stride;x++){
   const a=x>=bpp?px[y*stride+x-bpp]:0,b=y?px[(y-1)*stride+x]:0,c=x>=bpp&&y?px[(y-1)*stride+x-bpp]:0;
   const p=a+b-c,pa=Math.abs(p-a),pb=Math.abs(p-b),pc=Math.abs(p-c);
   const pred=[0,a,b,(a+b)>>1,pa<=pb&&pa<=pc?a:pb<=pc?b:c][filter];
   px[y*stride+x]=(line[x]+pred)&255;
  }
 }
 return {width,height,bpp,px};
}
