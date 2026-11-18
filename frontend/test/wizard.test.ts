// @vitest-environment jsdom
// The setup wizard, a dialog opened from the Home and App URLs: where it
// opens comes from the backend, the cards, the temporary address row, the
// Free link step, one approval for the Apps and DSM, and the addresses with
// the steps to test them from a phone.
import {afterEach,expect,test,vi} from 'vitest'
import type {ConnectionStatus} from '../src/context'
import {resume,selfTestSteps,stepFor,temporaryAvailable,wayState,TEST_SITES,type WayState,type WizardWay} from '../src/connect/wizard'
import {fixture,t,connected} from './providerFixture'
import {settle} from './support'

let env:ReturnType<typeof fixture>|undefined
afterEach(()=>{env?.done();env=undefined;vi.restoreAllMocks()})
const byStatus=(list:ConnectionStatus[])=>Object.fromEntries(list.map(c=>[c.provider,c]))
const states=(over:Partial<Record<WizardWay,WayState>>={})=>({workers:'off',"cloudflare-domain":'off',tailscale:'off',netbird:'off',ipv6:'off',temporary:'off',...over}) as Record<WizardWay,WayState>
const pin={mode:'W',names:['nas.example.com'],issuer:'Example CA',notAfter:'2030-01-01T00:00:00Z'} as const
const app=(pkg:string,extra:Record<string,unknown>={})=>({package:pkg,name:pkg[0]!.toUpperCase()+pkg.slice(1),port:28300,protection:{},publicCapable:true,...extra})
const wizard=()=>document.body.querySelector<HTMLElement>('[data-setup-wizard]')!
const q=<T extends HTMLElement=HTMLElement>(sel:string)=>wizard().querySelector<T>(sel)
const card=(way:WizardWay)=>q(`[data-wizard-way="${way}"]`)!
const next=()=>q<HTMLButtonElement>('[data-wizard-footer] [data-wizard-next]')!
// The Home's one button opens the wizard: Set up on the first visit, Add a way after.
async function openWizard(){env!.page('status').querySelector<HTMLElement>('[data-home-set-up],[data-home-add-way]')!.click();await settle()}
// Select a way's card, then go on with the footer's main action.
async function use(way:WizardWay){card(way).click();await settle();next().click();await settle()}

test('each way reads as off, started or on from what the backend reports', () => {
 expect(wayState('workers',{},undefined)).toBe('off')
 expect(wayState('workers',{},{connected:false,code:'workers_subdomain_required'})).toBe('started')
 expect(wayState('workers',{},{connected:true})).toBe('on')
 // A token that stopped working is not a way that works.
 expect(wayState('workers',{},{connected:true,invalid:true})).toBe('off')
 expect(wayState('tailscale',byStatus([{...connected('tailscale'),state:'needs-login'}]),undefined)).toBe('started')
 expect(wayState('cloudflare-domain',byStatus([connected('cloudflare',{mode:'domain'})]),undefined)).toBe('on')
 // The temporary address is not My own domain.
 expect(wayState('cloudflare-domain',byStatus([connected('cloudflare',{mode:'quick'})]),undefined)).toBe('off')
 expect(wayState('temporary',byStatus([connected('cloudflare',{mode:'quick'})]),undefined)).toBe('on')
 expect(wayState('temporary',byStatus([{...connected('cloudflare',{mode:'quick'}),state:'connecting'}]),undefined)).toBe('started')
 expect(wayState('temporary',byStatus([connected('cloudflare',{mode:'domain'})]),undefined)).toBe('off')
 // The temporary address replaces what Cloudflare runs, so it waits for Cloudflare to be free.
 expect(temporaryAvailable({})).toBe(true)
 expect(temporaryAvailable(byStatus([{...connected('cloudflare'),state:'failed'}]))).toBe(true)
 expect(temporaryAvailable(byStatus([connected('cloudflare',{mode:'quick'})]))).toBe(true)
 expect(temporaryAvailable(byStatus([connected('cloudflare',{mode:'domain'})]))).toBe(false)
})

