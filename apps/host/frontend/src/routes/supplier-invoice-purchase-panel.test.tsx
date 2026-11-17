import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  type RouteComponent,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch, TEST_SESSION } from "../test/fetch";
import { Route as SupplierInvoiceRoute } from "./accounting/supplier-invoices.$supplierInvoiceId";
import "../i18n";

// Accounting's page is its package's own, tested there; here it only draws
// what the host route hands its slot, for invoice 7001.
vi.mock("@vantigo/accounting-ui/pages/supplier-invoices", () => ({
  SupplierInvoicePage: ({ extraPanels }: { extraPanels?: (invoiceId: number) => ReactNode }) => (
    <section aria-label="Supplier invoice">{extraPanels?.(7001)}</section>
  ),
}));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/** Purchasing's panel answer: the lines' words only for accounting:read, as the server answers them (A20). */
const panel = (linesVisible: boolean) => ({
  invoiceId: 7001,
  supplierId: 40,
  supplierName: "Skruer AS",
  kind: "invoice",
  number: "F-100",
  invoiceDate: "2026-11-15",
  currency: "NOK",
  state: "posted",
  reference: linesVisible ? "PO 3001" : null,
  linesVisible,
  lines: [linesVisible ? { position: 1, text: "Skruer M8", net: "182.00" } : { position: 1 }],
  matches: [
    {
      id: 9001,
      supplierInvoiceId: 7001,
      invoiceNumber: "F-100",
      invoiceDate: "2026-11-15",
      currency: "NOK",
      kind: "invoice",
      state: "posted",
      linePosition: 1,
      orderId: 501,
      orderNumber: 3001,
      orderLineId: 11,
      orderLinePosition: 1,
      name: "Skrue 10 mm",
      unit: "stk",
      quantity: "400",
      orderUnitNet: "0.4550",
      invoiceUnitNet: linesVisible ? "0.4550" : null,
      lineNet: linesVisible ? "182.00" : null,
      warnings: [],
    },
  ],
  suggestions: [],
  differences: 0,
});

const renderInvoicePage = async (modules: string[], permissions: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const fetchMock = stubFetch(
    (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/identity/session") return Promise.resolve(json(TEST_SESSION));
      if (url.startsWith("/api/v1/identity/access/me"))
        return Promise.resolve(json({ permissions, canManageAuthorization: false, administrationScope: "none" }));
      if (url === "/api/v1/purchasing/supplier-invoices/7001/match")
        return Promise.resolve(json(panel(permissions.includes("accounting:read"))));
      return Promise.resolve(new Response(null, { status: 404 }));
    },
    { session: "delegate" },
  );
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/accounting/supplier-invoices/$supplierInvoiceId",
    component: SupplierInvoiceRoute.options.component as RouteComponent,
  });
  const order = createRoute({ getParentRoute: () => root, path: "/purchasing/orders/$orderId", component: () => null });
  const router = createRouter({
    routeTree: root.addChildren([page, order]),
    history: createMemoryHistory({ initialEntries: ["/accounting/supplier-invoices/7001"] }),
  });
  await router.load();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router as never} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { fetchMock, queryClient };
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
  vi.unstubAllGlobals();
});

describe("Purchasing's Purchase order panel on Accounting's supplier-invoice page", () => {
  it("is drawn with the invoice lines' words for a holder of accounting:read, and the match for purchasing:invoices-match", async () => {
    await renderInvoicePage(
      ["accounting", "purchasing"],
      ["accounting:read", "purchasing:access", "purchasing:invoices-match"],
    );
    const panelBox = await screen.findByTestId("purchase-order-panel");
    expect(await screen.findByText("1. Skruer M8")).toBeInTheDocument();
    expect(panelBox).toHaveTextContent("3001");
    expect(screen.getByRole("button", { name: "Unmatch" })).toBeInTheDocument();
  });

  it("shows the matched quantities alone without accounting:read, and no match actions without purchasing:invoices-match", async () => {
    await renderInvoicePage(["accounting", "purchasing"], ["purchasing:access"]);
    const panelBox = await screen.findByTestId("purchase-order-panel");
    await waitFor(() => expect(panelBox).toHaveTextContent("400 stk"));
    expect(panelBox).not.toHaveTextContent("Skruer M8");
    expect(screen.queryByRole("button", { name: "Unmatch" })).not.toBeInTheDocument();
  });

  it("is not drawn without purchasing:access, and Purchasing is not asked", async () => {
    const { fetchMock, queryClient } = await renderInvoicePage(["accounting", "purchasing"], ["accounting:read"]);
    await screen.findByRole("region", { name: "Supplier invoice" });
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => String(url).startsWith("/api/v1/identity/access/me"))).toBe(true),
    );
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    expect(screen.queryByTestId("purchase-order-panel")).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("/purchasing/"))).toBe(false);
  });

  it("is not drawn when Purchasing is off", async () => {
    const { queryClient } = await renderInvoicePage(["accounting"], ["*"]);
    await screen.findByRole("region", { name: "Supplier invoice" });
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    expect(screen.queryByTestId("purchase-order-panel")).not.toBeInTheDocument();
  });
});
