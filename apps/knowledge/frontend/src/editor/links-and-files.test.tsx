import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Editor, JSONContent } from "@tiptap/core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeFile } from "../api/files";
import { formatBytes } from "../lib/bytes";
import { fakeServer, ids, jsonResponse, refusal } from "../test/api";
import { renderEditor } from "../test/editor";
import { linkNodeForUrl } from "./nodes/links";
import { nodeForFile } from "./uploads";

const docOf = (editor: Editor) => editor.getJSON() as JSONContent;
const fileId = "00000000-0000-4000-8000-0000000000f1";

const type = (editor: Editor, text: string) =>
  act(() => {
    editor.chain().focus().insertContent(text).run();
  });

const press = (editor: Editor, key: string) =>
  act(() => {
    fireEvent.keyDown(editor.view.dom, { key });
  });

/** A stand-in for XMLHttpRequest, which the upload uses for its progress: the test answers each request. */
class FakeXhr {
  static last: FakeXhr | null = null;
  static sent: { url: string; body: FormData }[] = [];
  status = 0;
  responseText = "";
  responseType = "";
  withCredentials = false;
  url = "";
  upload: { onprogress: ((event: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = {
    onprogress: null,
  };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  open(_method: string, url: string) {
    this.url = url;
  }
  send(body: FormData) {
    FakeXhr.last = this;
    FakeXhr.sent.push({ url: this.url, body });
  }
  aborted = false;
  abort() {
    this.aborted = true;
    this.onabort?.();
  }
  getResponseHeader() {
    return "application/problem+json";
  }
  respond(status: number, body: unknown) {
    this.status = status;
    this.responseText = JSON.stringify(body);
    this.onload?.();
  }
}

const uploaded = (overrides: Partial<KnowledgeFile> = {}): KnowledgeFile => ({
  id: fileId,
  url: `/api/v1/knowledge/files/${fileId}`,
  filename: "door.png",
  contentType: "image/png",
  size: 2048,
  isImage: true,
  ...overrides,
});

describe("images and files", () => {
  it("turns a JPEG or PNG into an image and anything else into a file chip", () => {
    expect(nodeForFile(uploaded())).toEqual({
      type: "image",
      attrs: { src: `/api/v1/knowledge/files/${fileId}`, alt: "door.png" },
    });
    expect(nodeForFile(uploaded({ isImage: false, filename: "plan.pdf", contentType: "application/pdf" }))).toEqual({
      type: "fileAttachment",
      attrs: { fileId, filename: "plan.pdf", size: 2048, contentType: "application/pdf" },
    });
    expect(formatBytes(2048)).toBe("2.0 KB");
    expect(formatBytes(30 * 1024 * 1024)).toBe("30 MB");
  });

  it("uploads a pasted image into the article with its progress, and inserts it when done", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXhr);
    FakeXhr.sent = [];
    const { editor } = await renderEditor({ articleId: ids.routines });
    const file = new File(["png"], "door.png", { type: "image/png" });
    act(() => {
      editor.storage.knowledgeUploads.start([file]);
    });
    expect(FakeXhr.sent[0].url).toBe(`/api/v1/knowledge/articles/${ids.routines}/files`);
    expect(FakeXhr.sent[0].body.get("file")).toBe(file);
    expect(await screen.findByRole("status", { name: "Uploading door.png" })).toBeInTheDocument();
    act(() => FakeXhr.last?.upload.onprogress?.({ lengthComputable: true, loaded: 1, total: 2 }));
    expect(await screen.findByText("50 %")).toBeInTheDocument();
    act(() => FakeXhr.last?.respond(201, uploaded()));
    await waitFor(() => expect(docOf(editor).content?.some((node) => node.type === "image")).toBe(true));
    const image = docOf(editor).content?.find((node) => node.type === "image");
    expect(image?.attrs).toMatchObject({ src: `/api/v1/knowledge/files/${fileId}`, alt: "door.png" });
    expect(screen.queryByRole("status", { name: "Uploading door.png" })).toBeNull();
    expect(await screen.findByRole("img", { name: "door.png" })).toHaveAttribute(
      "src",
      `/api/v1/knowledge/files/${fileId}`,
    );
  });

  it("shows a file chip that downloads the file", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXhr);
    const { editor } = await renderEditor({ articleId: ids.routines });
    act(() => {
      editor.storage.knowledgeUploads.start([new File(["%PDF"], "plan.pdf", { type: "application/pdf" })]);
    });
    act(() =>
      FakeXhr.last?.respond(201, uploaded({ isImage: false, filename: "plan.pdf", contentType: "application/pdf" })),
    );
    const download = await screen.findByRole("link", { name: "Download plan.pdf" });
    expect(download).toHaveAttribute("href", `/api/v1/knowledge/files/${fileId}`);
    expect(screen.getByText("plan.pdf")).toBeInTheDocument();
  });

