import type {NameConflict} from './connect/nameConflict'
export interface AppStatus {nasName?:string;package: string; version: string; user: string}
export interface SupervisionStatus {state:string;attempts:number;nextRetry?:string;lastExit?:{code:number;reason?:string;permanent?:boolean};reason?:string;log?:string}
export interface ConnectionStatus extends Partial<NameConflict> {
 dsmPortChange?:{from:number;to:number}
 supervision?:SupervisionStatus
 recoveryNotice?:string;cleanupNote?:string;cleanupUrl?:string;
  trustedDsm?: {issuer:string;notAfter:string;certSha256?:string}
 suggestedName?:string;apply?:{phase:string;code?:string;addedPorts?:number[];removedPorts?:number[]};loginServer?:string;loginOrigin?:string;loginDeadline?:string;tailscaleIPs?:string[];attemptId?:number;phase?:string;startedAt?:string;deadline?:string;userCode?:string;loginOrigins?:string[];networkReady?:boolean;netbirdFQDN?:string;netbirdIP?:string;managementUrl?:string;connections?:ConnectionStatus[];serviceIds?:string[];knownLimitation?:string;mode?:string;provider: string; state: 'disconnected'|'connected'|'connecting'|'disconnecting'|'failed'|'stopping'|'needs-login'|'needs-machine-auth'|'unknown'|'waiting'|'backoff'; code?: string; simulation: boolean; shareUrl: string; externalVerified?: boolean; hostname?: string; loggedIn?:boolean;keptIdentity?:boolean; loginUrl?:string; tailnet?:string; magicDNS?:string; mappings?:PortMapping[]; endpoints?:Array<{url:string;public:boolean;localPort:number}>;peersOnline?:number}
export interface ExternalSettings {hostname:string;http_port?:number|null;https_port?:number|null}

export interface PortMapping {localPort:number;protocol:string;publicPort:number}
export interface PortEntry {id:string;name:string;protocol:'http'|'https'|'tcp';port:number|null;source:'dsm'|'custom'|'detected';enabled:boolean;state:'ready'|'disabled'|'needs-attention'|'conflict'}
export interface PortSnapshot {followServices?:boolean;followState?:'off'|'ready'|'unavailable'|'limit';services?:Array<{id:string;name:string;port:number}>;udpSkipped?:boolean;entries:PortEntry[];detection:'ready'|'needs-attention';mappingState:string;revision:number}
