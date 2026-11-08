import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Editor } from "@tiptap/react";
import { KIND_UPDATE, LiveClient } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type * as Y from "yjs";
import type { KnowledgeArticle } from "../api/articles";
import { BATCH_MS, CollabProvider } from "../editor/collab/provider";
import { seedState } from "../editor/collab/seed";
import { editorExtensions } from "../editor/extensions";
import { article, type Call, fakeServer, ids, jsonResponse, refusal } from "../test/api";
import { FakeDocServer } from "../test/collab-server";
import { renderRoute } from "../test/render";

const url = `/api/v1/knowledge/articles/${ids.routines}`;
const page = `/knowledge/articles/${ids.routines}`;
const topic = `knowledge.doc:${ids.routines}`;

let server: FakeDocServer;
let current: KnowledgeArticle;
beforeEach(() => {
  server = new FakeDocServer(topic);
  current = article();
});
afterEach(() => {
  vi.restoreAllMocks();
});

const saved = (version: number) =>
  jsonResponse(200, {
    id: ids.routines,
    version,
    updatedAt: "2026-11-07T10:00:00Z",
    updatedBy: { userId: ids.kari, displayName: "Kari Nordmann" },
  });

/** The article API: GET answers `current`; a content save is handed to `onPut`, which answers it. */
const api = (onPut: (call: Call) => Response) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
    if (call.method === "PUT" && call.url === `${url}/content`) return onPut(call);
    return undefined;
  });

const pump = () => act(() => server.settle((run) => act(run)));

const open = async (permission: "write" | "read" = "write") => {
  current = article({ permission });
  server.version = current.version;
  const view = renderRoute(page, undefined, undefined, { userId: ids.ola, createSocket: server.createSocket });
  await screen.findByRole("textbox", { name: "Title" });
  await waitFor(async () => {
    await pump();
    expect(screen.getByText("Lock the door.")).toBeInTheDocument();
  });
  // A writer's page binds to the log once it is seeded; until then the saved text is shown as in K-A.
  if (permission === "write") {
    await waitFor(async () => {
      await pump();
      expect(server.rows).toHaveLength(1);
    });
    await pump();
  }
  return view;
};

/** Another browser on the same page: a provider on its own socket, typing into its document. */
const peer = async (canWrite = true) => {
  const client = new LiveClient({ userId: ids.kari, createSocket: server.createSocket });
  client.start();
  const provider = new CollabProvider({
    topic,
    channel: client,
    canWrite,
    loadSource: async () => ({ content: current.content, version: current.version }),
    buildSeed: (content) => seedState(content, editorExtensions()),
    onReset: () => {},
  });
  await pump();
  return {
    provider,
    type: async (text: string) => {
      const paragraph = provider.doc.getXmlFragment("default").get(0) as Y.XmlElement;
      provider.doc.transact(() => (paragraph.get(0) as Y.XmlText).insert(0, text));
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, BATCH_MS + 20));
      });
      await pump();
    },
  };
};

