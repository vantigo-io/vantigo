import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

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

  it("says a move refused for a count still counting under the parent field, in the module's words", async () => {
    stubInventoryApi({
      write: (method, path) =>
        method === "PUT" && path === "/locations/11"
          ? inventoryProblem(409, "location_has_open_count", { title: "Location has open count" })
          : undefined,
    });
    renderRoute("/inventory/sites/1");
    await userEvent.click(await screen.findByRole("button", { name: "Actions for OSL-A" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit location" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(
        "A count is still counting this location or something below it. Post or cancel it first.",
      ),
    ).toBeInTheDocument();
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

describe("a site's transfers", () => {
  it("counts the open transfers to and from it, each linking to the list filtered", async () => {
    const [tsM] = variants();
    stubInventoryApi({
      transfers: [
        transferFixture({ id: 1, status: "sent", lines: [transferLine(1, tsM, 1, { sent: 1 })] }),
        transferFixture({
          id: 2,
          status: "partly_received",
          lines: [transferLine(2, tsM, 2, { sent: 2, received: 1 })],
        }),
        transferFixture({ id: 3, status: "draft", lines: [transferLine(3, tsM, 1)] }),
      ],
    });
    renderRoute("/inventory/sites/2");
    expect(await screen.findByRole("link", { name: "2 open transfers" })).toHaveAttribute(
      "href",
      "/inventory/transfers?toSiteId=2",
    );
    expect(screen.getByRole("link", { name: "0 open transfers" })).toHaveAttribute(
      "href",
      "/inventory/transfers?fromSiteId=2",
    );
  });
});

describe("a large site", () => {
  it("says the list was cut, and searches the server by code on Enter", async () => {
    const fetchMock = stubInventoryApi({ locationLimit: 3 });
    renderRoute("/inventory/sites/1");
    expect(await screen.findByText(/This site has more than 10,000 locations/)).toBeInTheDocument();
    const search = screen.getByRole("textbox", { name: "Search by code or name" });
    await userEvent.type(search, "a-02");
    // Nothing is filtered in the browser while typing: the list held is not the whole site.
    expect(screen.queryByRole("link", { name: "OSL-A-02" })).not.toBeInTheDocument();
    await userEvent.type(search, "{Enter}");
    expect(await screen.findByRole("link", { name: "OSL-A-02" })).toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/sites\/1\/locations\?status=active&q=a-02$/)).toHaveLength(1);
  });
});
