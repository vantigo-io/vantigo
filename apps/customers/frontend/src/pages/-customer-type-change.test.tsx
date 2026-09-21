import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { routeTree } from "../test/route-tree";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (type: "business" | "person", overrides: { revision?: number } = {}) => ({
  id: 1001,
  name: "Equinor",
  status: "active",
  type,
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  identity: null,
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  ...overrides,
});

// The Overview tab's billing card (design D6) always GETs the billing
// profile, which answers 200 for every customer (design D4) — with the ten
// optional fields left out entirely when nothing is set, as the wire really
// encodes them. Every route test below needs this stubbed the same way it
// stubs the customer's own GET, legal identity and timeline.
const emptyBillingProfile = { revision: 1, warnings: [] };

const renderCustomer = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: ["/customers/1001"] }),
  });
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
  await screen.findByRole("heading", { name: "Equinor" }, { timeout: 5000 });
};

describe("changing a customer's type", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("is a separate, confirmed action that warns before calling the type endpoint", async () => {
    let current = customer("business");
    const fetchMock = stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/type" && init?.method === "PUT") {
        current = customer("person");
        return Promise.resolve(jsonResponse(200, current));
      }
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, current));
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 204 }));
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, emptyBillingProfile));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderCustomer();
    expect(screen.getByText("Business")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /change type/i }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/change customer type/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/rarely the right action/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/Brønnøysundregistrene/)).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: /change to private/i }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/type", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type: "person" }),
      }),
    );
    expect(await screen.findByText("Private")).toBeInTheDocument();
  });

  it("sends the customer's revision along with the type change", async () => {
    let current = customer("business", { revision: 2 });
    const fetchMock = stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/type" && init?.method === "PUT") {
        current = customer("person", { revision: 3 });
        return Promise.resolve(jsonResponse(200, current));
      }
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, current));
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 204 }));
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, emptyBillingProfile));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderCustomer();
    await userEvent.click(screen.getByRole("button", { name: /change type/i }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /change to private/i }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/type", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type: "person", revision: 2 }),
      }),
    );
  });

  it("shows a revision-conflict message and refetches the customer on a stale type change", async () => {
    const initial = customer("business", { revision: 2 });
    const latest = customer("business", { revision: 3 });
    let putCalls = 0;
    const fetchMock = stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/type" && init?.method === "PUT") {
        putCalls += 1;
        return Promise.resolve(
          jsonResponse(409, {
            title: "Customer revision conflict",
            detail: "The customer was changed by someone else.",
            status: 409,
          }),
        );
      }
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, putCalls > 0 ? latest : initial));
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 204 }));
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, emptyBillingProfile));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderCustomer();
    const getCallsBefore = fetchMock.actualCalls.filter(([url]) => String(url) === "/api/v1/customers/1001").length;
    await userEvent.click(screen.getByRole("button", { name: /change type/i }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /change to private/i }));

    expect(
      await screen.findByText("This customer was changed by someone else. Reload to see the latest version."),
    ).toBeInTheDocument();
    await waitFor(() => {
      const getCallsAfter = fetchMock.actualCalls.filter(([url]) => String(url) === "/api/v1/customers/1001").length;
      expect(getCallsAfter).toBeGreaterThan(getCallsBefore);
    });
  });

  it("does nothing when the confirmation is cancelled", async () => {
    const fetchMock = stubFetch((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, customer("person")));
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 204 }));
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, emptyBillingProfile));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderCustomer();
    await userEvent.click(screen.getByRole("button", { name: /change type/i }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: /change to business/i })).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /^cancel$/i }));

    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "PUT")).toBe(false);
  });
});
