import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { resetExpanded } from "../lib/expanded";
import { moveFor } from "../lib/tree-moves";
import { fakeServer, ids, jsonResponse, node, refusal } from "../test/api";
import { renderRoute } from "../test/render";

const routines = node({ id: ids.routines, title: "Routines", icon: "📘", hasChildren: true });
const onboarding = node({ id: ids.onboarding, title: "Onboarding", parentId: ids.routines });
const notes = node({ id: ids.notes, title: "My notes", section: "private" });
const shared = node({ id: ids.shared, title: "Board minutes", section: "shared", permission: "read" });

afterEach(() => resetExpanded());

describe("the sidebar tree", () => {
  it("shows Favourites, Workspace, Shared and Private, and opens a level when expanded", async () => {
    const server = fakeServer(({ method, url }) => {
      if (method !== "GET") return undefined;
      if (url === "/api/v1/knowledge/tree") return jsonResponse(200, { items: [routines, shared, notes] });
      if (url === `/api/v1/knowledge/tree?parentId=${ids.routines}`) return jsonResponse(200, { items: [onboarding] });
      if (url === "/api/v1/knowledge/favourites") return jsonResponse(200, { items: [notes] });
      return undefined;
    });
    renderRoute("/knowledge");

    const favourites = await screen.findByRole("group", { name: "Favourites" });
    expect(within(favourites).getByText("My notes")).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "Workspace" })).getByText("Routines")).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "Shared" })).getByText("Board minutes")).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "Private" })).getByText("My notes")).toBeInTheDocument();
    // A reader cannot add a page inside one shared with them.
    expect(screen.queryByRole("button", { name: "New page in Board minutes" })).toBeNull();

    expect(screen.queryByText("Onboarding")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Expand Routines" }));
    expect(await screen.findByText("Onboarding")).toBeInTheDocument();
    expect(server.count("GET", `/api/v1/knowledge/tree?parentId=${ids.routines}`)).toBe(1);
  });

  it("leaves Shared out while nothing is shared with the person", async () => {
    fakeServer(({ method, url }) =>
      method === "GET" && url === "/api/v1/knowledge/tree" ? jsonResponse(200, { items: [routines] }) : undefined,
    );
    renderRoute("/knowledge");
    await screen.findByRole("group", { name: "Workspace" });
    expect(screen.queryByRole("group", { name: "Shared" })).toBeNull();
    expect(screen.queryByRole("group", { name: "Favourites" })).toBeNull();
  });

  it("confirms a move that changes who can read the page, then sends it with confirmVisibility", async () => {
    const server = fakeServer(({ method, url, body }) => {
      if (method === "GET" && url === "/api/v1/knowledge/tree") return jsonResponse(200, { items: [routines, notes] });
      if (method === "POST" && url === `/api/v1/knowledge/articles/${ids.notes}/move`)
        return (body as { confirmVisibility?: boolean }).confirmVisibility
          ? jsonResponse(200, { ...notes, parentId: ids.routines, section: "workspace" })
          : refusal(409, "visibility_change");
      return undefined;
    });
    renderRoute("/knowledge");

    const from = await screen.findByTestId(`tree-row-${ids.notes}`);
    const onto = screen.getByTestId(`tree-row-${ids.routines}`);
    fireEvent.dragStart(from);
    fireEvent.dragOver(onto);
    fireEvent.drop(onto);

    const dialog = await screen.findByRole("dialog", { name: "Change who can read this page?" });
    expect(server.sent("POST", `/api/v1/knowledge/articles/${ids.notes}/move`)?.body).toEqual({
      parentId: ids.routines,
      afterId: null,
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "Change access" }));
    await waitFor(() => expect(server.count("POST", `/api/v1/knowledge/articles/${ids.notes}/move`)).toBe(2));
    expect(server.sent("POST", `/api/v1/knowledge/articles/${ids.notes}/move`)?.body).toEqual({
      parentId: ids.routines,
      afterId: null,
      confirmVisibility: true,
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("says why a move was refused", async () => {
    fakeServer(({ method, url }) => {
      if (method === "GET" && url === "/api/v1/knowledge/tree") return jsonResponse(200, { items: [routines, notes] });
      if (method === "POST") return refusal(400, "too_deep");
      return undefined;
    });
    renderRoute("/knowledge");
    const from = await screen.findByTestId(`tree-row-${ids.notes}`);
    fireEvent.dragStart(from);
    fireEvent.drop(screen.getByTestId(`tree-row-${ids.routines}`));
    expect(await screen.findByText("Pages can be nested at most 10 levels deep.")).toBeInTheDocument();
  });

  it("creates a root in the Workspace or in Private from the section's +", async () => {
    const server = fakeServer(({ method, url }) => {
      if (method === "POST" && url === "/api/v1/knowledge/articles")
        return jsonResponse(201, { id: ids.notes, title: "" });
      return undefined;
    });
    const { router } = renderRoute("/knowledge");
    await userEvent.click(await screen.findByRole("button", { name: "New private page" }));
    await waitFor(() =>
      expect(server.sent("POST", "/api/v1/knowledge/articles")?.body).toEqual({ section: "private", title: "" }),
    );
    await waitFor(() => expect(router.state.location.pathname).toBe(`/knowledge/articles/${ids.notes}`));
  });

  it("keeps a long title to one truncated line, with the full title on hover and the row's buttons beside it", async () => {
    const title = "5. Opptreden mot partnere, leverandører og andre selskaper vi samarbeider med";
    const long = node({ id: ids.routines, title, hasChildren: true });
    fakeServer(({ method, url }) =>
      method === "GET" && url === "/api/v1/knowledge/tree" ? jsonResponse(200, { items: [long] }) : undefined,
    );
    renderRoute("/knowledge");

    const row = await screen.findByTestId(`tree-row-${ids.routines}`);
    const link = within(row).getByRole("link");
    expect(link).toHaveAttribute("title", title);
    // jsdom does no layout: what keeps the buttons in sight is that the title
    // may shrink and they may not.
    expect(within(link).getByText(title)).toHaveStyle({ minWidth: "0px" });
    for (const name of ["Add to favourites", `New page in ${title}`]) {
      expect(within(row).getByRole("button", { name })).toHaveStyle({ flexShrink: "0" });
    }
  });

  it("stars a page into the favourites", async () => {
    const server = fakeServer(({ method, url }) => {
      if (method === "GET" && url === "/api/v1/knowledge/tree") return jsonResponse(200, { items: [routines] });
      if (method === "PUT" && url === `/api/v1/knowledge/favourites/${ids.routines}`) return jsonResponse(204, null);
      return undefined;
    });
    renderRoute("/knowledge");
    await userEvent.click(await screen.findByRole("button", { name: "Add to favourites" }));
    await waitFor(() => expect(server.count("PUT", `/api/v1/knowledge/favourites/${ids.routines}`)).toBe(1));
  });
});

describe("the move a drop asks for", () => {
  const a = node({ id: "a", title: "A" });
  const b = node({ id: "b", title: "B" });
  const c = node({ id: "c", title: "C", section: "private" });
  const child = node({ id: "d", title: "D", parentId: "a" });

  it("puts it first inside, after the row, or after the row before", () => {
    expect(moveFor(a, "inside", [a, b], "x")).toEqual({ parentId: "a", afterId: null });
    expect(moveFor(a, "after", [a, b], "x")).toEqual({ parentId: null, afterId: "a", section: "workspace" });
    expect(moveFor(b, "before", [a, b], "x")).toEqual({ parentId: null, afterId: "a", section: "workspace" });
    expect(moveFor(a, "before", [a, b], "x")).toEqual({ parentId: null, afterId: null, section: "workspace" });
    expect(moveFor(c, "after", [c], "x")).toEqual({ parentId: null, afterId: "c", section: "private" });
    expect(moveFor(child, "before", [child], "x")).toEqual({ parentId: "a", afterId: null });
  });

  it("treats a Shared item under a parent the reader cannot see as the top level", () => {
    const top = node({ id: "s", title: "S", section: "shared", parentId: null });
    expect(moveFor(top, "before", [top], "x")).toEqual({ parentId: null, afterId: null });
    expect(moveFor(top, "after", [top], "x")).toEqual({ parentId: null, afterId: null });
    expect(moveFor(top, "inside", [top], "x")).toEqual({ parentId: "s", afterId: null });
  });

  it("skips the dragged row itself when it is the row before", () => {
    expect(moveFor(b, "before", [a, b], "a")).toEqual({ parentId: null, afterId: null, section: "workspace" });
  });
});
