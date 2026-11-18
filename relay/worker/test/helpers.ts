// Modified for Connect: fixed tunnel and per-Worker HMAC test credentials
import { signConnectToken } from "../src/token.ts";
import {
	type CreateTunnelResponse,
	decodeFrame,
	encodeFrame,
	encodeHead,
	type Frame,
	FrameType,
	type HeadMessage,
	PROTOCOL_HEADER,
	PROTOCOL_VERSION,
} from "../src/protocol/index.ts";
import { SELF } from "cloudflare:test";

export const API = "https://api.test";
export const PROTOCOL = { [PROTOCOL_HEADER]: String(PROTOCOL_VERSION) };

// One fixed tunnel per Worker, no public creation API
export async function createTunnel(): Promise<CreateTunnelResponse> {
  return {
    id: 'default', url: API,
    connectUrl: `${API}/_spl/relay/connect`,
    token: await signConnectToken('test-secret-that-is-at-least-32-bytes-long', 'default'),
  };
}

export async function connect(tunnel: CreateTunnelResponse): Promise<FakeClient> {
	const response = await SELF.fetch(tunnel.connectUrl.replace(/^ws/, "http"), {
		headers: { upgrade: "websocket", authorization: `Bearer ${tunnel.token}`, ...PROTOCOL },
	});
	if (!response.webSocket) {
		throw new Error(`connect failed: ${response.status} ${await response.text()}`);
	}
	response.webSocket.accept();
	return new FakeClient(response.webSocket);
}

export function publicUrl(tunnel: CreateTunnelResponse, path = "/"): string {
	return tunnel.url.replace("http://", "https://") + path;
}

/** Plays the role of the hostc client over a real tunnel connection. */
export class FakeClient {
	readonly frames: Frame[] = [];
	readonly texts: string[] = [];
	closed: { code: number; reason: string } | null = null;
	private waiters: (() => void)[] = [];

	readonly ws: WebSocket;

	constructor(ws: WebSocket) {
		this.ws = ws;
		ws.binaryType = "arraybuffer";
		ws.addEventListener("message", (event) => {
			if (typeof event.data === "string") {
				this.texts.push(event.data);
			} else {
				this.frames.push(decodeFrame(new Uint8Array(event.data as ArrayBuffer)));
			}
			this.notify();
		});
		ws.addEventListener("close", (event) => {
			this.closed = { code: event.code, reason: event.reason };
			this.notify();
		});
	}

	/** Removes and returns the first frame matching `predicate`, waiting for it if needed. */
	async next(predicate: (frame: Frame) => boolean = () => true, timeoutMs = 2000): Promise<Frame> {
		const deadline = Date.now() + timeoutMs;
		for (;;) {
			const index = this.frames.findIndex(predicate);
			if (index >= 0) {
				return this.frames.splice(index, 1)[0] as Frame;
			}
			if (Date.now() > deadline) {
				throw new Error(
					`timed out waiting for frame; have ${JSON.stringify(this.frames.map((f) => [f.type, f.stream]))}`,
				);
			}
			await this.wait(deadline - Date.now());
		}
	}

	async waitUntil(check: () => boolean, timeoutMs = 2000): Promise<void> {
		const deadline = Date.now() + timeoutMs;
		while (!check()) {
			if (Date.now() > deadline) {
				throw new Error("timed out");
			}
			await this.wait(deadline - Date.now());
		}
	}

	send(type: FrameType, stream: number, payload?: Uint8Array): void {
		this.ws.send(encodeFrame(type, stream, payload));
	}

	head(stream: number, head: Partial<HeadMessage> = {}): void {
		this.send(FrameType.Head, stream, encodeHead({ status: 200, headers: [], body: true, ...head }));
	}

	private wait(ms: number): Promise<void> {
		return new Promise((resolve) => {
			const timer = setTimeout(resolve, Math.max(ms, 0));
			this.waiters.push(() => {
				clearTimeout(timer);
				resolve();
			});
		});
	}

	private notify(): void {
		const waiters = this.waiters;
		this.waiters = [];
		for (const waiter of waiters) {
			waiter();
		}
	}
}

export async function readAll(response: Response): Promise<Uint8Array> {
	return new Uint8Array(await response.arrayBuffer());
}
