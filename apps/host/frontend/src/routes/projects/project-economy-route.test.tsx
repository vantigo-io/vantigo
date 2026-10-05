import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sessionQueryKey } from "../../api/auth";
import { ProjectEconomyRoute } from "./-project-economy-route";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useParams: () => ({ projectId: 31 }) };
});

vi.mock("@vantigo/projects-ui/pages/project-economy", () => ({
  ProjectEconomy: ({
    projectId,
    expensesHref,
    invoicingHref,
    invoiceHref,
  }: {
    projectId: number;
    expensesHref?: string;
    invoicingHref?: string;
    invoiceHref?: (invoiceId: number) => string;
  }) => (
    <div>
      <p>
        economy for project {projectId} → {expensesHref ?? "no expenses link"}
      </p>
      <p>invoicing → {invoicingHref ?? "no invoicing link"}</p>
      <p>invoice 990 → {invoiceHref?.(990) ?? "no invoice link"}</p>
    </div>
  ),
}));

const renderRoute = (modules: string[], permissions: string[] = ["expenses:access"]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // What the layout above already put there. The link is decided from the same
  // three answers the tab row is, so it cannot disagree with it.
  queryClient.setQueryData(sessionQueryKey, { user: { id: "user-1", roles: [] } });
  queryClient.setQueryData(["authorization", "me", "none"], { permissions });
  queryClient.setQueryData(["projects", "detail", 31], {
    id: 31,
    capabilities: { canManage: true, canContribute: true, canSeeFinancials: true },
  });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectEconomyRoute />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project page's economy route", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  // The projects package knows no route of the Expenses app and imports
  // nothing from it, so the host is the one that can say where the expenses
  // behind "ready to invoice" live.
  it("points the ready-to-invoice row at the project's own Expenses tab", () => {
    renderRoute(["projects", "expenses"]);

    expect(screen.getByText(/economy for project 31 → \/projects\/31\/expenses/)).toBeInTheDocument();
  });

  it("offers no link at all when the installation did not mount expenses", () => {
    renderRoute(["projects"]);

    expect(screen.getByText(/no expenses link/)).toBeInTheDocument();
  });

  // Economy needs only `projects:*`, so a manager with financial rights and no
  // Expenses app is an ordinary combination — and every operation of that API
  // demands `expenses:access`. A link for them would land on a page of
  // refusals under a tab strip the tab is not even in.
  // The Invoicing tab is the invoices module's, behind invoices:create (D18):
  // the link to it follows the tab, from the same function.
  it("points the invoice plan at the project's Invoicing tab when that tab is open to the caller", () => {
    renderRoute(["projects", "invoices"], ["invoices:access", "invoices:create"]);
    expect(screen.getByText("invoicing → /projects/31/invoicing")).toBeInTheDocument();
  });

  it("offers no invoicing link without invoices:create or without the module", () => {
    renderRoute(["projects", "invoices"], ["invoices:access"]);
    expect(screen.getByText("invoicing → no invoicing link")).toBeInTheDocument();
    cleanup();
    renderRoute(["projects", "invoices"], ["invoices:create"]);
    expect(screen.getByText("invoicing → no invoicing link")).toBeInTheDocument();
    cleanup();
    renderRoute(["projects"], ["invoices:access", "invoices:create"]);
    expect(screen.getByText("invoicing → no invoicing link")).toBeInTheDocument();
  });

  // "Invoiced by invoice n" links to the invoice for whoever may read invoices.
  it("says where an invoice lives to a caller who may read invoices, and to nobody else", () => {
    renderRoute(["projects", "invoices"], ["invoices:access"]);
    expect(screen.getByText("invoice 990 → /invoices/990")).toBeInTheDocument();
    cleanup();
    renderRoute(["projects", "invoices"], ["projects:access"]);
    expect(screen.getByText("invoice 990 → no invoice link")).toBeInTheDocument();
    cleanup();
    renderRoute(["projects"], ["invoices:access"]);
    expect(screen.getByText("invoice 990 → no invoice link")).toBeInTheDocument();
  });

  it("offers no link to a caller who may not open the Expenses tab", () => {
    renderRoute(["projects", "expenses"], ["projects:access"]);

    expect(screen.getByText(/no expenses link/)).toBeInTheDocument();
  });
});
