import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingDeviation } from "../api/quality";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi } from "../test/server";

const deviation = (patch: Partial<ManufacturingDeviation> = {}): ManufacturingDeviation => ({
  id: 7,
  number: "DEV-7",
  title: "Glass shard",
  description: "Found in a jar.",
  status: "open",
  disposition: null,
  quarantine: true,
  holdsReleased: false,
  order: null,
  workOrderId: null,
  workOrderName: null,
  checkResultId: null,
  lotId: null,
  lotNumber: null,
  variant: null,
  lots: [{ lotId: 501, lotNumber: "MO-12", heldAt: "2026-11-12T09:00:00Z" }],
  openedBy: { userId: null, displayName: "Kari" },
  openedAt: "2026-11-12T08:00:00Z",
  closedBy: null,
  closedAt: null,
  closingNote: null,
  revision: 2,
  ...patch,
});

describe("a deviation (manufacturing phase 2 M99)", () => {
  it("lists its held lots and closes with a disposition and a note", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageQuality: true },
      deviations: [deviation()],
      write: (method, path) =>
        method === "POST" && path === "/deviations/7/close"
          ? jsonResponse(200, deviation({ status: "closed", disposition: "use_as_is", holdsReleased: true }))
          : undefined,
    });
    renderRoute("/manufacturing/deviations/7");
    expect(await screen.findByText("MO-12")).toBeInTheDocument();
    const close = screen.getByRole("button", { name: "Close the deviation" });
    expect(close).toBeDisabled();
    await userEvent.click(screen.getByRole("combobox", { name: /Disposition/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Use as is" }));
    await userEvent.type(screen.getByRole("textbox", { name: /Closing note/ }), "Sieved, fine.");
    await userEvent.click(close);
    await waitFor(() => expect(sent(fetchMock, "POST", /\/deviations\/7\/close$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/deviations\/7\/close$/)[0].body).toEqual({
      disposition: "use_as_is",
      closingNote: "Sieved, fine.",
      revision: 2,
    });
  });

  it("offers Release its holds once closed as scrap, and no close without quality-manage", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageQuality: true },
      deviations: [deviation({ status: "closed", disposition: "scrap", closedAt: "2026-11-12T10:00:00Z" })],
      write: (method, path) =>
        method === "POST" && path === "/deviations/7/release"
          ? jsonResponse(200, deviation({ holdsReleased: true }))
          : undefined,
    });
    renderRoute("/manufacturing/deviations/7");
    expect(await screen.findByText("Its holds are kept")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Release its holds" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/deviations\/7\/release$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/deviations\/7\/release$/)[0].body).toEqual({ revision: 2 });
  });

  it("shows no close form to a reader", async () => {
    stubManufacturingApi({ meta: {}, deviations: [deviation()] });
    renderRoute("/manufacturing/deviations/7");
    expect(await screen.findByText("MO-12")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Close the deviation" })).toBeNull();
  });

  it("lists the open deviations", async () => {
    stubManufacturingApi({ deviations: [deviation(), deviation({ id: 8, number: "DEV-8", status: "closed" })] });
    renderRoute("/manufacturing/deviations");
    expect(await screen.findByRole("link", { name: "DEV-7" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "DEV-8" })).toBeNull();
  });
});
