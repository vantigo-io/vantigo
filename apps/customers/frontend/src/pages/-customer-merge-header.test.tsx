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

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

import { notifications } from "@mantine/notifications";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// Literally the body the server sends: nothing unset is on the wire, so
// timelineSummary carries no latestOccurredOn until there is one.
const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 1005,
  customerNumber: 5,
  name: "Acme Norge AS",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 0 },
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
      // A write from a tab opened before the customer was merged away: what
      // every customer-scoped write answers it.
      if (path === "/api/v1/customers/1005/type")
        return Promise.resolve(
          new Response(
            JSON.stringify({
              title: "Customer was merged",
              status: 409,
              code: "customer_merged",
              detail: "#5 Acme Norge AS was merged into #2 Acme AS.",
            }),
            { status: 409, headers: { "Content-Type": "application/problem+json" } },
          ),
        );
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
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.mocked(notifications.show).mockClear();
  });

  it("tells a tab opened before the merge that its customer was merged, in the catalog's words", async () => {
    await renderHeader(customer());
    await userEvent.click(screen.getByRole("button", { name: "Change type" }));
    await userEvent.click(await screen.findByRole("button", { name: "Change to private" }));

    await vi.waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({
          color: "red",
          title: "Customer type could not be changed",
          message: "This customer was merged into another",
        }),
      ),
    );
  });

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
 * host could pass. Each card's own read is answered with the empty body the
 * server sends for a customer with nothing on it, so every card settles into
 * the state that offers its action: the addresses and contacts empty states,
 * the timeline's, the billing profile (whose pencil waits for it), and the
 * relationship card's owner, group and tags editors. The Registry card is not
 * here: without a Norwegian organisation number it offers nothing to anyone.
 */
const renderOverview = async (body: ReturnType<typeof customer>) => {
  stubFetch(
    vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1005") return Promise.resolve(jsonResponse(200, body));
      if (path === "/api/v1/customers/1005/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.startsWith("/api/v1/customers/1005/contacts")) return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.startsWith("/api/v1/customers/1005/timeline")) return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path === "/api/v1/customers/1005/billing-profile")
        return Promise.resolve(jsonResponse(200, { revision: 4, warnings: [] }));
      if (path === "/api/v1/customers/tags" || path === "/api/v1/customers/groups")
        return Promise.resolve(jsonResponse(200, []));
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
  // Every card has settled — each empty state is what a card shows once its
  // read landed, and the billing card's "not set" lines appear with the
  // profile its pencil waits for — so an action missing below is withheld, not
  // merely not rendered yet.
  const settled = { timeout: 5_000 };
  await screen.findByText("No addresses yet", undefined, settled);
  await screen.findByText("No contacts associated with this customer yet.", undefined, settled);
  await screen.findByText("No events yet. Add the first moment worth remembering.", undefined, settled);
  await screen.findAllByText("Not set — the invoicing default applies", undefined, settled);
};

/** The actions each card offers an editor: buttons, and the relationship card's three editors. */
const cardButtons = ["Add contact", "Add event", "Add address", "Edit contact details", "Edit billing profile"];
const cardEditors = ["Owner", "Group", "Tags"];

describe("customer page body — a merged-away customer", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers no edit action on any card, whatever the host allows", async () => {
    // The positive control first: the same capabilities on a customer that was
    // not merged show the cards' actions, so their absence below is the gate's.
    await renderOverview(customer());
    for (const action of cardButtons) expect(screen.getByRole("button", { name: action })).toBeInTheDocument();
    for (const editor of cardEditors) expect(screen.getByRole("combobox", { name: editor })).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    await renderOverview(
      customer({ status: "archived", mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" } }),
    );
    for (const action of cardButtons) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
    for (const editor of cardEditors) {
      expect(screen.queryByRole("combobox", { name: editor })).not.toBeInTheDocument();
    }
  });
});
