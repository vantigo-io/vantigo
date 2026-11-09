import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

const [tsM, , bolt] = variants();

/** Catches the følgeseddel handed to the browser: jsdom makes no object URLs. */
const catchDownloads = () => {
  const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:note");
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
  return createObjectURL;
};

const draft = () =>
  transferFixture({ id: 5, number: 1042, revision: 2, lines: [transferLine(1, tsM, 6), transferLine(2, bolt, 2)] });

describe("sending a transfer (I39)", () => {
  it("defaults each line to the location holding most, warns of reserved stock, and sends under the revision", async () => {
    const downloads = catchDownloads();
    const fetchMock = stubInventoryApi({
      transfers: [draft()],
      balances: [
        { variantId: 101, locationId: 12, onHand: 2 },
        { variantId: 101, locationId: 13, onHand: 6 },
        { variantId: 201, locationId: 12, onHand: 9 },
      ],
      reservations: [{ variantId: 101, siteId: 1, quantity: 3 }],
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send…" }));
    const dialog = await screen.findByRole("dialog", { name: "Send transfer 1042 to BGO" });
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Source location, line 1" })).toHaveValue("OSL-A-02 · 6"),
    );
    expect(within(dialog).getByRole("combobox", { name: "Source location, line 2" })).toHaveValue("OSL-A-01 · 9");
    expect(
      within(dialog).getByText("3 pcs of TS-M are reserved for orders at OSL; sending leaves them short."),
    ).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: "Send 2 lines" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers\/5\/send$/)).toHaveLength(1));
    const [send] = sent(fetchMock, "POST", /\/transfers\/5\/send$/);
    expect(send.ifMatch).toBe("2");
    expect(send.body).toEqual({
      lines: [
        { lineId: 1, fromLocationId: 13 },
        { lineId: 2, fromLocationId: 12 },
      ],
    });
    // The følgeseddel is printed once it is sent, as ticked.
    await waitFor(() => expect(downloads).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const heading = await screen.findByRole("heading", { name: /Transfer 1042/ });
    expect(await within(heading).findByText("Sent")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Receive…" })).toHaveAttribute("href", "/inventory/transfers/5/receive");
  });

  it("marks a line the server refuses, and sends nothing without a source", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [draft()],
      balances: [{ variantId: 101, locationId: 12, onHand: 9 }],
      write: (method, path) =>
        method === "POST" && path === "/transfers/5/send"
          ? inventoryProblem(409, "insufficient_stock", {
              lines: [{ index: 0, code: "insufficient_stock", detail: "TS-M: 9 at OSL-A-01." }],
            })
          : undefined,
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send…" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Source location, line 1" })).toHaveValue("OSL-A-01 · 9"),
    );
    // Nothing of the bolt lies at Oslo, so its line has no source.
    await userEvent.click(within(dialog).getByRole("button", { name: "Send 2 lines" }));
    expect(await within(dialog).findByText("Choose the location the line is sent from.")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/send$/)).toHaveLength(0);
  });

  it("sends and receives, asking a destination for every line", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [draft()],
      balances: [
        { variantId: 101, locationId: 12, onHand: 9 },
        { variantId: 201, locationId: 12, onHand: 9 },
      ],
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send and receive…" }));
    const dialog = await screen.findByRole("dialog", { name: "Send and receive transfer 1042 at BGO" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Send and receive 2 lines" }));
    expect(await within(dialog).findAllByText("Pick where the line is received.")).toHaveLength(2);
    await choose(dialog, "Receive every line at a location of BGO", "BGO-SALG · Butikkgulv");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send and receive 2 lines" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/send$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/send$/)[0].body).toEqual({
      lines: [
        { lineId: 1, fromLocationId: 12 },
        { lineId: 2, fromLocationId: 12 },
      ],
      receive: {
        lines: [
          { lineId: 1, toLocationId: 21 },
          { lineId: 2, toLocationId: 21 },
        ],
      },
    });
    const heading = await screen.findByRole("heading", { name: /Transfer 1042/ });
    expect(await within(heading).findByText("Received")).toBeInTheDocument();
  });

  it("closes on the transfer as it is now when its revision moved on", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [draft()],
      balances: [
        { variantId: 101, locationId: 12, onHand: 9 },
        { variantId: 201, locationId: 12, onHand: 9 },
      ],
      write: (method, path) =>
        method === "POST" && path === "/transfers/5/send"
          ? inventoryProblem(409, "revision_conflict", { title: "Revision conflict" })
          : undefined,
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send…" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Source location, line 2" })).toHaveValue("OSL-A-01 · 9"),
    );
    const reads = sent(fetchMock, "GET", /\/transfers\/5$/).length;
    await userEvent.click(within(dialog).getByRole("button", { name: "Send 2 lines" }));
    expect(await screen.findByText("The transfer was changed by someone else")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sent(fetchMock, "GET", /\/transfers\/5$/).length).toBeGreaterThan(reads);
  });

  it("marks the line a send and receive was refused for", async () => {
    stubInventoryApi({
      transfers: [draft()],
      balances: [
        { variantId: 101, locationId: 12, onHand: 9 },
        { variantId: 201, locationId: 12, onHand: 9 },
      ],
      write: (method, path) =>
        method === "POST" && path === "/transfers/5/send"
          ? inventoryProblem(400, undefined, {
              lines: [{ index: 1, code: "location_archived", detail: "BGO-SALG is archived." }],
            })
          : undefined,
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send and receive…" }));
    const dialog = await screen.findByRole("dialog");
    await choose(dialog, "Receive every line at a location of BGO", "BGO-SALG · Butikkgulv");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send and receive 2 lines" }));
    expect(await within(dialog).findByText("The location is archived.")).toBeInTheDocument();
    const rows = within(dialog).getAllByRole("row");
    expect(rows[1]).not.toHaveAttribute("data-refused");
    expect(rows[2]).toHaveAttribute("data-refused", "true");
    expect(within(dialog).getByText(/1 line was refused/)).toBeInTheDocument();
  });
});
