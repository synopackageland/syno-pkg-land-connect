// Providers that finish connecting with a browser sign-in. A browser opens a
// new tab only within the user's click, but the sign-in address arrives after
// the connect call, so a blank tab opens at the click and is sent on once the
// address is known and trusted (D10).
import waitingPageTheme from '@synopackageland/ui/themes/dsm7-embedded-theme.css?inline'

const own = (u: URL, host: string) => u.protocol === 'https:' && u.host === host && !u.username && !u.password

// Cloudflare's page must also send the certificate back through Cloudflare.
const trusted: Record<string, (u: URL) => boolean> = {
  netbird: u => own(u, 'login.netbird.io'),
  tailscale: u => own(u, 'login.tailscale.com') && u.pathname.startsWith('/a/'),
  cloudflare: u => {
    if (!own(u, 'dash.cloudflare.com') || u.pathname !== '/argotunnel') return false
    try {
      return own(new URL(u.searchParams.get('callback') || ''), 'login.cloudflareaccess.org')
    } catch {
      return false
    }
  },
}

// needsSignIn tells whether this start finishes in the browser: Tailscale or
// NetBird without a key, and Cloudflare setting up a new own domain.
export function needsSignIn(params: Record<string, unknown>): boolean {
  if (params.provider === 'tailscale' || params.provider === 'netbird') return !params.token
  return params.provider === 'cloudflare' && params.mode === 'domain' && !!params.subdomain
}

// trustedLoginUrl returns the address only when it is the provider's own sign-in page.
export function trustedLoginUrl(provider: string, raw: string | undefined, userCodeOrConfig?:string|{loginServer?:string;loginOrigin?:string;managementUrl?:string;userCode?:string}, origins:string[]=[]): string {
  if (!raw || raw.length>8192 || /[^\x21-\x7e]|\\/.test(raw)) return ''
  const config=typeof userCodeOrConfig==='object'?userCodeOrConfig:undefined
  if(provider==='tailscale'){
    if(!config?.loginServer)return /^https:\/\/login\.tailscale\.com\/a\/[A-Za-z0-9]{1,128}$/.test(raw)?raw:''
    const origin=config.loginOrigin||config.loginServer
    try{
      const configured=new URL(origin),u=new URL(raw)
      if(!/^https:\/\/[a-z0-9.-]+(?::[0-9]+)?$/.test(origin)||!raw.startsWith(origin+'/'))return ''
      if(configured.protocol!=='https:'||configured.pathname!=='/'||configured.search!==''||configured.hash!==''||configured.username!==''||configured.hostname.endsWith('.')||u.username||u.password||u.hash||u.origin!==configured.origin||/%(?:2f|5c|2e|[01][0-9a-f]|7f)/i.test(raw)||/(?:^|\/)\.\.?(?:\/|$)/.test(raw))return ''
      return raw
    }catch{return ''}
  }
  try {
    const u=new URL(raw)
    if(provider==='netbird') {
      if(/^https:\/\/login\.netbird\.io\/activate\?user_code=[A-Z]{4}-[A-Z]{4}$/.test(raw)) return raw
      if(raw==='https://login.netbird.io/activate' && /^[A-Z]{4}-[A-Z]{4}$/.test(typeof userCodeOrConfig==='string'?userCodeOrConfig:config?.userCode??'')) return raw
      if(u.protocol!=='https:'||u.username||u.password||raw.includes('#')||raw.includes('%')||!u.pathname.startsWith('/')||raw.split(/[/?]/).some(part=>part==='.'||part==='..'))return ''
      if(!/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(u.hostname)||u.hostname.includes('netbird.io'))return ''
      return [...origins,...(config?.managementUrl?[config.managementUrl.replace(/\/$/,'')]:[])].some(origin=>{
       if(!/^https:\/\/[a-z0-9.-]+(?::[0-9]+)?$/.test(origin))return false
       try{return u.origin===new URL(origin).origin}catch{return false}
      })?raw:''
    }
    return trusted[provider]?.(u)?u.href:''
  }catch{return ''}
}

