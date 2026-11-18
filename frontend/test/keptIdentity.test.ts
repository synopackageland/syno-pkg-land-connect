// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {fixture} from './providerFixture'
import {settle} from './support'
afterEach(()=>vi.restoreAllMocks())
const off=(provider:string,more={})=>({provider,state:'disconnected' as const,simulation:false,shareUrl:'',...more})
test('an off Tailscale with its key kept says the device is still registered',async()=>{
 const open=vi.spyOn(window,'open').mockReturnValue(null)
 const f=fixture([off('tailscale',{keptIdentity:true})]);await settle();await f.nav('Tailscale')
 const note=f.page('tailscale').querySelector('[data-kept-identity]')
 expect(note?.textContent).toContain('still a device in your tailnet')
 note!.querySelector<HTMLElement>('[data-kept-console]')!.click()
 expect(open).toHaveBeenCalledWith('https://login.tailscale.com/admin/machines','_blank','noopener,noreferrer')
 f.done()
})
test('an off way without a kept key shows no registration note',async()=>{
 const f=fixture([off('tailscale')]);await settle();await f.nav('Tailscale')
 expect(f.page('tailscale').querySelector('[data-kept-identity]')).toBeNull();f.done()
})
test('Forget asks first, then deletes the key through connection.forget',async()=>{
 const f=fixture([off('netbird',{keptIdentity:true,managementUrl:'https://nb.example.com:443'})],{'connection.forget':()=>({})});await settle();await f.nav('NetBird')
 const open=vi.spyOn(window,'open').mockReturnValue(null)
 const note=f.page('netbird').querySelector('[data-kept-identity]')!
 note.querySelector<HTMLElement>('[data-kept-console]')!.click()
 expect(open).toHaveBeenCalledWith('https://nb.example.com/peers','_blank','noopener,noreferrer')
 note.querySelector<HTMLElement>('[data-kept-forget]')!.click();await settle()
 expect(f.call.mock.calls.some(c=>c[0]==='connection.forget')).toBe(false)
 expect(document.querySelector('[data-forget-dialog]')?.textContent).toContain('remove the old one in the admin console')
 document.querySelector<HTMLElement>('[data-forget-confirm]')!.click();await settle()
 expect(f.call.mock.calls.filter(c=>c[0]==='connection.forget').map(c=>c[1])).toEqual([{provider:'netbird'}])
 f.done()
})
