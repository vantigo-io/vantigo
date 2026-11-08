import { Editor } from "@tiptap/core";
import { LiveClient } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FakeDocServer, type FakeSocket } from "../../test/collab-server";
import { editorExtensions, safeCaretUser } from "../extensions";
import { BATCH_MS, CollabProvider } from "./provider";
import { seedState } from "./seed";
import { CURSOR_COLOURS, colourOf } from "./user";

const topic = "knowledge.doc:a1";
const stored = {
  type: "doc",
  content: [
    { type: "heading", attrs: { level: 1 }, content: [{ type: "text", text: "Routines" }] },
    { type: "paragraph", content: [{ type: "text", text: "Lock the door." }] },
  ],
};

let server: FakeDocServer;
beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0.5);
  server = new FakeDocServer(topic);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** A browser: a provider on its own socket and an editor bound to its document, as the page builds them. */
const browser = async (name: string) => {
  const mine: FakeSocket[] = [];
  const client = new LiveClient({
    userId: name,
    createSocket: () => {
      const socket = server.createSocket();
      mine.push(socket);
      return socket;
    },
  });
  client.start();
  const provider = new CollabProvider({
    topic,
    channel: client,
    canWrite: true,
    loadSource: async () => ({ content: stored, version: server.version }),
    buildSeed: (content) => seedState(content, editorExtensions()),
    onReset: () => {},
  });
  await server.settle();
  const editor = new Editor({
    extensions: editorExtensions("", { collab: { provider, user: { name, color: "#15803D" } } }),
  });
  return { provider, editor, socket: () => mine.at(-1) as FakeSocket };
};

const send = async () => {
  vi.advanceTimersByTime(BATCH_MS);
  await server.settle();
};

describe("the collaborative editor", () => {
  it("shows the seeded page with every block id the editor gives, the same in each browser", async () => {
    const a = await browser("a");
    const b = await browser("b");
    const json = a.editor.getJSON();
    expect(json.content?.map((block) => block.type)).toEqual(["heading", "paragraph"]);
    expect(json.content?.every((block) => typeof block.attrs?.id === "string")).toBe(true);
    expect(b.editor.getJSON()).toEqual(json);
  });

  it("converges after concurrent edits in one paragraph", async () => {
    const a = await browser("a");
    const b = await browser("b");
    // Positions: heading is 0..10 (8 chars + 2), the paragraph's text starts at 11.
    a.editor.commands.insertContentAt(11, "Always ");
    b.editor.commands.insertContentAt(11, "Never ");
    b.editor.commands.insertContentAt(b.editor.state.doc.content.size - 1, " Twice.");
    await send();
    await send();
    expect(a.editor.getJSON()).toEqual(b.editor.getJSON());
    const text = a.editor.getText();
    for (const part of ["Always ", "Never ", "Lock the door.", " Twice."]) expect(text).toContain(part);
  });

  it("converges after interleaved offline edits, a duplicate delivery and a compaction", async () => {
    const a = await browser("a");
    const b = await browser("b");
    b.socket().drop();
    a.editor.commands.insertContentAt(1, "A: ");
    b.editor.commands.insertContentAt(1, "B: ");
    await send();
    server.requestCompaction(a.socket());
    await server.settle();
    // The same row delivered twice is harmless.
    const last = server.rows.at(-1);
    if (last) a.socket().push({ type: "doc_stale", topic });
    vi.advanceTimersByTime(2_000);
    await server.settle();
    await send();
    await send();
    expect(a.editor.getJSON()).toEqual(b.editor.getJSON());
    expect(a.editor.getText()).toContain("A: ");
    expect(a.editor.getText()).toContain("B: ");
  });

  it("undoes only the person's own changes", async () => {
    const a = await browser("a");
    const b = await browser("b");
    a.editor.commands.insertContentAt(11, "Mine ");
    await send();
    b.editor.commands.insertContentAt(b.editor.state.doc.content.size - 1, " Theirs");
    await send();
    a.editor.commands.undo();
    await send();
    expect(a.editor.getText()).toContain("Theirs");
    expect(a.editor.getText()).not.toContain("Mine");
    expect(b.editor.getText()).toBe(a.editor.getText());
  });

  it("brings everyone along when one person replaces the whole text in one change", async () => {
    const a = await browser("a");
    const b = await browser("b");
    a.editor.commands.setContent({
      type: "doc",
      content: [{ type: "paragraph", content: [{ type: "text", text: "An older version." }] }],
    });
    await send();
    expect(b.editor.getText()).toContain("An older version.");
    expect(b.editor.getText()).not.toContain("Lock the door.");
    // Restoring is one change: one undo brings the previous text back for everyone.
    a.editor.commands.undo();
    await send();
    expect(b.editor.getText()).toContain("Lock the door.");
  });

  it("shows the others' cursors from their awareness", async () => {
    const a = await browser("a");
    const b = await browser("b");
    vi.advanceTimersByTime(300);
    await server.settle();
    expect(b.editor.storage.collaborationCaret.users.map((user) => user.name)).toContain("a");
    expect(a.editor.storage.collaborationCaret.users.map((user) => user.name)).toContain("b");
  });

  it("shows only what the page chooses of another's awareness: a cut name and one of its colours", () => {
    const hostile = safeCaretUser({ id: "u1", name: "N".repeat(200), color: "red; background: url(//evil)" });
    expect(hostile.name).toHaveLength(60);
    expect(hostile.color).toBe(colourOf("u1"));
    expect(CURSOR_COLOURS).toContain(hostile.color);
    expect(safeCaretUser({ name: "Kari", color: CURSOR_COLOURS[3] })).toEqual({
      name: "Kari",
      color: CURSOR_COLOURS[3],
    });
  });
});
