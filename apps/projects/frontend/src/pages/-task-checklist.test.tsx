import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ChecklistItem } from "../api/tasks";
import { CHECKLIST_TEXT_MAX } from "../lib/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TaskChecklist } from "./-task-checklist";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const items: ChecklistItem[] = [
  { id: 5, text: "Outline the pages", done: false, position: 1 },
  { id: 6, text: "Read it back", done: true, position: 2 },
];

const stubChecklist = (list: ChecklistItem[] = items, read?: Response) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/tasks/12/checklist") {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(201, { id: 7, text: "New", done: false }));
      return Promise.resolve(read ? read.clone() : jsonResponse(200, list));
    }
    if (url.pathname.startsWith("/api/v1/projects/tasks/12/checklist/")) {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(200, { ...list[0], done: true }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("TaskChecklist", () => {
  it("ticks an item off", async () => {
    const fetchMock = stubChecklist();
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    await userEvent.click(await screen.findByRole("checkbox", { name: "Outline the pages" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/checklist/5");
      expect(JSON.parse(String(init?.body))).toEqual({ done: true });
    });
  });

  it("adds an item and clears the box", async () => {
    const fetchMock = stubChecklist();
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    await screen.findByRole("checkbox", { name: "Outline the pages" });
    const box = screen.getByRole("textbox", { name: "Checklist item" });
    await userEvent.type(box, "Proofread it");
    await userEvent.click(screen.getByRole("button", { name: "Add item" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/checklist");
      expect(JSON.parse(String(init?.body))).toEqual({ text: "Proofread it" });
    });
    await waitFor(() => expect(box).toHaveValue(""));
  });

  it("leaves a half-typed item alone when another item is ticked off", async () => {
    stubChecklist();
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    await screen.findByRole("checkbox", { name: "Outline the pages" });
    const box = screen.getByRole("textbox", { name: "Checklist item" });
    await userEvent.type(box, "Still typing");
    await userEvent.click(screen.getByRole("checkbox", { name: "Outline the pages" }));

    await waitFor(() => expect(screen.getByRole("checkbox", { name: "Outline the pages" })).toBeEnabled());
    expect(box).toHaveValue("Still typing");
  });

  it("refuses an item longer than the contract allows", async () => {
    stubChecklist();
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    await screen.findByRole("checkbox", { name: "Outline the pages" });
    // Pasted rather than typed: 501 keystrokes would be needlessly slow.
    await userEvent.click(screen.getByRole("textbox", { name: "Checklist item" }));
    await userEvent.paste("x".repeat(CHECKLIST_TEXT_MAX + 1));

    expect(await screen.findByText("A checklist item is at most 500 characters")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add item" })).toBeDisabled();
  });

  it("deletes an item", async () => {
    const fetchMock = stubChecklist();
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    await screen.findByRole("checkbox", { name: "Outline the pages" });
    await userEvent.click(screen.getByRole("button", { name: "Delete item 1: Outline the pages" }));

    await waitFor(() => {
      const [url] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/checklist/5");
    });
  });

  // Every item's delete button used to share the one name "Delete the item",
  // which is unusable from a screen reader's list of buttons — this pins that
  // each is named after its own item, cut short before it runs on too long.
  it("names each item's delete button after its own text, cutting off a very long one", async () => {
    const long = "x".repeat(80);
    stubChecklist([
      { id: 5, text: "Outline the pages", done: false, position: 1 },
      { id: 6, text: long, done: false, position: 2 },
    ]);
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    expect(await screen.findByRole("button", { name: "Delete item 1: Outline the pages" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: `Delete item 2: ${long.slice(0, 60)}…` })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete the item" })).not.toBeInTheDocument();
  });

  // Two items with the same text used to collide once both were cut to the
  // same 60-character excerpt; the position tells them apart even then.
  it("tells two identical items apart by their position", async () => {
    stubChecklist([
      { id: 5, text: "Buy cable", done: false, position: 1 },
      { id: 6, text: "Buy cable", done: false, position: 2 },
    ]);
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    expect(await screen.findByRole("button", { name: "Delete item 1: Buy cable" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete item 2: Buy cable" })).toBeInTheDocument();
  });

  it("shows a viewer the items without a way to change them", async () => {
    stubChecklist();
    renderWithProviders(<TaskChecklist taskId={12} canContribute={false} />);

    expect(await screen.findByRole("checkbox", { name: "Outline the pages" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Add item" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Delete item /i })).not.toBeInTheDocument();
  });

  it("says when there is nothing on the checklist", async () => {
    stubChecklist([]);
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    expect(await screen.findByText("No checklist items yet.")).toBeInTheDocument();
  });

  it("reports a checklist it could not read", async () => {
    stubChecklist([], jsonResponse(500, { title: "Unavailable" }));
    renderWithProviders(<TaskChecklist taskId={12} canContribute />);

    expect(await screen.findByText("Could not load the checklist")).toBeInTheDocument();
  });
});
