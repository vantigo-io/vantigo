import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi, vatTermDetail, vatTermItem } from "../test/server";

describe("the VAT terms", () => {
  it("lists the terms with their state, due date and total, and says filing is at Skatteetaten", async () => {
    stubAccountingApi({ vatTerms: [vatTermItem({ number: 3, state: "closed", needsClosing: false }), vatTermItem()] });
    renderRoute("/accounting/vat");
    expect(await screen.findByRole("link", { name: "Term 4 2026 · juli-august" })).toHaveAttribute(
      "href",
      "/accounting/vat/2026/4",
    );
    expect(screen.getByText("Filing is done at Skatteetaten")).toBeInTheDocument();
    expect(screen.getAllByText("Ready to close")).toHaveLength(1);
    expect(screen.getAllByText("Past due")).toHaveLength(2);
    expect(screen.getByText("Closed")).toBeInTheDocument();
  });

  it("explains, and shows no terms, when the company is not VAT-registered", async () => {
    const m = meta();
    stubAccountingApi({ meta: meta({ settings: m.settings && { ...m.settings, vatTerm: "none" } }) });
    renderRoute("/accounting/vat");
    expect(await screen.findByText("The company is not VAT-registered")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("a VAT term", () => {
  it("shows the return as figures with its drill-down, and closes after the dialog says what will happen", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/vat/2026/4");
    const ret = await screen.findByTestId("vat-return");
    expect(within(ret).getByText("Sales of goods and services in Norway")).toBeInTheDocument();
    expect(within(ret).getByText("25 %")).toBeInTheDocument();
    expect(within(ret).getByText(/the difference 0.67 is posted to the rounding account/)).toBeInTheDocument();
    const spec = screen.getByTestId("vat-specification");
    expect(within(spec).getByRole("link", { name: "60,000.40" })).toHaveAttribute(
      "href",
      "/accounting/vat/postings?from=2026-07-01&to=2026-08-31&taxCodeId=2003&part=base",
    );
    await userEvent.click(screen.getByRole("button", { name: "Close the term" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/locks the books through/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Close the term" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/vat\/terms\/2026\/4\/close$/)).toHaveLength(1));
  });

  it("explains each refusal the close would meet, and does not offer it", async () => {
    stubAccountingApi({
      vatTerm: vatTermDetail({
        checks: {
          ended: true,
          attention: 2,
          lockAfterEnd: true,
          reconciled: false,
          earlierOpen: vatTermItem({ number: 3, periodName: "mai-juni" }),
        },
      }),
    });
    renderRoute("/accounting/vat/2026/4");
    await userEvent.click(await screen.findByRole("button", { name: "Close the term" }));
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText("Close Term 3 2026 · mai-juni first: terms are closed in order."),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(/2 facts dated in the term wait under Needs attention/)).toBeInTheDocument();
    expect(within(dialog).getByText(/move the lock back first/)).toBeInTheDocument();
    expect(within(dialog).getByText(/do not reconcile with the return/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Close the term" })).toBeDisabled();
  });

  it("downloads a closed term's XML and marks it as filed with the day and the reference", async () => {
    const fetchMock = stubAccountingApi({
      vatTerm: vatTermDetail({ term: vatTermItem({ state: "closed", needsClosing: false }) }),
    });
    renderRoute("/accounting/vat/2026/4");
    expect(await screen.findByRole("link", { name: "Download XML" })).toHaveAttribute(
      "href",
      "/api/v1/accounting/vat/terms/2026/4/xml",
    );
    expect(screen.queryByRole("button", { name: "Close the term" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Mark as filed" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/Skatteetaten's reference/), "AR-1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Mark as filed" }));
    await waitFor(() =>
      expect(sent(fetchMock, "POST", /\/vat\/terms\/2026\/4\/filing$/)[0]?.body).toEqual({
        filedAt: "2026-11-14",
        reference: "AR-1",
      }),
    );
  });

  it("reopens with a reason, and offers neither reopening nor unmarking to someone who cannot close", async () => {
    const fetchMock = stubAccountingApi({
      vatTerm: vatTermDetail({ term: vatTermItem({ state: "closed", needsClosing: false }) }),
    });
    renderRoute("/accounting/vat/2026/4");
    await userEvent.click(await screen.findByRole("button", { name: "Reopen" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/Reason/), "A purchase is missing");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reopen" }));
    await waitFor(() =>
      expect(sent(fetchMock, "POST", /\/reopen$/)[0]?.body).toEqual({ reason: "A purchase is missing" }),
    );
  });

  it("lists the postings behind a figure", async () => {
    stubAccountingApi();
    renderRoute("/accounting/vat/postings?from=2026-07-01&to=2026-08-31&taxCodeId=2003&part=base");
    expect(await screen.findByRole("link", { name: "2026-000007" })).toHaveAttribute(
      "href",
      "/accounting/vouchers/5001",
    );
    expect(screen.getByText("1 postings")).toBeInTheDocument();
  });

  it("shows the next term due on the overview", async () => {
    stubAccountingApi({ meta: meta({ vatNextTerm: vatTermItem(), vatTermsToClose: 2, vatTermsToFile: 1 }) });
    renderRoute("/accounting");
    const card = await screen.findByTestId("vat-card");
    expect(within(card).getByRole("link", { name: /Term 4 2026 · juli-august: Open/ })).toHaveAttribute(
      "href",
      "/accounting/vat/2026/4",
    );
    expect(within(card).getByText("2 terms are ready to close.")).toBeInTheDocument();
  });
});