test('the wizard opens on a way still waiting, else on the cards with the Free link', () => {
 expect(resume(states())).toEqual({way:'workers',step:'choose'})
 expect(resume(states({tailscale:'started'}))).toEqual({way:'tailscale',step:'connect'})
 expect(resume(states({tailscale:'on'}))).toEqual({way:'workers',step:'choose'})
 expect(resume(states({temporary:'started'}))).toEqual({way:'temporary',step:'connect'})
 // A way that is on goes straight to its Apps, or to the end without any.
 expect(stepFor('tailscale','on')).toBe('apps')
 expect(stepFor('ipv6','on')).toBe('done')
 expect(stepFor('temporary','on')).toBe('done')
 expect(stepFor('netbird','off')).toBe('connect')
})

test('the self-test checks a phone has IPv6 first, and a private network needs its app', () => {
 expect(selfTestSteps('ipv6')).toEqual(['phone-ipv6','open','outside'])
 expect(selfTestSteps('tailscale')).toEqual(['phone-app','open'])
 expect(selfTestSteps('workers')).toEqual(['open'])
 expect(TEST_SITES.phoneIpv6).toBe('https://test-ipv6.com/')
})

test('the first visit shows three ways side by side with the same facts, the Free link first',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 expect(wizard().dataset.wizardStep).toBe('choose')
 expect(q('[data-wizard-heading]')!.textContent).toContain(t('wizard.step.choose'))
 const cards=[...wizard().querySelectorAll<HTMLElement>('[data-wizard-choose] > [role="radiogroup"] > [data-wizard-way]')].map(c=>c.dataset.wizardWay)
 expect(cards).toEqual(['workers','cloudflare-domain','tailscale'])
 for(const way of cards)expect(q(`[data-wizard-way="${way}"]`)!.querySelectorAll('[data-wizard-fact]')).toHaveLength(5)
 expect(q('[data-wizard-way="workers"] [data-wizard-recommended]')).toBeTruthy()
 // Each card is drawn with the shared path, never text inside the picture.
 expect(q('[data-wizard-way="tailscale"] [data-way-path]')?.getAttribute('data-phase')).toBe('off')
 // Nothing was written by opening it.
 expect(env.call.mock.calls.map(c=>c[0]).filter(m=>/set|connect|start/.test(m)&&m!=='connection.status')).toEqual([])
})

test('the cards are one radio group: the Free link starts selected and the footer goes on with the selection',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 const checked=()=>[...wizard().querySelectorAll<HTMLElement>('[role="radio"][aria-checked="true"]')].map(c=>c.dataset.wizardWay)
 expect(checked()).toEqual(['workers'])
 // No card has a button of its own: the footer holds the one main action.
 expect(q('[data-wizard-choose] [role="radiogroup"] button')).toBeNull()
 expect(next().textContent).toContain(t('wizard.use').replace('{way}',t('wizard.way.workers')))
 // Only the selected card is a tab stop; the arrows move the selection.
 expect(card('workers').tabIndex).toBe(0)
 expect(card('tailscale').tabIndex).toBe(-1)
 card('workers').dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowLeft',bubbles:true}));await settle()
 expect(checked()).toEqual(['tailscale'])
 expect(document.activeElement).toBe(card('tailscale'))
 card('tailscale').dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true}));await settle()
 expect(checked()).toEqual(['workers'])
 card('cloudflare-domain').dispatchEvent(new KeyboardEvent('keydown',{key:' ',bubbles:true}));await settle()
 expect(checked()).toEqual(['cloudflare-domain'])
 expect(next().textContent).toContain(t('wizard.way.cloudflare-domain'))
 // Selecting writes nothing and stays on Choose.
 expect(wizard().dataset.wizardStep).toBe('choose')
 expect(env.call.mock.calls.map(c=>c[0]).filter(m=>/set|connect|start/.test(m)&&m!=='connection.status')).toEqual([])
 next().click();await settle()
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(wizard().dataset.wizardCurrent).toBe('cloudflare-domain')
})

