// Both Cloudflare diagrams describe the same provider node.
import {Cloud} from '@lucide/vue'
import {translate} from '../i18n'
export const cloudflareNode=(kind:'free'|'domain',locale:string)=>({label:'Cloudflare',caption:translate(locale,'cfpage.path.'+(kind==='free'?'worker':'tunnel')),icon:Cloud})
