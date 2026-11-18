// The services IPv6 opens: a running connection's own, else DSM's web
// service, over HTTPS when DSM serves it.
import type {ConnectionStatus,PortSnapshot} from '../context'

export function ipv6Selection(view:PortSnapshot|undefined,status:ConnectionStatus|undefined):string[] {
 if(status?.serviceIds?.length)return [...status.serviceIds]
 const usable=view?.detection==='ready'?view.entries.filter(e=>e.port!==null&&['ready','disabled'].includes(e.state)):[]
 const dsm=usable.filter(e=>e.source==='dsm')
 const id=(dsm.find(e=>e.protocol==='https')??dsm.find(e=>e.protocol==='http'))?.id
 return id?[id]:[]
}