// LoginFallback names the provider whose sign-in tab the browser blocked, or
// the user closed before the sign-in address arrived.
export interface LoginFallback {
  provider: string
  reason: 'blocked' | 'closed'
}

export interface LoginTab {
  provider: string
  tab: Window
}

// LoginPage is the waiting page's text, already in the app language.
export interface LoginPage {
  lang: string
  title: string
  wait: string
  next: string
}

// The waiting tab has its own document: include the pinned UI theme so its
// tokens resolve there too, without depending on the DSM host's styles.
const pageStyle = `
html,body{height:100%;margin:0}
body{display:flex;align-items:center;justify-content:center;padding:24px;box-sizing:border-box;font:14px/1.6 system-ui,-apple-system,"Segoe UI",sans-serif;background:var(--ui-surface);color:var(--ui-text)}
main{max-width:480px;text-align:center}
.spinner{width:32px;height:32px;margin:0 auto 20px;border:3px solid var(--ui-control-track);border-top-color:var(--ui-primary);border-radius:50%;animation:spin 1s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
@media (prefers-reduced-motion:reduce){.spinner{animation-duration:3s}}
h1{margin:0 0 12px;font-size:20px;font-weight:600}
p{margin:0 0 8px}
.next{color:var(--ui-text-secondary)}`

// openLoginTab opens a tab that says the sign-in page is on its way, what it
// waits for and how long; null means the browser blocked it.
export function openLoginTab(provider: string, page: LoginPage): LoginTab | null {
  const tab = window.open('', '_blank')
  if (!tab) return null
  try {
    writeLoginPage(tab.document, page)
  } catch {
    // A tab whose page this one cannot write still takes the sign-in address.
  }
  return {provider, tab}
}

function writeLoginPage(doc: Document, page: LoginPage, failed = false): void {
  const make = (tag: string, text = '', attrs: Record<string, string> = {}) => {
    const el = doc.createElement(tag)
    el.textContent = text
    for (const [name, value] of Object.entries(attrs)) el.setAttribute(name, value)
    return el
  }
  doc.documentElement.lang = page.lang
  doc.body.setAttribute('data-ui-app', '')
  doc.title = page.title
  doc.head.append(make('meta', '', {name: 'viewport', content: 'width=device-width, initial-scale=1'}), make('style', waitingPageTheme), make('style', pageStyle))
  const main = make('main', '', {role: failed ? 'alert' : 'status', 'aria-busy': String(!failed)})
  main.append(
    ...(!failed ? [make('div', '', {class: 'spinner', 'data-login-spinner': '', 'aria-hidden': 'true'})] : []),
    make('h1', page.title),
    make('p', page.wait, {[failed ? 'data-login-error' : 'data-login-wait']: ''}),
    make('p', page.next, {class: 'next', 'data-login-next': ''}),
  )
  doc.body.replaceChildren(main)
}

// sendLoginTab cuts the tab from this page and sends it to the sign-in page.
export function sendLoginTab(login: LoginTab, url: string): void {
  login.tab.opener = null
  login.tab.location.replace(url)
}

export function netbirdPendingOrigin(value:string|undefined):string {
 if(!value||!/^https:\/\/[a-z0-9.-]+(?::[0-9]+)?$/.test(value))return ''
 return trustedLoginUrl('netbird',value+'/',undefined,[value])?value:''
}

// A refused start is visible in the tab the user just opened, too. Disconnect
// it from the opener, but leave the reason on screen until the user closes it.
export function failLoginTab(login: LoginTab, page: LoginPage): void {
  if (login.tab.closed) return
  try {
    writeLoginPage(login.tab.document, page, true)
    login.tab.opener = null
  } catch {
    // The originating form still shows the refusal if this document is gone.
  }
}
