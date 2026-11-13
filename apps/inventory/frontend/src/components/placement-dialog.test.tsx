import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InventoryPlacement } from "../api/placements";
import { choose, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type InventoryStub, stubInventoryApi, variants } from "../test/server";
import { PlacementDialog } from "./placement-dialog";
import "../i18n";

const [tsM] = variants();

const puts = (fetchMock: InventoryStub) =>
  fetchMock.actualCalls
    .filter(([url, init]) => init?.method === "PUT" && /\/placements\/\d+$/.test(String(url)))
    .map(([url, init]) => ({
      url: String(url),
      body: JSON.parse(String(init?.body)),
      ifMatch: new Headers(init?.headers).get("If-Match"),
      ifNoneMatch: new Headers(init?.headers).get("If-None-Match"),
    }));

/** The variant's site heading on its stock page, with its placement's line beside it. */
const siteBlock = async (name: string) => (await screen.findByText(name)).closest("div[class*='Stack']") as HTMLElement;

describe("a variant's placement at a site (I54, I61)", () => {
  it("shows the primary location first, the levels and Low on the site's rows", async () => {
    stubInventoryApi({
      balances: [
        { variantId: 101, locationId: 12, onHand: 9 },
        { variantId: 101, locationId: 13, onHand: 3 },
      ],
      placements: [{ variantId: 101, siteId: 1, primaryLocationId: 13, reorderPoint: 20, maximum: 60, revision: 2 }],
    });
    renderRoute("/inventory/variants/101");
    const block = await siteBlock("OSL · Oslo lager");
    expect(within(block).getByText("Reorder point 20 pcs · Maximum 60 pcs")).toBeInTheDocument();
    expect(within(block).getByText("Low")).toBeInTheDocument();
    // The only site holding it, so the page's one table.
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0]).toHaveTextContent("OSL-A-02");
    expect(within(rows[0]).getByText("primary")).toBeInTheDocument();
    expect(rows[1]).toHaveTextContent("OSL-A-01");
  });

  it("creates a placement under If-None-Match, refusing a maximum below the reorder point and an empty one first", async () => {
    const fetchMock = stubInventoryApi({ balances: [{ variantId: 101, locationId: 12, onHand: 4 }] });
    renderRoute("/inventory/variants/101");
    const block = await siteBlock("OSL · Oslo lager");
    await userEvent.click(within(block).getByRole("button", { name: "Edit placement" }));
    const dialog = await screen.findByRole("dialog", { name: "Placement at Oslo lager" });
    expect(within(dialog).getByText("Low when available + incoming is at or below it.")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();

    // Nothing set is never stored: it says so, and sends nothing.
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(
        "Set a primary location, a reorder point or a maximum — or remove the placement.",
      ),
    ).toBeInTheDocument();

    await retype(within(dialog).getByRole("textbox", { name: "Reorder point" }), "10");
    await retype(within(dialog).getByRole("textbox", { name: "Maximum" }), "5");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("The maximum cannot be below the reorder point.")).toBeInTheDocument();
    expect(puts(fetchMock)).toHaveLength(0);

    await retype(within(dialog).getByRole("textbox", { name: "Maximum" }), "40");
    // The multiple a suggestion is rounded up to (manufacturing phase 2 M105).
    await retype(within(dialog).getByRole("textbox", { name: "Multiple" }), "0");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("The multiple must be more than 0.")).toBeInTheDocument();
    await retype(within(dialog).getByRole("textbox", { name: "Multiple" }), "12");
    await choose(dialog, "Primary location", "OSL-A-02");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(puts(fetchMock)).toHaveLength(1));
    const [put] = puts(fetchMock);
    expect(put.url).toContain("/api/v1/inventory/variants/101/placements/1");
    expect(put.ifNoneMatch).toBe("*");
    expect(put.ifMatch).toBeNull();
    expect(put.body).toEqual({ primaryLocationId: 13, reorderPoint: 10, maximum: 40, multiple: 12 });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(
      await within(await siteBlock("OSL · Oslo lager")).findByText(
        "Reorder point 10 pcs · Maximum 40 pcs · Multiple 12 pcs",
      ),
    ).toBeInTheDocument();
  });

  it("changes a placement under If-Match, clears a field to null, and removes it", async () => {
    const fetchMock = stubInventoryApi({
      balances: [{ variantId: 101, locationId: 12, onHand: 40 }],
      placements: [{ variantId: 101, siteId: 1, primaryLocationId: 12, reorderPoint: 10, maximum: 50, revision: 4 }],
    });
    renderRoute("/inventory/variants/101");
    await userEvent.click(within(await siteBlock("OSL · Oslo lager")).getByRole("button", { name: "Edit placement" }));
    let dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Reorder point" })).toHaveValue("10");
    await retype(within(dialog).getByRole("textbox", { name: "Maximum" }), "");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(puts(fetchMock)).toHaveLength(1));
    expect(puts(fetchMock)[0]).toMatchObject({
      ifMatch: "4",
      ifNoneMatch: null,
      body: { primaryLocationId: 12, reorderPoint: 10, maximum: null },
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await userEvent.click(within(await siteBlock("OSL · Oslo lager")).getByRole("button", { name: "Edit placement" }));
    dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/placements\/1$/)).toHaveLength(1));
    expect(sent(fetchMock, "DELETE", /\/placements\/1$/)[0].ifMatch).toBe("5");
    await waitFor(() => expect(fetchMock.state.placements).toEqual([]));
  });

  it("reloads and says so when the placement moved on since it was read", async () => {
    const fetchMock = stubInventoryApi({
      balances: [{ variantId: 101, locationId: 12, onHand: 40 }],
      placements: [
        { variantId: 101, siteId: 1, primaryLocationId: null, reorderPoint: 10, maximum: null, revision: 1 },
      ],
    });
    renderRoute("/inventory/variants/101");
    await userEvent.click(within(await siteBlock("OSL · Oslo lager")).getByRole("button", { name: "Edit placement" }));
    const dialog = await screen.findByRole("dialog");
    // Somebody else saves it meanwhile.
    fetchMock.state.placements = [{ ...fetchMock.state.placements[0], reorderPoint: 25, revision: 2 }];
    await retype(within(dialog).getByRole("textbox", { name: "Reorder point" }), "12");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Changed by someone else")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await within(await siteBlock("OSL · Oslo lager")).findByText("Reorder point 25 pcs")).toBeInTheDocument();
    expect(fetchMock.state.placements[0].reorderPoint).toBe(25);
  });

  it("offers a placement at a site holding none of it", async () => {
    stubInventoryApi({ balances: [{ variantId: 101, locationId: 12, onHand: 4 }] });
    renderRoute("/inventory/variants/101");
    const bergen = await screen.findByText("BGO · Bergen butikk");
    expect(
      within(bergen.parentElement as HTMLElement).getByRole("button", { name: "Edit placement" }),
    ).toBeInTheDocument();
  });

  it("hides Edit placement from a caller who cannot adjust stock", async () => {
    stubInventoryApi({ balances: [{ variantId: 101, locationId: 12, onHand: 4 }], meta: { canAdjust: false } });
    renderRoute("/inventory/variants/101");
    await screen.findByText("OSL · Oslo lager");
    expect(screen.queryByRole("button", { name: "Edit placement" })).not.toBeInTheDocument();
    expect(screen.queryByText("BGO · Bergen butikk")).not.toBeInTheDocument();
  });
});

