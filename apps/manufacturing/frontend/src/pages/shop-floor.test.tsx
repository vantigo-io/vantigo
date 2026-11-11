import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SHOP_FLOOR_WORK_CENTER_STORAGE_KEY } from "../lib/shop-floor-storage";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { people, stubManufacturingApi, workOrderFixture } from "../test/server";

afterEach(() => {
  try {
    window.localStorage.removeItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY);
  } catch {
    // Nothing to clean when storage is unavailable.
  }
});

describe("the work center picker", () => {
  it("offers every active work center, and the one this device last used first", async () => {
    window.localStorage.setItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY, "2");
    stubManufacturingApi();
    renderRoute("/manufacturing/shop-floor");
    expect(await screen.findByRole("link", { name: "Continue at PAINT · PAINT station" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "SAW · SAW station" })).toBeInTheDocument();
  });

  it("renders the same picker when storage is blocked", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    stubManufacturingApi();
    renderRoute("/manufacturing/shop-floor");
    expect(await screen.findByRole("link", { name: "SAW · SAW station" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^Continue at/ })).not.toBeInTheDocument();
  });

  it("remembers the work center opened", async () => {
    stubManufacturingApi();
    renderRoute("/manufacturing/shop-floor/work-centers/1");
    expect(await screen.findByText("Queue")).toBeInTheDocument();
    expect(window.localStorage.getItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY)).toBe("1");
  });
});

describe("a work center's queue", () => {
  it("shows each work order's order, product, what remains, its reservation and who runs it", async () => {
    stubManufacturingApi({
      workOrders: [
        workOrderFixture({
          status: "in_progress",
          operators: [{ user: people()[0], kind: "productive", startedAt: "2026-11-11T08:00:00Z" }],
        }),
      ],
    });
    renderRoute("/manufacturing/shop-floor/work-centers/1");
    const card = await screen.findByRole("link", { name: "MO-12 · Cut" });
    expect(within(card).getByText("TABLE · Oak table")).toBeInTheDocument();
    expect(within(card).getByText("10 pcs remaining")).toBeInTheDocument();
    expect(within(card).getByText("Reserved")).toBeInTheDocument();
    expect(within(card).getByText("In progress")).toBeInTheDocument();
    expect(within(card).getByText("Running: Kari Nordmann")).toBeInTheDocument();
  });

  it("blocks with a reason and a note", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canWorkShopFloor: true } });
    renderRoute("/manufacturing/shop-floor/work-centers/1");
    await userEvent.click(await screen.findByRole("button", { name: "Block" }));
    const dialog = await screen.findByRole("dialog");
    const block = within(dialog).getByRole("button", { name: "Block" });
    expect(block).toBeDisabled();
    await userEvent.click(within(dialog).getByLabelText("Material missing"));
    await userEvent.type(within(dialog).getByLabelText("Note"), "No boards");
    await userEvent.click(block);
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-centers\/1\/block$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-centers\/1\/block$/)[0].body).toEqual({
      reason: "material_missing",
      note: "No boards",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("says why it is blocked and offers Unblock", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      blocks: { 1: { reason: "breakdown", note: "Blade broke", since: "2026-11-11T08:00:00Z" } },
    });
    renderRoute("/manufacturing/shop-floor/work-centers/1");
    expect(await screen.findByText("Blocked: Breakdown")).toBeInTheDocument();
    expect(screen.getByText("Blade broke")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Unblock" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-centers\/1\/unblock$/)).toHaveLength(1));
  });

  it("offers neither Block nor Unblock without a shop-floor permission", async () => {
    stubManufacturingApi();
    renderRoute("/manufacturing/shop-floor/work-centers/1");
    expect(await screen.findByText("Queue")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Block" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Unblock" })).not.toBeInTheDocument();
  });
});
