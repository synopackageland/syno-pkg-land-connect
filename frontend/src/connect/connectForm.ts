// What every provider's connect form shares: start the provider through the
// shared state and tell the host (the provider page or the setup wizard)
// when this form's start was sent and when the provider came up.
import {watch} from 'vue'
import {useConnectState} from './state'

export interface ConnectFormEvents {submitted():void;connected():void}
/**
 * A form's one primary action. With the footer prop the form hides its own
 * button and the host (the setup wizard) draws it as its footer's main
 * action; run resolves once the action is done, so the footer shows it busy.
 */
export interface FormPrimary {label:string;disabled:boolean;loading:boolean;run():Promise<unknown>}

export function useConnectForm(provider:string,events:ConnectFormEvents){
 const s=useConnectState()
 // sent: a start from this form waits for the provider; pending: its call is
 // still out, so submitted comes before connected.
 let sent=false,pending=false
 watch(()=>s.connectionFor(provider)?.state,state=>{
  if(!sent||pending)return
  if(state==='connected'){sent=false;events.connected()}
  else if(state==='disconnected')sent=false
 })
 async function start(params:Record<string,unknown>,serviceIds?:string[],signIn?:boolean){
  if(s.busy.value)return
  sent=true;pending=true
  try{await s.connectProvider(params,serviceIds,signIn)}finally{pending=false}
  events.submitted()
  const state=s.connectionFor(provider)?.state
  if(state==='connected'){sent=false;events.connected()}
  else if(state==='disconnected')sent=false
 }
 return {s,start}
}
