// LolMobil relay: lets the phone reach the PC companion from anywhere (like
// Mimic's Rift), on Cloudflare Workers' free plan.
//
// - Serves the mobile web interface (../web) over HTTPS.
// - /host/<room>?auth=<hex>  : the PC companion (one per room).
// - /client/<room>           : phones (up to 8 per room).
//
// Each room is a Durable Object that only forwards messages. Everything the
// phone and the PC exchange is encrypted end to end with a key derived from
// the secret in the pairing link fragment (see internal/relay/crypto.go): the
// relay never sees the secret, the League data or the commands.
//
// Frames:
//   phone -> room : base64 ciphertext (or "ping", answered with "pong")
//   room -> host  : {"t":"open"|"close","c":id} and {"t":"msg","c":id,"d":ciphertext}
//   host -> room  : {"t":"msg","c":id,"d":ciphertext} (or "ping")
//   room -> phone : ciphertext, or {"t":"host","online":bool}

import { DurableObject } from "cloudflare:workers";

export interface Env {
  ROOMS: DurableObjectNamespace<Room>;
  ASSETS: Fetcher;
}

const ROUTE = /^\/(host|client)\/([0-9a-f]{32})$/;
const MAX_CLIENTS = 8;

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const route = ROUTE.exec(url.pathname);
    if (route) {
      if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
        return new Response("WebSocket expected", { status: 426 });
      }
      const stub = env.ROOMS.get(env.ROOMS.idFromName(route[2]));
      return stub.fetch(request);
    }
    if (url.pathname === "/relay-info") {
      return Response.json({ app: "lolmobil-relay", version: 1 }, { headers: { "Cache-Control": "no-store" } });
    }
    return env.ASSETS.fetch(request);
  },
} satisfies ExportedHandler<Env>;

type Attachment = { role: "host" } | { role: "client"; id: string };

async function sha256Hex(text: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export class Room extends DurableObject<Env> {
  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    // Keepalive answered without waking the object.
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
  }

  private host(except?: WebSocket): WebSocket | undefined {
    return this.ctx.getWebSockets("host").find((ws) => ws !== except);
  }

  private clients(): WebSocket[] {
    return this.ctx.getWebSockets("client");
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const isHost = url.pathname.startsWith("/host/");
    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);

    if (isHost) {
      // The first host to connect with a given secret owns the room.
      const auth = url.searchParams.get("auth") ?? "";
      if (!/^[0-9a-f]{64}$/.test(auth)) return new Response("Bad auth", { status: 403 });
      const digest = await sha256Hex(auth);
      const stored = await this.ctx.storage.get<string>("hostAuth");
      if (stored && stored !== digest) return new Response("Forbidden", { status: 403 });
      if (!stored) await this.ctx.storage.put("hostAuth", digest);

      for (const old of this.ctx.getWebSockets("host")) {
        try {
          old.close(4000, "replaced");
        } catch {
          /* already closed */
        }
      }
      this.ctx.acceptWebSocket(server, ["host"]);
      server.serializeAttachment({ role: "host" } satisfies Attachment);
      for (const phone of this.clients()) {
        const a = phone.deserializeAttachment() as Attachment;
        if (a.role === "client") server.send(JSON.stringify({ t: "open", c: a.id }));
        phone.send(JSON.stringify({ t: "host", online: true }));
      }
    } else {
      if (this.clients().length >= MAX_CLIENTS) return new Response("Too many phones", { status: 429 });
      const id = crypto.randomUUID().slice(0, 8);
      this.ctx.acceptWebSocket(server, ["client"]);
      server.serializeAttachment({ role: "client", id } satisfies Attachment);
      const host = this.host();
      server.send(JSON.stringify({ t: "host", online: Boolean(host) }));
      host?.send(JSON.stringify({ t: "open", c: id }));
    }
    return new Response(null, { status: 101, webSocket: client });
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (typeof message !== "string") return;
    const a = ws.deserializeAttachment() as Attachment;
    if (a.role === "client") {
      this.host()?.send(JSON.stringify({ t: "msg", c: a.id, d: message }));
      return;
    }
    let frame: { t?: string; c?: string; d?: string };
    try {
      frame = JSON.parse(message);
    } catch {
      return;
    }
    if (frame.t !== "msg" || typeof frame.d !== "string") return;
    for (const phone of this.clients()) {
      const pa = phone.deserializeAttachment() as Attachment;
      if (pa.role === "client" && pa.id === frame.c) phone.send(frame.d);
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string): Promise<void> {
    this.closed(ws);
    try {
      ws.close(code === 1005 || code === 1006 ? 1000 : code, reason);
    } catch {
      /* already closed */
    }
  }

  async webSocketError(ws: WebSocket): Promise<void> {
    this.closed(ws);
  }

  private closed(ws: WebSocket): void {
    const a = ws.deserializeAttachment() as Attachment | null;
    if (!a) return;
    if (a.role === "client") {
      this.host()?.send(JSON.stringify({ t: "close", c: a.id }));
    } else if (!this.host(ws)) {
      for (const phone of this.clients()) {
        try {
          phone.send(JSON.stringify({ t: "host", online: false }));
        } catch {
          /* closing */
        }
      }
    }
  }
}