  it("refuses a file over 25 MB without sending it, and says why a refused one failed", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXhr);
    FakeXhr.sent = [];
    const { editor } = await renderEditor({ articleId: ids.routines });
    const big = new File(["x"], "film.mov");
    Object.defineProperty(big, "size", { value: 26 * 1024 * 1024 });
    act(() => {
      editor.storage.knowledgeUploads.start([big]);
    });
    expect(FakeXhr.sent).toHaveLength(0);
    expect(await screen.findByRole("alert")).toHaveTextContent("A file can be at most 25 MB.");

    act(() => {
      editor.storage.knowledgeUploads.start([new File(["x"], "door.png", { type: "image/png" })]);
    });
    act(() => FakeXhr.last?.respond(409, { title: "Locked", status: 409, code: "article_locked" }));
    await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(2));
    expect(screen.getByText("The page is locked, so nothing can be added to it.")).toBeInTheDocument();
    await userEvent.click(within(screen.getAllByRole("alert")[0]).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(1));
  });

  it("offers Image and File in the slash menu, which open the file picker", async () => {
    const { editor } = await renderEditor({ articleId: ids.routines });
    const picker = screen.getByTestId("knowledge-file-picker") as HTMLInputElement;
    const click = vi.spyOn(picker, "click").mockImplementation(() => undefined);
    type(editor, "/bilde");
    await screen.findByRole("listbox", { name: "Blocks" });
    press(editor, "Enter");
    expect(click).toHaveBeenCalled();
    expect(picker.accept).toBe("image/jpeg,image/png");
  });
});

describe("uploads that outlive their moment", () => {
  it("aborts an upload when the editor goes away", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXhr);
    const { editor, unmount } = await renderEditor({ articleId: ids.routines });
    act(() => {
      editor.storage.knowledgeUploads.start([new File(["png"], "door.png", { type: "image/png" })]);
    });
    const request = FakeXhr.last;
    unmount();
    // TipTap destroys an unmounted editor on the next tick.
    await waitFor(() => expect(request?.aborted).toBe(true));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("adds nothing to a page locked before the upload finished, and says so", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXhr);
    const { editor } = await renderEditor({ articleId: ids.routines });
    act(() => {
      editor.storage.knowledgeUploads.start([new File(["png"], "door.png", { type: "image/png" })]);
    });
    act(() => editor.setEditable(false));
    act(() => FakeXhr.last?.respond(201, uploaded()));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The page was locked before it finished, so it was not added.",
    );
    expect(JSON.stringify(docOf(editor))).not.toContain(fileId);
  });
});

