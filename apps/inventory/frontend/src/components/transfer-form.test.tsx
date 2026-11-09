import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { choose, retype, scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import {
  inventoryProblem,
  packagings,
  stubInventoryApi,
  transferFixture,
  transferLine,
  variants,
} from "../test/server";

const scanField = () => screen.findByRole("textbox", { name: "Scan an item" });

describe("a transfer's draft", () => {
  it("shows the stock at both sites beside each line, and warns of a short one", async () => {
    stubInventoryApi({
      balances: [
        { variantId: 101, locationId: 12, onHand: 3 },
        { variantId: 201, locationId: 12, onHand: 40 },
        { variantId: 201, locationId: 21, onHand: 4 },
      ],
      reservations: [{ variantId: 201, siteId: 2, quantity: 1 }],
    });
    renderRoute("/inventory/transfers/new?fromSiteId=1&toSiteId=2");
    const field = await scanField();
    await scan(field, "TS-M");
    await scan(field, "BOLT-10");
    const table = await screen.findByRole("table");
    expect(within(table).getByRole("columnheader", { name: "At OSL: available" })).toBeInTheDocument();
    expect(within(table).getByRole("columnheader", { name: "At BGO: available · incoming" })).toBeInTheDocument();
    const [, tsM, bolt] = within(table).getAllByRole("row");
    await waitFor(() => expect(within(tsM).getByText("3 pcs")).toBeInTheDocument());
    expect(within(bolt).getByText("40 pcs")).toBeInTheDocument();
    expect(within(bolt).getByText("3 pcs")).toBeInTheDocument();
    expect(within(tsM).queryByText("Short")).not.toBeInTheDocument();

    // Asking for more than the source has is warned of, not refused.
    await retype(within(tsM).getByRole("textbox", { name: "Quantity, line 1" }), "5");
    expect(await within(tsM).findByText("Short")).toBeInTheDocument();
  });

  it("saves the draft with its header and lines, and opens it", async () => {
    const fetchMock = stubInventoryApi();
    const { router } = renderRoute("/inventory/transfers/new");
    await screen.findByRole("combobox", { name: "From site" });
    await choose(document.body, "From site", "OSL · Oslo lager");
    await choose(document.body, "To site", "BGO · Bergen butikk");
    await userEvent.type(screen.getByRole("textbox", { name: "Reference" }), "Weekly restock");
    await scan(await scanField(), "TS-M");
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 1" }), "4");
    await userEvent.click(screen.getByRole("button", { name: "Save draft" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/transfers$/)[0].body).toEqual({
      fromSiteId: 1,
      toSiteId: 2,
      reference: "Weekly restock",
      lines: [{ variantId: 101, quantity: 4 }],
    });
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/inventory\/transfers\/\d+$/));
    expect(await screen.findByRole("heading", { name: /Transfer 1001/ })).toBeInTheDocument();
  });

  it("asks for different sites and at least one line before saving", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/transfers/new?fromSiteId=1&toSiteId=1");
    await userEvent.click(await screen.findByRole("button", { name: "Save draft" }));
    expect(await screen.findByText("Add at least one line.")).toBeInTheDocument();
    expect(screen.getByText("The from site and the to site must be different.")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/transfers$/)).toHaveLength(0);
  });

  it("edits a draft under its revision, and keeps a line's suggestion until the line is edited", async () => {
    const [tsM, tsL] = variants();
    const suggestion = { needed: 3, packagingId: null, count: null };
    const fetchMock = stubInventoryApi({
      transfers: [
        transferFixture({
          id: 5,
          revision: 3,
          lines: [
            { ...transferLine(1, tsM, 3), suggestion },
            { ...transferLine(2, tsL, 2), suggestion },
          ],
        }),
      ],
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 2" }), "6");
    await userEvent.click(screen.getByRole("button", { name: "Save draft" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/transfers\/5$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/transfers\/5$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body.lines).toEqual([
      { variantId: 101, quantity: 3, suggestion },
      { variantId: 102, quantity: 6 },
    ]);
    // Back on the transfer, as saved.
    expect(await screen.findByRole("button", { name: "Edit" })).toBeInTheDocument();
  });

  it("says how a line drafted from the low-stock list was found, until the line is edited (I57)", async () => {
    const [, , bolt] = variants();
    const boxed = { ...bolt, packagings: packagings() };
    stubInventoryApi({
      variants: [...variants().slice(0, 2), boxed],
      placements: [{ variantId: 201, siteId: 2, primaryLocationId: null, reorderPoint: 5, maximum: 24, revision: 1 }],
      transfers: [
        transferFixture({
          id: 5,
          lines: [{ ...transferLine(1, boxed, 30), suggestion: { needed: 21, packagingId: 72, count: 2 } }],
        }),
      ],
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    expect(
      await screen.findByText("21 needed, rounded up to 2 cases of 15 = 30, 9 above the maximum of 24"),
    ).toBeInTheDocument();
    await retype(screen.getByRole("textbox", { name: "Loose, line 1" }), "25");
    expect(screen.queryByText(/21 needed/)).not.toBeInTheDocument();
  });

  it("closes the edit on the transfer as it is now when someone else changed it", async () => {
    const [tsM] = variants();
    const fetchMock = stubInventoryApi({
      transfers: [transferFixture({ id: 5, lines: [transferLine(1, tsM, 3)] })],
      write: (method, path) =>
        method === "PUT" && path === "/transfers/5"
          ? inventoryProblem(409, "revision_conflict", { title: "Revision conflict" })
          : undefined,
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const reads = sent(fetchMock, "GET", /\/transfers\/5$/).length;
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 1" }), "4");
    await userEvent.click(screen.getByRole("button", { name: "Save draft" }));
    expect(await screen.findByText("The transfer was changed by someone else")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save draft" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/transfers\/5$/).length).toBeGreaterThan(reads);
  });

  it("asks before leaving a draft with changes, and Cancel leaves the edit", async () => {
    const [tsM] = variants();
    stubInventoryApi({ transfers: [transferFixture({ id: 5, lines: [transferLine(1, tsM, 3)] })] });
    const { router } = renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 1" }), "4");
    void router.navigate({ to: "/inventory/transfers" });
    const ask = await screen.findByRole("dialog", { name: "Leave without saving?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Keep editing" }));
    expect(router.state.location.pathname).toBe("/inventory/transfers/5");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    const again = await screen.findByRole("dialog", { name: "Leave without saving?" });
    await userEvent.click(within(again).getByRole("button", { name: "Leave without saving" }));
    expect(await screen.findByRole("button", { name: "Edit" })).toBeInTheDocument();
  });
});
