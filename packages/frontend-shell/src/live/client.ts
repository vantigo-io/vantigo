import { appUrl } from "../app-config";

/** `connecting` until the first socket opens and while it reconnects, `live` when events flow, `degraded` while the server's listener is down, `unavailable` after five failed attempts. Only `live` turns fallback polling off. */
export type LiveStatus = "connecting" | "live" | "degraded" | "unavailable";

/** What a person is doing on a topic. `editing` beats `replying` beats `viewing`. */
export type PresenceState = "viewing" | "editing" | "replying";

/** One person present on a topic, collapsed over their tabs and without the signed-in person. */
export type PresenceMember = { userId: string; name: string; state: PresenceState; since: string };

/**
 * What `useLiveTopic`'s callback receives, at most once per kind per 250 ms:
 * `{ kind, ids }` with the distinct ids of the events heard in that window (empty when the events
 * carried none), or `{ kind: "resync" }` when events may have been missed and the screen must
 * refetch whatever it shows. Tell them apart with `"ids" in event`.
 */
export type LiveEvent = { kind: string; ids: string[] } | { kind: "resync" };

/** The part of `WebSocket` the client uses, so a test can supply a fake. */
export interface LiveSocket {
  send(data: string): void;
  close(code?: number): void;
  onopen: (() => void) | null;
  onclose: ((event: { code: number }) => void) | null;
  onmessage: ((event: { data: unknown }) => void) | null;
}

export type LiveClientOptions = {
  /** The signed-in person's id, to leave them out of the presence members. */
  userId: string;
  /** The socket's address; defaults to the API's `/api/v1/live?v=1` under the app's base path, as ws: or wss:. */
  url?: string;
  createSocket?: (url: string) => LiveSocket;
};

export const DEBOUNCE_MS = 250;
export const PRESENCE_REPEAT_MS = 20_000;
export const PING_MS = 30_000;
export const BACKOFF_START_MS = 1_000;
export const BACKOFF_MAX_MS = 30_000;
export const UNAVAILABLE_AFTER = 5;
export const UNAVAILABLE_RETRY_MS = 60_000;
/** A connection counts as successful after its first server frame or this long open. */
export const SUCCESS_AFTER_MS = 5_000;
/** The socket is dead when nothing at all arrives this long after a ping. */
export const PONG_TIMEOUT_MS = 10_000;
export const SUB_RETRY_START_MS = 2_000;
export const SUB_RETRY_MAX_MS = 60_000;
const CLOSE_POLICY = 1008;
const CLOSE_TOO_MANY = 4429;
const jitter = (ms: number) => ms * (0.8 + 0.4 * Math.random());
/** The close code the server uses when the session is gone: signed out, so no reconnecting. */
const CLOSE_SESSION_GONE = 4401;

const STRENGTH: Record<PresenceState, number> = { viewing: 0, replying: 1, editing: 2 };
const NO_MEMBERS: readonly PresenceMember[] = [];

