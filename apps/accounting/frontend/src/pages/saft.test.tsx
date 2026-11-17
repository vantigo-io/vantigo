import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi } from "../test/server";

describe("SAF-T export", () => {
  it("warns of the accounts with no grouping code, asks for the year and lists the files", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/saft");
    expect(await screen.findByText("1 accounts have no grouping code for the year")).toBeInTheDocument();
    expect(screen.getByText(/7999 Egen konto/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the chart of accounts" })).toBeInTheDocument();
    expect(await screen.findByText(/SAF-T Financial_923609016_20261114090000_1_1\.xml/)).toBeInTheDocument();
    expect(screen.getByText("Kari Regnskap")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Export SAF-T" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/saft-exports$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/saft-exports$/)[0].body).toEqual({
      fiscalYear: 2026,
      fromPeriod: 1,
      toPeriod: 12,
    });
  });

  it("says who may export when the caller may not", async () => {
    stubAccountingApi({ meta: meta({ canExport: false }) });
    renderRoute("/accounting/saft");
    expect(await screen.findByText(/needs the permission Export the books/)).toBeInTheDocument();
  });
});

describe("Coming from another system", () => {
  it("uploads the file, shows its preview, links the customer picked in Customers and applies it", async () => {
    const fetchMock = stubAccountingApi({ meta: meta({ setUp: false, settings: null }) });
    renderRoute("/accounting/setup");
    await screen.findByText("Coming from another system?");
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["<AuditFile/>"], "gammel.xml", { type: "application/xml" }));
    await userEvent.click(screen.getByRole("button", { name: "Read the file" }));
    expect(await screen.findByText(/Fjord Bygg AS \(923609016\), SAF-T 1\.40/)).toBeInTheDocument();
    expect(screen.getByText("Not in Customers")).toBeInTheDocument();
    expect(screen.getByText(/2060 Åpningsbalanse, motkonto/)).toBeInTheDocument();
    await userEvent.click((await screen.findAllByLabelText("Customer in Customers for Local Retail Inc"))[0]);
    await userEvent.click(await screen.findByRole("option", { name: "10501 Acme AS" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/saft-imports\/8001\/customers\/1\/link$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/saft-imports\/8001\/customers\/1\/link$/)[0].body).toEqual({ customerId: 501 });
    await userEvent.click(screen.getByRole("button", { name: "Set up the books from the file" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/saft-imports\/8001\/apply$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/saft-imports\/8001\/apply$/)[0].body).toEqual({
      vatTerm: "bimonthly",
      language: "en",
      fiscalYearStartMonth: 1,
    });
  });

  it("takes a supplier of the register picked where several have the file's name", async () => {
    const fetchMock = stubAccountingApi({ meta: meta({ setUp: false, settings: null }) });
    renderRoute("/accounting/setup");
    await screen.findByText("Coming from another system?");
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["<AuditFile/>"], "gammel.xml", { type: "application/xml" }));
    await userEvent.click(screen.getByRole("button", { name: "Read the file" }));
    const table = await screen.findByTestId("saft-suppliers");
    expect(within(table).getByText("Several in the register")).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("combobox", { name: "Take Verksted AS as" }));
    await userEvent.click(await screen.findByRole("option", { name: "2 Verksted AS" }));
    expect(within(table).getByText("Picked")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Set up the books from the file" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/saft-imports\/8001\/apply$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/saft-imports\/8001\/apply$/)[0].body).toMatchObject({
      suppliers: { "20001": 3002 },
    });
  });
});
