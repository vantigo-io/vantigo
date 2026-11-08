import { KIND_AWARENESS, KIND_COMPACT, KIND_SEED, KIND_UPDATE, LiveClient } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as Y from "yjs";
import { type FakeDocServer, type FakeSocket, FakeDocServer as Server } from "../../test/collab-server";
import { editorExtensions } from "../extensions";
import { BATCH_MS, CollabProvider, REPLY_WATCH_MS } from "./provider";
import { seedState } from "./seed";

const topic = "knowledge.doc:a1";
const stored = {
  type: "doc",
  content: [{ type: "paragraph", content: [{ type: "text", text: "Lock the door." }] }],
};

interface Peer {
  client: LiveClient;
  provider: CollabProvider;
  resets: { unsent: boolean }[];
  sources: number;
  /** The newest socket of this peer. */
  socket(): FakeSocket;
}

let server: FakeDocServer;
beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0.5);
  server = new Server(topic);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const join = (
  options: {
    canWrite?: boolean;
    source?: () => Promise<{ content: unknown; version: number }>;
    buildSeed?: (content: unknown) => Uint8Array;
  } = {},
): Peer => {
  const mine = new Set<FakeSocket>();
  const client = new LiveClient({
    userId: `u${server.sockets.size}`,
    createSocket: () => {
      const socket = server.createSocket();
      socket.canWrite = options.canWrite ?? true;
      mine.add(socket);
      return socket;
    },
  });
  client.start();
  const peer: Peer = {
    client,
    resets: [],
    sources: 0,
    socket: () => [...mine].at(-1) as FakeSocket,
    provider: undefined as unknown as CollabProvider,
  };
  peer.provider = new CollabProvider({
    topic,
    channel: client,
    canWrite: options.canWrite ?? true,
    loadSource:
      options.source ??
      (async () => {
        peer.sources += 1;
        return { content: stored, version: server.version };
      }),
    buildSeed: options.buildSeed ?? ((content) => seedState(content, editorExtensions())),
    onReset: (info) => peer.resets.push(info),
  });
  return peer;
};

/** The paragraph texts of a provider's document. */
const textOf = (peer: Peer): string[] =>
  peer.provider.doc
    .getXmlFragment("default")
    .toArray()
    .map((node) => (node as Y.XmlElement).toString().replace(/<[^>]+>/g, ""));

/** Types `text` at the start of the first paragraph, as an editor's transaction would. */
const type = (peer: Peer, text: string) => {
  const paragraph = peer.provider.doc.getXmlFragment("default").get(0) as Y.XmlElement;
  peer.provider.doc.transact(() => (paragraph.get(0) as Y.XmlText).insert(0, text));
};

const batch = () => vi.advanceTimersByTime(BATCH_MS);

