import {
  decodeFrame,
  encodeFrame,
  KIND_AWARENESS,
  KIND_COMPACT,
  KIND_SEED,
  KIND_UPDATE,
  type LiveSocket,
} from "@vantigo/frontend-shell";

const be64 = (value: number): Uint8Array => {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigUint64(0, BigInt(value));
  return out;
};
const readBe64 = (bytes: Uint8Array): number => Number(new DataView(bytes.buffer, bytes.byteOffset, 8).getBigUint64(0));
const joined = (head: Uint8Array, tail: Uint8Array): Uint8Array => {
  const out = new Uint8Array(head.length + tail.length);
  out.set(head, 0);
  out.set(tail, head.length);
  return out;
};

/**
 * One connection of the fake server: the `LiveSocket` the shell's client opens. What the server
 * sends waits in `outbox` until `deliver`, so a test decides the order things arrive in.
 */
export class FakeSocket implements LiveSocket {
  binaryType?: string;
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  opened = false;
  closed = false;
  outbox: (string | ArrayBuffer)[] = [];
  /** The epoch the server bound this connection to at its last `synced`; 0 before. */
  epoch = 0;
  canWrite = true;
  /** The server has told this connection `read_only` once; later writes are dropped silently (K26). */
  readOnlyTold = false;
  /** While set, `synced` is held back and rows appended meanwhile wait behind it (the replay window, K28). */
  holding = false;
  held: Uint8Array[] = [];
  readonly topics = new Set<string>();
  /** Frames the client sent, for assertions. */
  readonly texts: Record<string, unknown>[] = [];
  readonly binaries: { kind: number; payload: Uint8Array }[] = [];
  private readonly server: FakeDocServer;

  constructor(server: FakeDocServer) {
    this.server = server;
  }

  send(data: string | Uint8Array): void {
    if (this.closed) return;
    this.server.receive(this, data);
  }

  close(): void {
    this.closed = true;
    this.server.sockets.delete(this);
  }

  open(): void {
    if (this.opened || this.closed) return;
    this.opened = true;
    this.onopen?.();
  }

  /** The network drops: the client sees the close; nothing more is delivered. */
  drop(code = 1006): void {
    this.closed = true;
    this.outbox = [];
    this.server.sockets.delete(this);
    this.onclose?.({ code });
  }

  push(frame: Record<string, unknown>): void {
    this.outbox.push(JSON.stringify(frame));
  }

  pushBinary(kind: number, topic: string, payload: Uint8Array): void {
    const bytes = encodeFrame(kind, topic, payload);
    this.outbox.push(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer);
  }

  deliver(): boolean {
    const frames = this.outbox;
    this.outbox = [];
    for (const data of frames) this.onmessage?.({ data });
    return frames.length > 0;
  }
}

/**
 * An in-memory server of the document protocol (knowledge design K25–K32, as `srv/live` ships
 * it): the update log with its epoch, seeds that win only on an empty log at the article's
 * version, acks in order, compaction, resets, and fan-out that never echoes. Enough of the
 * socket (`sub`, `ping`) for the shell's real client to talk to it.
 */
export class FakeDocServer {
  readonly sockets = new Set<FakeSocket>();
  rows: { seq: number; data: Uint8Array }[] = [];
  epoch = 1;
  /** The article version a seed must name. */
  version = 3;
  /** Answers the next write with `update_rejected` of this reason, once. */
  failNext: string | null = null;
  /** After an append that leaves more rows than this, the sender is asked to compact (K31). Infinity: never. */
  compactAfter = Number.POSITIVE_INFINITY;
  /** Whether the log has had an append lately: a single-mode save is then refused `collab_active`. */
  recentlyAppended = false;
  /** Hold the next replay's `synced` until `releaseReplay` (set before the client syncs). */
  holdReplay = false;
  /** Every `doc_error` heard. */
  errors: string[] = [];
  readonly topic: string;
  private readonly unopened: FakeSocket[] = [];
  private last = 0;

  constructor(topic: string) {
    this.topic = topic;
  }

  /** The `createSocket` for a `LiveClient`: each call is a new connection, opened by `settle`. */
  createSocket = (): FakeSocket => {
    const socket = new FakeSocket(this);
    this.sockets.add(socket);
    this.unopened.push(socket);
    return socket;
  };

  get newest(): number {
    return this.rows.at(-1)?.seq ?? 0;
  }

  /** Opens new sockets and delivers everything waiting, until the network is quiet. */
  async settle(wrap: (run: () => void) => void = (run) => run()): Promise<void> {
    for (let round = 0; round < 50; round += 1) {
      let busy = false;
      wrap(() => {
        for (const socket of this.unopened.splice(0)) {
          socket.open();
          busy = true;
        }
        for (const socket of [...this.sockets]) busy = socket.deliver() || busy;
      });
      // The provider's seed is read asynchronously.
      for (let tick = 0; tick < 5; tick += 1) await Promise.resolve();
      if (!busy && this.unopened.length === 0 && [...this.sockets].every((socket) => socket.outbox.length === 0))
        return;
    }
    throw new Error("the fake network never went quiet");
  }

  receive(socket: FakeSocket, data: string | Uint8Array): void {
    if (typeof data === "string") {
      const frame = JSON.parse(data) as Record<string, unknown> & { type: string; topic?: string };
      socket.texts.push(frame);
      this.text(socket, frame);
      return;
    }
    const frame = decodeFrame(data);
    if (!frame) return;
    socket.binaries.push({ kind: frame.kind, payload: frame.payload });
    if (frame.topic === this.topic) this.binary(socket, frame.kind, frame.payload);
  }