describe("the placement dialog's revision (I61)", () => {
  const site = { id: 1, code: "OSL", name: "Oslo lager", kind: "warehouse" as const, status: "active" as const };
  const read = (revision: number) => ({
    primaryLocationId: null,
    reorderPoint: 10,
    maximum: null,
    multiple: null,
    revision,
    low: false,
  });

  /** The dialog alone, re-rendered with what a refetch would hand it. */
  const mount = (placement: InventoryPlacement | undefined) => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const tree = (current: InventoryPlacement | undefined) => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <PlacementDialog opened onClose={() => {}} variant={tsM} site={site} placement={current} />
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    );
    const view = render(tree(placement));
    return (next: InventoryPlacement | undefined) => view.rerender(tree(next));
  };

  it("changes under the revision it was opened at, though a refetch hands it a newer one", async () => {
    const fetchMock = stubInventoryApi({
      placements: [
        { variantId: 101, siteId: 1, primaryLocationId: null, reorderPoint: 10, maximum: null, revision: 3 },
      ],
    });
    const rerender = mount(read(3));
    // Somebody else saved it, and a refetch brought their revision in while the dialog is open.
    rerender(read(4));
    await retype(screen.getByRole("textbox", { name: "Reorder point" }), "12");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(puts(fetchMock)).toHaveLength(1));
    expect(puts(fetchMock)[0]).toMatchObject({ ifMatch: "3", ifNoneMatch: null });
  });

  it("creates under If-None-Match though a refetch found a placement meanwhile, and offers no Remove", async () => {
    const fetchMock = stubInventoryApi();
    const rerender = mount(undefined);
    rerender(read(1));
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
    await retype(screen.getByRole("textbox", { name: "Reorder point" }), "12");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(puts(fetchMock)).toHaveLength(1));
    expect(puts(fetchMock)[0]).toMatchObject({ ifMatch: null, ifNoneMatch: "*" });
  });
});