test('More ways holds NetBird and IPv6, and IPv6 needs the risk acknowledged',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 expect(q('[data-wizard-way="ipv6"]')).toBeNull()
 q('[data-wizard-choose] [data-use-more]')!.click();await settle()
 expect(q('[data-wizard-ipv6-ack]')).toBeNull()
 card('ipv6').click();await settle()
 expect(card('ipv6').getAttribute('aria-checked')).toBe('true')
 expect(next().disabled).toBe(true)
 q('[data-wizard-ipv6-ack]')!.click();await settle()
 expect(next().disabled).toBe(false)
 next().click();await settle()
 expect(wizard().dataset.wizardStep).toBe('connect')
 // IPv6 opens no Apps: three steps in all.
 expect(q('[data-wizard-progress]')!.textContent).toContain('2')
 expect(q('[data-wizard-progress]')!.textContent).toContain('3')
})

test('the Free link connects right inside the wizard and Back returns to the cards',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 await use('workers')
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(q('[data-wizard-connect] [data-free-token]')).toBeTruthy()
 q('[data-wizard-back]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('choose')
})

test('Only my devices uses the shared form with its sign-in action and Back in the wizard footer',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 await use('tailscale')
 const form=q('[data-wizard-connect] [data-connect-form]')!
 expect(form.getAttribute('data-connect-form')).toBe('tailscale')
 expect(form.querySelector('[data-ts-connect]')).toBeNull()
 expect(form.querySelector('[data-ts-key]')).toBeTruthy()
 const signIn=next()
 expect(signIn.textContent).toBe(t('provider.login.tailscale'))
 expect(signIn.disabled).toBe(false)
 expect(q('[data-wizard-footer]')!.querySelectorAll('[data-wizard-next]')).toHaveLength(1)
 signIn.click();await settle()
 expect(env.call).toHaveBeenCalledWith('connection.connect',{provider:'tailscale'})
 expect(env.call.mock.calls.filter(c=>c[0]==='connection.connect')).toHaveLength(1)
 // The way's path is the step's picture.
 expect(q('[data-wizard-step="connect"] [data-wizard-hero] [data-way-path]')).toBeTruthy()
 q('[data-wizard-footer] [data-wizard-back]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('choose')
 // The way the user went on with stays selected.
 expect(card('tailscale').getAttribute('aria-checked')).toBe('true')
})

test('Add a way resumes a sign-in still waiting at Connect, and moves on to the Apps once it is on',async()=>{
 env=fixture([{...connected('tailscale'),state:'needs-login'}],{
  'connection.check':p=>({provider:p.provider,state:'ok',checkedAt:'2026-10-26T08:00:00Z'}),
  'apps.workersstatus':()=>({connected:false}),
  'apps.list':()=>({private:{available:true},public:{available:false},apps:[app('convertx')]}),
  'apps.inspectdsm':()=>({scope:'private'}),
 });await settle()
 await openWizard()
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(wizard().dataset.wizardCurrent).toBe('tailscale')
 expect(q('[data-wizard-waiting]')).toBeTruthy()
 env.setStatus([connected('tailscale')])
 env.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('apps')
 // The dialog stays open even though a way is on now; the Home behind it shows its lane.
 expect(wizard()).toBeTruthy()
 expect(env.page('status').querySelector('[data-overview] [data-lane-id="tailscale"]')).toBeTruthy()
 // DSM goes already: nothing left to review.
 expect(q('[data-wizard-dsm]')!.dataset.dsmState).toBe('on')
})