export const defaultLiveUrl = (): string => {
  const url = new URL(`${appUrl("/api/v1/live")}?v=1`, window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.toString();
};

type Pending = { kinds: Map<string, Set<string>>; resync: boolean; timer: ReturnType<typeof setTimeout> | null };
type TopicEntry = {
  listeners: Set<(event: LiveEvent) => void>;
  pending: Pending;
  /** The server answered `ok` for this subscription on the current connection. */
  ok: boolean;
  retries: number;
  retryTimer: ReturnType<typeof setTimeout> | null;
};

const isPresenceState = (value: unknown): value is PresenceState =>
  value === "viewing" || value === "editing" || value === "replying";

/** The collapsed members of a presence frame: one per person, the strongest state, leaving out `self`. */
const collapse = (members: unknown, self: string): PresenceMember[] => {
  if (!Array.isArray(members)) return [];
  const byUser = new Map<string, PresenceMember>();
  for (const raw of members) {
    const m = raw as Partial<PresenceMember> | null;
    if (!m || typeof m.userId !== "string" || m.userId === self || !isPresenceState(m.state)) continue;
    const since = typeof m.since === "string" ? m.since : "";
    const known = byUser.get(m.userId);
    if (!known) {
      byUser.set(m.userId, { userId: m.userId, name: typeof m.name === "string" ? m.name : "", state: m.state, since });
    } else {
      if (STRENGTH[m.state] > STRENGTH[known.state]) known.state = m.state;
      if (since && (!known.since || since < known.since)) known.since = since;
    }
  }
  return [...byUser.values()].sort((a, b) => a.since.localeCompare(b.since) || a.userId.localeCompare(b.userId));
};

/**
 * One socket to `/api/v1/live`, the subscriptions and presence on it. React-free; `LiveProvider`
 * owns one and the hooks talk to it. It survives `stop()`/`start()` (StrictMode runs both).
 */
export class LiveClient {
  private status: LiveStatus = "connecting";
  private readonly statusListeners = new Set<() => void>();
  private readonly topics = new Map<string, TopicEntry>();
  private readonly presenceWanted = new Map<string, Map<symbol, PresenceState>>();
  private readonly presenceMembers = new Map<string, readonly PresenceMember[]>();
  private readonly presenceListeners = new Map<string, Set<() => void>>();
  private socket: LiveSocket | null = null;
  private started = false;
  private opened = false;
  private failures = 0;
  private succeeded = false;
  private readonly liveListeners = new Set<() => void>();
  private successTimer: ReturnType<typeof setTimeout> | null = null;
  private pongTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private presenceTimer: ReturnType<typeof setInterval> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;

  private readonly options: LiveClientOptions;

  constructor(options: LiveClientOptions) {
    this.options = options;
  }

  start(): void {
    if (this.started) return;
    this.started = true;
    this.failures = 0;
    this.connect();
  }

  stop(): void {
    this.started = false;
    this.clearTimers();
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    const socket = this.socket;
    this.socket = null;
    if (socket) {
      socket.onopen = null;
      socket.onclose = null;
      socket.onmessage = null;
      socket.close();
    }
    this.opened = false;
    this.resetTopics();
    this.setStatus("connecting");
  }

  getStatus = (): LiveStatus => this.status;

  subscribeStatus = (listener: () => void): (() => void) => {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  };

  /** Subscribes to a topic; two listeners on one topic share one subscription. Returns the unsubscribe. */
  listen(topic: string, listener: (event: LiveEvent) => void): () => void {
    let entry = this.topics.get(topic);
    if (!entry) {
      entry = {
        listeners: new Set(),
        pending: { kinds: new Map(), resync: false, timer: null },
        ok: false,
        retries: 0,
        retryTimer: null,
      };
      this.topics.set(topic, entry);
    }
    const first = entry.listeners.size === 0;
    entry.listeners.add(listener);
    if (first) {
      this.send({ type: "sub", topic });
      // A presence announced before the subscription existed goes out right behind it.
      if (this.presenceWanted.has(topic)) this.sendPresence(topic);
    }
    return () => {
      const current = this.topics.get(topic);
      if (!current?.listeners.delete(listener) || current.listeners.size > 0) return;
      if (current.pending.timer) clearTimeout(current.pending.timer);
      if (current.retryTimer) clearTimeout(current.retryTimer);
      this.topics.delete(topic);
      this.send({ type: "unsub", topic });
      this.notifyLive();
    };
  }

  /** Announces `state` on a topic for as long as the returned function is not called; `null` announces nothing. */
  announce(topic: string, state: PresenceState | null): () => void {
    const token = Symbol("presence");
    this.setWanted(topic, token, state);
    return () => this.setWanted(topic, token, null);
  }

  /** Whether the socket is live and the server answered `ok` for this topic: false while pending, denied or unavailable. */
  isTopicLive = (topic: string): boolean => this.status === "live" && this.topics.get(topic)?.ok === true;

  subscribeLive = (listener: () => void): (() => void) => {
    this.liveListeners.add(listener);
    return () => this.liveListeners.delete(listener);
  };

  private notifyLive(): void {
    for (const listener of [...this.liveListeners]) listener();
  }

  getMembers = (topic: string): readonly PresenceMember[] => this.presenceMembers.get(topic) ?? NO_MEMBERS;

  subscribeMembers = (topic: string, listener: () => void): (() => void) => {
    let set = this.presenceListeners.get(topic);
    if (!set) {
      set = new Set();
      this.presenceListeners.set(topic, set);
    }
    const listeners = set;
    listeners.add(listener);
    return () => listeners.delete(listener);
  };

  private setWanted(topic: string, token: symbol, state: PresenceState | null): void {
    let wanted = this.presenceWanted.get(topic);
    if (state === null) wanted?.delete(token);
    else {
      if (!wanted) {
        wanted = new Map();
        this.presenceWanted.set(topic, wanted);
      }
      wanted.set(token, state);
    }
    this.sendPresence(topic);
    if (wanted && wanted.size === 0) this.presenceWanted.delete(topic);
  }

  private effective(topic: string): PresenceState | null {
    let best: PresenceState | null = null;
    for (const state of this.presenceWanted.get(topic)?.values() ?? []) {
      if (best === null || STRENGTH[state] > STRENGTH[best]) best = state;
    }
    return best;
  }

  private sendPresence(topic: string): void {
    // The server takes presence only on a subscribed topic.
    if (!this.topics.has(topic)) return;
    this.send({ type: "presence", topic, state: this.effective(topic) });
  }

  private send(frame: Record<string, unknown>): void {
    if (this.socket && this.opened) this.socket.send(JSON.stringify(frame));
  }

  private setStatus(status: LiveStatus): void {
    if (this.status === status) return;
    this.status = status;
    for (const listener of [...this.statusListeners]) listener();
    this.notifyLive();
  }

  private connect(): void {
    if (!this.started) return;
    const create = this.options.createSocket ?? ((url: string) => new WebSocket(url) as unknown as LiveSocket);
    const socket = create(this.options.url ?? defaultLiveUrl());
    this.socket = socket;
    socket.onopen = () => this.handleOpen(socket);
    socket.onmessage = (event) => this.handleMessage(socket, event.data);
    socket.onclose = (event) => this.handleClose(socket, event.code);
  }

  private handleOpen(socket: LiveSocket): void {
    if (socket !== this.socket) return;
    this.opened = true;
    this.succeeded = false;
    this.setStatus("live");
    this.successTimer = setTimeout(() => this.markSucceeded(), SUCCESS_AFTER_MS);
    for (const [topic, entry] of this.topics) {
      entry.retries = 0;
      this.send({ type: "sub", topic });
    }
    for (const topic of this.presenceWanted.keys()) this.sendPresence(topic);
    this.presenceTimer = setInterval(() => {
      for (const topic of this.presenceWanted.keys()) this.sendPresence(topic);
    }, PRESENCE_REPEAT_MS);
    this.pingTimer = setInterval(() => {
      this.send({ type: "ping" });
      if (!this.pongTimer) this.pongTimer = setTimeout(() => this.dropDead(socket), PONG_TIMEOUT_MS);
    }, PING_MS);
  }

  /** The connection has proven itself, so the failure count starts over. */
  private markSucceeded(): void {
    if (this.successTimer) clearTimeout(this.successTimer);
    this.successTimer = null;
    this.succeeded = true;
    this.failures = 0;
  }

  /** Nothing arrived after a ping: close the socket and take the ordinary close path. */
  private dropDead(socket: LiveSocket): void {
    if (socket !== this.socket) return;
    socket.onopen = null;
    socket.onclose = null;
    socket.onmessage = null;
    socket.close();
    this.handleClose(socket, 1006);
  }

  private resetTopics(): void {
    for (const entry of this.topics.values()) {
      entry.ok = false;
      if (entry.retryTimer) clearTimeout(entry.retryTimer);
      entry.retryTimer = null;
    }
    this.notifyLive();
  }

  private handleClose(socket: LiveSocket, code: number): void {
    if (socket !== this.socket) return;
    this.socket = null;
    this.clearTimers();
    this.resetTopics();
    // What members were present is unknown until the next connection says.
    for (const topic of [...this.presenceMembers.keys()]) this.setMembers(topic, NO_MEMBERS);
    // A connection that never proved itself, or that the server refused, is a failure.
    const refused = code === CLOSE_POLICY || code === CLOSE_TOO_MANY;
    if (!this.succeeded || refused) this.failures += 1;
    this.opened = false;
    this.succeeded = false;
    if (code === CLOSE_SESSION_GONE) {
      this.started = false;
      this.setStatus("unavailable");
      return;
    }
    if (code === CLOSE_TOO_MANY) this.failures = Math.max(this.failures, UNAVAILABLE_AFTER);
    const unavailable = this.failures >= UNAVAILABLE_AFTER;
    this.setStatus(unavailable ? "unavailable" : "connecting");
    const base = unavailable
      ? UNAVAILABLE_RETRY_MS
      : Math.min(BACKOFF_MAX_MS, BACKOFF_START_MS * 2 ** Math.max(0, this.failures - 1));
    // Jitter of plus or minus 20 percent, so tabs that lost one server do not return together.
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, jitter(base));
  }

  private handleMessage(socket: LiveSocket, data: unknown): void {
    if (socket !== this.socket || typeof data !== "string") return;
    let frame: { type?: string; topic?: string; kind?: string; id?: string; reason?: string; members?: unknown };
    try {
      frame = JSON.parse(data);
    } catch {
      return;
    }
    // Any frame proves the connection and the server alive.
    this.markSucceeded();
    if (this.pongTimer) clearTimeout(this.pongTimer);
    this.pongTimer = null;
    switch (frame.type) {
      case "event":
        if (typeof frame.topic === "string" && typeof frame.kind === "string") {
          this.queueEvent(frame.topic, frame.kind, typeof frame.id === "string" ? frame.id : undefined);
        }
        break;
      case "presence":
        if (typeof frame.topic === "string") this.setMembers(frame.topic, collapse(frame.members, this.options.userId));
        break;
      case "ok":
        if (typeof frame.topic === "string") this.handleOk(frame.topic);
        break;
      case "denied":
        if (typeof frame.topic === "string") this.handleDenied(frame.topic, frame.reason);
        break;
      case "resync":
        // The listener's return ends degraded; a slow consumer's resync says nothing of the server.
        if (frame.reason !== "slow_consumer") this.setStatus("live");
        this.resyncAll();
        break;
      case "degraded":
        this.setStatus("degraded");
        break;
      default:
        // pong needs nothing beyond the proof of life above.
        break;
    }
  }

  /** The subscription is in place: refetch once, since an event may have committed before it was. */
  private handleOk(topic: string): void {
    const entry = this.topics.get(topic);
    if (!entry) return;
    entry.ok = true;
    entry.retries = 0;
    entry.pending.resync = true;
    this.schedule(topic, entry);
    this.notifyLive();
  }

  private handleDenied(topic: string, reason: string | undefined): void {
    const entry = this.topics.get(topic);
    if (!entry) return;
    entry.ok = false;
    if (reason === "unavailable") {
      // A transient server error: ask again, 2 s doubling to 60 s.
      const delay = Math.min(SUB_RETRY_MAX_MS, SUB_RETRY_START_MS * 2 ** entry.retries);
      entry.retries += 1;
      if (entry.retryTimer) clearTimeout(entry.retryTimer);
      entry.retryTimer = setTimeout(() => {
        entry.retryTimer = null;
        if (this.topics.get(topic) === entry) this.send({ type: "sub", topic });
      }, jitter(delay));
    }
    // Any other reason leaves the topic dead until the next connection re-subscribes it.
    this.notifyLive();
  }

  private setMembers(topic: string, members: readonly PresenceMember[]): void {
    const known = this.getMembers(topic);
    if (members.length === 0 && known.length === 0) return;
    if (members.length === 0) this.presenceMembers.delete(topic);
    else this.presenceMembers.set(topic, members);
    for (const listener of [...(this.presenceListeners.get(topic) ?? [])]) listener();
  }

  private queueEvent(topic: string, kind: string, id: string | undefined): void {
    const entry = this.topics.get(topic);
    if (!entry) return;
    let ids = entry.pending.kinds.get(kind);
    if (!ids) {
      ids = new Set();
      entry.pending.kinds.set(kind, ids);
    }
    if (id) ids.add(id);
    this.schedule(topic, entry);
  }

  private resyncAll(): void {
    for (const [topic, entry] of this.topics) {
      entry.pending.resync = true;
      this.schedule(topic, entry);
    }
  }

  private schedule(topic: string, entry: TopicEntry): void {
    if (entry.pending.timer) return;
    entry.pending.timer = setTimeout(() => this.flush(topic), DEBOUNCE_MS);
  }

  private flush(topic: string): void {
    const entry = this.topics.get(topic);
    if (!entry) return;
    const { kinds, resync } = entry.pending;
    entry.pending = { kinds: new Map(), resync: false, timer: null };
    // A resync says refetch everything shown, which makes the single events redundant.
    const events: LiveEvent[] = resync
      ? [{ kind: "resync" }]
      : [...kinds].map(([kind, ids]) => ({ kind, ids: [...ids] }));
    for (const event of events) {
      for (const listener of [...entry.listeners]) listener(event);
    }
  }

  private clearTimers(): void {
    if (this.presenceTimer) clearInterval(this.presenceTimer);
    if (this.pingTimer) clearInterval(this.pingTimer);
    if (this.successTimer) clearTimeout(this.successTimer);
    if (this.pongTimer) clearTimeout(this.pongTimer);
    this.presenceTimer = null;
    this.pingTimer = null;
    this.successTimer = null;
    this.pongTimer = null;
  }
}
