import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InventoryCount } from "../api/counts";
import { choose, retype, scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, locations, packagings, stubInventoryApi, variants } from "../test/server";

const ref = (id: number) => {
  const one = locations().find((location) => location.id === id);
  if (!one) throw new Error(`no location ${id}`);
  return { id: one.id, siteId: one.siteId, fullCode: one.fullCode, kind: one.kind, status: one.status };
};
const variant = (id: number) => variants().find((one) => one.id === id) ?? variants()[0];

/** A count of Oslo's aisle A: a T-shirt expected on 01 (5), a bolt on 02 (4) whose balance has since moved to 6. */
const openCount = (): InventoryCount => ({
  id: 500,
  site: { id: 1, code: "OSL", name: "Oslo lager", kind: "warehouse", status: "active" },
  scope: ref(11),
  status: "counting",
  openedAt: "2026-11-02T09:00:00Z",
  openedBy: { displayName: "Kari" },
  revision: 1,
  lineCount: 2,
  countedLines: 0,
  movedLines: 0,
  lines: [
    { id: 1, variant: variant(101), location: ref(12), expected: 5, current: 5, added: false, moved: false },
    { id: 2, variant: variant(201), location: ref(13), expected: 4, current: 4, added: false, moved: false },
  ],
});
const balances = () => [
  { variantId: 101, locationId: 12, onHand: 5 },
  { variantId: 201, locationId: 13, onHand: 6 },
];

const scanField = () => screen.findByRole("textbox", { name: "Scan a location or an item" });

