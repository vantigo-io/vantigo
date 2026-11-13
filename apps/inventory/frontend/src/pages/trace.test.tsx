import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { InventoryLot } from "../api/lots";
import type { InventoryVariantRef } from "../api/stock";
import type { InventoryTrace, InventoryTraceNode } from "../api/trace";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi, variants } from "../test/server";

// The trace, the block with its downstream list and GS1 labels
// (manufacturing phase 2 T, M87–M93).

const flour: InventoryVariantRef = { id: 701, sku: "FLOUR-1", productName: "Flour", unit: "kg" };
const bread: InventoryVariantRef = { id: 703, sku: "BREAD-1", productName: "Bread", unit: "kg" };

const lotNode = (
  key: string,
  id: number,
  number: string,
  variant: InventoryVariantRef,
  fields: Partial<InventoryTraceNode> = {},
): InventoryTraceNode => ({
  key,
  ...(key.includes(".") ? { parentKey: key.slice(0, key.lastIndexOf(".")) } : {}),
  depth: key.split(".").length - 1,
  kind: "lot",
  variant,
  depthLimit: false,
  lot: { id, number, serial: false, status: "available", quantityIn: 10, onHand: 2 },
  ...fields,
});

const edge = { movementId: 5001, postedAt: "2026-09-12T08:00:00Z", consumed: 4, produced: 4 };

/** The flour's usage: the dough made of it, the bread made of the dough and delivered, and the bread again. */
const usage: InventoryTrace = {
  lotId: 1001,
  direction: "usage",
  components: "tracked",
  maxDepth: 10,
  generatedAt: "2026-09-12T10:00:00Z",
  deliveries: "shown",
  customerNames: true,
  nodes: [
    lotNode("0", 1001, "F1", flour),
    lotNode(
      "0.0",
      1002,
      "D1",
      { id: 702, sku: "DOUGH-1", productName: "Dough", unit: "kg" },
      {
        edge: { ...edge, source: { module: "manufacturing", kind: "production", id: "1", lineId: "5" } },
      },
    ),
    lotNode("0.0.0", 1003, "B1", bread, { edge: { ...edge, consumed: 3, produced: 3 } }),
    {
      key: "0.0.0.0",
      parentKey: "0.0.0",
      depth: 3,
      kind: "issue",
      variant: bread,
      depthLimit: false,
      quantity: 2,
      movement: {
        id: 6001,
        kind: "issue",
        postedAt: "2026-09-12T09:00:00Z",
        source: { module: "orders", kind: "delivery", id: "71" },
      },
      delivery: {
        deliveryId: 71,
        deliveryNumber: 3001,
        orderId: 9,
        orderNumber: 2001,
        customerId: 42,
        customerName: "Bakeriet AS",
        postedAt: "2026-09-12T09:00:00Z",
      },
    },
    lotNode("0.1", 1003, "B1", bread, { edge: { ...edge, consumed: 1, produced: 3 }, seenAt: "0.0.0" }),
  ],
};

