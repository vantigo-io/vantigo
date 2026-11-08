import {
  type DocControlFrame,
  type DocHandlers,
  KIND_AWARENESS,
  KIND_COMPACT,
  KIND_SEED,
  KIND_UPDATE,
} from "@vantigo/frontend-shell";
import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate, removeAwarenessStates } from "y-protocols/awareness";
import * as Y from "yjs";

/** The part of the shell's `LiveClient` the provider uses, so a test can supply a fake socket's worth. */
export interface DocChannel {
  listenDoc(topic: string, handlers: DocHandlers): () => void;
  sendBinary(topic: string, kind: number, payload: Uint8Array): boolean;
  sendJson(frame: Record<string, unknown>): boolean;
}

/** The limits of the protocol (knowledge design K25). */
export const MAX_UPDATE_BYTES = 256 * 1024;
export const MAX_STATE_BYTES = 2 * 1024 * 1024;
export const MAX_AWARENESS_BYTES = 4 * 1024;
/** Local changes are collected this long and sent as one frame (K34). */
export const BATCH_MS = 100;
/** The newest awareness state goes out at most this often. */
export const AWARENESS_MS = 100;
/** A seed or a compaction unanswered for this long is given up on. */
export const REPLY_WATCH_MS = 10_000;
export const RETRY_START_MS = 1_000;
export const RETRY_MAX_MS = 30_000;

const jitter = (ms: number) => ms * (0.8 + 0.4 * Math.random());

/**
 * `offline`: no confirmed subscription (the document is kept and typed in); `syncing`: `doc_sync`
 * sent, `synced` awaited; `seeding`: the log is empty and the stored JSON is being turned into
 * it (K29); `ready`: the document and the log agree; `broken`: the live copy cannot be used (K35).
 */
export type CollabStatus = "offline" | "syncing" | "seeding" | "ready" | "broken";

export interface CollabState {
  status: CollabStatus;
  /** The log holds text, so the editor may be bound to the document. A reader on an empty log waits for the first update. */
  hasLog: boolean;
  /** The log is full: writes are held until a compaction lands (K27, K35). */
  paused: boolean;
  /** The server stores nothing from this connection (a reader, a locked or trashed page). */
  readOnly: boolean;
  /** Why the document is `broken`: an update that cannot be applied, or one over the 256 KiB limit. */
  failure: "apply" | "too_large" | null;
  /** Changes the server has not acknowledged. */
  unsent: boolean;
}

export interface CollabOptions {
  /** `knowledge.doc:<articleId>` */
  topic: string;
  channel: DocChannel;
  /** The person may write: they seed, send updates, awareness and compactions (K26). */
  canWrite: boolean;
  /** The article as stored now, for seeding (K29): a fresh read, never a cached one. */
  loadSource: () => Promise<{ content: unknown; version: number }>;
  /** The whole Yjs state of a stored document (normalised, with block ids). */
  buildSeed: (content: unknown) => Uint8Array;
  /** The log was reset (K32): this provider is finished; `unsent` says whether typed text had not reached the log. */
  onReset: (info: { unsent: boolean }) => void;
}

type Sent = { kind: "update"; data: Uint8Array } | { kind: "seed" } | { kind: "compact" };

const be64 = (value: number): Uint8Array => {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigUint64(0, BigInt(value));
  return out;
};

const joined = (head: Uint8Array, tail: Uint8Array): Uint8Array => {
  const out = new Uint8Array(head.length + tail.length);
  out.set(head, 0);
  out.set(tail, head.length);
  return out;
};

const numberOf = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);

/**
 * The browser's side of the document protocol (K24–K35): one Yjs document and its awareness,
 * kept in step with the server's log of opaque updates. The server is not a Yjs peer, so the
 * provider is the one that remembers what it has sent and what the server holds: the
 * contiguous watermark of rows it has applied (sent as `doc_sync`'s `after`), the updates not
 * yet acknowledged (resent after a reconnect), the seed and the compaction.
 *
 * The text typed is never lost to the protocol: an update that is refused for a reason that
 * will pass (`rate`, `log_full`, `unavailable`) is kept and sent again with backoff, and a
 * lost connection resends everything not acknowledged after the next `synced`.
 */
export class CollabProvider {
  readonly doc = new Y.Doc();
  readonly awareness = new Awareness(this.doc);

