import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerDetailHeader } from "./customers.$customerId";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 1001,
  customerNumber: 5001,
  name: "Equinor",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  identity: null,
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision: 4,
  ...overrides,
});

const renderHeader = async (
  fetchMock: (url: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
  props: { canArchive?: boolean; canRestore?: boolean } = {},
) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <Suspense fallback={<div>Loading…</div>}>
            <CustomerDetailHeader customerId={1001} {...props} />
          </Suspense>
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  await screen.findByRole("heading", { name: "Equinor" }, { timeout: 5000 });
};

const legalIdentity404 = (url: RequestInfo | URL) =>
  String(url) === "/api/v1/customers/1001/legal-identity" ? Promise.resolve(new Response(null, { status: 403 })) : null;

describe("customer detail header — archive and restore", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows no archive or restore action without the matching permission", async () => {
    await renderHeader((url) => {
      const identity = legalIdentity404(url);
      if (identity) return identity;
      if (String(url) === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, customer()));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    expect(screen.queryByRole("button", { name: /archive customer/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /restore customer/i })).not.toBeInTheDocument();
  });

  it("archives an active customer through the shared confirm modal", async () => {
    let current = customer();
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const identity = legalIdentity404(url);
      if (identity) return identity;
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "DELETE") {
        current = customer({ status: "archived", revision: 5 });
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, current));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    await renderHeader(fetchMock, { canArchive: true });

    expect(screen.queryByText(/this customer is archived/i)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /archive customer/i }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /archive customer/i }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", { method: "DELETE" }));
  });

  it("hides Archive for an already-archived customer even with canArchive", async () => {
    await renderHeader(
      (url) => {
        const identity = legalIdentity404(url);
        if (identity) return identity;
        if (String(url) === "/api/v1/customers/1001")
          return Promise.resolve(jsonResponse(200, customer({ status: "archived" })));
        return Promise.resolve(new Response(null, { status: 404 }));
      },
      { canArchive: true },
    );

    expect(screen.getByText(/this customer is archived/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /archive customer/i })).not.toBeInTheDocument();
  });

  it("shows an archived banner and restores through updateCustomer with the current revision", async () => {
    let current = customer({ status: "archived" });
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const identity = legalIdentity404(url);
      if (identity) return identity;
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        current = customer({ status: "active", revision: 5 });
        return Promise.resolve(jsonResponse(200, current));
      }
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, current));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    await renderHeader(fetchMock, { canRestore: true });

    expect(screen.getByText(/this customer is archived/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /archive customer/i })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /restore customer/i }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Equinor", status: "active", revision: 4 }),
      }),
    );
  });

  it("tells the user the customer changed, and refetches it, when Restore hits a revision conflict", async () => {
    // Restore sends the revision the page was rendered with, so it can lose the
    // race like any other write. It must recover the way the type change does —
    // say so and refetch — not leave a red "could not be restored" behind a
    // button that will keep failing on the same stale revision.
    let putCalls = 0;
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const identity = legalIdentity404(url);
      if (identity) return identity;
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        putCalls += 1;
        return Promise.resolve(
          jsonResponse(409, {
            title: "Customer revision conflict",
            detail: "The customer was changed by someone else.",
            status: 409,
          }),
        );
      }
      if (path === "/api/v1/customers/1001")
        return Promise.resolve(jsonResponse(200, customer({ status: "archived", revision: putCalls > 0 ? 6 : 4 })));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const customerGets = () =>
      fetchMock.mock.calls.filter(([url, init]) => String(url) === "/api/v1/customers/1001" && !init?.method).length;
    await renderHeader(fetchMock, { canRestore: true });
    const getsBefore = customerGets();

    await userEvent.click(screen.getByRole("button", { name: /restore customer/i }));

    expect(
      await screen.findByText("This customer was changed by someone else. Reload to see the latest version."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/could not be restored/i)).not.toBeInTheDocument();
    await waitFor(() => expect(customerGets()).toBeGreaterThan(getsBefore));
  });
});
