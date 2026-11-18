import {validLabel} from './freeLink'

export function splitHostname(host: string): {label:string;suffix:string} {
 const dot=host.indexOf('.')
 return dot<0?{label:host,suffix:''}:{label:host.slice(0,dot),suffix:host.slice(dot)}
}

export function hostnameIssue(host:string,reference=host):string {
 const {suffix}=splitHostname(reference)
 const label=suffix&&host.endsWith(suffix)?host.slice(0,-suffix.length):host
 if(!label)return 'required'
 if(label.length>63)return 'length'
 if(!suffix||!validLabel(label))return 'format'
 return ''
}
