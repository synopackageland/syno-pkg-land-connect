(() => {
  const marker = %s, target = %s, storageKey = %s;
  const authRequests = new WeakSet();
  let fragment = location.hash, leaving = false, polls = 0;
  // DSM's login router owns its hash. Keep the App fragment in this tab only,
  // remove it before DSM boots, and restore it on the fixed SDK handoff.
  try {
    const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null');
    if (saved?.target === target) fragment = saved.fragment || fragment;
    if (fragment) sessionStorage.setItem(storageKey, JSON.stringify({target, fragment}));
  } catch {}
  if (location.hash) history.replaceState(history.state, '', location.pathname + location.search);
  const resume = (complete = false) => {
    if (leaving || (!complete && !document.cookie.split(';').some(v => v.trim() === marker))) return false;
    leaving = true;
    document.cookie = marker.split('=')[0] + '=; Path=/; Max-Age=0; Secure; SameSite=Lax';
    try { sessionStorage.removeItem(storageKey); } catch {}
    location.replace(target + fragment);
    return true;
  };
  const isAuth = value => {
    try {
      const u = new URL(value, location.href);
      return u.origin === location.origin && ['/webapi/auth.cgi', '/webapi/entry.cgi', '/webapi/entry.cgi/SYNO.API.Auth'].includes(u.pathname);
    } catch { return false; }
  };
  const originalFetch = window.fetch;
  window.fetch = function(input) {
    const auth = isAuth(input?.url ?? input);
    return Reflect.apply(originalFetch, this, arguments).then(response =>
      auth && resume(response.headers.get('X-Connect-Login-Complete') === '1') ? new Promise(() => {}) : response);
  };
  const open = XMLHttpRequest.prototype.open, send = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function(method, url) {
    if (isAuth(url)) authRequests.add(this);
    if (authRequests.has(this)) this.addEventListener('readystatechange', event => {
      if (this.readyState >= 2 && resume(this.getResponseHeader('X-Connect-Login-Complete') === '1')) {
        event.stopImmediatePropagation();
        this.abort();
      }
    }, true);
    return Reflect.apply(open, this, arguments);
  };
  XMLHttpRequest.prototype.send = function() {
    if (authRequests.has(this)) {
      const previous = this.onreadystatechange;
      this.onreadystatechange = function(event) {
        if (this.readyState === 4 && resume(this.getResponseHeader('X-Connect-Login-Complete') === '1')) {
          event.stopImmediatePropagation();
          return;
        }
        if (previous) return Reflect.apply(previous, this, arguments);
      };
    }
    return Reflect.apply(send, this, arguments);
  };
  const timer = setInterval(() => { if (resume() || ++polls >= 1200) clearInterval(timer); }, 250);
})()