describe("the provider's sync", () => {
  it("sends doc_sync from zero after the subscription is confirmed and is ready once synced", async () => {
    const a = join();
    await server.settle();
    expect(a.socket().texts.filter((f) => f.type === "doc_sync")[0]).toEqual({
      type: "doc_sync",
      topic,
      after: 0,
      epoch: 0,
    });
    // An empty log: the first writer seeds it (K29) from a fresh read of the article.
    expect(a.sources).toBe(1);
    expect(a.provider.getState()).toMatchObject({ status: "ready", hasLog: true });
    expect(textOf(a)).toEqual(["Lock the door."]);
    expect(a.provider.applied).toBe(1);
  });

  it("seeds with the article version it fetched, and a joiner replays the log without seeding", async () => {
    const a = join();
    await server.settle();
    const seed = a.socket().binaries.find((frame) => frame.kind === KIND_SEED);
    expect(seed && Number(new DataView(seed.payload.buffer, seed.payload.byteOffset, 8).getBigUint64(0))).toBe(3);
    const b = join();
    await server.settle();
    expect(b.sources).toBe(0);
    expect(textOf(b)).toEqual(["Lock the door."]);
    expect(b.socket().binaries.some((frame) => frame.kind === KIND_SEED)).toBe(false);
    expect(server.rows).toHaveLength(1);
  });

  it("when two writers open an unseeded page at once, one seed wins and the text is not doubled", async () => {
    const a = join();
    const b = join();
    await server.settle();
    expect(server.rows).toHaveLength(1);
    expect(textOf(a)).toEqual(["Lock the door."]);
    expect(textOf(b)).toEqual(["Lock the door."]);
    const rejected = [a, b].filter((peer) => peer.socket().texts.filter((f) => f.type === "doc_sync").length > 1);
    expect(rejected).toHaveLength(1);
    // The loser threw its seed away and asked again from its watermark (it already held the winner's row).
    expect(
      rejected[0]
        .socket()
        .texts.filter((f) => f.type === "doc_sync")
        .at(-1),
    ).toMatchObject({ after: 1 });
  });

  it("seeds again from a fresh read when the article moved on", async () => {
    server.version = 4;
    const versions = [3, 4];
    const a = join({ source: async () => ({ content: stored, version: versions.shift() ?? 4 }) });
    await server.settle();
    // The first seed named version 3 and was refused; the client synced again and seeded from a second read.
    expect(versions).toEqual([]);
    expect(a.provider.getState()).toMatchObject({ status: "ready", hasLog: true });
    expect(server.rows).toHaveLength(1);
  });

  it("a reader on an empty log shows nothing of its own and binds when the first update arrives", async () => {
    const reader = join({ canWrite: false });
    await server.settle();
    expect(reader.provider.getState()).toMatchObject({ status: "ready", hasLog: false });
    expect(reader.sources).toBe(0);
    const writer = join();
    await server.settle();
    expect(reader.provider.getState().hasLog).toBe(true);
    expect(textOf(reader)).toEqual(["Lock the door."]);
    expect(writer.provider.applied).toBe(1);
  });
});

describe("the provider's updates", () => {
  it("batches local changes for 100 ms into one frame and the others see them", async () => {
    const a = join();
    const b = join();
    await server.settle();
    const sentBefore = a.socket().binaries.filter((f) => f.kind === KIND_UPDATE).length;
    type(a, "A");
    type(a, "B");
    type(a, "C");
    await server.settle();
    expect(a.socket().binaries.filter((f) => f.kind === KIND_UPDATE)).toHaveLength(sentBefore);
    batch();
    await server.settle();
    expect(a.socket().binaries.filter((f) => f.kind === KIND_UPDATE)).toHaveLength(sentBefore + 1);
    expect(textOf(b)).toEqual(["CBALock the door."]);
    expect(a.provider.getState().unsent).toBe(false);
    expect(a.provider.applied).toBe(2);
  });

  it("never sends what it received back", async () => {
    const a = join();
    const b = join();
    await server.settle();
    type(a, "A");
    batch();
    await server.settle();
    expect(b.socket().binaries.filter((f) => f.kind === KIND_UPDATE)).toHaveLength(0);
  });

  it("keeps unacknowledged updates through a dropped connection and sends them after the next synced", async () => {
    const a = join();
    const b = join();
    await server.settle();
    type(a, "Offline ");
    batch();
    // The network drops before the server answers: nothing is acked.
    a.socket().drop();
    expect(a.provider.getState()).toMatchObject({ status: "offline", unsent: true });
    type(a, "more ");
    batch();
    vi.advanceTimersByTime(2_000);
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ status: "ready", unsent: false });
    batch();
    await server.settle();
    expect(textOf(a)).toEqual(["more Offline Lock the door."]);
    expect(textOf(b)).toEqual(textOf(a));
    // The reconnect asked for the log from the watermark it held.
    expect(a.socket().texts.find((f) => f.type === "doc_sync")).toMatchObject({ after: 1, epoch: 1 });
  });

  it("splits what it must send into frames of at most 256 KiB", async () => {
    const a = join();
    await server.settle();
    const sent = a.socket().binaries.length;
    a.socket().drop();
    // Each update is a little under a third of the limit once encoded.
    for (let index = 0; index < 4; index += 1) type(a, "x".repeat(90_000));
    batch();
    vi.advanceTimersByTime(2_000);
    await server.settle();
    const updates = a.socket().binaries.filter((f) => f.kind === KIND_UPDATE);
    expect(updates.length).toBeGreaterThan(1);
    expect(updates.every((f) => f.payload.length <= 256 * 1024)).toBe(true);
    expect(sent).toBeGreaterThan(0);
    expect(server.rows.length).toBe(1 + updates.length);
  });

  it("retries an unavailable store with backoff, and keeps the text", async () => {
    const a = join();
    const b = join();
    await server.settle();
    server.failNext = "unavailable";
    type(a, "Kept ");
    batch();
    await server.settle();
    expect(a.provider.getState().unsent).toBe(true);
    expect(textOf(b)).toEqual(["Lock the door."]);
    vi.advanceTimersByTime(1_100);
    await server.settle();
    expect(textOf(b)).toEqual(["Kept Lock the door."]);
    expect(a.provider.getState().unsent).toBe(false);
  });

  it("backs off on rate, and pauses with log_full until an ack", async () => {
    const a = join();
    await server.settle();
    server.failNext = "log_full";
    type(a, "x");
    batch();
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ paused: true, unsent: true });
    vi.advanceTimersByTime(1_100);
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ paused: false, unsent: false });
    server.failNext = "rate";
    type(a, "y");
    batch();
    await server.settle();
    expect(a.provider.getState().unsent).toBe(true);
    vi.advanceTimersByTime(2_100);
    await server.settle();
    expect(a.provider.getState().unsent).toBe(false);
  });

  it("goes read-only when the server refuses a reader's or a locked page's update", async () => {
    const a = join();
    await server.settle();
    a.socket().canWrite = false;
    type(a, "x");
    batch();
    await server.settle();
    expect(a.provider.getState().readOnly).toBe(true);
    a.provider.setCanWrite(false);
    a.provider.setCanWrite(true);
    expect(a.provider.getState().readOnly).toBe(false);
  });

  it("is broken, and says so to the server, when an update cannot be applied", async () => {
    const a = join();
    await server.settle();
    const garbage = new Uint8Array([0, 0, 0, 0, 0, 0, 0, 9, 255, 255, 255, 255, 255, 255, 255, 255, 255]);
    a.socket().pushBinary(KIND_UPDATE, topic, garbage);
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ status: "broken", failure: "apply" });
    expect(server.errors).toHaveLength(1);
  });
});

