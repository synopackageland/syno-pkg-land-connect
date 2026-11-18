// The addresses a connection serves, each once: its endpoints, then its share link.
import type {ConnectionStatus} from '../context'

export function addressesOf(status:ConnectionStatus|undefined):string[] {
 return [...new Set([...(status?.endpoints??[]).map(e=>e.url),...(status?.shareUrl?[status.shareUrl]:[])])]
}
