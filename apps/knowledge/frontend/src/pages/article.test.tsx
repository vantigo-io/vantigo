import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { KnowledgeArticle } from "../api/articles";
import { writeDraft } from "../lib/draft";
import { article, type Call, fakeServer, ids, jsonResponse, refusal } from "../test/api";
import { renderRoute } from "../test/render";

const url = `/api/v1/knowledge/articles/${ids.routines}`;
const page = `/knowledge/articles/${ids.routines}`;

const serving = (current: KnowledgeArticle, put?: (call: Call) => Response | undefined) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
    if (call.method === "PUT" && call.url === `${url}/content`) return put?.(call);
    return undefined;
  });

const saved = (version: number) =>
  jsonResponse(200, {
    id: ids.routines,
    version,
    updatedAt: "2026-11-07T10:00:00Z",
    updatedBy: { userId: ids.kari, displayName: "Kari Nordmann" },
  });

const retitle = async (text: string) => {
  const title = await screen.findByRole("textbox", { name: "Title" });
  await userEvent.clear(title);
  await userEvent.type(title, text);
};

describe("an article", () => {
  it("shows its icon, title, author and content", async () => {
    serving(article());
    renderRoute(page);
    expect(await screen.findByRole("textbox", { name: "Title" })).toHaveValue("Routines");
    expect(screen.getByText("📘")).toBeInTheDocument();
    expect(screen.getByText(/Last changed by Kari Nordmann/)).toBeInTheDocument();
    expect(await screen.findByText("Lock the door.")).toBeInTheDocument();
  });

  it("saves 1.5 s after typing stops, with the title and the version it was typed over", async () => {
    const server = serving(article(), () => saved(4));
    renderRoute(page);
    await retitle("Routines 2026");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    expect(server.count("PUT", `${url}/content`)).toBe(0);
    await waitFor(() => expect(server.count("PUT", `${url}/content`)).toBe(1), { timeout: 4000 });
    expect(server.sent("PUT", `${url}/content`)?.body).toMatchObject({
      title: "Routines 2026",
      expectedVersion: 3,
      content: { type: "doc" },
    });
    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });

  it("asks what to do when someone saved in between, and Keep mine saves over their version", async () => {
    let puts = 0;
    const server = serving(article(), () => {
      puts += 1;
      return puts === 1
        ? refusal(409, "version_conflict", {
            currentVersion: 5,
            updatedAt: "2026-11-07T09:59:00Z",
            updatedBy: { userId: ids.ola, displayName: "Ola Hansen" },
          })
        : saved(6);
    });
    renderRoute(page);
    await retitle("Mine");
    const dialog = await screen.findByRole(
      "dialog",
      { name: "Ola Hansen changed this page while you were editing" },
      { timeout: 4000 },
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Keep mine" }));
    await waitFor(() => expect(server.count("PUT", `${url}/content`)).toBe(2));
    expect(server.sent("PUT", `${url}/content`)?.body).toMatchObject({ title: "Mine", expectedVersion: 5 });
  });

  it("Reload drops the local text and shows the saved version", async () => {
    // The fake answers whatever the article is now: someone else saved version 5 before this save.
    let current = article();
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
      if (call.method === "PUT") {
        current = article({ title: "Theirs", version: 5 });
        return refusal(409, "version_conflict", { currentVersion: 5, updatedBy: null });
      }
      return undefined;
    });
    renderRoute(page);
    await retitle("Mine");
    const dialog = await screen.findByRole(
      "dialog",
      { name: "Someone changed this page while you were editing" },
      { timeout: 4000 },
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Title" })).toHaveValue("Theirs"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("says when the page is too large, and keeps the text", async () => {
    serving(article(), () => refusal(413, "document_too_large"));
    renderRoute(page);
    await retitle("Huge");
    expect(await screen.findByText("This page is too large to save", {}, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveValue("Huge");
    expect(window.localStorage.getItem(`knowledge.draft.${ids.routines}`)).toContain("Huge");
  });

  it("says when the server refuses the document", async () => {
    serving(article(), () => refusal(400, "invalid_document", { path: "$.content[0].marks[0]" }));
    renderRoute(page);
    await retitle("Odd");
    expect(await screen.findByText(/\$\.content\[0\]\.marks\[0\]/, {}, { timeout: 4000 })).toBeInTheDocument();
  });

  it("is read-only for a reader", async () => {
    serving(article({ permission: "read" }));
    renderRoute(page);
    expect(await screen.findByText("You can read this page but not edit it.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveAttribute("readonly");
    expect(screen.queryByRole("button", { name: "Change icon" })).toBeNull();
  });

  it("is read-only while locked, and says so", async () => {
    serving(article({ locked: true }));
    renderRoute(page);
    expect(
      await screen.findByText("This page is locked. Unlock it from the page menu to edit it."),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveAttribute("readonly");
    await userEvent.click(screen.getByRole("button", { name: "Page menu" }));
    expect(await screen.findByRole("menuitem", { name: "Unlock page" })).toBeInTheDocument();
  });

  it("offers back a local draft newer than the saved version", async () => {
    writeDraft(ids.routines, {
      title: "Typed before leaving",
      content: { type: "doc", content: [] },
      baseVersion: 3,
      savedAt: Date.parse("2026-11-02T10:00:00Z"),
    });
    const server = serving(article(), () => saved(4));
    renderRoute(page);
    expect(await screen.findByText("Unsaved changes on this device")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Restore them" }));
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveValue("Typed before leaving");
    await waitFor(() => expect(server.count("PUT", `${url}/content`)).toBe(1), { timeout: 4000 });
    expect(server.sent("PUT", `${url}/content`)?.body).toMatchObject({ expectedVersion: 3 });
  });

  it("offers a draft by its content, not by the device's clock", async () => {
    // A clock far behind the server's: the draft looks older than the save, yet it was never saved.
    writeDraft(ids.routines, {
      title: "Typed on a slow clock",
      content: { type: "doc", content: [] },
      baseVersion: 3,
      savedAt: Date.parse("2020-01-01T00:00:00Z"),
    });
    serving(article());
    renderRoute(page);
    expect(await screen.findByText("Unsaved changes on this device")).toBeInTheDocument();
  });

  it("does not offer a draft that matches what the server holds", async () => {
    const current = article();
    writeDraft(ids.routines, {
      title: current.title,
      content: current.content,
      baseVersion: 2,
      savedAt: Date.parse("2030-01-01T00:00:00Z"),
    });
    serving(current);
    renderRoute(page);
    await screen.findByRole("textbox", { name: "Title" });
    expect(screen.queryByText("Unsaved changes on this device")).toBeNull();
  });

  it("keeps typing during a save unsaved, and saves it next", async () => {
    let release: (() => void) | undefined;
    let puts = 0;
    // The first save waits until the test lets it answer; the second answers at once.
    const server = fakeServer(async (call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, article());
      if (call.method !== "PUT") return undefined;
      puts += 1;
      if (puts === 1) await new Promise<void>((resolve) => (release = resolve));
      return saved(3 + puts);
    });
    renderRoute(page);
    await retitle("First");
    await waitFor(() => expect(release).toBeDefined(), { timeout: 4000 });
    await userEvent.type(screen.getByRole("textbox", { name: "Title" }), " and more");
    release?.();
    expect(await screen.findByText("Unsaved changes")).toBeInTheDocument();
    await waitFor(() => expect(puts).toBe(2), { timeout: 4000 });
    expect(server.calls.filter((c) => c.method === "PUT").map((c) => (c.body as { title: string }).title)).toEqual([
      "First",
      "First and more",
    ]);
    expect(server.sent("PUT", `${url}/content`)?.body).toMatchObject({ expectedVersion: 4 });
    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });

  it("stops saving and keeps the draft when a save finds the page gone", async () => {
    let gone = false;
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url)
        return gone ? new Response(null, { status: 404 }) : jsonResponse(200, article());
      if (call.method === "PUT") {
        gone = true;
        return new Response(null, { status: 404 });
      }
      return undefined;
    });
    renderRoute(page);
    await retitle("Lost");
    expect(
      await screen.findByText("You no longer have access to this page", {}, { timeout: 4000 }),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveAttribute("readonly");
    expect(window.localStorage.getItem(`knowledge.draft.${ids.routines}`)).toContain("Lost");
  });

  it("says when the person no longer has access", async () => {
    fakeServer(() => undefined);
    renderRoute(page);
    expect(await screen.findByText("You no longer have access to this page")).toBeInTheDocument();
  });

  it("locks the page from the menu", async () => {
    const server = fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, article());
      if (call.method === "PATCH" && call.url === url) return jsonResponse(200, article({ locked: true }));
      return undefined;
    });
    renderRoute(page);
    await userEvent.click(await screen.findByRole("button", { name: "Page menu" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Lock page" }));
    await waitFor(() => expect(server.sent("PATCH", url)?.body).toEqual({ locked: true }));
    expect(
      await screen.findByText("This page is locked. Unlock it from the page menu to edit it."),
    ).toBeInTheDocument();
  });

  it("says when a change meets a lock taken meanwhile", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, article());
      if (call.method === "PATCH") return refusal(409, "article_locked");
      return undefined;
    });
    renderRoute(page);
    await userEvent.click(await screen.findByRole("button", { name: "Page menu" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: /Hidden from the Assistant/ }));
    expect(await screen.findByText("The page was locked, so your change was not saved.")).toBeInTheDocument();
  });

  it("shows Hidden from the Assistant as inherited when a parent hides it", async () => {
    serving(article({ aiHiddenInherited: true }));
    renderRoute(page);
    await userEvent.click(await screen.findByRole("button", { name: "Page menu" }));
    const item = await screen.findByRole("menuitem", { name: /Hidden from the Assistant/ });
    expect(item).toHaveTextContent("Hidden through a parent page");
    expect(item).toBeDisabled();
  });
});