  /** Deletes the log and tells every connection (a single-editor save, a restore or the rebuild button). */
  reset(): void {
    this.rows = [];
    this.last = 0;
    this.recentlyAppended = false;
    this.epoch += 1;
    for (const socket of this.sockets) {
      socket.epoch = 0;
      socket.push({ type: "doc_reset", topic: this.topic, epoch: this.epoch });
    }
  }

  /** What a single-mode save or a restore meets: 409 `collab_active` while the log has rows and was appended to lately. */
  collabActive(): boolean {
    return this.rows.length > 0 && this.recentlyAppended;
  }

  /** Tells `socket` its copy may miss rows (a row compaction removed, or discarded frames). */
  stale(socket: FakeSocket): void {
    socket.push({ type: "doc_stale", topic: this.topic });
  }

  /** The server's global `resync` (the listener returned, or a slow consumer was cut off). */
  resync(socket: FakeSocket): void {
    socket.push({ type: "resync" });
  }

  /** Ends a held replay: `synced`, then the rows that arrived meanwhile. */
  releaseReplay(socket: FakeSocket): void {
    socket.holding = false;
    socket.push({ type: "synced", topic: this.topic, seq: this.newest - socket.held.length, epoch: this.epoch });
    for (const row of socket.held.splice(0)) socket.outbox.push(row as unknown as ArrayBuffer);
  }

  /** Asks `socket` for a compaction up to the newest row. */
  requestCompaction(socket: FakeSocket): void {
    socket.push({ type: "compact_requested", topic: this.topic, upTo: this.newest });
  }

  private text(socket: FakeSocket, frame: Record<string, unknown> & { type: string; topic?: string }): void {
    switch (frame.type) {
      case "sub":
        socket.topics.add(String(frame.topic));
        socket.push({ type: "ok", topic: frame.topic });
        break;
      case "unsub":
        socket.topics.delete(String(frame.topic));
        break;
      case "ping":
        socket.push({ type: "pong" });
        break;
      case "doc_error":
        this.errors.push(String(frame.message));
        break;
      case "doc_sync":
        this.sync(socket, Number(frame.after), Number(frame.epoch));
        break;
    }
  }

  private sync(socket: FakeSocket, after: number, epoch: number): void {
    if ((epoch !== 0 && epoch !== this.epoch) || after > this.newest) {
      socket.epoch = 0;
      socket.push({ type: "doc_reset", topic: this.topic, epoch: this.epoch });
      return;
    }
    for (const row of this.rows) {
      if (row.seq > after) socket.pushBinary(KIND_UPDATE, this.topic, joined(be64(row.seq), row.data));
    }
    socket.epoch = this.epoch;
    if (this.holdReplay) {
      this.holdReplay = false;
      socket.holding = true;
      return;
    }
    socket.push({ type: "synced", topic: this.topic, seq: this.newest, epoch: this.epoch });
  }

  private append(from: FakeSocket, data: Uint8Array): number {
    this.last = Math.max(this.last, this.newest) + 1;
    this.rows.push({ seq: this.last, data });
    this.recentlyAppended = true;
    for (const other of this.sockets) {
      if (other === from || other.epoch !== this.epoch) continue;
      if (other.holding) {
        const bytes = encodeFrame(KIND_UPDATE, this.topic, joined(be64(this.last), data));
        other.held.push(
          bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as unknown as Uint8Array,
        );
      } else other.pushBinary(KIND_UPDATE, this.topic, joined(be64(this.last), data));
    }
    return this.last;
  }

  private binary(socket: FakeSocket, kind: number, payload: Uint8Array): void {
    const reject = (reason: string) => socket.push({ type: "update_rejected", topic: this.topic, reason });
    if (kind === KIND_AWARENESS) {
      for (const other of this.sockets) if (other !== socket) other.pushBinary(KIND_AWARENESS, this.topic, payload);
      return;
    }
    if (!socket.canWrite) {
      if (kind === KIND_UPDATE && !socket.readOnlyTold) {
        socket.readOnlyTold = true;
        reject("read_only");
      }
      return;
    }
    if (kind === KIND_UPDATE) {
      if (this.failNext) {
        reject(this.failNext);
        this.failNext = null;
      } else if (socket.epoch !== this.epoch) {
        reject("stale");
        socket.push({ type: "doc_reset", topic: this.topic, epoch: this.epoch });
      } else if (this.rows.length === 0) reject("not_seeded");
      else {
        socket.push({ type: "ack", topic: this.topic, seq: this.append(socket, payload) });
        if (this.rows.length > this.compactAfter) this.requestCompaction(socket);
      }
    } else if (kind === KIND_SEED) {
      const version = readBe64(payload);
      if (socket.epoch === this.epoch && this.rows.length === 0 && version === this.version) {
        const seq = this.append(socket, payload.subarray(8));
        socket.push({ type: "seed_accepted", topic: this.topic });
        socket.push({ type: "ack", topic: this.topic, seq });
      } else {
        socket.push({ type: "seed_rejected", topic: this.topic });
        if (socket.epoch !== this.epoch) socket.push({ type: "doc_reset", topic: this.topic, epoch: this.epoch });
      }
    } else if (kind === KIND_COMPACT) {
      const upTo = readBe64(payload);
      const first = this.rows[0]?.seq ?? 0;
      if (socket.epoch === this.epoch && upTo > first && upTo <= this.newest) {
        this.rows = [{ seq: upTo, data: payload.subarray(8) }, ...this.rows.filter((row) => row.seq > upTo)];
        socket.push({ type: "ack", topic: this.topic, seq: upTo });
      } else {
        socket.push({ type: "compact_rejected", topic: this.topic });
        if (socket.epoch !== this.epoch) socket.push({ type: "doc_reset", topic: this.topic, epoch: this.epoch });
      }
    }
  }
}