test('the Free link Apps step: video unticked, DSM goes with the Apps, one approval, then the addresses',async()=>{
 const approvals:Record<string,unknown>[]=[]
 env=fixture([],{
  'apps.workersstatus':()=>({connected:true,subdomain:'acme'}),
  'apps.list':()=>({private:{available:false},public:{available:false},relay:{connected:true,subdomain:'acme'},apps:approvals.length
   ?[app('convertx',{relay:{state:'active',url:'https://convertx.acme.workers.dev'}}),app('jellyfin',{media:'high'})]
   :[app('convertx'),app('jellyfin',{media:'high'})]}),
  'apps.inspectdsm':()=>({scope:'public',dsm:{hostname:'dsm.acme.workers.dev',port:5001,pin},token:'d1'}),
  'apps.inspect':p=>({ready:true,package:p.package,name:'Convertx',port:28300,protection:{},pin,token:'a1',hostname:'convertx.acme.workers.dev',dsm:{hostname:'dsm.acme.workers.dev',port:5001,pin}}),
  'apps.setrelay':p=>{approvals.push(p);return {results:[{package:'convertx',ok:true}]}},
 });await settle()
 // The Free link is on: Add a way, and the footer offers to continue with it, which goes to the Apps.
 await openWizard()
 expect(q('[data-wizard-way="workers"] [data-wizard-already]')).toBeTruthy()
 expect(next().textContent).toContain(t('wizard.continueWith').replace('{way}',t('wizard.way.workers')))
 await use('workers')
 expect(wizard().dataset.wizardStep).toBe('apps')
 const box=(pkg:string)=>q(`[data-wizard-app="${pkg}"] [role="checkbox"]`)!.getAttribute('aria-checked')
 expect(box('convertx')).toBe('true')
 expect(box('jellyfin')).toBe('false')
 expect(q('[data-wizard-app="jellyfin"] [data-wizard-app-media]')).toBeTruthy()
 expect(q('[data-wizard-dsm]')!.dataset.dsmState).toBe('with-apps')
 expect(q('[data-wizard-dsm] [data-protection-checks]')).toBeTruthy()
 q('[data-wizard-footer] [data-wizard-next]')!.click();await settle()
 const dialog=document.body.querySelector<HTMLElement>('[data-apps-approval]')!
 expect(dialog.querySelector('[data-approve-app="convertx"]')).toBeTruthy()
 expect(dialog.querySelector('[data-approve-dsm]')).toBeTruthy()
 dialog.querySelector<HTMLButtonElement>('[data-approve-confirm]')!.click();await settle()
 expect(approvals).toHaveLength(1)
 expect(approvals[0]).toMatchObject({packages:['convertx'],enabled:true,approvals:{convertx:{token:'a1',port:28300,dsm:{port:5001}}}})
 expect(wizard().dataset.wizardStep).toBe('done')
 const addresses=[...wizard().querySelectorAll<HTMLElement>('[data-wizard-address]')].map(a=>a.querySelector('input')?.value)
 expect(addresses).toEqual(['https://dsm.acme.workers.dev','https://convertx.acme.workers.dev'])
 expect(q('[data-wizard-test]')!.textContent).toContain(t('wizard.test.open'))
})

test('IPv6 that is on ends with the phone IPv6 check and the outside check',async()=>{
 env=fixture([connected('ipv6',{endpoints:[{url:'https://[2001:db8::10]:5001',public:true,localPort:5001}]})],{'apps.workersstatus':()=>({connected:false})});await settle()
 await openWizard()
 q('[data-wizard-choose] [data-use-more]')!.click();await settle()
 // A way already on needs no acknowledgement to continue.
 expect(q('[data-wizard-ipv6-ack]')).toBeNull()
 await use('ipv6')
 expect(wizard().dataset.wizardStep).toBe('done')
 expect(q('[data-wizard-address] input')?.getAttribute('value')??q<HTMLInputElement>('[data-wizard-address] input')!.value).toBe('https://[2001:db8::10]:5001')
 expect(q('[data-wizard-test-ipv6] a')!.getAttribute('href')).toBe(TEST_SITES.phoneIpv6)
 expect(q('[data-wizard-test-outside]')!.getAttribute('href')).toBe(TEST_SITES.outsideIpv6)
})

test('Compare all ways closes the dialog and opens the folded comparison',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 const notes=()=>env!.page('status').querySelector('[data-choice-notes] [aria-expanded]')!.getAttribute('aria-expanded')
 expect(notes()).toBe('false')
 q('[data-wizard-compare]')!.click();await settle()
 expect(document.body.querySelector('[data-setup-wizard]')).toBeNull()
 expect(notes()).toBe('true')
})

test('the wizard is a dialog: it opens on the cards, its header close closes it, and nothing was written',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle()
 expect(document.body.querySelector('[data-setup-wizard]')).toBeNull()
 await openWizard()
 expect(wizard().closest('[role="dialog"]')).toBeTruthy()
 expect(wizard().dataset.wizardStep).toBe('choose')
 // Close lives in the header only.
 expect(wizard().querySelector('[data-wizard-footer] [data-wizard-cancel]')).toBeNull()
 wizard().closest('[role="dialog"]')!.querySelector<HTMLButtonElement>('header [data-dialog-close]')!.click();await settle()
 expect(document.body.querySelector('[data-setup-wizard]')).toBeNull()
 expect(env.call.mock.calls.map(c=>c[0]).filter(m=>/set|connect|start/.test(m)&&m!=='connection.status')).toEqual([])
})

