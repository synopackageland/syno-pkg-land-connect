// The tabs of a connected provider page: the page's own tabs, the most used
// first, then Advanced. A page keeps a tab to one mode of the running
// connection by leaving it out of its computed list.
import type {InjectionKey} from 'vue'
// list: the tab is a list that fills it, not a settings page.
export interface PageTab {value:string;label:string;list?:boolean}
/** The provider's documentation, for a risk's or a field's "Learn more". */
export const learnMoreKey:InjectionKey<string|undefined>=Symbol('learnMore')
/** Each provider page's one "Learn more" link, at the foot of the page. */
export const LEARN_MORE:Record<string,string>={
 cloudflare:'https://developers.cloudflare.com/tunnel/get-started/',
 tailscale:'https://tailscale.com/kb/1017/install',
 netbird:'https://docs.netbird.io/manage/access-control',
 ipv6:'https://kb.synology.com/DSM/help/DSM/AdminCenter/connection_network_ipv6',
}