  private readonly options: CollabOptions;
  private state: CollabState = {
    status: "offline",
    hasLog: false,
    paused: false,
    readOnly: false,
    failure: null,
    unsent: false,
  };
  private readonly listeners = new Set<() => void>();
  private readonly handlers = new Map<string, Set<() => void>>();
  private leftWith: Record<string, unknown> | null = null;
  private seedWatch: ReturnType<typeof setTimeout> | null = null;
  private compactWatch: ReturnType<typeof setTimeout> | null = null;
  private outbox: Uint8Array[] = [];
  private inflight: Sent[] = [];
  private canWrite: boolean;
  private epoch = 0;
  private watermark = 0;
  private readonly seen = new Set<number>();
  private synced = false;
  private seedState: Uint8Array | null = null;
  private seedToken = 0;
  private seedRejections = 0;
  private compacting = false;
  private retries = 0;
  private destroyed = false;
  private batchTimer: ReturnType<typeof setTimeout> | null = null;
  private awarenessTimer: ReturnType<typeof setTimeout> | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private resyncTimer: ReturnType<typeof setTimeout> | null = null;
  private readonly stopListening: () => void;

  constructor(options: CollabOptions) {
    this.options = options;
    this.canWrite = options.canWrite;
    this.doc.on("update", this.onDocUpdate);
    this.awareness.on("update", this.onAwarenessUpdate);
    this.stopListening = options.channel.listenDoc(options.topic, {
      onSubscribed: () => this.onSubscribed(),
      onUnsubscribed: () => this.onUnsubscribed(),
      onBinary: (kind, payload) => this.onBinary(kind, payload),
      onControl: (frame) => this.onControl(frame),
    });
  }

  /**
   * The event interface TipTap's UniqueID expects of a collaboration provider: it gives ids to
   * blocks that lack one when the provider is `synced` (here: ready with the log's text).
   */
  on(event: string, handler: () => void): void {
    let set = this.handlers.get(event);
    if (!set) {
      set = new Set();
      this.handlers.set(event, set);
    }
    set.add(handler);
  }

