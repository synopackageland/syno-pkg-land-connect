// @vitest-environment jsdom
import {createApp,h} from 'vue'
import {expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {provideConnectState} from '../src/connect/state'
import ConnectionError from '../src/components/ConnectionError.vue'
import {translate} from '../src/i18n'
import {settle} from './support'
test('a domain or mode change refusal stays inline with impact, order and server navigation',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async()=>({}))
 let go:ReturnType<typeof vi.spyOn>
 const app=createApp({setup(){const s=provideConnectState(createStandaloneContext({call,locale:'en'}));go=vi.spyOn(s,'go');return()=>h(ConnectionError,{message:s.message({code:'netbird_cloudflare_in_use'})})}})
 app.mount(root);await settle()
 expect(root.textContent).toContain('even if its devices are offline')
 expect(root.textContent).toContain(translate('en','dependency.changeBlocked'))
 root.querySelector<HTMLButtonElement>('button')!.click()
 expect(go!).toHaveBeenCalledWith('netbird',{tab:'server'})
 app.unmount();root.remove()
})
