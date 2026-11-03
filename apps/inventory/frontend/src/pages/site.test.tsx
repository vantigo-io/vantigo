import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi } from "../test/server";

describe("the site page", () => {
  it("opens the tree a level at a time, and searches it by code", async () => {
    stubInventoryApi();
    renderRoute("/inventory/sites/1");
    expect(await screen.findByRole("link", { name: "A" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "01" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Expand OSL-A" }));
    expect(await screen.findByRole("link", { name: "01" })).toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Search by code or name" }), "a-02");
    expect(await screen.findByRole("link", { name: "OSL-A-02" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "01" })).not.toBeInTheDocument();
  });

  it("creates a location under the site", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/sites/1");
    await userEvent.click(await screen.findByRole("button", { name: "New location" }));
    await userEvent.type(await screen.findByRole("textbox", { name: "Code" }), "b");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/locations$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/locations$/)[0].body).toEqual({ parentId: 10, code: "B", kind: "shelf" });
    expect(await screen.findByRole("link", { name: "B" })).toBeInTheDocument();
  });

  it("hides the manager's controls from a caller who may only look", async () => {
    stubInventoryApi({ meta: { canManageLocations: false } });
    renderRoute("/inventory/sites/1");
    expect(await screen.findByRole("link", { name: "A" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New location" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Bulk add" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Actions for OSL-A" })).not.toBeInTheDocument();
  });
});