describe("an article in live co-editing", () => {
  it("seeds an unseeded page from its saved text and shows no K-A editing aids", async () => {
    api(() => saved(4));
    await open();
    expect(server.rows).toHaveLength(1);
    expect(screen.queryByText("Unsaved changes on this device")).toBeNull();
    expect(screen.queryByText(/is editing/)).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows what another person types, and a reader sees it too", async () => {
    api(() => saved(4));
    await open();
    const kari = await peer();
    await kari.type("Kari was here. ");
    await waitFor(async () => {
      await pump();
      expect(screen.getByText(/Kari was here\. Lock the door\./)).toBeInTheDocument();
    });
    // What the log delivers is not this person's change to save.
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  it("a reader keeps the saved text on an unseeded page and follows the log once someone seeds it", async () => {
    api(() => saved(4));
    await open("read");
    expect(server.rows).toHaveLength(0);
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveAttribute("readonly");
    const kari = await peer();
    await kari.type("Live: ");
    await waitFor(async () => {
      await pump();
      expect(screen.getByText(/Live: Lock the door\./)).toBeInTheDocument();
    });
  });

  it("saves the text through the log's save after 5 seconds, without a version", async () => {
    const mine = api(() => saved(4));
    await open();
    const title = screen.getByRole("textbox", { name: "Title" });
    await userEvent.clear(title);
    await userEvent.type(title, "Routines, together");
    expect(mine.count("PUT", `${url}/content`)).toBe(0);
    await waitFor(() => expect(mine.count("PUT", `${url}/content`)).toBe(1), { timeout: 9000 });
    const body = mine.sent("PUT", `${url}/content`)?.body as Record<string, unknown>;
    expect(body).toMatchObject({ title: "Routines, together", mode: "collab", content: { type: "doc" } });
    expect(body).not.toHaveProperty("expectedVersion");
    expect(screen.queryByRole("dialog")).toBeNull();
  }, 20_000);

  it("holds a save while the connection is down and sends it through the log when it returns", async () => {
    const mine = api(() => saved(4));
    await open();
    for (const socket of [...server.sockets]) act(() => socket.drop());
    expect(await screen.findByText(/Offline\. Your changes are kept/)).toBeInTheDocument();
    const title = screen.getByRole("textbox", { name: "Title" });
    await userEvent.clear(title);
    await userEvent.type(title, "Offline title");
    // K-A's save would reset the log under the others, or be refused: nothing is sent while offline.
    await new Promise((resolve) => setTimeout(resolve, 2500));
    expect(mine.count("PUT", `${url}/content`)).toBe(0);
    await waitFor(
      async () => {
        await pump();
        expect(mine.count("PUT", `${url}/content`)).toBe(1);
      },
      { timeout: 12_000 },
    );
    expect(mine.sent("PUT", `${url}/content`)?.body).toMatchObject({ title: "Offline title", mode: "collab" });
    expect(screen.queryByText(/Offline\. Your changes are kept/)).toBeNull();
  }, 25_000);

  it("keeps a single-mode typist's text as a draft when the log turns out to be live (409 collab_active)", async () => {
    const mine = api(() => refusal(409, "collab_active"));
    current = article();
    server.version = current.version;
    // The socket is not opened yet: the page is K-A's single editor.
    renderRoute(page, undefined, undefined, { userId: ids.ola, createSocket: server.createSocket });
    const title = await screen.findByRole("textbox", { name: "Title" });
    await userEvent.clear(title);
    await userEvent.type(title, "Typed alone");
    await waitFor(() => expect(mine.count("PUT", `${url}/content`)).toBe(1), { timeout: 4000 });
    expect(mine.sent("PUT", `${url}/content`)?.body).toMatchObject({ expectedVersion: 3 });
    expect(
      await screen.findByText(/This page was replaced by a save made without the live connection/),
    ).toBeInTheDocument();
    expect(window.localStorage.getItem(`knowledge.draft.${ids.routines}`)).toContain("Typed alone");
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveValue("Routines");
    await pump();
    await waitFor(async () => {
      await pump();
      expect(server.rows).toHaveLength(1);
    });
  }, 20_000);

  it("offers back a typed title that the log's reset replaced, though it was never in the log", async () => {
    api(() => saved(4));
    await open();
    await userEvent.type(screen.getByRole("textbox", { name: "Title" }), "!");
    current = article({ version: 4, title: "Theirs" });
    server.version = 4;
    server.reset();
    await pump();
    expect(
      await screen.findByText(/This page was replaced by a save made without the live connection/),
    ).toBeInTheDocument();
    expect(window.localStorage.getItem(`knowledge.draft.${ids.routines}`)).toContain("Routines!");
  });

  it("restores a version into the editor as one change, without the restore endpoint", async () => {
    const mine = fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
      if (call.method === "GET" && call.url === `${url}/versions`)
        return jsonResponse(200, {
          items: [
            { version: 3, title: "Routines", current: true, createdAt: "2026-11-07T10:00:00Z", createdBy: null },
            { version: 2, title: "Old routines", current: false, createdAt: "2026-11-06T10:00:00Z", createdBy: null },
          ],
        });
      if (call.method === "GET" && call.url === `${url}/versions/2`)
        return jsonResponse(200, {
          version: 2,
          title: "Old routines",
          createdAt: "2026-11-06T10:00:00Z",
          createdBy: null,
          content: {
            type: "doc",
            content: [{ type: "paragraph", content: [{ type: "text", text: "Leave the door open." }] }],
          },
        });
      if (call.method === "PUT" && call.url === `${url}/content`) return saved(4);
      return undefined;
    });
    await open();
    const kari = await peer();
    await userEvent.click(screen.getByRole("button", { name: "Page menu" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /history/i }));
    const drawer = await screen.findByRole("dialog");
    await userEvent.click(await within(drawer).findByText(/Old routines/));
    await userEvent.click(await within(drawer).findByRole("button", { name: "Restore this version" }));
    await waitFor(
      async () => {
        await pump();
        expect(mine.count("PUT", `${url}/content`)).toBe(1);
      },
      { timeout: 4000 },
    );
    expect(mine.sent("PUT", `${url}/content`)?.body).toMatchObject({ mode: "collab", title: "Old routines" });
    expect(mine.calls.some((call) => call.url.includes("/restore"))).toBe(false);
    // The other person's document has the restored text.
    await waitFor(async () => {
      await pump();
      expect(kari.provider.doc.getXmlFragment("default").toString()).toContain("Leave the door open.");
    });
    expect(kari.provider.doc.getXmlFragment("default").toString()).not.toContain("Lock the door.");
  }, 20_000);

  it("says the live copy cannot be opened when an update cannot be applied, and rebuilds it on request", async () => {
    const mine = fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
      if (call.method === "POST" && call.url === `${url}/collab/reset`) return new Response(null, { status: 204 });
      return undefined;
    });
    await open();
    const garbage = new Uint8Array([0, 0, 0, 0, 0, 0, 0, 9, 255, 255, 255, 255, 255, 255, 255, 255, 255]);
    act(() => [...server.sockets].at(0)?.pushBinary(KIND_UPDATE, topic, garbage));
    await pump();
    expect(await screen.findByText("The live copy of this page cannot be opened")).toBeInTheDocument();
    expect(server.errors).toHaveLength(1);
    // The saved text is shown, read-only, until the writer rebuilds.
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveAttribute("readonly");
    await userEvent.click(screen.getByRole("button", { name: "Rebuild from the last saved version" }));
    await waitFor(() => expect(mine.count("POST", `${url}/collab/reset`)).toBe(1));
  });

  it("says live editing is paused while the log is full, and keeps the text", async () => {
    api(() => saved(4));
    await open();
    const dom = document.querySelector(".ProseMirror") as HTMLElement & { editor: Editor };
    server.failNext = "log_full";
    act(() => void dom.editor.commands.insertContentAt(1, "Kept "));
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, BATCH_MS + 20));
    });
    await pump();
    expect(await screen.findByText("Live editing is paused for this page")).toBeInTheDocument();
    expect(screen.getByText(/Kept Lock the door\./)).toBeInTheDocument();
  });
});
