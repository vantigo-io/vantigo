import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import type { WorkView } from "../api/work";
import { jsonResponse, path } from "../test/api";
import { stubFetch } from "../test/fetch";
import { meta, workView } from "../test/fixtures";
import { renderAtHost } from "../test/route-tree";
import { UninvoicedWorkPanel } from "./uninvoiced-work-panel";

/** The fake: meta (with `workAvailable` as asked), the view for customer 2001 and for project 41. */
const server = (view: WorkView = workView(), options: { workAvailable?: boolean } = {}) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = path(input);
    if (url === "/api/v1/invoices/meta")
      return jsonResponse(200, meta({ workAvailable: options.workAvailable ?? true }));
    if (url === "/api/v1/invoices/work?customerId=2001" || url === "/api/v1/invoices/work?projectId=41") {
      return jsonResponse(200, view);
    }
    return new Response(null, { status: 404 });
  });

const table = (name: string) => screen.findByRole("table", { name });

describe("the uninvoiced work panel", () => {
  it("lists the work per project and kind, greying what cannot be chosen with its reason", async () => {
    server();
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);

    const hours = await table("Hours on P-41");
    expect(await table("Expenses on P-41")).toBeInTheDocument();
    expect(await table("Milestones on P-41")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "P-41 · Apollo" })).toBeInTheDocument();

    // Kari's hours can be chosen; Ola's are on draft 1001, a link to it.
    expect(within(hours).getByRole("checkbox", { name: "Choose Kari Nordmann, Sep 1, 2026" })).toBeEnabled();
    expect(within(hours).getByRole("checkbox", { name: "Choose Ola Hansen, Sep 2, 2026" })).toBeDisabled();
    expect(within(hours).getByRole("link", { name: "On draft 1001" })).toHaveAttribute("href", "/invoices/1001");

    // A fixed-price project's hours are shown as information, never chosen.
    const fixed = await table("Hours on P-42");
    expect(within(fixed).getByRole("checkbox", { name: "Choose Kari Nordmann, Jul 3, 2026" })).toBeDisabled();
    expect(within(fixed).getByText("Fixed-price project: the hours are shown, not invoiced")).toBeInTheDocument();
    expect(screen.getByText("Fixed price")).toBeInTheDocument();

    // The view's own total is over the selectable work.
    expect(screen.getByTestId("work-totals")).toHaveTextContent("Ready to invoice: NOK 15,205.00");
  });

  it("names the invoice that holds issued work, and says the other reasons in words", async () => {
    const view = workView();
    const [apollo] = view.projects;
    apollo.hours[1] = { ...apollo.hours[1], heldBy: { invoiceId: 990, number: 985, status: "issued" } };
    apollo.expenses[0] = { ...apollo.expenses[0], selectable: false, reason: "currency", currency: "EUR" };
    apollo.milestones[0] = { ...apollo.milestones[0], selectable: false, reason: "non_billable" };
    server(view);
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);

    expect(await screen.findByRole("link", { name: "On invoice 985" })).toHaveAttribute("href", "/invoices/990");
    expect(within(await table("Expenses on P-41")).getByText("Not in NOK")).toBeInTheDocument();
    expect(within(await table("Milestones on P-41")).getByText("The project is not billable")).toBeInTheDocument();
  });

  it("says the view's warnings where they belong: the answer, the project and the row", async () => {
    const view = workView({ warnings: ["work_truncated"] });
    const [apollo] = view.projects;
    apollo.warnings = ["work_overdue_to_invoice"];
    apollo.expenses[0] = { ...apollo.expenses[0], warnings: ["supplier_invoice_rebilled"] };
    server(view);
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);

    expect(await screen.findByText(/Not all the work is listed/)).toBeInTheDocument();
    expect(
      within(screen.getByTestId("work-project-41")).getByText(/delivered more than a month ago/),
    ).toBeInTheDocument();
    expect(
      within(await table("Expenses on P-41")).getByText(/A supplier invoice with this supplier and number/),
    ).toBeInTheDocument();
  });

  it("totals the chosen work per currency and offers the wizard only once something is chosen", async () => {
    server();
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);

    const invoice = await screen.findByRole("button", { name: "Invoice the chosen work" });
    expect(invoice).toBeDisabled();
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose Kari Nordmann, Sep 1, 2026" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose Fase 1" }));
    expect(screen.getByTestId("work-chosen")).toHaveTextContent("2 chosen: NOK 14,800.00");
    expect(invoice).toBeEnabled();

    // Choosing all of a kind takes only what can be chosen.
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose all Expenses on P-41" }));
    expect(screen.getByTestId("work-chosen")).toHaveTextContent("3 chosen: NOK 15,205.00");

    await userEvent.click(invoice);
    expect(await screen.findByRole("dialog", { name: "Invoice the work" })).toBeInTheDocument();
  });

  it("lists one project's work for the project's Invoicing tab", async () => {
    const fetchMock = server(workView({ projects: workView().projects.slice(0, 1) }));
    renderAtHost(<UninvoicedWorkPanel projectId={41} />);

    expect(await table("Hours on P-41")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/invoices/work?projectId=41")).toBe(true);
  });

  it("says a project that bills no customer cannot be invoiced from here, and offers no wizard", async () => {
    const view = workView({ projects: workView().projects.slice(0, 1) });
    delete view.customerId;
    server(view);
    renderAtHost(<UninvoicedWorkPanel projectId={41} />);

    expect(await screen.findByText(/This project bills no customer/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Invoice the chosen work" })).not.toBeInTheDocument();
  });

  it("shows nothing on a customer when no work can be invoiced here, and says why on a project", async () => {
    const fetchMock = server(workView(), { workAvailable: false });
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);
    await waitFor(() => expect(fetchMock.actualCalls.length).toBeGreaterThan(0));
    await waitFor(() => expect(screen.queryByTestId("uninvoiced-work")).not.toBeInTheDocument());
    expect(fetchMock.actualCalls.some(([url]) => path(url).startsWith("/api/v1/invoices/work"))).toBe(false);
  });

  it("says on a project's tab that no module records billable work", async () => {
    server(workView(), { workAvailable: false });
    renderAtHost(<UninvoicedWorkPanel projectId={41} />);
    expect(await screen.findByText(/No module that records billable work/)).toBeInTheDocument();
  });

  it("says there is nothing to invoice when the view is empty", async () => {
    server(workView({ projects: [], totals: [] }));
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);
    expect(await screen.findByText("Nothing to invoice")).toBeInTheDocument();
  });

  it("words the panel from the nb catalog", async () => {
    setLanguagePreference("nb");
    try {
      server();
      renderAtHost(<UninvoicedWorkPanel customerId={2001} />);
      expect(await screen.findByText("Ufakturert arbeid")).toBeInTheDocument();
      expect(await screen.findByRole("table", { name: "Timer på P-41" })).toBeInTheDocument();
      expect(screen.getByRole("link", { name: "På utkast 1001" })).toBeInTheDocument();
      expect(screen.getByText("Fastprisprosjekt: timene vises, men faktureres ikke")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Fakturer det valgte arbeidet" })).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});
