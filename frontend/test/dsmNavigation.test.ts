// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {openNetworkSettings,openFirewallSettings,openDdnsSettings,openQuickConnectSettings} from '../src/dsm-navigation'
import {createStandaloneContext, createStandaloneHost} from '@synopackageland/sdk'
afterEach(()=>vi.unstubAllGlobals())
test('only a verified DSM context can request the fixed network interface location',()=>{
 const AppLaunch=vi.fn();vi.stubGlobal('SYNO',{SDS:{AppLaunch,Config:{FnMap:{'SYNO.SDS.AdminCenter.Application':{config:{}}}},StatusNotifier:{isAppEnabled:()=>true,isAppLaunchable:()=>true}}})
 expect(openNetworkSettings(createStandaloneContext({call:vi.fn()}))).toBe(false);expect(AppLaunch).not.toHaveBeenCalled()
 const ctx=createStandaloneContext({call:vi.fn(),fullPage:false,host:createStandaloneHost({host:'dsm',dsmStandalone:false})})
 expect(openNetworkSettings(ctx)).toBe(true)
 expect(AppLaunch).toHaveBeenCalledWith('SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.Network.Main',tab:'InterfaceTab'})
})
test('blocked or missing DSM navigation reports no successful request',()=>{
 const ctx=createStandaloneContext({call:vi.fn(),fullPage:false,host:createStandaloneHost({host:'dsm',dsmStandalone:false})})
 expect(openNetworkSettings(ctx)).toBe(false)
 vi.stubGlobal('SYNO',{SDS:{AppLaunch:vi.fn(),Config:{FnMap:{}},StatusNotifier:{isAppEnabled:()=>false,isAppLaunchable:()=>false}}})
 expect(openNetworkSettings(ctx)).toBe(false)
})

test('the firewall action opens its own fixed DSM settings tab',()=>{
 const AppLaunch=vi.fn();vi.stubGlobal('SYNO',{SDS:{AppLaunch,Config:{FnMap:{'SYNO.SDS.AdminCenter.Application':{}}},StatusNotifier:{isAppEnabled:()=>true,isAppLaunchable:()=>true}}})
 const ctx=createStandaloneContext({call:vi.fn(),host:createStandaloneHost({host:'dsm',dsmStandalone:false})})
 expect(openFirewallSettings(ctx)).toBe(true)
 expect(AppLaunch).toHaveBeenCalledWith('SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.Security.Main',tab:'FirewallTab'})
})

test('QuickConnect and DDNS open their tabs of Control Panel > External Access',()=>{
 const AppLaunch=vi.fn();vi.stubGlobal('SYNO',{SDS:{AppLaunch,Config:{FnMap:{'SYNO.SDS.AdminCenter.Application':{}}},StatusNotifier:{isAppEnabled:()=>true,isAppLaunchable:()=>true}}})
 const ctx=createStandaloneContext({call:vi.fn(),host:createStandaloneHost({host:'dsm',dsmStandalone:false})})
 expect(openDdnsSettings(ctx)).toBe(true)
 expect(AppLaunch).toHaveBeenLastCalledWith('SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.PublicAccess.Main',tab:'ddnstab'})
 // QuickConnect is the first tab, which DSM opens without one named.
 expect(openQuickConnectSettings(ctx)).toBe(true)
 expect(AppLaunch).toHaveBeenLastCalledWith('SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.PublicAccess.Main'})
 expect(openQuickConnectSettings(createStandaloneContext({call:vi.fn()}))).toBe(false)
})