test('App URLs Set up a way opens the same wizard',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false}),'apps.list':()=>({private:{available:false},public:{available:false},apps:[app('convertx')]})});await settle()
 await env.nav('App URLs')
 env.page('apps').querySelector<HTMLElement>('[data-apps-setup]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('choose')
 expect(card('workers').getAttribute('aria-checked')).toBe('true')
})

test('the temporary address is one quiet row under the cards: confirm, Start, then its address and the Free link',async()=>{
 env=fixture([],{'apps.workersstatus':()=>({connected:false}),'connection.check':p=>({provider:p.provider,state:'ok',externalVerified:true,checkedAt:'2026-10-26T08:00:00Z'})});await settle();await openWizard()
 const row=q('[data-wizard-temporary]')!
 // Under the three cards, above More ways, and never a card or a recommendation.
 const choose=q('[data-wizard-choose]')!
 const order=[...choose.children]
 expect(order.indexOf(row)).toBe(order.indexOf(choose.querySelector('[role="radiogroup"]')!)+1)
 expect(row.querySelector('[role="radio"],[data-wizard-recommended]')).toBeNull()
 expect(row.textContent).toContain(t('wizard.temporary.row'))
 row.querySelector<HTMLButtonElement>('[data-wizard-temporary-open]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(wizard().dataset.wizardCurrent).toBe('temporary')
 // The confirm: the address that changes and who reaches DSM, then one Start.
 const facts=q('[data-temporary-facts]')!
 expect(facts.textContent).toContain(t('temporary.changes'))
 expect(facts.textContent).toContain(t('temporary.anyone'))
 expect(q('[data-wizard-step="connect"] [data-wizard-hero] [data-way-path]')?.getAttribute('data-way')).toBe('cloudflare-quick')
 expect(next().textContent).toContain(t('wizard.temporary.start'))
 expect(env.call.mock.calls.some(c=>c[0]==='connection.connect')).toBe(false)
 env.setStatus([{...connected('cloudflare',{mode:'quick'}),state:'connecting'}])
 next().click();await settle()
 expect(env.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'quick',confirmExposure:true})
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(q('[data-wizard-waiting]')!.textContent).toBe(t('wizard.temporary.starting'))
 env.setStatus([connected('cloudflare',{mode:'quick',endpoints:[{url:'https://fixture-quick.trycloudflare.com',public:true,localPort:5000}]})])
 env.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('done')
 expect(q<HTMLInputElement>('[data-wizard-address] input')!.value).toBe('https://fixture-quick.trycloudflare.com')
 expect(q('[data-wizard-address] [data-url-qr]')).toBeTruthy()
 expect(q('[data-wizard-temporary-hint]')!.textContent).toContain(t('wizard.temporary.stable'))
 // Use Free link goes back to the cards with the Free link selected.
 q('[data-wizard-use-free-link]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('choose')
 expect(card('workers').getAttribute('aria-checked')).toBe('true')
 // The row now shows the address that runs.
 expect(q('[data-wizard-temporary-open]')!.textContent).toContain(t('wizard.temporary.show'))
})

test('no temporary row while Cloudflare runs an own domain',async()=>{
 env=fixture([connected('cloudflare',{mode:'domain',shareUrl:'https://nas.example.com'})],{'apps.workersstatus':()=>({connected:false})});await settle();await openWizard()
 expect(q('[data-wizard-choose]')).toBeTruthy()
 expect(q('[data-wizard-temporary]')).toBeNull()
})

for(const way of ['cloudflare-domain','tailscale'] as const)test(`wizard ${way} keeps certificate recovery inside its connect step`,async()=>{
 const provider=way==='tailscale'?'tailscale':'cloudflare'
 const failed={...connected(provider,{mode:provider==='cloudflare'?'domain':undefined}),state:'failed' as const,code:'pin_proxy_required'}
 env=fixture([failed],{'apps.inspectdsm':()=>({scope:provider==='tailscale'?'private':'public',dsm:{port:5001,pin},token:'changed'})})
 await settle();await openWizard();await use(way)
 const review=q('[data-wizard-connect] [data-dsm-review-open]')
 expect(review).not.toBeNull()
 review!.click();await settle()
 expect(env.call).toHaveBeenCalledWith('apps.inspectdsm',{scope:provider==='tailscale'?'private':'public'})
 expect(document.querySelector('[data-apps-approval]')).not.toBeNull()
})

test('Connect waits while a new way settles, then moves on by itself once its check passes',async()=>{
 let check:Record<string,unknown>={state:'starting',code:'not_connected'}
 env=fixture([{...connected('tailscale'),state:'needs-login'}],{
  'connection.check':p=>({provider:p.provider,checkedAt:'2026-10-26T08:00:00Z',...check}),
  'apps.workersstatus':()=>({connected:false}),
  'apps.list':()=>({private:{available:true},public:{available:false},apps:[app('convertx')]}),
  'apps.inspectdsm':()=>({scope:'private'}),
 });await settle()
 await openWizard()
 env.setStatus([connected('tailscale')])
 env.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 // Running but not yet working: the step stays, setting up, never failed, no modal.
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(q('[data-wizard-connect] [data-setting-up]')!.textContent).toContain(t('connect.settling'))
 expect(q('[data-wizard-connect]')!.textContent).not.toContain(t('connect.check.failed'))
 expect(q('[data-wizard-hero] [data-way-path]')?.getAttribute('data-phase')).toBe('connecting')
 expect(document.body.querySelectorAll('[role="alertdialog"]')).toHaveLength(0)
 check={state:'ok'}
 q<HTMLButtonElement>('[data-connection-check] button')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('apps')
})

test('a way whose check still fails after the settling window names the reason and may go on',async()=>{
 env=fixture([{...connected('tailscale'),state:'needs-login'}],{
  'connection.check':p=>({provider:p.provider,state:'failed',code:'not_connected',checkedAt:'2026-10-26T08:00:00Z'}),
  'apps.workersstatus':()=>({connected:false}),
  'apps.list':()=>({private:{available:true},public:{available:false},apps:[app('convertx')]}),
  'apps.inspectdsm':()=>({scope:'private'}),
 });await settle()
 await openWizard()
 env.setStatus([connected('tailscale')])
 env.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(q('[data-connection-check]')!.textContent).toContain(t('connect.check.code.not_connected'))
 expect(q('[data-wizard-hero] [data-way-path]')?.getAttribute('data-phase')).toBe('error')
 expect(next().textContent).toContain(t('wizard.connect.continueAnyway'))
 next().click();await settle()
 expect(wizard().dataset.wizardStep).toBe('apps')
})

for(const asynchronous of [false,true])for(const code of ['netbird_cloudflare_in_use','cloudflare_login_failed'])test(`Cloudflare wizard shows a refused start inside the dialog: ${code}, asynchronous=${asynchronous}`,async()=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 env=fixture([],{'apps.workersstatus':()=>({connected:false}),'connection.connect':()=>{if(asynchronous)return {provider:'cloudflare',state:'failed',code,mode:'domain'};throw {code}}});await settle()
 await openWizard();await use('cloudflare-domain')
 const input=q<HTMLInputElement>('[data-connect-form="cloudflare"] input')!
 input.value='nas';input.dispatchEvent(new Event('input',{bubbles:true}))
 q('[data-cf-confirm]')!.click();await settle()
 next().click();await settle()
 expect(env.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'nas',confirmExposure:true})
 expect(wizard().dataset.wizardStep).toBe('connect')
 expect(q('[data-wizard-error]')?.textContent).toContain(t(code==='netbird_cloudflare_in_use'?'dependency.changeBlocked':'cloudflare.code.cloudflare_login_failed'))
 if(code==='netbird_cloudflare_in_use'){
  q<HTMLButtonElement>('[data-wizard-error] button')!.click();await settle()
  expect(document.querySelector('[data-setup-wizard]')).toBeNull()
  expect(env.page('netbird').style.display).not.toBe('none')
 }
})