describe("images pasted from another article", () => {
  const foreign = "00000000-0000-4000-8000-0000000000f9";
  const copyUrl = `/api/v1/knowledge/articles/${ids.routines}/files/copy`;
  // jsdom has no ClipboardEvent, which ProseMirror's pasteHTML builds.
  beforeEach(() => {
    vi.stubGlobal(
      "ClipboardEvent",
      class extends Event {
        clipboardData = null;
      },
    );
  });

  it("copies the file into this article and inserts the copy; the other article's id never enters the page", async () => {
    let answer: (response: Response) => void = () => undefined;
    const server = fakeServer((call) => {
      if (call.method === "POST" && call.url === copyUrl) return new Promise<Response>((resolve) => (answer = resolve));
      return undefined;
    });
    const { editor } = await renderEditor({ articleId: ids.routines });
    act(() => {
      editor.view.pasteHTML(`<img src="/api/v1/knowledge/files/${foreign}" alt="door.png">`);
    });
    await waitFor(() => expect(server.sent("POST", copyUrl)?.body).toEqual({ fileId: foreign }));
    expect(JSON.stringify(docOf(editor))).not.toContain(foreign);
    expect(screen.getByRole("status", { name: "Uploading door.png" })).toBeInTheDocument();

    act(() => answer(jsonResponse(201, uploaded())));
    await waitFor(() => expect(docOf(editor).content?.some((node) => node.type === "image")).toBe(true));
    expect(JSON.stringify(docOf(editor))).toContain(fileId);
    expect(JSON.stringify(docOf(editor))).not.toContain(foreign);
  });

  it("copies nothing for a file this page already holds", async () => {
    const server = fakeServer(() => undefined);
    const { editor } = await renderEditor({
      articleId: ids.routines,
      content: { type: "doc", content: [{ type: "image", attrs: { src: `/api/v1/knowledge/files/${fileId}` } }] },
    });
    act(() => {
      editor.commands.focus("end");
      editor.view.pasteHTML(`<img src="/api/v1/knowledge/files/${fileId}" alt="again">`);
    });
    expect(server.count("POST", copyUrl)).toBe(0);
    expect(docOf(editor).content?.filter((node) => node.type === "image")).toHaveLength(2);
  });

  it("leaves the image out and says so when the copy fails", async () => {
    fakeServer((call) =>
      call.method === "POST" && call.url === copyUrl ? refusal(503, "storage_unavailable") : undefined,
    );
    const { editor } = await renderEditor({ articleId: ids.routines });
    act(() => {
      editor.view.pasteHTML(`<p>Before</p><img src="/api/v1/knowledge/files/${foreign}" alt="door.png">`);
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("The file storage could not be reached.");
    expect(JSON.stringify(docOf(editor))).not.toContain(foreign);
    expect(docOf(editor).content?.some((node) => node.type === "image")).toBe(false);
    expect(JSON.stringify(docOf(editor))).toContain("Before");
  });
});

describe("mentions", () => {
  it("opens the directory on @, inserts the chosen person and shows their current name", async () => {
    fakeServer((call) => {
      if (call.url.startsWith("/api/v1/knowledge/assignable-users"))
        return jsonResponse(200, [{ userId: ids.kari, displayName: "Kari Nordmann" }]);
      return undefined;
    });
    const { editor } = await renderEditor();
    type(editor, "Ask @ka");
    const menu = await screen.findByRole("listbox", { name: "People" });
    await within(menu).findByRole("option", { name: /Kari Nordmann/ });
    press(editor, "Enter");
    await waitFor(() =>
      expect(docOf(editor).content?.[0].content).toContainEqual({
        type: "mention",
        attrs: { id: ids.kari, label: "Kari Nordmann" },
      }),
    );
    expect(await screen.findByText("@Kari Nordmann")).toBeInTheDocument();
  });

  it("shows the person's name as the directory has it now", async () => {
    fakeServer((call) => {
      if (call.url.startsWith("/api/v1/knowledge/assignable-users"))
        return jsonResponse(200, [{ userId: ids.kari, displayName: "Kari Nordmann-Berg" }]);
      return undefined;
    });
    await renderEditor({
      editable: false,
      content: {
        type: "doc",
        content: [
          { type: "paragraph", content: [{ type: "mention", attrs: { id: ids.kari, label: "Kari Nordmann" } }] },
        ],
      },
    });
    expect(await screen.findByText("@Kari Nordmann-Berg")).toBeInTheDocument();
  });
});

describe("links between articles", () => {
  it("opens a page search on [[ and inserts a link to the chosen page", async () => {
    fakeServer((call) => {
      if (call.url.startsWith("/api/v1/knowledge/search"))
        return jsonResponse(200, {
          items: [
            {
              id: ids.onboarding,
              title: "Onboarding",
              icon: null,
              path: [{ id: ids.routines, title: "Routines" }],
              excerpt: [],
              updatedAt: "2026-11-01T10:00:00Z",
            },
          ],
        });
      if (call.url === "/api/v1/knowledge/links/resolve")
        return jsonResponse(200, [{ id: ids.onboarding, state: "ok", title: "Onboarding", icon: "🧭" }]);
      return undefined;
    });
    const { editor } = await renderEditor({ articleId: ids.routines });
    type(editor, "See [[");
    const menu = await screen.findByRole("listbox", { name: "Pages" });
    expect(menu).toHaveTextContent("Type to search for a page.");
    type(editor, "onb");
    await within(menu).findByRole("option", { name: /Onboarding/ });
    press(editor, "Enter");
    await waitFor(() =>
      expect(docOf(editor).content?.[0].content).toContainEqual({
        type: "articleLink",
        attrs: { id: ids.onboarding, label: null },
      }),
    );
    const chip = await screen.findByRole("link", { name: /Onboarding/ });
    expect(chip).toHaveAttribute("href", `/knowledge/articles/${ids.onboarding}`);
  });

  it("shows the current title, No access or Deleted, asking for all of a page's links at once", async () => {
    const server = fakeServer((call) => {
      if (call.url === "/api/v1/knowledge/links/resolve")
        return jsonResponse(200, [
          { id: ids.onboarding, state: "ok", title: "Onboarding 2026", icon: null },
          { id: ids.notes, state: "no_access", title: null, icon: null },
          { id: ids.shared, state: "deleted", title: null, icon: null },
        ]);
      return undefined;
    });
    const link = (id: string, label: string) => ({ type: "articleLink", attrs: { id, label } });
    await renderEditor({
      editable: false,
      content: {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [link(ids.onboarding, "Onboarding"), link(ids.notes, "Salaries"), link(ids.shared, "Old page")],
          },
        ],
      },
    });
    expect(await screen.findByRole("link", { name: /Onboarding 2026/ })).toBeInTheDocument();
    expect(await screen.findByText("No access")).toBeInTheDocument();
    expect(screen.getByText("Deleted")).toBeInTheDocument();
    expect(screen.queryByText("Salaries")).toBeNull();
    expect(screen.queryByText("Old page")).toBeNull();
    expect(document.querySelector('[title="Salaries"], [title="Old page"], [label]')).toBeNull();
    expect(server.count("POST", "/api/v1/knowledge/links/resolve")).toBe(1);
    const asked =
      (server.sent("POST", "/api/v1/knowledge/links/resolve")?.body as { ids: string[] } | undefined)?.ids ?? [];
    expect([...asked].sort()).toEqual([ids.onboarding, ids.notes, ids.shared].sort());
  });
});

