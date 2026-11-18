interface Env {
  TUNNEL: DurableObjectNamespace<import('./tunnel.ts').Tunnel>;
  RELAY_SECRET: string;
}