const lotFixture = (fields: Partial<InventoryLot> = {}): InventoryLot => ({
  id: 1001,
  number: "F1",
  serial: false,
  variant: flour,
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

const keysShown = () =>
  Array.from(screen.getByTestId("trace-tree").querySelectorAll("[data-trace-key]")).map((row) =>
    row.getAttribute("data-trace-key"),
  );

describe("the trace (M87–M91)", () => {
  it("shows the tree, folds a branch, marks a lot traced already and links its first place", async () => {
    stubInventoryApi({ variants: [...variants(), flour, bread], trace: () => usage });
    renderRoute("/inventory/trace?lotId=1001");
    expect(await screen.findByRole("heading", { name: "Trace of lot F1" })).toBeInTheDocument();
    await waitFor(() => expect(keysShown()).toEqual(["0", "0.0", "0.0.0", "0.0.0.0", "0.1"]));
    expect(screen.getByRole("link", { name: "D1" })).toHaveAttribute("href", "/inventory/lots/1002");
    expect(screen.getByText("Delivery 3001, order 2001 · Bakeriet AS")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Production 1" })).toHaveAttribute("href", "/manufacturing/orders/5");
    expect(screen.getByRole("link", { name: "Already shown above" })).toHaveAttribute("href", "#trace-node-0.0.0");

    await userEvent.click(screen.getByRole("button", { name: "Fold what is under D1" }));
    expect(keysShown()).toEqual(["0", "0.0", "0.1"]);
    await userEvent.click(screen.getByRole("button", { name: "Show what is under D1" }));
    expect(keysShown()).toEqual(["0", "0.0", "0.0.0", "0.0.0.0", "0.1"]);
  });

  it("opens the path to a lot traced already before it jumps there", async () => {
    stubInventoryApi({ variants: [...variants(), flour, bread], trace: () => usage });
    renderRoute("/inventory/trace?lotId=1001");
    await waitFor(() => expect(keysShown()).toEqual(["0", "0.0", "0.0.0", "0.0.0.0", "0.1"]));
    await userEvent.click(screen.getByRole("button", { name: "Fold what is under D1" }));
    expect(keysShown()).toEqual(["0", "0.0", "0.1"]);
    const scrolled = vi.mocked(window.scrollTo);
    scrolled.mockClear();
    await userEvent.click(screen.getByRole("link", { name: "Already shown above" }));
    await waitFor(() => expect(keysShown()).toEqual(["0", "0.0", "0.0.0", "0.0.0.0", "0.1"]));
    await waitFor(() => expect(scrolled).toHaveBeenCalled());
  });

  it("names where a transfer sent the lot", async () => {
    stubInventoryApi({
      variants: [...variants(), flour],
      trace: () => ({
        ...usage,
        nodes: [
          usage.nodes[0],
          {
            key: "0.0",
            parentKey: "0",
            depth: 1,
            kind: "transfer_out",
            variant: flour,
            depthLimit: false,
            quantity: 4,
            movement: {
              id: 6002,
              kind: "transfer_out",
              postedAt: "2026-09-12T09:00:00Z",
              source: { module: "inventory", kind: "transfer", id: "1001" },
              toSiteName: "Bergen",
            },
          },
        ],
      }),
    });
    renderRoute("/inventory/trace?lotId=1001");
    expect(await screen.findByText("Sent to Bergen")).toBeInTheDocument();
  });

  it("links an unbuild to its page in Manufacturing (manufacturing phase 2 M123)", async () => {
    stubInventoryApi({
      variants: [...variants(), flour],
      trace: () => ({
        ...usage,
        nodes: [
          usage.nodes[0],
          {
            key: "0.0",
            parentKey: "0",
            depth: 1,
            kind: "unbuild",
            variant: flour,
            depthLimit: false,
            quantity: 1,
            movement: {
              id: 6003,
              kind: "unbuild",
              postedAt: "2026-09-12T09:00:00Z",
              source: { module: "manufacturing", kind: "unbuild", id: "1001" },
            },
          },
        ],
      }),
    });
    renderRoute("/inventory/trace?lotId=1001");
    expect(await screen.findByText("Unbuilt")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Unbuild" })).toHaveAttribute("href", "/manufacturing/unbuilds/1001");
  });

  it("asks the other direction and the components, says when deliveries are hidden, and exports", async () => {
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:file");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const fetchMock = stubInventoryApi({
      variants: [...variants(), flour, bread],
      trace: (query) =>
        query.get("direction") === "origin"
          ? { ...usage, direction: "origin", deliveries: "hidden", customerNames: false, nodes: [usage.nodes[0]] }
          : usage,
    });
    renderRoute("/inventory/trace?lotId=1001");
    await screen.findByRole("heading", { name: "Trace of lot F1" });
    await userEvent.click(screen.getByText("Where it came from"));
    expect(await screen.findByText(/You cannot see the orders/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("switch", { name: "Show the components without a lot" }));
    await waitFor(() =>
      expect(sent(fetchMock, "GET", /\/trace\?/).at(-1)?.url).toContain("direction=origin&components=all"),
    );
    await userEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    await waitFor(() => expect(createObjectURL).toHaveBeenCalled());
    expect(sent(fetchMock, "GET", /\/trace\.csv\?/).at(-1)?.url).toContain(
      "lotId=1001&direction=origin&components=all",
    );
  });

  it("answers trace_too_large in words", async () => {
    stubInventoryApi({
      trace: () => jsonResponse(400, { status: 400, title: "The trace is too large", code: "trace_too_large" }),
    });
    renderRoute("/inventory/trace?lotId=1001");
    expect(await screen.findByText(/more than 2,000 lots/)).toBeInTheDocument();
  });
});

describe("the block with its downstream list (M92)", () => {
  it("shows what the lot reached, blocks it and the lots made from it, and marks a lot the summary missed", async () => {
    const fetchMock = stubInventoryApi({
      variants: [...variants(), flour, bread],
      lots: [lotFixture()],
      trace: () => usage,
      write: (method, path) =>
        method === "POST" && path === "/lots/1001/block"
          ? jsonResponse(200, {
              lot: lotFixture({ status: "blocked" }),
              blocked: [
                { id: 1001, number: "F1", sku: "FLOUR-1", productName: "Flour", root: true },
                { id: 1002, number: "D1", sku: "DOUGH-1", productName: "Dough", root: false },
                { id: 1003, number: "B1", sku: "BREAD-1", productName: "Bread", root: false },
                { id: 1004, number: "B3", sku: "BREAD-1", productName: "Bread", root: false },
              ],
            })
          : undefined,
    });
    renderRoute("/inventory/lots/1001");
    await userEvent.click(await screen.findByRole("button", { name: "Block" }));
    const summary = await screen.findByTestId("block-summary");
    expect(within(summary).getByText(/^D1 · DOUGH-1/)).toBeInTheDocument();
    expect(within(summary).getByText(/Delivery 3001, order 2001 · Bakeriet AS/)).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Also block the lots made from it" })).toBeChecked();
    const dialog = screen.getByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Block" }));
    expect(await screen.findByText("Blocked 4 lots:")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/lots\/1001\/block$/)[0].body).toEqual({ cause: "recall", downstream: true });
    const b3 = screen.getByRole("link", { name: "B3" }).closest("li") as HTMLElement;
    expect(within(b3).getByText("New")).toBeInTheDocument();
    const d1 = screen.getByRole("link", { name: "D1" }).closest("li") as HTMLElement;
    expect(within(d1).queryByText("New")).not.toBeInTheDocument();
  });

  it("releases the lot page's block with the blocks it put downstream", async () => {
    const fetchMock = stubInventoryApi({
      variants: [...variants(), flour],
      lots: [
        lotFixture({
          status: "blocked",
          holds: [
            {
              id: 77,
              kind: "block",
              cause: "recall",
              source: { module: "inventory", kind: "lot", id: "1001" },
              inventorys: true,
              createdAt: "2026-09-12T08:00:00Z",
            },
          ],
        }),
      ],
    });
    renderRoute("/inventory/lots/1001");
    await userEvent.click(await screen.findByRole("button", { name: "Release" }));
    const downstream = await screen.findByRole("checkbox", {
      name: "Also release the blocks it put on the lots made from it",
    });
    expect(downstream).toBeChecked();
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Release" }));
    await waitFor(() =>
      expect(sent(fetchMock, "POST", /\/lots\/1001\/holds\/77\/release$/)[0]?.body).toEqual({ downstream: true }),
    );
  });

  it("prints GS1-128 labels when asked", async () => {
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:file");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const fetchMock = stubInventoryApi({ variants: [...variants(), flour], lots: [lotFixture()] });
    renderRoute("/inventory/lots/1001");
    await userEvent.click(await screen.findByRole("button", { name: "Print labels" }));
    await userEvent.click(await screen.findByRole("radio", { name: "GS1-128: the GTIN, the dates and the lot" }));
    await userEvent.click(screen.getByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(sent(fetchMock, "GET", /\/lots\/labels\.pdf/)[0]?.url).toContain("format=gs1"));
  });
});
