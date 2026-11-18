import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {decodePng} from './png.mjs';

// DSM window/desktop sizes plus Package Center's PACKAGE_ICON (64) and
// PACKAGE_ICON_256; the CLI's scaffold icon is one solid colour.
const SIZES=[16,24,32,48,64,72,256];

for(const size of SIZES)test(`icon_${size}.png is a ${size}px multi-colour render`,()=>{
 const {width,height,bpp,px}=decodePng(readFileSync(`assets/icon/icon_${size}.png`));
 assert.deepEqual([width,height],[size,size]);
 const colours=new Set();
 for(let i=0;i<px.length;i+=bpp)colours.add(px.subarray(i,i+3).toString('hex'));
 assert.ok(colours.size>=3,`${colours.size} colours`);
});

test('icon source is the committed SVG', () => {
 assert.match(readFileSync('assets/icon/icon.svg','utf8'),/<svg [^>]*viewBox="0 0 256 256"/);
});
