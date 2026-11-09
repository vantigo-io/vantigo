import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { InventoryLowStockRow } from "../api/placements";
import { lowStockTransfer, richestSource } from "../lib/low-stock";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { packagings, sites, stubInventoryApi, variants } from "../test/server";

const [osl, bgo] = sites().map(({ id, code, name, kind, status }) => ({ id, code, name, kind, status }));
const [tsM, tsL, bolt] = variants();
const boxed = { ...bolt, packagings: packagings() };

/** A low row at Bergen unless told otherwise, nothing elsewhere and no draft open. */
const lowRow = (
  fields: Partial<InventoryLowStockRow> & Pick<InventoryLowStockRow, "variant">,
): InventoryLowStockRow => ({
  site: bgo,
  onHand: 0,
  reserved: 0,
  available: 0,
  incoming: 0,
  reorderPoint: 5,
  maximum: null,
  needed: 6,
  suggested: 6,
  packaging: null,
  elsewhere: [],
  openDrafts: [],
  ...fields,
});

const rows = () => [
  lowRow({
    variant: boxed,
    available: 3,
    reorderPoint: 5,
    maximum: 24,
    needed: 21,
    suggested: 30,
    packaging: { id: 72, kind: "case", name: "Box", quantity: 15, count: 2 },
    elsewhere: [{ siteId: 1, siteName: "Oslo lager", available: 80 }],
  }),
  lowRow({
    variant: tsM,
    incoming: 2,
    reorderPoint: 20,
    maximum: 60,
    needed: 58,
    suggested: 58,
    elsewhere: [{ siteId: 1, siteName: "Oslo lager", available: 4 }],
  }),
  lowRow({ variant: tsL, site: osl, reorderPoint: 1, needed: 2, suggested: 2 }),
];

const rowOf = (sku: string) => (screen.getByRole("link", { name: sku }).closest("tr") as HTMLElement) ?? undefined;

