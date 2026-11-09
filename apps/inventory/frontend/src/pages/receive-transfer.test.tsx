import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import {
  inventoryProblem,
  packagings,
  stubInventoryApi,
  transferFixture,
  transferLine,
  variants,
} from "../test/server";

const [tsM, , bolt] = variants();

const inTransit = () =>
  transferFixture({
    id: 7,
    number: 1043,
    status: "sent",
    sentAt: "2026-11-08T13:02:00Z",
    sentBy: { displayName: "Kari N." },
    lines: [transferLine(1, tsM, 3, { sent: 3 }), transferLine(2, bolt, 12, { sent: 12 })],
  });
const scanField = () => screen.findByRole("textbox", { name: "Scan an item or the transfer note" });
const now = (line: string) => screen.getByRole("textbox", { name: `Now, ${line}` });
const receives = (fetchMock: Parameters<typeof sent>[0]) => sent(fetchMock, "POST", /\/transfers\/7\/receive$/);

describe("receiving a transfer (I40)", () => {
  it("counts one more per scan, receives at the site's root by default under one key, and opens the transfer", async () => {
    const fetchMock = stubInventoryApi({ transfers: [inTransit()] });
    const { router } = renderRoute("/inventory/transfers/7/receive");
    expect(await screen.findByRole("heading", { name: "Receive transfer 1043 from OSL" })).toBeInTheDocument();
    const field = await scanField();
    await scan(field, "TS-M");
    await scan(field, "7070000000101");
    await waitFor(() => expect(now("TS-M 1")).toHaveValue("2"));
    await scan(field, "TS-L");
    expect(
      await screen.findByText("“TS-L” is not on this transfer, or nothing of it is still in transit."),
    ).toBeInTheDocument();
    expect(screen.getByText("2 lines will still be in transit.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(1));
    expect(receives(fetchMock)[0].body).toEqual({
      clientRequestId: expect.stringMatching(/^[0-9a-f-]{36}$/),
      lines: [{ lineId: 1, toLocationId: 20, quantity: 2 }],
    });
    expect(receives(fetchMock)[0].ifMatch).toBeNull();
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/transfers/7"));
    const heading = await screen.findByRole("heading", { name: /Transfer 1043/ });
    expect(within(heading).getByText("Partly received")).toBeInTheDocument();
  });

  it("accepts everything at once", async () => {
    const fetchMock = stubInventoryApi({ transfers: [inTransit()] });
    renderRoute("/inventory/transfers/7/receive");
    await userEvent.click(await screen.findByRole("button", { name: "Accept all" }));
    expect(now("BOLT-10 2")).toHaveValue("12");
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(1));
    expect(receives(fetchMock)[0].body.lines).toEqual([
      { lineId: 1, toLocationId: 20, quantity: 3 },
      { lineId: 2, toLocationId: 20, quantity: 12 },
    ]);
  });

  it("after a refusal reloads the transfer, keeps the counts, and retries under the same key", async () => {
    let refuse = true;
    const fetchMock = stubInventoryApi({
      transfers: [inTransit()],
      write: (method, path) => {
        if (method !== "POST" || path !== "/transfers/7/receive" || !refuse) return undefined;
        refuse = false;
        return inventoryProblem(400, undefined, {
          lines: [{ index: 0, code: "transfer_over_received", detail: "Someone received part of it." }],
        });
      },
    });
    renderRoute("/inventory/transfers/7/receive");
    await scan(await scanField(), "BOLT-10");
    await waitFor(() => expect(now("BOLT-10 2")).toHaveValue("1"));
    const reads = sent(fetchMock, "GET", /\/transfers\/7$/).length;
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    expect(await screen.findByText("More than is still in transit on the line.")).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/transfers\/7$/).length).toBeGreaterThan(reads));
    expect(now("BOLT-10 2")).toHaveValue("1");
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(2));
    const [first, second] = receives(fetchMock);
    expect(second.body.clientRequestId).toBe(first.body.clientRequestId);
  });

  it("closes the difference with a reason and a note", async () => {
    const fetchMock = stubInventoryApi({ transfers: [inTransit()] });
    renderRoute("/inventory/transfers/7/receive");
    await userEvent.click(await screen.findByRole("button", { name: "Accept all" }));
    await userEvent.clear(now("BOLT-10 2"));
    await userEvent.type(now("BOLT-10 2"), "10");
    await userEvent.click(screen.getByRole("button", { name: "Receive and close the difference…" }));
    const dialog = await screen.findByRole("dialog", { name: "Close the difference" });
    expect(within(dialog).getByText("2 pcs not received")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Close transfer 1043" }));
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("radio", { name: "Damaged" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note (optional)" }), "Box arrived opened");
    await userEvent.click(within(dialog).getByRole("button", { name: "Close transfer 1043" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(1));
    expect(receives(fetchMock)[0].body).toEqual({
      clientRequestId: expect.stringMatching(/^[0-9a-f-]{36}$/),
      lines: [
        { lineId: 1, toLocationId: 20, quantity: 3 },
        { lineId: 2, toLocationId: 20, quantity: 10 },
      ],
      close: { reason: "damaged", note: "Box arrived opened" },
    });
    const heading = await screen.findByRole("heading", { name: /Transfer 1043/ });
    expect(await within(heading).findByText("Received")).toBeInTheDocument();
  });

  it("opens another transfer's receive page when its note is scanned", async () => {
    const other = transferFixture({
      id: 8,
      number: 1050,
      status: "sent",
      lines: [transferLine(9, tsM, 1, { sent: 1 })],
    });
    stubInventoryApi({ transfers: [inTransit(), other] });
    const { router } = renderRoute("/inventory/transfers/7/receive");
    await scan(await scanField(), "TRF-1050");
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/transfers/8/receive"));
    expect(await screen.findByRole("heading", { name: "Receive transfer 1050 from OSL" })).toBeInTheDocument();
  });

  it("says a transfer that is not in transit cannot be received", async () => {
    stubInventoryApi({ transfers: [transferFixture({ id: 7, lines: [transferLine(1, tsM, 3)] })] });
    renderRoute("/inventory/transfers/7/receive");
    expect(
      await screen.findByText("The transfer cannot be received: it is a draft, received or cancelled."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Scan an item or the transfer note" })).not.toBeInTheDocument();
  });

  it("regenerates its key when an earlier receive from the page already landed", async () => {
    let reused = true;
    const fetchMock = stubInventoryApi({
      transfers: [inTransit()],
      write: (method, path) => {
        if (method !== "POST" || path !== "/transfers/7/receive" || !reused) return undefined;
        reused = false;
        return inventoryProblem(409, "client_request_reused", { title: "Reused" });
      },
    });
    renderRoute("/inventory/transfers/7/receive");
    await scan(await scanField(), "TS-M");
    await waitFor(() => expect(now("TS-M 1")).toHaveValue("1"));
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    expect(await screen.findByText(/An earlier receive from this page was already posted/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(2));
    const [first, second] = receives(fetchMock);
    expect(second.body.clientRequestId).not.toBe(first.body.clientRequestId);
  });

  it("counts one box for a scan of the box's barcode", async () => {
    const boxed = { ...bolt, packagings: packagings() };
    const fetchMock = stubInventoryApi({
      variants: [tsM, boxed],
      transfers: [transferFixture({ id: 7, status: "sent", lines: [transferLine(2, boxed, 30, { sent: 30 })] })],
    });
    renderRoute("/inventory/transfers/7/receive");
    await scan(await scanField(), "27070000000201");
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Now Box, BOLT-10 2" })).toHaveValue("1"));
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(1));
    expect(receives(fetchMock)[0].body.lines).toEqual([
      { lineId: 2, toLocationId: 20, entered: { packagings: [{ packagingId: 72, count: 1 }] } },
    ]);
  });

  it("asks before leaving with counts not received", async () => {
    stubInventoryApi({ transfers: [inTransit()] });
    const { router } = renderRoute("/inventory/transfers/7/receive");
    await scan(await scanField(), "TS-M");
    await waitFor(() => expect(now("TS-M 1")).toHaveValue("1"));
    await userEvent.click(screen.getByRole("link", { name: "Back" }));
    const ask = await screen.findByRole("dialog", { name: "Leave without receiving?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Keep receiving" }));
    expect(router.state.location.pathname).toBe("/inventory/transfers/7/receive");
    expect(now("TS-M 1")).toHaveValue("1");
    await userEvent.click(screen.getByRole("link", { name: "Back" }));
    const again = await screen.findByRole("dialog", { name: "Leave without receiving?" });
    await userEvent.click(within(again).getByRole("button", { name: "Leave without receiving" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/transfers/7"));
  });

  it("after no answer sends the same counts again for the server to replay", async () => {
    let fail = true;
    const fetchMock = stubInventoryApi({
      transfers: [inTransit()],
      write: (method, path) => {
        if (method !== "POST" || path !== "/transfers/7/receive" || !fail) return undefined;
        fail = false;
        return new Response(null, { status: 502 });
      },
    });
    renderRoute("/inventory/transfers/7/receive");
    await scan(await scanField(), "TS-M");
    await waitFor(() => expect(now("TS-M 1")).toHaveValue("1"));
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    expect(await screen.findByText(/the receive may or may not have been posted/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(receives(fetchMock)).toHaveLength(2));
    const [first, second] = receives(fetchMock);
    expect(second.body).toEqual(first.body);
  });
});
