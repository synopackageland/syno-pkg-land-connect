// The self-hosted NetBird server on the NetBird page: connectd runs it and
// its one setup job; the page only reads its status and asks for changes.
import type {SupervisionStatus} from '../context'
import type {DependencyReport} from './dependency'
import type {NameConflict} from './nameConflict'
import type {StepListItem} from '@synopackageland/ui'

export const serverApi={status:'netbirdserver.status',setup:'netbirdserver.setup',disable:'netbirdserver.disable',credentials:'netbirdserver.credentials'} as const

export type ServerState='off'|'working'|'starting'|'running'|'failed'|'waiting'|'backoff'
export interface ServerStep {id:string;state:'pending'|'running'|'done'|'failed';code?:string;conflict?:NameConflict}
export interface ServerStatus {
 job?:{step:string;startedAt:string}
 resumeRequired?:boolean
 supervision?:SupervisionStatus
 dependencies?:DependencyReport
 state:ServerState
 prerequisite?:string
 zone?:string
 steps?:ServerStep[]
 code?:string
 hostname?:string
 managementUrl?:string
 adminEmail?:string
 joined?:boolean
 version?:string
 peers?:number
 peersOnline?:number
 uptimeSeconds?:number
 memoryBytes?:number
 dataBytes?:number
 cleanupHostname?:string
}

export const SERVER_STEPS=['prerequisites','binary','config','route','bridge','start','account','setupkey','check','join'] as const

// Codes the page explains, each with the next thing to do.
export const SERVER_CODES=[
 'cloudflare_connecting','cloudflare_not_connected','cloudflare_own_domain_required','cloudflare_reconnect_required',
 'cloudflare_name_taken','cloudflare_route_failed','cloudflare_busy',
 'netbird_server_bridge_missing','netbird_server_setup_incomplete','netbird_server_binary_failed','netbird_server_port_in_use','netbird_server_start_failed','netbird_server_not_ready',
 'netbird_server_owner_exists','netbird_server_account_failed','netbird_server_setup_key_failed',
 'netbird_server_grpc_blocked','netbird_server_signal_blocked','netbird_server_unreachable','netbird_server_join_failed','netbird_server_join_pending',
 'netbird_server_workers_required','workers_not_connected','workers_routes_permission','workers_permission_denied','workers_zone_account',
 'workers_route_conflict','workers_name_conflict','workers_token_invalid','workers_unavailable',
 'netbird_server_save_failed','netbird_server_disabled','netbird_server_off','netbird_server_leave_failed','netbird_server_busy','netbird_server_unavailable','netbird_server_failed',
] as const

// Missing Cloudflare, or its Workers token, is fixed on the Cloudflare page.
export const CLOUDFLARE_CODES=new Set(['cloudflare_not_connected','cloudflare_own_domain_required','cloudflare_reconnect_required',
 'netbird_server_workers_required','workers_not_connected','workers_routes_permission','workers_permission_denied','workers_zone_account','workers_token_invalid'])

// The zone's gRPC setting has no API: while the check waits for it, the
// page links the zone's Network settings, and setup continues by itself.
export const GRPC_BLOCKED='netbird_server_grpc_blocked'
export function grpcSettingsUrl(zone:string):string{
 return `https://dash.cloudflare.com/?to=/:account/${encodeURIComponent(zone)}/network`
}
export function waitingForGRPC(steps:ServerStep[]|undefined):boolean{
 return !!steps?.some(step=>step.id==='check'&&step.state==='running'&&step.code===GRPC_BLOCKED)
}

export function validStatus(v:unknown):v is ServerStatus{
 return !!v&&typeof v==='object'&&['off','working','starting','running','failed','waiting','backoff'].includes((v as ServerStatus).state)
}

// The code's message key, or the generic one for a code the page does not know.
export function codeKey(code:string|undefined):string{
 return 'nbserver.code.'+((SERVER_CODES as readonly string[]).includes(code??'')?code:'netbird_server_failed')
}

// One StepList row per step; the failed row says why and what to do next.
export function stepItems(steps:ServerStep[]|undefined,t:(key:string)=>string):StepListItem[]{
 const status={pending:'pending',running:'current',done:'done',failed:'error'} as const
 return (steps??[]).map(step=>({
  title:t('nbserver.step.'+step.id),
  status:status[step.state]??'pending',
  ...(step.state==='failed'?{description:t(codeKey(step.code))}:step.state==='running'&&step.code===GRPC_BLOCKED?{description:t('nbserver.guide.title')}:{}),
 }))
}

// Polling is quick while something changes and slow while it holds still.
export function pollDelay(state:ServerState|undefined,checking=false):number{
 return checking||state==='working'||state==='starting'?2000:15000
}
