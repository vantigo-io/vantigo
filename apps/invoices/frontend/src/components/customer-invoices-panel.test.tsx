import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { draft, listPage, meta } from "../test/fixtures";
import { CustomerInvoicesPanel } from "./customer-invoices-panel";
import "../i18n";
import { INVOICE_ROUTE_PATH } from "../lib/routes";

const path = (input: RequestInfo | URL) => String(input);

/**
 * The host's customer page as far as the panel needs it: the customer's tab
 * at its path, and the document route a row and a new draft lead to.
 */
const renderPanel = ({ canCreate }: { canCreate: boolean }) => {
  const root = createRootRoute({ component: () => <Outlet /> });
  const routeTree = root.addChildren([
    createRoute({
      getParentRoute: () => root,
      path: "/customers/$customerId/invoices",
      component: () => <CustomerInvoicesPanel customerId={2001} canCreate={canCreate} userDisplayName="Ola Nordmann" />,
    }),
    createRoute({ getParentRoute: () => root, path: INVOICE_ROUTE_PATH, component: () => <p>The document</p> }),
  ]);
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ["/customers/2001/invoices"] }),
  });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { router };
};

/** The fetch fake: meta, the customer's list (paged by its query) and a create. */
const server = () =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url === "/api/v1/invoices" && method === "POST") return jsonResponse(201, draft({ id: 1010, lines: [] }));
    if (url.startsWith("/api/v1/invoices?")) {
      const page = new URL(url, "http://localhost").searchParams.get("page") ?? "1";
      return jsonResponse(
        200,
        listPage({ page: Number(page), totalPages: 2, totalCount: 30, hasNextPage: page === "1" }),
      );
    }
    return new Response(null, { status: 404 });
  });

describe("the customer's invoices", () => {
  it("lists the customer's documents only, each with its state and open amount", async () => {
    const fetchMock = server();
    renderPanel({ canCreate: false });

    const rows = await screen.findAllByRole("row");
    expect(
      fetchMock.actualCalls.some(
        ([url, init]) => path(url) === "/api/v1/invoices?customerId=2001&page=1" && (init?.method ?? "GET") === "GET",
      ),
    ).toBe(true);
    expect(within(rows[3]).getByTestId("state-badge")).toHaveTextContent("Open");
    expect(within(rows[3]).getByTestId("open-amount")).toHaveTextContent(/NOK\s?124\.99/);
    expect(within(rows[1]).getByTestId("state-badge")).toHaveTextContent("Draft");
  });

  it("pages through the customer's documents", async () => {
    const fetchMock = server();
    renderPanel({ canCreate: false });
    await screen.findAllByRole("row");

    await userEvent.click(screen.getByRole("button", { name: "2" }));
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/invoices?customerId=2001&page=2")).toBe(true),
    );
  });

  it("offers no New invoice without canCreate", async () => {
    server();
    renderPanel({ canCreate: false });
    await screen.findAllByRole("row");
    expect(screen.queryByRole("button", { name: "New invoice" })).not.toBeInTheDocument();
  });

  it("creates a draft for this customer with today and our reference, and opens it", async () => {
    const fetchMock = server();
    const { router } = renderPanel({ canCreate: true });

    await userEvent.click(await screen.findByRole("button", { name: "New invoice" }));
    const dialog = await screen.findByRole("dialog");
    // The customer is the page's: there is nobody to pick.
    expect(within(dialog).queryByRole("combobox", { name: "Customer" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Create the draft" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1010"));
    const create = fetchMock.actualCalls.find(
      ([url, init]) => path(url) === "/api/v1/invoices" && init?.method === "POST",
    );
    expect(JSON.parse(String(create?.[1]?.body))).toEqual({
      customerId: 2001,
      lines: [],
      deliveryDate: "2026-09-12",
      ourReference: "Ola Nordmann",
    });
  });
});