describe("resets and stale copies", () => {
  it("on doc_reset reports unsent text and finishes", async () => {
    const a = join();
    await server.settle();
    type(a, "Typed ");
    server.reset();
    await server.settle();
    expect(a.resets).toEqual([{ unsent: true }]);
    // A finished provider writes nothing more: the late flush finds the log's epoch gone.
    batch();
    await server.settle();
    expect(server.rows).toHaveLength(0);
  });

  it("on doc_reset with nothing unsent reports so", async () => {
    const a = join();
    await server.settle();
    server.reset();
    await server.settle();
    expect(a.resets).toEqual([{ unsent: false }]);
  });

  it("asks again from its watermark on doc_stale", async () => {
    const a = join();
    await server.settle();
    a.socket().push({ type: "doc_stale", topic });
    await server.settle();
    const syncs = a.socket().texts.filter((f) => f.type === "doc_sync");
    expect(syncs.at(-1)).toEqual({ type: "doc_sync", topic, after: 1, epoch: 1 });
    expect(a.provider.getState().status).toBe("ready");
  });

  it("does not write between a doc_stale and the next synced", async () => {
    const a = join();
    await server.settle();
    a.socket().push({ type: "doc_stale", topic });
    a.socket().deliver();
    type(a, "Held ");
    batch();
    expect(a.socket().binaries.filter((f) => f.kind === KIND_UPDATE)).toHaveLength(0);
    await server.settle();
    batch();
    await server.settle();
    expect(server.rows).toHaveLength(2);
  });
});

