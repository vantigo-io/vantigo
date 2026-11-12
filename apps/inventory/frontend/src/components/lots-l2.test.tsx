import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { InventoryCount } from "../api/counts";
import type { InventoryLot } from "../api/lots";
import type { InventoryVariantRef } from "../api/stock";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { locations, stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

// Lots in counts, transfers, the expiring list and labels (manufacturing
// phase 2 L2, M69–M73). Tracked variants are made in the fake's catalog.
const milk: InventoryVariantRef = {
  id: 601,
  productId: 6,
  sku: "MILK-1",
  productName: "Milk",
  unit: "l",
  tracking: "lot",
};

const ref = (id: number) => {
  const one = locations().find((location) => location.id === id);
  if (!one) throw new Error(`no location ${id}`);
  return { id: one.id, siteId: one.siteId, fullCode: one.fullCode, kind: one.kind, status: one.status };
};

/** Where a fixture lot lies: on hand on one of Oslo's racks. */
const at = (locationId: 12 | 13, onHand: number): InventoryLot["locations"][number] => ({
  site: { id: 1, code: "OSL", name: "Oslo lager", kind: "warehouse", status: "active" },
  location: ref(locationId),
  onHand,
});

const lot = (id: number, number: string, fields: Partial<InventoryLot> = {}): InventoryLot => ({
  id,
  number,
  serial: false,
  variant: milk,
  status: "available",
  expired: false,
  receivedAt: "2026-09-01T08:00:00Z",
  onHand: 5,
  revision: 1,
  createdAt: "2026-09-01T08:00:00Z",
  holds: [],
  locations: [],
  movements: [],
  attachments: [],
  ...fields,
});

/** Catches what is handed to the browser: jsdom makes no object URLs. */
const catchDownloads = () => {
  const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:file");
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
  return createObjectURL;
};

describe("the expiring list (M72)", () => {
  it("lists the lots soonest first with their days left, exports them and prints their labels", async () => {
    const downloads = catchDownloads();
    const fetchMock = stubInventoryApi({
      variants: [...variants(), milk],
      lots: [
        lot(1002, "E2", { expiry: "2026-09-20" }),
        lot(1001, "E1", { expiry: "2026-09-10", expired: true }),
        lot(1003, "E3", { expiry: "2026-12-31" }),
      ],
    });
    renderRoute("/inventory/expiring");
    const first = await screen.findByRole("link", { name: "E1" });
    expect(first).toHaveAttribute("href", "/inventory/lots/1001");
    const table = screen.getByRole("table");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getAllByRole("link")[0].textContent)).toEqual(["E1", "E2"]);
    expect(within(rows[0]).getByText("2 days ago")).toBeInTheDocument();
    expect(within(rows[1]).getByText("8 days")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    await waitFor(() => expect(sent(fetchMock, "GET", /\/lots\/expiring\.csv/)).toHaveLength(1));

    await userEvent.click(screen.getByRole("button", { name: "Print labels" }));
    const dialog = await screen.findByRole("dialog", { name: "Print labels" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(sent(fetchMock, "GET", /\/lots\/labels\.pdf/)).toHaveLength(1));
    expect(sent(fetchMock, "GET", /\/lots\/labels\.pdf/)[0].url).toContain("ids=1001%2C1002");
    await waitFor(() => expect(downloads).toHaveBeenCalledTimes(2));
  });
});

describe("a transfer's lots (M70)", () => {
  it("proposes the first usable lot on the source location in FEFO order and sends it", async () => {
    catchDownloads();
    const fetchMock = stubInventoryApi({
      variants: [...variants(), milk],
      transfers: [transferFixture({ id: 5, number: 1042, revision: 2, lines: [transferLine(1, milk, 3)] })],
      balances: [{ variantId: 601, locationId: 12, onHand: 10 }],
      lots: [
        lot(1001, "OLD", { expiry: "2026-09-01", expired: true, locations: [at(12, 4)] }),
        lot(1002, "NEXT", { expiry: "2026-10-01", locations: [at(12, 6)] }),
      ],
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send…" }));
    const dialog = await screen.findByRole("dialog", { name: "Send transfer 1042 to BGO" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Lot of line 1" })).toHaveValue(
        "NEXT · Expires Oct 1, 2026 · 6 l",
      ),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Send 1 line" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers\/5\/send$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/transfers\/5\/send$/)[0].body).toEqual({
      lines: [{ lineId: 1, fromLocationId: 12, lotId: 1002 }],
    });
  });
});

describe("a count's lots (M69)", () => {
  it("shows a row per lot of a tracked item and counts each by its lot", async () => {
    const count: InventoryCount = {
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
        {
          id: 1,
          variant: milk,
          location: ref(12),
          lot: { id: 1001, number: "A-1", expiry: "2026-10-12" },
          expected: 5,
          current: 5,
          added: false,
          moved: false,
        },
        {
          id: 2,
          variant: milk,
          location: ref(12),
          lot: { id: 1002, number: "A-2" },
          expected: 3,
          current: 3,
          added: false,
          moved: false,
        },
      ],
    };
    const fetchMock = stubInventoryApi({
      variants: [...variants(), milk],
      counts: [count],
      balances: [{ variantId: 601, locationId: 12, onHand: 8 }],
    });
    renderRoute("/inventory/counts/500");
    expect(await screen.findByRole("link", { name: "A-1" })).toHaveAttribute("href", "/inventory/lots/1001");
    expect(screen.getByText("Expires Oct 12, 2026")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Counted, OSL-A-01 MILK-1 A-2" }), "2");
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)).toHaveLength(1), { timeout: 3000 });
    expect(sent(fetchMock, "PUT", /\/counts\/500\/lines$/)[0].body).toEqual({
      lines: [{ variantId: 601, locationId: 12, lotId: 1002, counted: 2 }],
    });
  });
});