describe("pasted Vantigo URLs", () => {
  const here = {
    origin: "https://acme.vantigo.io",
    base: "/",
    enabled: ["customers", "projects", "orders", "knowledge"],
    moduleName: (module: string) => module.charAt(0).toUpperCase() + module.slice(1),
  };

  it("turns this Vantigo's article URL into an article link and an enabled module's path into a record chip", () => {
    expect(linkNodeForUrl(`https://acme.vantigo.io/knowledge/articles/${ids.onboarding}`, here)).toEqual({
      type: "articleLink",
      attrs: { id: ids.onboarding, label: null },
    });
    expect(linkNodeForUrl(`https://acme.vantigo.io/customers/${ids.kari}`, here)).toEqual({
      type: "recordLink",
      attrs: { path: `/customers/${ids.kari}`, label: "Customers · 00000000" },
    });
    expect(linkNodeForUrl("https://acme.vantigo.io/projects/active-jobs", here)).toEqual({
      type: "recordLink",
      attrs: { path: "/projects/active-jobs", label: "Projects · active jobs" },
    });
    expect(linkNodeForUrl("https://acme.vantigo.io/orders", here)).toEqual({
      type: "recordLink",
      attrs: { path: "/orders", label: "Orders" },
    });
  });

  it("leaves query strings, fragments, disabled or unknown modules and other sites as text", () => {
    expect(linkNodeForUrl(`https://acme.vantigo.io/customers/${ids.kari}?tab=orders`, here)).toBeNull();
    expect(linkNodeForUrl("https://acme.vantigo.io/customers/1#notes", here)).toBeNull();
    expect(linkNodeForUrl("https://acme.vantigo.io/invoices/12", here)).toBeNull();
    expect(linkNodeForUrl("https://acme.vantigo.io/settings/profile", here)).toBeNull();
    expect(linkNodeForUrl("https://example.com/customers/1", here)).toBeNull();
    expect(linkNodeForUrl("customers", here)).toBeNull();
    expect(linkNodeForUrl("https://acme.vantigo.io/", here)).toBeNull();
    expect(linkNodeForUrl("https://acme.vantigo.io/other/orders/1", { ...here, base: "/vantigo/" })).toBeNull();
    expect(linkNodeForUrl("https://acme.vantigo.io/vantigo/orders/12", { ...here, base: "/vantigo/" })).toEqual({
      type: "recordLink",
      attrs: { path: "/orders/12", label: "Orders · 12" },
    });
  });

  const paste = (editor: Editor, text: string) =>
    act(() => {
      const data = { getData: (kind: string) => (kind === "text/plain" ? text : ""), files: [] as unknown as FileList };
      editor.view.someProp("handlePaste", (handler) =>
        handler(editor.view, { clipboardData: data } as unknown as ClipboardEvent, editor.state.doc.slice(0, 0)),
      );
    });

  it("inserts a record chip on paste, named in the reader's language", async () => {
    const { editor } = await renderEditor();
    paste(editor, `${window.location.origin}/projects/active-jobs`);
    await waitFor(() =>
      expect(docOf(editor).content?.[0].content?.[0]).toEqual({
        type: "recordLink",
        attrs: { path: "/projects/active-jobs", label: "Projects · active jobs" },
      }),
    );
    expect(await screen.findByRole("link", { name: /Projects · active jobs/ })).toHaveAttribute(
      "href",
      "/projects/active-jobs",
    );
  });

  it("does not turn a pasted URL with a query string into a record chip", async () => {
    const { editor } = await renderEditor();
    paste(editor, `${window.location.origin}/projects/1?tab=time`);
    expect(JSON.stringify(docOf(editor))).not.toContain("recordLink");
    expect(screen.queryByRole("link", { name: /Projects/ })).toBeNull();
  });
});