describe("compaction", () => {
  it("answers a request with the whole state, up to its watermark", async () => {
    const a = join();
    const b = join();
    await server.settle();
    for (const word of ["a", "b", "c"]) {
      type(b, word);
      batch();
      await server.settle();
    }
    expect(server.newest).toBe(4);
    server.requestCompaction(a.socket());
    await server.settle();
    const compact = a.socket().binaries.find((frame) => frame.kind === KIND_COMPACT);
    expect(compact && Number(new DataView(compact.payload.buffer, compact.payload.byteOffset, 8).getBigUint64(0))).toBe(
      4,
    );
    expect(server.rows).toHaveLength(1);
    // A joiner now replays one row and has everything.
    const c = join();
    await server.settle();
    expect(textOf(c)).toEqual(["cbaLock the door."]);
  });

  it("does not compact beyond what it has applied", async () => {
    const a = join();
    await server.settle();
    a.socket().push({ type: "compact_requested", topic, upTo: 9 });
    await server.settle();
    expect(a.socket().binaries.some((frame) => frame.kind === KIND_COMPACT)).toBe(false);
  });

  it("keeps converging after a compaction", async () => {
    const a = join();
    const b = join();
    await server.settle();
    type(a, "1");
    batch();
    await server.settle();
    server.requestCompaction(b.socket());
    await server.settle();
    type(a, "2");
    type(b, "3");
    batch();
    await server.settle();
    expect(textOf(a)).toEqual(textOf(b));
    expect(textOf(a)[0]).toContain("Lock the door.");
    expect(textOf(a)[0]).toHaveLength("123Lock the door.".length);
  });
});

describe("awareness", () => {
  it("relays a writer's state to the others as awareness frames, and never a reader's", async () => {
    const a = join();
    const b = join();
    const reader = join({ canWrite: false });
    await server.settle();
    a.provider.awareness.setLocalStateField("user", { name: "Kari", color: "#15803D" });
    reader.provider.awareness.setLocalStateField("user", { name: "Leser", color: "#1D4ED8" });
    vi.advanceTimersByTime(150);
    await server.settle();
    expect(b.provider.awareness.getStates().get(a.provider.doc.clientID)).toEqual({
      user: { name: "Kari", color: "#15803D" },
    });
    expect(reader.socket().binaries.filter((f) => f.kind === KIND_AWARENESS)).toHaveLength(0);
    expect(b.provider.awareness.getStates().has(reader.provider.doc.clientID)).toBe(false);
  });

  it("tells the others it left", async () => {
    const a = join();
    const b = join();
    await server.settle();
    a.provider.awareness.setLocalStateField("user", { name: "Kari", color: "#15803D" });
    vi.advanceTimersByTime(150);
    await server.settle();
    const id = a.provider.doc.clientID;
    expect(b.provider.awareness.getStates().has(id)).toBe(true);
    a.provider.destroy();
    await server.settle();
    expect(b.provider.awareness.getStates().has(id)).toBe(false);
  });
});

describe("convergence", () => {
  it("two clients that edit at the same time, offline and across a compaction end with the same document", async () => {
    const a = join();
    const b = join();
    await server.settle();
    // Concurrent: neither has heard the other.
    type(a, "A1 ");
    type(b, "B1 ");
    batch();
    await server.settle();
    expect(textOf(a)).toEqual(textOf(b));
    // One goes offline and both keep typing.
    b.socket().drop();
    type(a, "A2 ");
    type(b, "B2 ");
    batch();
    await server.settle();
    server.requestCompaction(a.socket());
    await server.settle();
    vi.advanceTimersByTime(2_000);
    await server.settle();
    batch();
    await server.settle();
    expect(textOf(a)).toEqual(textOf(b));
    for (const part of ["A1", "B1", "A2", "B2", "Lock the door."]) expect(textOf(a)[0]).toContain(part);
    // A newcomer replays the compacted log and agrees.
    const c = join();
    await server.settle();
    expect(textOf(c)).toEqual(textOf(a));
    expect(Y.encodeStateVector(c.provider.doc)).toEqual(Y.encodeStateVector(a.provider.doc));
  });
});

