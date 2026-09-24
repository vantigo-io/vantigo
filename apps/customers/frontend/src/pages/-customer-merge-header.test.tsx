import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerDetailHeader, CustomerOverview } from "./customers.$customerId";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 1005,
  customerNumber: 5,
  name: "Acme Norge AS",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision: 4,
  ...overrides,
});

/**
 * The header under a real router, since the merged-away banner links to the
 * survivor through the package's router-Link convention (see
 * `-customer-form-modal.test.tsx`'s `renderModalWithRouter`).
 */
const renderHeader = async (
  body: ReturnType<typeof customer>,
  props: { canArchive?: boolean; canRestore?: boolean; canMerge?: boolean } = {},
) => {
  stubFetch(
    vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1005/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
      if (path === "/api/v1/customers/1005") return Promise.resolve(jsonResponse(200, body));
      return Promise.resolve(new Response(null, { status: 404 }));
    }),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <Suspense fallback={<div>Loading…</div>}>
              <CustomerDetailHeader customerId={1005} {...props} />
            </Suspense>
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
  await screen.findByRole("heading", { name: body.name }, { timeout: 5000 });
};

describe("customer detail header — merge", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers Merge… only to a caller the host says may merge, and opens the modal", async () => {
    await renderHeader(customer());
    expect(screen.queryByRole("button", { name: "Merge…" })).not.toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    await renderHeader(customer(), { canMerge: true });
    await userEvent.click(screen.getByRole("button", { name: "Merge…" }));

    expect(await screen.findByRole("dialog", { name: "Merge a duplicate into Acme Norge AS" })).toBeInTheDocument();
  });

  it("shows where a merged-away customer went, and hides every action that would edit it", async () => {
    await renderHeader(customer({ status: "archived", mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" } }), {
      canArchive: true,
      canRestore: true,
      canMerge: true,
    });

    expect(screen.getByText("Merged into #2 Acme AS")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open #2 Acme AS" })).toHaveAttribute("href", "/customers/1002");
    expect(screen.queryByText(/this customer is archived/i)).not.toBeInTheDocument();
    for (const action of ["Edit customer", "Change type", "Archive customer", "Restore customer", "Merge…"]) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
  });
});

/**
 * The page body (every card) under the same router, with every capability the
 * host could pass. Only the customer is answered; each card's own read gets a
 * 404, which is enough: the three actions asserted here — two in their cards'
 * headers, the contact card's in its empty state, which reads the customer —
 * render whatever the other reads answer.
 */
const renderOverview = async (body: ReturnType<typeof customer>) => {
  stubFetch(
    vi.fn((url: RequestInfo | URL) =>
      Promise.resolve(
        String(url) === "/api/v1/customers/1005" ? jsonResponse(200, body) : new Response(null, { status: 404 }),
      ),
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <Suspense fallback={<div>Loading…</div>}>
              <CustomerOverview
                customerId={1005}
                canEdit
                canManageBilling
                canViewIdentity
                canManageIdentity
                canManageTimeline
              />
            </Suspense>
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
};

describe("customer page body — a merged-away customer", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers no edit action on any card, whatever the host allows", async () => {
    // The positive control first: the same capabilities on a customer that was
    // not merged show the cards' actions, so their absence below is the gate's.
    await renderOverview(customer());
    expect(await screen.findByRole("button", { name: "Add contact" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Add event" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Edit contact details" })).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    await renderOverview(
      customer({ status: "archived", mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" } }),
    );
    expect((await screen.findAllByText("Contacts")).length).toBeGreaterThan(0);
    for (const action of ["Add contact", "Add event", "Add address", "Edit contact details"]) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
  });
});
