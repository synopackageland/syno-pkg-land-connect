// jsdom has no CSS.escape, which the UI's tab strip uses to find its tabs.
if(typeof globalThis.CSS==='undefined')(globalThis as {CSS?:unknown}).CSS={}
if(typeof CSS.escape!=='function')CSS.escape=(value:string)=>String(value).replace(/[^a-zA-Z0-9_-]/g,c=>'\\'+c)
