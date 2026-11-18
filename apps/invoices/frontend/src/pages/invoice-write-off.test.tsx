import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceDocument } from "../api/invoices";
import { jsonResponse, refusal } from "../test/api";
import { requestTo, documentServer as server } from "../test/document-server";
import { partlyPaid } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** The invoice written off: 74.99 of it, the VAT taken back, 20.00 recovered since. */
const writtenOff = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument =>
  partlyPaid({
    state: "written_off",
    openAmount: 0,
    writtenOffAmount: 74.99,
    recoveredAmount: 20,
    writeOffs: [
      {
        id: 41,
        writtenOffOn: "2026-09-12",
        amount: 74.99,
        currency: "NOK",
        reason: "loss",
        vatCorrection: true,
        recoveredAmount: 20,
        source: "manual",
        registeredAt: "2026-09-12T09:00:00Z",
        registeredByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
      },
    ],
    ...overrides,
  });

describe("writing an invoice off", () => {
  it("writes the open amount off with the day, the reason and the VAT correction", async () => {
    const fetchMock = server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/write-off": () => jsonResponse(200, writtenOff()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Write off" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/The open amount of NOK\s?74\.99 is given up/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Take the VAT back/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Write off" }));

    await waitFor(() =>
      expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/write-off")).toEqual({
        writtenOffOn: "2026-09-12",
        reason: "loss",
        vatCorrection: true,
      }),
    );
    expect(await screen.findByText("Invoice written off")).toBeInTheDocument();
  });

  it("says a Vipps payment in progress in the reader's language", async () => {
    server(() => partlyPaid(), {
      "POST /api/v1/invoices/1001/write-off": () => refusal(409, "vipps_in_progress"),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Write off" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Write off" }));
    expect(await screen.findByText(/A Vipps payment of the invoice is being completed/)).toBeInTheDocument();
  });
});

describe("a written-off invoice", () => {
  it("shows the state, the write-off and what was recovered, and offers no ordinary payment", async () => {
    server(() => writtenOff());
    renderRoute("/invoices/1001");

    expect(
      within(await screen.findByRole("heading", { name: /^Invoice 1000/ })).getByText("Written off"),
    ).toBeInTheDocument();
    const standing = await screen.findByTestId("write-off-41");
    expect(standing).toHaveTextContent(
      /Written off on Sep 12, 2026: NOK\s?74\.99 \(Loss \(konstatert tap\)\) · VAT taken back/,
    );
    expect(standing).toHaveTextContent(/Recovered after the write-off: NOK\s?20\.00/);
    expect(screen.queryByRole("button", { name: "Register payment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Write off" })).not.toBeInTheDocument();
  });

  it("registers a recovered payment prefilled with what is left to recover", async () => {
    const fetchMock = server(() => writtenOff(), {
      "POST /api/v1/invoices/1001/payments": () => jsonResponse(200, writtenOff()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Register recovered payment" }));
    const dialog = await screen.findByRole("dialog", { name: "Register recovered payment" });
    expect(within(dialog).getByRole("textbox", { name: "Amount" })).toHaveValue("54.99");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    await waitFor(() =>
      expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/payments")).toEqual({
        paidOn: "2026-09-12",
        amount: 54.99,
        kind: "recovered",
      }),
    );
  });

  it("undoes the write-off with a reason, and says why it cannot while money is recovered", async () => {
    let refused = true;
    const fetchMock = server(() => writtenOff(), {
      "POST /api/v1/invoices/1001/write-off/undo": () =>
        refused ? refusal(409, "write_off_recovered") : jsonResponse(200, partlyPaid()),
    });
    renderRoute("/invoices/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Undo write-off" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Kunden betaler");
    await userEvent.click(within(dialog).getByRole("button", { name: "Undo write-off" }));
    expect(await screen.findByText(/Money was recovered after the write-off/)).toBeInTheDocument();
    refused = false;
    await userEvent.click(within(dialog).getByRole("button", { name: "Undo write-off" }));
    expect(await screen.findByText("Write-off undone")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/1001/write-off/undo")).toEqual({ reason: "Kunden betaler" });
  });

  it("strikes an undone write-off through with its reason", async () => {
    server(() =>
      partlyPaid({
        writeOffs: [
          {
            ...(writtenOff().writeOffs?.[0] as NonNullable<InvoiceDocument["writeOffs"]>[number]),
            recoveredAmount: 0,
            undoneAt: "2026-09-12T10:00:00Z",
            undoneByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
            undoReason: "Feil faktura",
          },
        ],
      }),
    );
    renderRoute("/invoices/1001");

    const undone = await screen.findByTestId("write-off-41");
    expect(undone).toHaveAttribute("data-undone", "true");
    expect(undone).toHaveTextContent("Undone: Feil faktura");
    expect(screen.getByRole("button", { name: "Write off" })).toBeInTheDocument();
  });
});

describe("a small difference rounded off", () => {
  it("names the automatic rounding write-off of a paid invoice", async () => {
    server(() =>
      partlyPaid({
        state: "paid",
        openAmount: 0,
        writtenOffAmount: 3,
        writeOffs: [
          {
            id: 42,
            writtenOffOn: "2026-09-12",
            amount: 3,
            currency: "NOK",
            reason: "rounding",
            vatCorrection: false,
            recoveredAmount: 0,
            source: "auto",
            registeredAt: "2026-09-12T09:00:00Z",
          },
        ],
      }),
    );
    renderRoute("/invoices/1001");

    expect(await screen.findByTestId("write-off-42")).toHaveTextContent(/Rounded off automatically/);
  });
});