describe("the low-stock list (I58–I60)", () => {
  it("lists each low item with its levels, the rounding sentence and what other sites have", async () => {
    stubInventoryApi({ lowStock: rows() });
    renderRoute("/inventory/low-stock");
    const row = rowOf((await screen.findByRole("link", { name: "BOLT-10" })).textContent ?? "");
    expect(within(row).getByText("30 pcs")).toBeInTheDocument();
    expect(
      within(row).getByText("21 needed, rounded up to 2 cases of 15 = 30, 9 above the maximum of 24"),
    ).toBeInTheDocument();
    expect(within(row).getByText("Oslo lager 80")).toBeInTheDocument();
    expect(within(rowOf("TS-M")).getByText("58 needed")).toBeInTheDocument();
    // Not filtered to a site, so each row names its site.
    expect(within(rowOf("TS-L")).getByText("OSL")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "BOLT-10" })).toHaveAttribute("href", "/inventory/variants/201");
  });

  it("filters by site in the URL and by search, and loads the next page", async () => {
    const fetchMock = stubInventoryApi({ lowStock: rows(), lowStockPageSize: 1 });
    const { router } = renderRoute("/inventory/low-stock?siteId=2");
    expect(await screen.findByRole("link", { name: "BOLT-10" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "TS-M" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("link", { name: "TS-M" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Search" }), "ts-m{Enter}");
    await waitFor(() => expect(router.state.location.search).toEqual({ siteId: 2, q: "ts-m" }));
    await waitFor(() => expect(screen.queryByRole("link", { name: "BOLT-10" })).not.toBeInTheDocument());
    expect(sent(fetchMock, "GET", /\/low-stock\?/).at(-1)?.url).toContain("siteId=2&q=ts-m");

    await choose(document.body, "Site", "OSL · Oslo lager");
    await waitFor(() => expect(router.state.location.search).toEqual({ siteId: 1, q: "ts-m" }));
  });

  it("opens on the search the URL carries", async () => {
    const fetchMock = stubInventoryApi({ lowStock: rows() });
    renderRoute("/inventory/low-stock?q=BOLT");
    expect(await screen.findByRole("link", { name: "BOLT-10" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Search" })).toHaveValue("BOLT");
    expect(screen.queryByRole("link", { name: "TS-M" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/low-stock\?/)[0].url).toContain("q=BOLT");
  });

  it("shows a row the server answers again on a later page once", async () => {
    // TS-M's SKU was refreshed between the two reads, so the second page carries it again.
    const [boltRow, tsMRow] = rows();
    stubInventoryApi({ lowStock: [boltRow, tsMRow, { ...tsMRow, suggested: 99 }], lowStockPageSize: 2 });
    renderRoute("/inventory/low-stock?siteId=2");
    await userEvent.click(await screen.findByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument());
    expect(screen.getAllByRole("link", { name: "TS-M" })).toHaveLength(1);
    expect(screen.queryByText("99 pcs")).not.toBeInTheDocument();
  });

  it("forgets a chosen row a refetch dropped, and sends a suggestion as it is now", async () => {
    const fixture = rows();
    const fetchMock = stubInventoryApi({ lowStock: fixture });
    const { queryClient } = renderRoute("/inventory/low-stock");
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose BOLT-10 at BGO" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose TS-M at BGO" }));
    expect(screen.getByRole("checkbox", { name: "Choose TS-L at OSL" })).toBeDisabled();
    // The bolt was filled up meanwhile, and TS-M needs more.
    fixture.splice(0, 1);
    fixture[0] = { ...fixture[0], needed: 60, suggested: 60 };
    await queryClient.invalidateQueries();
    await waitFor(() => expect(screen.queryByRole("link", { name: "BOLT-10" })).not.toBeInTheDocument());
    expect(screen.getByText("1 selected")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose TS-M at BGO" }));
    // Nothing chosen any more: a row of another site may be.
    expect(screen.getByRole("checkbox", { name: "Choose TS-L at OSL" })).toBeEnabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose TS-M at BGO" }));
    await userEvent.click(screen.getByRole("button", { name: "Create transfer" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/transfers$/)[0].body).toMatchObject({
      toSiteId: 2,
      lines: [{ variantId: 101, quantity: 60, suggestion: { needed: 60, packagingId: null, count: null } }],
    });
  });

  it("creates a transfer of the chosen rows from the site with most of them, with their suggestions, and opens it", async () => {
    const fetchMock = stubInventoryApi({ lowStock: rows() });
    const { router } = renderRoute("/inventory/low-stock?siteId=2");
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose BOLT-10 at BGO" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose TS-M at BGO" }));
    expect(screen.getByText("2 selected")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Create transfer to Bergen butikk from" })).toHaveValue(
      "OSL · Oslo lager",
    );
    await userEvent.click(screen.getByRole("button", { name: "Create transfer" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/transfers$/)[0].body).toEqual({
      fromSiteId: 1,
      toSiteId: 2,
      lines: [
        { variantId: 201, quantity: 30, suggestion: { needed: 21, packagingId: 72, count: 2 } },
        { variantId: 101, quantity: 58, suggestion: { needed: 58, packagingId: null, count: null } },
      ],
    });
    await waitFor(() =>
      expect(router.state.location.pathname).toBe(`/inventory/transfers/${fetchMock.state.transfers[0].id}`),
    );
  });

  it("chooses rows at one site only", async () => {
    stubInventoryApi({ lowStock: rows() });
    renderRoute("/inventory/low-stock");
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose TS-M at BGO" }));
    expect(screen.getByRole("checkbox", { name: "Choose TS-L at OSL" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Choose BOLT-10 at BGO" })).toBeEnabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose TS-M at BGO" }));
    expect(screen.getByRole("checkbox", { name: "Choose TS-L at OSL" })).toBeEnabled();
  });

  it("says a draft is open on the row, and asks before making a second", async () => {
    const fetchMock = stubInventoryApi({
      lowStock: [
        lowRow({ variant: tsM, openDrafts: [1043], elsewhere: [{ siteId: 1, siteName: "Oslo lager", available: 9 }] }),
      ],
    });
    renderRoute("/inventory/low-stock?siteId=2");
    expect(await screen.findByText("Draft 1043 open")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose TS-M at BGO" }));
    await userEvent.click(screen.getByRole("button", { name: "Create transfer" }));
    const ask = await screen.findByRole("dialog", { name: "A draft is already open" });
    await userEvent.click(within(ask).getByRole("button", { name: "Cancel" }));
    expect(sent(fetchMock, "POST", /\/transfers$/)).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "Create transfer" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "A draft is already open" })).getByRole("button", {
        name: "Create another draft",
      }),
    );
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers$/)).toHaveLength(1));
  });

  it("hides choosing and Create transfer without Transfer stock, and exports the filtered list", async () => {
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:csv");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const fetchMock = stubInventoryApi({ lowStock: rows(), meta: { canTransfer: false } });
    renderRoute("/inventory/low-stock?siteId=2");
    await screen.findByRole("link", { name: "BOLT-10" });
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create transfer" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    await waitFor(() => expect(createObjectURL).toHaveBeenCalledTimes(1));
    expect(sent(fetchMock, "GET", /\/low-stock\.csv/)[0].url).toContain("/api/v1/inventory/low-stock.csv?siteId=2");
    expect(await createObjectURL.mock.calls[0][0].text()).toContain("BGO,BOLT-10,30");
  });

  it("says so to a caller without both views", async () => {
    stubInventoryApi({ meta: { canViewStock: false } });
    renderRoute("/inventory/low-stock");
    expect(
      await screen.findByText(
        "You cannot see the low-stock list. Ask an administrator for the permissions View locations and View stock.",
      ),
    ).toBeInTheDocument();
  });
});

describe("the transfer drafted from the list (I59)", () => {
  it("defaults to the site with most available across the chosen rows, summed in thousandths", () => {
    const at = (siteId: number, available: number) => ({ siteId, siteName: String(siteId), available });
    // 0.1 + 0.2 is 0.3 in thousandths, a tie with site 3, which came first: in floating point it would be more.
    expect(
      richestSource([
        lowRow({ variant: tsM, elsewhere: [at(3, 0.3), at(1, 0.1)] }),
        lowRow({ variant: tsL, elsewhere: [at(1, 0.2)] }),
      ]),
    ).toBe(3);
    expect(richestSource([lowRow({ variant: tsM, elsewhere: [at(1, 2), at(3, 5)] })])).toBe(3);
    expect(richestSource([lowRow({ variant: tsM, elsewhere: [at(1, 0), at(3, -2)] })])).toBeUndefined();
    expect(lowStockTransfer([lowRow({ variant: tsM })], 1, 2)).toEqual({
      fromSiteId: 1,
      toSiteId: 2,
      lines: [{ variantId: 101, quantity: 6, suggestion: { needed: 6, packagingId: null, count: null } }],
    });
  });
});
