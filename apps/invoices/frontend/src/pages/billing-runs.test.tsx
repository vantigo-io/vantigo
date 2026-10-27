import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, refusal, sent } from "../test/api";
import { billingServer } from "../test/billing-server";
import { renderRoute } from "../test/route-tree";

const openModal = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "New billing run" }));
  return screen.findByRole("dialog");
};

describe("Billing runs", () => {
  it("BillingRuns_ListsRunsWithTheirCounts", async () => {
    billingServer();
    renderRoute("/invoices/billing-runs");

    expect(await screen.findByRole("heading", { name: "Billing runs" })).toBeInTheDocument();
    const running = await screen.findByTestId("billing-run-7");
    expect(within(running).getByRole("link", { name: "Billing run 7" })).toHaveAttribute(
      "href",
      "/invoices/billing-runs/7",
    );
    expect(within(running).getByText("Aug 31, 2026")).toBeInTheDocument();
    expect(within(running).getByText("Issue")).toBeInTheDocument();
    expect(within(running).getByText("Running")).toBeInTheDocument();
    const counts = within(running).getByTestId("billing-run-counts");
    expect(within(counts).getByText("Queued: 1")).toBeInTheDocument();
    expect(within(counts).getByText("Issued: 1")).toBeInTheDocument();
    expect(within(counts).getByText("Skipped: 1")).toBeInTheDocument();
    expect(within(counts).getByText("Failed: 1")).toBeInTheDocument();
    // A status with no items is not counted on the list.
    expect(within(counts).queryByText(/^Drafted/)).not.toBeInTheDocument();

    const done = screen.getByTestId("billing-run-6");
    expect(within(done).getByText("Draft only")).toBeInTheDocument();
    expect(within(done).getByText("Done")).toBeInTheDocument();
    expect(within(done).getByText("Drafted: 2")).toBeInTheDocument();
  });

  it("hides New billing run from a caller who may not create", async () => {
    billingServer({ capabilities: { canCreate: false } });
    renderRoute("/invoices/billing-runs");

    expect(await screen.findByTestId("billing-run-7")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New billing run" })).not.toBeInTheDocument();
  });

  it("NewBillingRun_PostsTheBodyAndOpensTheRun", async () => {
    const fetchMock = billingServer();
    const { router } = renderRoute("/invoices/billing-runs");

    const dialog = await openModal();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Issue the invoices" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Customer ids" }), "2001, 2002 2003");
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/billing-runs/8"));
    expect(sent(fetchMock, "POST")).toEqual({
      url: "/api/v1/invoices/billing-runs",
      body: { issue: true, through: "2026-09-12", customerIds: [2001, 2002, 2003] },
    });
  });

  it("leaves the customers out to bill every customer, and does not issue unless asked", async () => {
    const fetchMock = billingServer();
    renderRoute("/invoices/billing-runs");

    const dialog = await openModal();
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toEqual({ issue: false, through: "2026-09-12" }));
  });

  it("NewBillingRun_IssueHiddenWithoutIssuePermission", async () => {
    const fetchMock = billingServer({ capabilities: { canIssue: false } });
    renderRoute("/invoices/billing-runs");

    const dialog = await openModal();
    expect(within(dialog).queryByRole("checkbox", { name: "Issue the invoices" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toEqual({ issue: false, through: "2026-09-12" }));
  });

  it("NewBillingRun_ActiveRunInWordsWithItsLink", async () => {
    billingServer({
      answers: { "POST /api/v1/invoices/billing-runs": refusal(409, "billing_run_active", { runId: 7 }) },
    });
    renderRoute("/invoices/billing-runs");

    const dialog = await openModal();
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));

    expect(
      await within(dialog).findByText(
        "A billing run is queued or running already. Wait for it to finish, or cancel it.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Open the run in progress" })).toHaveAttribute(
      "href",
      "/invoices/billing-runs/7",
    );
    expect(within(dialog).queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it.each([
    ["consumption_unavailable", "The Energy module is switched off, so no consumption can be invoiced."],
    ["too_many_customers", "More than 5 000 supplies are due. Name the customers to bill."],
  ])("says the %s refusal in words", async (code, words) => {
    billingServer({ answers: { "POST /api/v1/invoices/billing-runs": refusal(409, code) } });
    renderRoute("/invoices/billing-runs");

    const dialog = await openModal();
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));

    expect(await within(dialog).findByText(words)).toBeInTheDocument();
    expect(within(dialog).queryByRole("link", { name: "Open the run in progress" })).not.toBeInTheDocument();
  });

  it("shows a customer id the server refused on the input, and a malformed one before sending", async () => {
    const fetchMock = billingServer({
      answers: {
        "POST /api/v1/invoices/billing-runs": problemResponse(400, "Validation failed", {
          "customerIds[1]": ["unknown customer"],
        }),
      },
    });
    renderRoute("/invoices/billing-runs");

    const dialog = await openModal();
    const ids = within(dialog).getByRole("textbox", { name: "Customer ids" });
    await userEvent.type(ids, "2001, Acme");
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));
    expect(
      await within(dialog).findByText("Write the customer ids as whole numbers, separated by commas or spaces."),
    ).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);

    await userEvent.clear(ids);
    await userEvent.type(ids, "2001, 9999");
    await userEvent.click(within(dialog).getByRole("button", { name: "Start the run" }));
    expect(
      await within(dialog).findByText(
        "Name between 1 and 5 000 customers, each once, by an id the customer register knows.",
      ),
    ).toBeInTheDocument();
  });
});
