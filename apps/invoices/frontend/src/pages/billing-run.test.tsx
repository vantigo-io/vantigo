import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse, refusal } from "../test/api";
import { billingServer } from "../test/billing-server";
import { billingRun } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

describe("a billing run's page", () => {
  it("BillingRunPage_HeaderCountsAndItems", async () => {
    billingServer();
    renderRoute("/invoices/billing-runs/7");

    expect(await screen.findByRole("heading", { name: "Billing run 7" })).toBeInTheDocument();
    expect(await screen.findByText("Billing periods ending on or before Aug 31, 2026.")).toBeInTheDocument();
    expect(screen.getByText("Issues the invoices it makes.")).toBeInTheDocument();
    expect(screen.getByTestId("billing-run-status")).toHaveTextContent("Running");
    expect(screen.getByText(/^Created /)).toBeInTheDocument();
    expect(screen.getByText(/^Started /)).toBeInTheDocument();
    // The run's page counts every status, none included.
    const counts = screen.getByTestId("billing-run-counts");
    expect(within(counts).getByText("Drafted: 0")).toBeInTheDocument();
    expect(within(counts).getByText("Issued: 1")).toBeInTheDocument();

    const items = screen.getByTestId("billing-run-items");
    const acme = within(items).getByTestId("billing-run-item-71");
    expect(within(acme).getByText("Acme AS")).toBeInTheDocument();
    expect(within(acme).getByText("Issued")).toBeInTheDocument();
    expect(within(acme).getByRole("link", { name: "Invoice 1201" })).toHaveAttribute("href", "/invoices/1101");
    expect(within(acme).getByRole("link", { name: "Invoice 1202" })).toHaveAttribute("href", "/invoices/1102");

    const kari = within(items).getByTestId("billing-run-item-72");
    expect(within(kari).getByText("Skipped")).toBeInTheDocument();
    expect(within(kari).getByText("A settlement cannot be billed yet.")).toBeInTheDocument();
    expect(within(kari).getByText("Metering point 707057500000000017: spot prices are missing.")).toBeInTheDocument();

    // A customer the directory does not name is shown by its id; a draft kept after a refused issue links as Draft.
    const unnamed = within(items).getByTestId("billing-run-item-73");
    expect(within(unnamed).getByText("Customer 2099")).toBeInTheDocument();
    expect(within(unnamed).getByText("Failed")).toBeInTheDocument();
    expect(within(unnamed).getByRole("link", { name: "Draft" })).toHaveAttribute("href", "/invoices/1103");
    expect(
      within(unnamed).getByText("The document store was unavailable, so the draft was not issued."),
    ).toBeInTheDocument();

    expect(within(items).getByTestId("billing-run-item-74")).toHaveTextContent("Bygg AS");
  });

  it("puts an item code this version has no words for as the code itself", async () => {
    billingServer({
      answers: {
        "GET /api/v1/invoices/billing-runs/7": jsonResponse(200, {
          run: billingRun(),
          items: [{ id: 75, customerId: 2001, status: "failed", attempts: 1, invoices: [], code: "something_new" }],
          pagination: {
            page: 1,
            pageSize: 25,
            totalCount: 1,
            totalPages: 1,
            hasNextPage: false,
            hasPreviousPage: false,
          },
        }),
      },
    });
    renderRoute("/invoices/billing-runs/7");

    expect(await screen.findByText("Refused (something_new).")).toBeInTheDocument();
  });

  it("BillingRunPage_FiltersTheItemsByStatus", async () => {
    const fetchMock = billingServer();
    renderRoute("/invoices/billing-runs/7");

    expect(await screen.findByTestId("billing-run-item-71")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Skipped" }));

    await waitFor(() => expect(screen.queryByTestId("billing-run-item-71")).not.toBeInTheDocument());
    expect(screen.getByTestId("billing-run-item-72")).toBeInTheDocument();
    expect(fetchMock.actualCalls.map(([url]) => String(url))).toContain(
      "/api/v1/invoices/billing-runs/7?status=skipped&page=1",
    );

    await userEvent.click(screen.getByRole("tab", { name: "Cancelled" }));
    expect(await screen.findByText("No customers with this outcome")).toBeInTheDocument();
  });

  it("BillingRunPage_CancelConfirmsAndPosts", async () => {
    const fetchMock = billingServer({
      answers: {
        "POST /api/v1/invoices/billing-runs/7/cancel": jsonResponse(
          200,
          billingRun({ status: "cancelled", finishedAt: "2026-09-12T08:05:00Z" }),
        ),
      },
    });
    renderRoute("/invoices/billing-runs/7");

    await userEvent.click(await screen.findByRole("button", { name: "Cancel the run" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/The customers not billed yet are not billed by this run/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel the run" }));

    expect(await screen.findByText("The billing run is cancelled")).toBeInTheDocument();
    expect(
      fetchMock.actualCalls.some(
        ([url, init]) => String(url) === "/api/v1/invoices/billing-runs/7/cancel" && init?.method === "POST",
      ),
    ).toBe(true);
  });

  it("says a run that finished meanwhile in words", async () => {
    billingServer({
      answers: { "POST /api/v1/invoices/billing-runs/7/cancel": refusal(409, "billing_run_finished") },
    });
    renderRoute("/invoices/billing-runs/7");

    await userEvent.click(await screen.findByRole("button", { name: "Cancel the run" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel the run" }));

    expect(await screen.findByText("The billing run is finished or cancelled already.")).toBeInTheDocument();
  });

  it.each([
    ["done", { status: "done", finishedAt: "2026-09-12T08:05:00Z" }, {}],
    ["running, to a caller who may not create", {}, { canCreate: false }],
  ])("offers no Cancel on a run %s", async (_, run, capabilities) => {
    billingServer({ run: billingRun(run), capabilities });
    renderRoute("/invoices/billing-runs/7");

    expect(await screen.findByTestId("billing-run-items")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel the run" })).not.toBeInTheDocument();
  });
});