describe("the counts list", () => {
  it("lists the counts with their progress and moved lines, and opens one on its counting screen", async () => {
    const fetchMock = stubInventoryApi({ counts: [openCount()], balances: balances() });
    const { router } = renderRoute("/inventory/counts");
    const link = await screen.findByRole("link", { name: "Count 500" });
    const row = link.closest("tr") as HTMLElement;
    expect(within(row).getByText("OSL-A")).toBeInTheDocument();
    expect(within(row).getByText("0 of 2 lines counted")).toBeInTheDocument();
    expect(within(row).getByText("1 moved")).toBeInTheDocument();
    expect(within(row).getByText("Kari")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Open count" }));
    const dialog = await screen.findByRole("dialog");
    await choose(dialog, "Site", "BGO · Bergen butikk");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Year end");
    await userEvent.click(within(dialog).getByRole("button", { name: "Open count" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/counts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/counts$/)[0].body).toEqual({ siteId: 2, note: "Year end" });
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/counts/901"));
  });

  it("says a scope that overlaps a count still counting in the module's words", async () => {
    stubInventoryApi({ counts: [openCount()], balances: balances() });
    renderRoute("/inventory/counts?new=1");
    const dialog = await screen.findByRole("dialog");
    await choose(dialog, "Site", "OSL · Oslo lager");
    await userEvent.click(within(dialog).getByRole("button", { name: "Open count" }));
    expect(
      await within(dialog).findByText("Another count still counting covers part of this scope."),
    ).toBeInTheDocument();
  });

  it("tells a caller without counts-manage instead of listing", async () => {
    stubInventoryApi({ meta: { canCount: false } });
    renderRoute("/inventory/counts");
    expect(await screen.findByText(/You cannot count stock/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open count" })).not.toBeInTheDocument();
  });
});

describe("the counting screen", () => {
  it("jumps to a scanned location, focuses a scanned item's line, and saves what is typed", async () => {
    const fetchMock = stubInventoryApi({ counts: [openCount()], balances: balances() });
    renderRoute("/inventory/counts/500");
    const field = await scanField();

    // An item before a location: the screen asks for the location.
    await scan(field, "TS-M");
    expect(await screen.findByText("Scan a location first, then the items on it.")).toBeInTheDocument();

    await scan(field, "LOC12");
    expect(await screen.findByText("Counting at")).toBeInTheDocument();
    // Only the location's own lines are listed while counting there.
    expect(screen.queryByRole("textbox", { name: "Counted, OSL-A-02 BOLT-10" })).not.toBeInTheDocument();

    await scan(field, "TS-M");
    const counted = await screen.findByRole("textbox", { name: "Counted, OSL-A-01 TS-M" });
    await waitFor(() => expect(counted).toHaveFocus());
    await userEvent.type(counted, "3");

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)).toHaveLength(1), { timeout: 4000 });
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[0].body).toEqual({
      lines: [{ variantId: 101, locationId: 12, counted: 3 }],
    });
    expect(await screen.findByText("All changes saved")).toBeInTheDocument();
    expect(screen.getByText("1 of 2 lines counted")).toBeInTheDocument();

    // An item found where nothing was expected gets a line of its own.
    await scan(field, "BOLT-10");
    const added = await screen.findByRole("textbox", { name: "Counted, OSL-A-01 BOLT-10" });
    expect(within(added.closest("tr") as HTMLElement).getByText("Added")).toBeInTheDocument();
    await userEvent.type(added, "2");
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)).toHaveLength(2), { timeout: 4000 });
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[1].body).toEqual({
      lines: [{ variantId: 201, locationId: 12, counted: 2 }],
    });
  });

  it("leaves the scope check to the server when the site's tree is truncated", async () => {
    stubInventoryApi({ counts: [openCount()], balances: balances(), locationLimit: 1 });
    renderRoute("/inventory/counts/500");
    await scan(await scanField(), "LOC12");
    expect(await screen.findByText("Counting at")).toBeInTheDocument();
    expect(screen.queryByText(/is not in this count/)).not.toBeInTheDocument();
  });

  it("saves what was counted before leaving, and asks before leaving what could not be saved", async () => {
    let refuse = false;
    const fetchMock = stubInventoryApi({
      counts: [openCount()],
      balances: balances(),
      write: (method, path) =>
        refuse && method === "PUT" && path.endsWith("/lines")
          ? inventoryProblem(400, undefined, { lines: [{ index: 0, code: "quantity_invalid", detail: "No." }] })
          : undefined,
    });
    const { router } = renderRoute("/inventory/counts/500");
    // Leaving before the save's moment: the count is saved on the way out.
    await userEvent.type(await screen.findByRole("textbox", { name: "Counted, OSL-A-01 TS-M" }), "3");
    await router.navigate({ to: "/inventory/counts" });
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/counts"));
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[0].body).toEqual({
      lines: [{ variantId: 101, locationId: 12, counted: 3 }],
    });

    // A count that cannot be saved: leaving asks, Keep counting stays, Leave without saving goes.
    await router.navigate({ to: "/inventory/counts/$countId", params: { countId: "500" } });
    refuse = true;
    await userEvent.type(await screen.findByRole("textbox", { name: "Counted, OSL-A-02 BOLT-10" }), "7");
    void router.navigate({ to: "/inventory/counts" });
    const ask = await screen.findByRole("dialog", { name: "Leave without saving?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Keep counting" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(router.state.location.pathname).toBe("/inventory/counts/500");
    void router.navigate({ to: "/inventory/counts" });
    const again = await screen.findByRole("dialog", { name: "Leave without saving?" });
    await userEvent.click(within(again).getByRole("button", { name: "Leave without saving" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/counts"));
  });

  it("reads the count anew when a post is refused because it is no longer open", async () => {
    const fetchMock = stubInventoryApi({
      counts: [openCount()],
      balances: balances(),
      write: (method, path) => {
        // Somebody else cancelled it just before.
        if (method === "POST" && path.endsWith("/post")) fetchMock.state.counts[0].status = "cancelled";
        return undefined;
      },
    });
    renderRoute("/inventory/counts/500");
    await userEvent.click(await screen.findByRole("tab", { name: "Review" }));
    await userEvent.click(screen.getByRole("button", { name: "Post count" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Post count" }));
    expect((await screen.findAllByText("Cancelled")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("textbox", { name: "Scan a location or an item" })).not.toBeInTheDocument();
  });

  it("refuses a scanned location outside the count's scope", async () => {
    stubInventoryApi({ counts: [openCount()], balances: balances() });
    renderRoute("/inventory/counts/500");
    await scan(await scanField(), "LOC10");
    expect(await screen.findByText("OSL is not in this count.")).toBeInTheDocument();
  });

  it("flags a moved line, posts only once it is accepted, with the uncounted lines set to zero", async () => {
    const fetchMock = stubInventoryApi({ counts: [openCount()], balances: balances() });
    renderRoute("/inventory/counts/500");
    const bolt = (await screen.findByRole("textbox", { name: "Counted, OSL-A-02 BOLT-10" })).closest(
      "tr",
    ) as HTMLElement;
    expect(bolt).toHaveAttribute("data-moved", "true");
    expect(within(bolt).getByText("Moved")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Review" }));
    // Nothing is counted and the uncounted are kept: the moved line would not post, so nothing asks to accept it.
    const post = await screen.findByRole("button", { name: "Post count" });
    expect(post).toBeEnabled();
    expect(screen.queryByText("1 line moved since the count opened")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Not counted: set to zero" }));
    expect(await screen.findByText("1 line moved since the count opened")).toBeInTheDocument();
    expect(screen.getByText(/expected 4 pcs, now 6 pcs/)).toBeInTheDocument();
    expect(post).toBeDisabled();
    // Every line is uncounted and set to zero: both are differences.
    expect(screen.getByText("-5 pcs")).toBeInTheDocument();
    expect(screen.getByText("-6 pcs")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox", { name: "Post the moved lines against the stock now" }));
    await userEvent.click(post);
    const confirm = await screen.findByRole("dialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "Post count" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/counts\/500\/post$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/counts\/500\/post$/)[0].body).toEqual({ uncounted: "zero", acceptMoved: true });
    expect(await screen.findAllByText("Count posted")).not.toHaveLength(0);
    expect(screen.getAllByRole("link", { name: "Open movement 901" })[0]).toHaveAttribute(
      "href",
      "/inventory/movements/901",
    );
    expect(fetchMock.state.balances.every((one) => one.onHand === 0)).toBe(true);
  });

  it("keeps the stock of uncounted lines by default, and says count_lines_moved when the balances moved again", async () => {
    let refuse = true;
    const fetchMock = stubInventoryApi({
      counts: [{ ...openCount(), lines: openCount().lines?.slice(0, 1) }],
      balances: balances(),
      write: (method, path) => {
        if (method !== "POST" || !path.endsWith("/post") || !refuse) return undefined;
        refuse = false;
        return inventoryProblem(409, "count_lines_moved", {
          title: "Count lines moved",
          lines: [{ index: 0, code: "count_lines_moved", detail: "TS-M at OSL-A-01" }],
        });
      },
    });
    renderRoute("/inventory/counts/500");
    await retype(await screen.findByRole("textbox", { name: "Counted, OSL-A-01 TS-M" }), "4");
    await userEvent.click(screen.getByRole("tab", { name: "Review" }));
    await userEvent.click(screen.getByRole("button", { name: "Post count" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Post count" }));
    expect(
      await screen.findByText("Stock moved on some lines since the count opened. Review them and accept them to post."),
    ).toBeInTheDocument();
    // What was typed was saved before posting.
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[0].body).toEqual({
      lines: [{ variantId: 101, locationId: 12, counted: 4 }],
    });
    expect(sent(fetchMock, "POST", /\/counts\/500\/post$/)[0].body).toEqual({ uncounted: "keep" });
  });

  it("cancels a count after a confirmation", async () => {
    const fetchMock = stubInventoryApi({ counts: [openCount()], balances: balances() });
    renderRoute("/inventory/counts/500");
    await userEvent.click(await screen.findByRole("button", { name: "Cancel count" }));
    const confirm = await screen.findByRole("dialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "Cancel count" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/counts\/500\/cancel$/)).toHaveLength(1));
    expect((await screen.findAllByText("Cancelled")).length).toBeGreaterThan(0);
    expect(screen.getByText("Cancelled by")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Scan a location or an item" })).not.toBeInTheDocument();
  });

  it("counts a scanned box in packagings and saves it as entered, broken down beside the base quantity", async () => {
    const pallets = {
      id: 301,
      productId: 3,
      sku: "PAL-1",
      productName: "Pallet goods",
      unit: "pcs",
      packagings: packagings(),
    };
    const fetchMock = stubInventoryApi({
      variants: [...variants(), pallets],
      balances: [{ variantId: 301, locationId: 12, onHand: 1830 }],
      counts: [
        {
          ...openCount(),
          lines: [
            { id: 3, variant: pallets, location: ref(12), expected: 1830, current: 1830, added: false, moved: false },
          ],
        },
      ],
    });
    renderRoute("/inventory/counts/500");
    expect((await screen.findAllByText("3 Euro pallet, 2 Box")).length).toBe(2);
    const field = await scanField();
    await scan(field, "LOC12");
    await scan(field, "27070000000201");
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Counted Box, OSL-A-01 PAL-1" })).toHaveValue("1"));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)).toHaveLength(1), { timeout: 4000 });
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[0].body).toEqual({
      lines: [{ variantId: 301, locationId: 12, entered: { packagings: [{ packagingId: 72, count: 1 }] } }],
    });
  });

  it("counts 0 when every packaging of a line saved in packagings is stepped down to 0", async () => {
    const pallets = {
      id: 301,
      productId: 3,
      sku: "PAL-1",
      productName: "Pallet goods",
      unit: "pcs",
      packagings: packagings(),
    };
    const fetchMock = stubInventoryApi({
      variants: [...variants(), pallets],
      balances: [{ variantId: 301, locationId: 12, onHand: 30 }],
      counts: [
        {
          ...openCount(),
          lines: [
            {
              id: 3,
              variant: pallets,
              location: ref(12),
              expected: 30,
              current: 30,
              added: false,
              moved: false,
              counted: 30,
              entered: { packagings: [{ packagingId: 72, count: 2, name: "Box", quantity: 15 }] },
            },
          ],
        },
      ],
    });
    renderRoute("/inventory/counts/500");
    const boxes = await screen.findByRole("textbox", { name: "Counted Box, OSL-A-01 PAL-1" });
    expect(boxes).toHaveValue("2");
    await retype(boxes, "0");
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)).toHaveLength(1), { timeout: 4000 });
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[0].body).toEqual({
      lines: [{ variantId: 301, locationId: 12, counted: 0 }],
    });
  });
});
