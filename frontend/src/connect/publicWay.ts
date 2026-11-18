// A saved credential is not use. Warn only for a way that is on or retains routes.
import type {AppsView,PublicScope} from './appUrls'
import type {WorkersStatus} from './freeLink'
export function publicWayInUse(scope:PublicScope,view?:AppsView,workers?:WorkersStatus):boolean {
 if(!view&&workers?.usage)return workers.usage[scope]
 const dsm=workers?.dsm??view?.relay?.dsm
 return !!view?.apps.some(a=>a[scope]) || (scope==='public'
  ? !!(view?.public.enabled??view?.public.available)||dsm?.provider==='cloudflare'
  : !!(workers?.connected??view?.relay?.connected)||dsm?.provider==='workers')
}
