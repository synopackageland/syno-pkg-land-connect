// The DSM-style shell shows one sidebar entry per feature this build provides.
// Connect ships every page in one product; a page still gates its own actions
// (for example the connection pages show administrator guidance to other
// accounts instead of the settings themselves).
export type ShellPage = 'status' | 'apps' | 'cloudflare' | 'tailscale' | 'netbird' | 'ipv6' | 'settings'

const ORDER: readonly ShellPage[] = ['status', 'apps', 'cloudflare', 'tailscale', 'netbird', 'ipv6', 'settings']

/** 'status' is always first and always available. */
export function shellPages(): ShellPage[] {
  return [...ORDER]
}