  off(event: string, handler: () => void): void {
    this.handlers.get(event)?.delete(handler);
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getState = (): CollabState => this.state;

  /** The newest row such that every row up to it is applied here. */
  get applied(): number {
    return this.watermark;
  }

  /** Whether the server stores what this provider sends. A page that gets locked stops writing. */
  setCanWrite(value: boolean): void {
    // After the server refused writes, a repeat of `true` is a retry: the page's access was re-read.
    if (this.canWrite === value && !(value && this.state.readOnly)) return;
    this.canWrite = value;
    if (value) {
      // Unlocked or promoted: the server's own check decides again whether it stores.
      const retry = this.state.readOnly;
      this.setState({ readOnly: false });
      if (retry && this.synced) this.startSync();
      else this.scheduleFlush();
    }
  }

  /** Sends what is waiting and tells the others this person left; the document and the socket's listener are then released. */
  destroy(): void {
    if (this.destroyed) return;
    this.flush();
    this.leave();
    this.destroyed = true;
    for (const timer of [
      this.batchTimer,
      this.awarenessTimer,
      this.retryTimer,
      this.resyncTimer,
      this.seedWatch,
      this.compactWatch,
    ]) {
      if (timer) clearTimeout(timer);
    }
    this.seedToken += 1;
    this.stopListening();
    this.listeners.clear();
    this.awareness.destroy();
    this.doc.destroy();
  }

  /** After a page restored from the back/forward cache (`pageshow` persisted): the cursor state `leave` removed comes back. */
  rejoin(): void {
    if (this.destroyed || !this.leftWith) return;
    this.awareness.setLocalState(this.leftWith);
    this.leftWith = null;
    this.startSync();
  }

  /** Asks the server for the log again from the watermark (a save was refused because the log was empty, say). */
  resync(): void {
    this.startSync();
  }

  /** Best effort, on unload: the others drop this person's cursor at once instead of after 30 s (K30). */
  leave(): void {
    if (this.destroyed) return;
    this.flush();
    this.leftWith = this.awareness.getLocalState();
    removeAwarenessStates(this.awareness, [this.doc.clientID], "leave");
    if (this.mayWrite()) {
      this.options.channel.sendBinary(
        this.options.topic,
        KIND_AWARENESS,
        encodeAwarenessUpdate(this.awareness, [this.doc.clientID]),
      );
    }
  }

  // --- state ---------------------------------------------------------------------------------

  private setState(patch: Partial<CollabState>): void {
    const next = { ...this.state, ...patch };
    const changed = (Object.keys(next) as (keyof CollabState)[]).some((key) => next[key] !== this.state[key]);
    if (!changed) return;
    const wasSynced = this.state.status === "ready" && this.state.hasLog;
    this.state = next;
    for (const listener of [...this.listeners]) listener();
    if (!wasSynced && next.status === "ready" && next.hasLog) {
      for (const handler of [...(this.handlers.get("synced") ?? [])]) handler();
    }
  }

  private refreshUnsent(): void {
    this.setState({ unsent: this.outbox.length > 0 || this.inflight.some((sent) => sent.kind === "update") });
  }

  /** Writes go out only when the server holds this connection's epoch, the person may write and nothing is backing off. */
  private mayWrite(): boolean {
    return this.synced && this.canWrite && !this.state.readOnly && this.state.failure === null;
  }

  private fail(failure: "apply" | "too_large", message: string): void {
    this.setState({ status: "broken", failure });
    this.options.channel.sendJson({ type: "doc_error", topic: this.options.topic, message });
  }

  // --- the connection ------------------------------------------------------------------------

  private onSubscribed(): void {
    if (this.destroyed) return;
    // Not requeued here: on a resync the answers to what was sent still come, in order; a lost
    // connection already put them back (`onUnsubscribed`), so no ack can belong to an older one.
    this.clearRetry();
    if (this.state.readOnly && this.canWrite) this.setState({ readOnly: false });
    this.startSync();
  }

  private onUnsubscribed(): void {
    if (this.destroyed) return;
    this.synced = false;
    this.seedToken += 1;
    this.requeueInflight();
    this.clearRetry();
    if (this.resyncTimer) clearTimeout(this.resyncTimer);
    this.resyncTimer = null;
    if (this.state.status !== "broken") this.setState({ status: "offline" });
  }

  /** What was sent and not answered is unknown: the updates go back to the outbox, a seed or compaction is forgotten. */
  private requeueInflight(): void {
    const lost = this.inflight.flatMap((sent) => (sent.kind === "update" ? [sent.data] : []));
    this.inflight = [];
    this.seedState = null;
    this.compacting = false;
    this.unwatch();
    this.outbox = [...lost, ...this.outbox];
    this.refreshUnsent();
  }

  private startSync(): void {
    if (this.destroyed) return;
    this.synced = false;
    this.seedToken += 1;
    if (this.state.status !== "broken") this.setState({ status: "syncing" });
    this.options.channel.sendJson({
      type: "doc_sync",
      topic: this.options.topic,
      after: this.watermark,
      epoch: this.epoch,
    });
  }

  private unwatch(): void {
    for (const key of ["seedWatch", "compactWatch"] as const) {
      if (this[key]) clearTimeout(this[key]);
      this[key] = null;
    }
  }

  /** A seed or compaction the server never answers (lost between hub and store): sync again after 10 s. */
  private watch(key: "seedWatch" | "compactWatch", kind: "seed" | "compact"): void {
    if (this[key]) clearTimeout(this[key]);
    this[key] = setTimeout(() => {
      this[key] = null;
      this.removeFirst(kind);
      if (kind === "seed") {
        this.seedState = null;
        this.seedRejections += 1;
      } else this.compacting = false;
      this.resyncLater();
    }, REPLY_WATCH_MS);
  }

  private clearRetry(): void {
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
  }

  /** Backoff with jitter for a refusal that will pass (`rate`, `log_full`, `unavailable`). */
  private scheduleRetry(): void {
    if (this.retryTimer || this.destroyed) return;
    const delay = Math.min(RETRY_MAX_MS, RETRY_START_MS * 2 ** this.retries);
    this.retries += 1;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.flush();
    }, jitter(delay));
  }

  // --- frames from the server ----------------------------------------------------------------

  private onControl(frame: DocControlFrame): void {
    if (this.destroyed) return;
    switch (frame.type) {
      case "synced":
        this.onSynced(numberOf(frame.seq), numberOf(frame.epoch));
        break;
      case "ack":
        this.onAck(numberOf(frame.seq));
        break;
      case "update_rejected":
        this.onUpdateRejected(String(frame.reason ?? ""));
        break;
      case "seed_accepted":
        this.onSeedAccepted();
        break;
      case "seed_rejected":
        this.onSeedRejected();
        break;
      case "compact_requested":
        this.onCompactRequested(numberOf(frame.upTo));
        break;
      case "compact_rejected":
        this.removeFirst("compact");
        this.compacting = false;
        if (this.compactWatch) clearTimeout(this.compactWatch);
        this.compactWatch = null;
        break;
      case "doc_reset":
        this.reset();
        break;
      case "doc_stale":
        this.startSync();
        break;
    }
  }

  private onSynced(seq: number, epoch: number): void {
    this.epoch = epoch;
    this.seen.clear();
    this.watermark = Math.max(this.watermark, seq);
    this.synced = true;
    if (seq === 0 && !this.state.hasLog) {
      if (!this.canWrite) {
        // A reader keeps the stored JSON on screen and binds when the first update arrives (K29).
        this.setState({ status: "ready" });
        return;
      }
      this.setState({ status: "seeding" });
      void this.seed();
      return;
    }
    this.setState({ status: "ready", hasLog: true });
    this.afterReady();
  }

  private afterReady(): void {
    this.seedRejections = 0;
    this.flush();
    this.sendAwareness();
  }

  /** K29: the stored JSON, read afresh, becomes one seed frame; the server lets one seed win. */
  private async seed(): Promise<void> {
    const token = ++this.seedToken;
    let source: { content: unknown; version: number };
    try {
      source = await this.options.loadSource();
    } catch {
      if (token === this.seedToken) this.resyncLater();
      return;
    }
    if (this.destroyed || token !== this.seedToken || !this.synced) return;
    let state: Uint8Array;
    try {
      state = this.options.buildSeed(source.content);
    } catch (error) {
      this.fail("apply", error instanceof Error ? error.message : String(error));
      return;
    }
    if (state.length > MAX_STATE_BYTES) {
      this.fail("too_large", "the stored document is larger than a seed may be");
      return;
    }
    if (!this.options.channel.sendBinary(this.options.topic, KIND_SEED, joined(be64(source.version), state))) return;
    this.seedState = state;
    this.inflight.push({ kind: "seed" });
    this.watch("seedWatch", "seed");
  }

  private onSeedAccepted(): void {
    if (this.seedWatch) clearTimeout(this.seedWatch);
    this.seedWatch = null;
    const state = this.seedState;
    this.seedState = null;
    if (!state) return;
    // The seed is not echoed to its sender, so this document takes it in itself.
    try {
      Y.applyUpdate(this.doc, state, this);
    } catch (error) {
      this.fail("apply", error instanceof Error ? error.message : String(error));
      return;
    }
    this.setState({ status: "ready", hasLog: true });
    this.afterReady();
  }

  /** Another seed won, the article moved on, or the store failed: throw ours away and ask for the log again. */
  private onSeedRejected(): void {
    if (this.seedWatch) clearTimeout(this.seedWatch);
    this.seedWatch = null;
    this.removeFirst("seed");
    this.seedState = null;
    this.seedRejections += 1;
    this.synced = false;
    this.seedToken += 1;
    this.setState({ status: "syncing" });
    if (this.seedRejections === 1) this.startSync();
    else this.resyncLater();
  }

  private resyncLater(): void {
    if (this.resyncTimer || this.destroyed) return;
    const delay = Math.min(RETRY_MAX_MS, RETRY_START_MS * 2 ** Math.max(0, this.seedRejections - 1));
    this.resyncTimer = setTimeout(() => {
      this.resyncTimer = null;
      this.startSync();
    }, jitter(delay));
  }

  private onAck(seq: number): void {
    this.inflight.shift();
    this.noteSeq(seq);
    this.retries = 0;
    this.compacting = this.inflight.some((sent) => sent.kind === "compact");
    if (!this.compacting && this.compactWatch) clearTimeout(this.compactWatch);
    if (!this.compacting) this.compactWatch = null;
    this.setState({ paused: false });
    this.refreshUnsent();
  }

  private onUpdateRejected(reason: string): void {
    const head = this.inflight.shift();
    const data = head?.kind === "update" ? head.data : null;
    switch (reason) {
      case "read_only":
        // Nothing this person types is stored for now; the page shows the document read-only and the
        // text is kept for the next `setCanWrite(true)` or reconnect to send.
        if (data) this.outbox.unshift(data);
        this.setState({ readOnly: true });
        break;
      case "too_large":
        this.fail("too_large", "an update was over the size limit");
        break;
      case "not_seeded":
        // The log is empty though this copy has text: it was reset without this copy hearing.
        if (data) this.outbox.unshift(data);
        this.refreshUnsent();
        this.reset();
        return;
      case "stale":
        if (data) this.outbox.unshift(data);
        this.startSync();
        break;
      default:
        // rate, log_full, unavailable: keep the update and send it again with backoff.
        if (data) this.outbox.unshift(data);
        if (reason === "log_full") this.setState({ paused: true });
        this.scheduleRetry();
    }
    this.refreshUnsent();
  }

  private removeFirst(kind: Sent["kind"]): void {
    const index = this.inflight.findIndex((sent) => sent.kind === kind);
    if (index >= 0) this.inflight.splice(index, 1);
  }

  /** `doc_reset` (K32): the JSON is the truth again. This copy is discarded; the page starts a new provider. */
  private reset(): void {
    const unsent = this.state.unsent;
    // Nothing more may leave this copy: it belongs to a log that is gone.
    this.synced = false;
    this.options.onReset({ unsent });
    this.destroy();
  }

  // --- binary frames from the server ---------------------------------------------------------

  private onBinary(kind: number, payload: Uint8Array): void {
    if (this.destroyed) return;
    if (kind === KIND_UPDATE && payload.length > 8) {
      const seq = Number(new DataView(payload.buffer, payload.byteOffset, 8).getBigUint64(0));
      try {
        Y.applyUpdate(this.doc, payload.subarray(8), this);
      } catch (error) {
        this.fail("apply", error instanceof Error ? error.message : String(error));
        return;
      }
      this.noteSeq(seq);
      this.setState({ hasLog: true });
    } else if (kind === KIND_AWARENESS) {
      try {
        applyAwarenessUpdate(this.awareness, payload, this);
      } catch {
        // A frame that is not awareness is not worth a failure: the cursor just does not show.
      }
    }
  }

  /** Records that row `seq` is applied here (sent by the server or acknowledged to this client) and advances the contiguous watermark. */
  private noteSeq(seq: number): void {
    if (seq <= this.watermark) return;
    this.seen.add(seq);
    while (this.seen.delete(this.watermark + 1)) this.watermark += 1;
  }

  // --- frames to the server ------------------------------------------------------------------

  private readonly onDocUpdate = (update: Uint8Array, origin: unknown): void => {
    if (origin === this) return;
    this.outbox.push(update);
    this.refreshUnsent();
    this.scheduleFlush();
  };

  private scheduleFlush(): void {
    if (this.batchTimer || this.destroyed) return;
    this.batchTimer = setTimeout(() => {
      this.batchTimer = null;
      this.flush();
    }, BATCH_MS);
  }

  /** The outbox merged into frames of at most 256 KiB, each kept until its ack. */
  private flush(): void {
    if (!this.mayWrite() || this.retryTimer || this.outbox.length === 0) return;
    const groups: Uint8Array[] = [];
    let batch: Uint8Array[] = [];
    let size = 0;
    const close = () => {
      if (batch.length > 0) groups.push(batch.length === 1 ? batch[0] : Y.mergeUpdates(batch));
      batch = [];
      size = 0;
    };
    for (const update of this.outbox) {
      if (batch.length > 0 && size + update.length > MAX_UPDATE_BYTES) close();
      batch.push(update);
      size += update.length;
    }
    close();
    this.outbox = [];
    for (const [index, data] of groups.entries()) {
      if (data.length > MAX_UPDATE_BYTES) {
        this.fail("too_large", "an update was over the size limit");
        return;
      }
      if (!this.options.channel.sendBinary(this.options.topic, KIND_UPDATE, data)) {
        this.outbox = groups.slice(index);
        break;
      }
      this.inflight.push({ kind: "update", data });
    }
    this.refreshUnsent();
  }

  private onCompactRequested(upTo: number): void {
    // Only a copy that holds every row up to `upTo` may replace them (K31).
    if (!this.mayWrite() || this.compacting || this.state.status !== "ready" || this.watermark < upTo || upTo <= 0) {
      return;
    }
    const state = Y.encodeStateAsUpdate(this.doc);
    if (state.length > MAX_STATE_BYTES) return;
    if (!this.options.channel.sendBinary(this.options.topic, KIND_COMPACT, joined(be64(this.watermark), state))) return;
    this.compacting = true;
    this.inflight.push({ kind: "compact" });
    this.watch("compactWatch", "compact");
  }

  private readonly onAwarenessUpdate = (
    change: { added: number[]; updated: number[]; removed: number[] },
    origin: unknown,
  ): void => {
    if (origin === this || this.destroyed) return;
    if (![...change.added, ...change.updated, ...change.removed].includes(this.doc.clientID)) return;
    if (this.awarenessTimer) return;
    this.awarenessTimer = setTimeout(() => {
      this.awarenessTimer = null;
      this.sendAwareness();
    }, AWARENESS_MS);
  };

  /** This person's whole awareness state: only the newest frame of a connection matters (K30). */
  private sendAwareness(): void {
    if (!this.mayWrite() || this.destroyed) return;
    const update = encodeAwarenessUpdate(this.awareness, [this.doc.clientID]);
    if (update.length > MAX_AWARENESS_BYTES) return;
    this.options.channel.sendBinary(this.options.topic, KIND_AWARENESS, update);
  }
}
