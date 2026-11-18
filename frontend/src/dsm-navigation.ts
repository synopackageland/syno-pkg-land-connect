import type {Context} from '@synopackageland/sdk'
type DSM={SDS:{AppLaunch:(id:string,params:Record<string,string>)=>unknown;Config:{FnMap:Record<string,unknown>};StatusNotifier:{isAppEnabled:(id:string)=>boolean;isAppLaunchable:(id:string)=>boolean}}}
// Fixed, read-only navigation. This does not apply network/firewall settings.
function open(ctx:Context,fn:string,tab?:string):boolean {
 try{
  if(ctx.host?.platform().host!=='dsm')return false
  const dsm=(window as unknown as {SYNO?:DSM}).SYNO?.SDS,id='SYNO.SDS.AdminCenter.Application'
  if(!dsm?.Config?.FnMap?.[id]||typeof dsm.AppLaunch!=='function'||!dsm.StatusNotifier?.isAppEnabled(id)||!dsm.StatusNotifier.isAppLaunchable(id))return false
  dsm.AppLaunch(id,tab?{fn,tab}:{fn});return true
 }catch{return false}
}
export const openNetworkSettings=(ctx:Context)=>open(ctx,'SYNO.SDS.AdminCenter.Network.Main','InterfaceTab')
export const openFirewallSettings=(ctx:Context)=>open(ctx,'SYNO.SDS.AdminCenter.Security.Main','FirewallTab')
// Control Panel > External Access: QuickConnect is its first tab, DDNS has its own.
export const openQuickConnectSettings=(ctx:Context)=>open(ctx,'SYNO.SDS.AdminCenter.PublicAccess.Main')
export const openDdnsSettings=(ctx:Context)=>open(ctx,'SYNO.SDS.AdminCenter.PublicAccess.Main','ddnstab')
