// Minimal MQTT 3.1.1 client over WebSocket (QoS 0, retained messages, last
// will, keepalive): the phone side of remote access through public brokers.
// Same topics as internal/relay/mqtt.go.

const enc = new TextEncoder();
const dec = new TextDecoder();

export const DEFAULT_BROKERS = [
  "wss://broker.emqx.io:8084/mqtt",
  "wss://broker.hivemq.com:8884/mqtt",
  "wss://test.mosquitto.org:8081/mqtt",
];

function str(s) {
  const b = enc.encode(s);
  return [b.length >> 8, b.length & 0xff, ...b];
}

function bytes(b) {
  return [b.length >> 8, b.length & 0xff, ...b];
}

function packet(header, body) {
  const len = [];
  let n = body.length;
  do {
    let d = n % 128;
    n = Math.floor(n / 128);
    if (n > 0) d |= 0x80;
    len.push(d);
  } while (n > 0);
  const out = new Uint8Array(1 + len.length + body.length);
  out[0] = header;
  out.set(len, 1);
  out.set(body, 1 + len.length);
  return out;
}

export class MqttClient {
  // options: { clientId, keepAlive (s), will: { topic, payload, retain } }
  constructor(url, options) {
    this.url = url;
    this.options = options;
    this.buf = new Uint8Array(0);
    this.onmessage = null; // (topic, text) => void
    this.onclose = null;
    this.packetId = 0;
  }

  connect(timeoutMs = 8000) {
    return new Promise((resolve, reject) => {
      let settled = false;
      const fail = (err) => {
        if (!settled) {
          settled = true;
          reject(err);
        }
      };
      const ws = new WebSocket(this.url, ["mqtt"]);
      ws.binaryType = "arraybuffer";
      this.ws = ws;
      const timer = setTimeout(() => {
        fail(new Error("timeout"));
        try {
          ws.close();
        } catch (e) {
          /* ignore */
        }
      }, timeoutMs);
      ws.onopen = () => {
        const o = this.options;
        let flags = 0x02;
        const will = o.will;
        if (will) flags |= 0x04 | (will.retain ? 0x20 : 0);
        const keep = o.keepAlive || 30;
        const body = [...str("MQTT"), 4, flags, keep >> 8, keep & 0xff, ...str(o.clientId)];
        if (will) body.push(...str(will.topic), ...bytes(enc.encode(will.payload)));
        ws.send(packet(0x10, body));
      };
      ws.onmessage = (event) => {
        this.feed(new Uint8Array(event.data), (type, flags, body) => {
          if (type === 2) {
            clearTimeout(timer);
            if (body[1] !== 0) {
              fail(new Error("refused"));
              ws.close();
              return;
            }
            settled = true;
            this.startPing();
            resolve();
          } else if (type === 3) {
            this.handlePublish(flags, body);
          }
        });
      };
      ws.onerror = () => fail(new Error("unreachable"));
      ws.onclose = () => {
        clearTimeout(timer);
        clearInterval(this.pingTimer);
        fail(new Error("closed"));
        if (this.onclose) this.onclose();
      };
    });
  }

  feed(chunk, onPacket) {
    const merged = new Uint8Array(this.buf.length + chunk.length);
    merged.set(this.buf, 0);
    merged.set(chunk, this.buf.length);
    this.buf = merged;
    for (;;) {
      if (this.buf.length < 2) return;
      let len = 0;
      let mult = 1;
      let i = 1;
      for (; i < 5 && i < this.buf.length; i++) {
        len += (this.buf[i] & 0x7f) * mult;
        if ((this.buf[i] & 0x80) === 0) break;
        mult *= 128;
      }
      if (i >= this.buf.length || (this.buf[i] & 0x80) !== 0) return;
      const start = i + 1;
      if (this.buf.length < start + len) return;
      const header = this.buf[0];
      const body = this.buf.slice(start, start + len);
      this.buf = this.buf.slice(start + len);
      onPacket(header >> 4, header & 0x0f, body);
    }
  }

  handlePublish(flags, body) {
    const n = (body[0] << 8) | body[1];
    const topic = dec.decode(body.subarray(2, 2 + n));
    let rest = body.subarray(2 + n);
    if ((flags >> 1) & 3) {
      this.send(new Uint8Array([0x40, 2, rest[0], rest[1]]));
      rest = rest.subarray(2);
    }
    if (this.onmessage) this.onmessage(topic, dec.decode(rest));
  }

  startPing() {
    clearInterval(this.pingTimer);
    const every = ((this.options.keepAlive || 30) * 1000 * 2) / 3;
    this.pingTimer = setInterval(() => this.send(new Uint8Array([0xc0, 0])), every);
  }

  send(data) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data);
      return true;
    }
    return false;
  }

  subscribe(topic) {
    this.packetId = (this.packetId % 65535) + 1;
    return this.send(packet(0x82, [this.packetId >> 8, this.packetId & 0xff, ...str(topic), 0]));
  }

  publish(topic, text, retain = false) {
    const payload = enc.encode(text);
    const t = str(topic);
    const body = new Uint8Array(t.length + payload.length);
    body.set(t, 0);
    body.set(payload, t.length);
    return this.send(packet(0x30 | (retain ? 1 : 0), body));
  }

  close() {
    clearInterval(this.pingTimer);
    this.send(new Uint8Array([0xe0, 0]));
    try {
      this.ws.close();
    } catch (e) {
      /* ignore */
    }
  }
}
