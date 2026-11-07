import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fakeServer, ids, jsonResponse } from "../test/api";
import { renderRoute } from "../test/render";

const group = {
  id: ids.routines,
  title: "Old routines",
  icon: "📘",
  articleCount: 3,
  trashedAt: "2026-11-05T10:00:00Z",
  trashedBy: { userId: ids.kari, displayName: "Kari Nordmann" },
  parent: null,
  purgeAt: "2026-12-05T10:00:00Z",
};

const serving = () =>
  fakeServer(({ method, url }) => {
    if (method === "GET" && url === "/api/v1/knowledge/trash") return jsonResponse(200, { items: [group] });
    if (method === "POST" && url === `/api/v1/knowledge/articles/${ids.routines}/restore`)
      return jsonResponse(200, { id: ids.routines, restoredCount: 3, groupRestored: false, movedToRoot: true });
    if (method === "DELETE" && url === "/api/v1/knowledge/trash") return jsonResponse(204, null);
    return undefined;
  });

describe("the trash", () => {
  it("lists the trash groups and restores one whole", async () => {
    const server = serving();
    renderRoute("/knowledge/trash");
    const table = await screen.findByRole("table", { name: "Trash" });
    expect(within(table).getByText("Old routines")).toBeInTheDocument();
    expect(within(table).getByText("and 2 subpages")).toBeInTheDocument();
    expect(within(table).getByText("Kari Nordmann")).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(server.count("POST", `/api/v1/knowledge/articles/${ids.routines}/restore`)).toBe(1));
    expect(await screen.findByText(/3 pages were restored\. Its old parent is gone/)).toBeInTheDocument();
  });

  it("offers Empty trash only to knowledge:manage", async () => {
    serving();
    renderRoute("/knowledge/trash");
    await screen.findByRole("table", { name: "Trash" });
    expect(screen.queryByRole("button", { name: "Empty trash" })).toBeNull();
  });

  it("empties the trash after confirmation", async () => {
    const server = serving();
    renderRoute("/knowledge/trash", { canManage: true });
    await userEvent.click(await screen.findByRole("button", { name: "Empty trash" }));
    const dialog = await screen.findByRole("dialog", { name: "Empty the trash?" });
    expect(server.count("DELETE", "/api/v1/knowledge/trash")).toBe(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "Empty trash" }));
    await waitFor(() => expect(server.count("DELETE", "/api/v1/knowledge/trash")).toBe(1));
  });
});
