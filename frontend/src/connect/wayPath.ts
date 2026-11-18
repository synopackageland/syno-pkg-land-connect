// The one picture every page uses to explain a way: who connects, through
// what, to the NAS (or one App on it). It is a single ConnectionTopology lane,
// so it shares the Overview's nodes, colours and flow; the words are DOM labels
// (message keys here), never text inside the drawing.
import type {LinkState, TopologyLane} from '@synopackageland/ui'
import type {ConnectionStatus} from '../context'

/** The ways a path can show; the wizard's temporary address is cloudflare-quick. */
export type PathWay = 'workers' | 'cloudflare-domain' | 'cloudflare-quick' | 'tailscale' | 'netbird' | 'ipv6'
/** One status vocabulary for every way: Off, Connecting, On, Turning off, Needs attention, Failed. */
export type WayPhase = 'off' | 'connecting' | 'on' | 'turning-off' | 'attention' | 'error'
/** The nodes a path is drawn from; the page maps each to an icon. */
export type PathNode = 'anyone' | 'devices' | 'internet' | 'cloudflare' | 'workers' | 'tailscale' | 'netbird' | 'nas' | 'app'

/** Who can open it (the source) and what carries it (the relay). */
export const PATH_NODES: Record<PathWay, {source: PathNode; via: PathNode}> = {
  workers: {source: 'anyone', via: 'workers'},
  'cloudflare-domain': {source: 'anyone', via: 'cloudflare'},
  'cloudflare-quick': {source: 'anyone', via: 'cloudflare'},
  tailscale: {source: 'devices', via: 'tailscale'},
  netbird: {source: 'devices', via: 'netbird'},
  // Nothing in between protects it: the reach link carries the warning colour while on.
  ipv6: {source: 'anyone', via: 'internet'},
}

/** The status word's key and the Tag colour, the same on every page. */
export const PHASE_TAG: Record<WayPhase, {label: string; tone: 'neutral' | 'info' | 'success' | 'warning' | 'danger'}> = {
  off: {label: 'path.phase.off', tone: 'neutral'},
  connecting: {label: 'path.phase.connecting', tone: 'info'},
  on: {label: 'path.phase.on', tone: 'success'},
  'turning-off': {label: 'ux.state.disconnecting', tone: 'info'},
  attention: {label: 'path.phase.attention', tone: 'warning'},
  error: {label: 'path.phase.error', tone: 'danger'},
}

/** A provider's reported state in the shared vocabulary. */
export function wayPhase(status: Pick<ConnectionStatus, 'state'> | undefined): WayPhase {
  switch (status?.state) {
    case 'connected': return 'on'
    case 'waiting':case 'backoff':case 'connecting': return 'connecting'
    case 'disconnecting': case 'stopping': return 'turning-off'
    case 'needs-login': case 'needs-machine-auth': return 'attention'
    case 'failed': return 'error'
    default: return 'off'
  }
}

/**
 * Both links for a phase: connecting flows along both (ConnectionTopology
 * animates pending links, and stops under prefers-reduced-motion); on works
 * end to end; turning off flows on the NAS side only, as the Overview lane;
 * a problem colours the NAS side.
 */
export function phaseLinks(way: PathWay, phase: WayPhase): [LinkState, LinkState] {
  switch (phase) {
    case 'connecting': return ['pending', 'pending']
    case 'on': return [way === 'ipv6' ? 'attention' : 'active', 'active']
    case 'turning-off': return ['idle', 'pending']
    case 'attention': return ['idle', 'attention']
    case 'error': return ['idle', 'error']
    default: return ['idle', 'idle']
  }
}

/**
 * The one word a stopped way says on its NAS-side link: the user turned it
 * off, so it reads as turned off and never as a failure. Shown whatever the
 * reason the state is off (a fresh page, a deliberate turn-off, a stop).
 */
export const OFF_LINK_LABEL = 'overview.reach.off'

/** Link labels for a phase: only a stopped way names itself; a failure has no place for a reason here. */
export function phaseLinkLabels(phase: WayPhase): [string?, string?] | undefined {
  return phase === 'off' ? ['', OFF_LINK_LABEL] : undefined
}

export interface PathOptions {
  /** Draws the App as the far end instead of the NAS. */
  app?: string
  /** Overrides the links, e.g. a wizard step that turns the first one green before the second. */
  links?: [LinkState, LinkState]
  /** Short labels on the links; the state word is used when absent, '' shows none. */
  linkLabels?: [string?, string?]
}

export interface PathModel {source: PathNode; via: PathNode; target: PathNode; links: TopologyLane['links']}

/** The three nodes and two links a path draws; the component names and draws them. */
export function pathModel(way: PathWay, phase: WayPhase, options: PathOptions = {}): PathModel {
  const [left, right] = options.links ?? phaseLinks(way, phase)
  const [leftLabel, rightLabel] = options.linkLabels ?? ['', '']
  return {...PATH_NODES[way], target: options.app ? 'app' : 'nas', links: [{state: left, label: leftLabel}, {state: right, label: rightLabel}]}
}