describe("hardening", () => {
  it("gives up on a seed the server never answers after 10 s and syncs again", async () => {
    // The seed is swallowed: no seed_accepted, no seed_rejected.
    const a = join();
    const real = server.receive.bind(server);
    vi.spyOn(server, "receive").mockImplementation((socket, data) => {
      if (typeof data !== "string" && data[0] === KIND_SEED) return;
      real(socket, data);
    });
    await server.settle();
    expect(a.provider.getState().status).toBe("seeding");
    vi.advanceTimersByTime(REPLY_WATCH_MS + 100);
    vi.advanceTimersByTime(1_500);
    await server.settle();
    const syncs = a.socket().texts.filter((f) => f.type === "doc_sync");
    expect(syncs.length).toBeGreaterThan(1);
  });

  it("gives up on an unanswered compaction and may compact again after", async () => {
    const a = join();
    await server.settle();
    const real = server.receive.bind(server);
    let swallow = true;
    let compactions = 0;
    vi.spyOn(server, "receive").mockImplementation((socket, data) => {
      if (typeof data !== "string" && data[0] === KIND_COMPACT) {
        compactions += 1;
        if (swallow) return;
      }
      real(socket, data);
    });
    type(a, "x");
    batch();
    await server.settle();
    server.requestCompaction(a.socket());
    await server.settle();
    expect(compactions).toBe(1);
    server.requestCompaction(a.socket());
    await server.settle();
    expect(compactions).toBe(1);
    vi.advanceTimersByTime(REPLY_WATCH_MS + 2_000);
    await server.settle();
    swallow = false;
    server.requestCompaction(a.socket());
    await server.settle();
    expect(compactions).toBe(2);
  });

  it("keeps its text when the server says read_only, and sends it when access is read again", async () => {
    const a = join();
    const b = join();
    await server.settle();
    a.socket().canWrite = false;
    type(a, "Held ");
    batch();
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ readOnly: true, unsent: true });
    a.socket().canWrite = true;
    a.provider.setCanWrite(true);
    await server.settle();
    batch();
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ readOnly: false, unsent: false });
    expect(textOf(b)).toEqual(["Held Lock the door."]);
  });

  it("counts a late ack of an old send once: a resync does not requeue what is still answered", async () => {
    const a = join();
    const b = join();
    await server.settle();
    type(a, "Once ");
    batch();
    // The update is stored and acked, but the server resyncs before the client hears the ack.
    server.resync(a.socket());
    await server.settle();
    batch();
    await server.settle();
    expect(a.provider.getState().unsent).toBe(false);
    // Exactly one row for the update: nothing was sent twice.
    expect(server.rows).toHaveLength(2);
    expect(textOf(b)).toEqual(["Once Lock the door."]);
  });

  it("fails cleanly when the seed cannot be built", async () => {
    const a = join({
      buildSeed: () => {
        throw new Error("no schema");
      },
    });
    await server.settle();
    expect(a.provider.getState()).toMatchObject({ status: "broken", failure: "apply" });
    expect(server.errors).toEqual(["no schema"]);
  });

  it("applies rows that arrive during a replay after synced, and agrees with the log", async () => {
    const a = join();
    await server.settle();
    type(a, "Early ");
    batch();
    await server.settle();
    server.holdReplay = true;
    const b = join();
    await server.settle();
    type(a, "Late ");
    batch();
    await server.settle();
    expect(b.provider.getState().status).toBe("syncing");
    server.releaseReplay(b.socket());
    await server.settle();
    expect(b.provider.getState().status).toBe("ready");
    expect(textOf(b)).toEqual(textOf(a));
  });

  it("is asked to compact when an append leaves the log long", async () => {
    server.compactAfter = 2;
    const a = join();
    await server.settle();
    for (const word of ["a", "b"]) {
      type(a, word);
      batch();
      await server.settle();
    }
    expect(a.socket().binaries.some((f) => f.kind === KIND_COMPACT)).toBe(true);
    expect(server.rows.length).toBeLessThan(3);
  });

  it("answers a stale epoch's seed with a reset", async () => {
    const a = join();
    await server.settle();
    server.reset();
    await server.settle();
    expect(a.resets).toHaveLength(1);
  });

  it("a doc_stale from the server resyncs", async () => {
    const a = join();
    await server.settle();
    server.stale(a.socket());
    await server.settle();
    expect(a.socket().texts.filter((f) => f.type === "doc_sync")).toHaveLength(2);
  });
});
