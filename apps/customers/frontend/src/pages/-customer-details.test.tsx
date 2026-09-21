import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { routeTree } from "../test/route-tree";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// The Overview tab's billing card (design D6) always GETs the billing
// profile, which answers 200 for every customer (design D4) — with the ten
// optional fields left out entirely when nothing is set, as the wire really
// encodes them. Every route test below needs this stubbed the same way it
// stubs the customer's own GET and legal identity.
const emptyBillingProfile = { revision: 1, warnings: [] };

const renderRoute = async (path: string, heading: string) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });

  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>,
  );

  // Rendering the route resolves the customer and its legal identity, which can
  // exceed the one-second default when the suite runs workers in parallel.
  await screen.findByRole("heading", { name: heading }, { timeout: 5000 });
};

describe("customer details page", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows basic customer data and separately fetched legal identity", async () => {
    stubFetch((url: RequestInfo | URL) => {
      if (String(url) === "/api/v1/customers/1001") {
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Equinor",
            status: "active",
            type: "business",
            createdAt: "2026-06-01T10:00:00Z",
            updatedAt: "2026-07-01T10:00:00Z",
            identity: null,
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      }
      if (String(url) === "/api/v1/customers/1001/legal-identity")
        return Promise.resolve(
          jsonResponse(200, { country: "no", type: "business", id: "923609016", name: "EQUINOR ASA", source: "brreg" }),
        );
      if (String(url) === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, emptyBillingProfile));
      if (String(url) === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderRoute("/customers/1001", "Equinor");

    expect(await screen.findByRole("heading", { name: "Equinor" })).toBeInTheDocument();
    expect(screen.getByText("#1001")).toBeInTheDocument();
    expect(screen.getByText("EQUINOR ASA")).toBeInTheDocument();
    expect(screen.getByText("923609016")).toBeInTheDocument();
    expect(screen.getByText("NO")).toBeInTheDocument();
    // Once as the customer type badge, once as the legal identity's type.
    expect(screen.getAllByText("Business")).toHaveLength(2);
    expect(screen.getByRole("link", { name: "Brønnøysundregistrene" })).toHaveAttribute(
      "href",
      "https://virksomhet.brreg.no/nb/oppslag/enheter/923609016",
    );
    expect(screen.getByRole("button", { name: /edit customer/i })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /edit customer/i }));
    expect(await screen.findByLabelText(/^name/i)).toHaveValue("Equinor");
  });

  it("clearly explains when legal identity is unavailable", async () => {
    stubFetch((url: RequestInfo | URL) =>
      String(url) === "/api/v1/customers/1002"
        ? Promise.resolve(
            jsonResponse(200, {
              id: 1002,
              name: "Acme",
              status: "active",
              type: "person",
              createdAt: "2026-06-01T10:00:00Z",
              updatedAt: "2026-07-01T10:00:00Z",
              identity: null,
              timelineSummary: { entryCount: 0, latestOccurredOn: null },
            }),
          )
        : String(url) === "/api/v1/customers/1002/legal-identity"
          ? Promise.resolve(new Response(null, { status: 403 }))
          : String(url) === "/api/v1/customers/1002/billing-profile"
            ? Promise.resolve(jsonResponse(200, emptyBillingProfile))
            : String(url) === "/api/v1/customers/1002/addresses"
              ? Promise.resolve(jsonResponse(200, { data: [] }))
              : String(url).includes("/timeline")
                ? Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }))
                : Promise.resolve(new Response(null, { status: 404 })),
    );

    await renderRoute("/customers/1002", "Acme");

    expect(await screen.findByRole("heading", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText("#1002")).toBeInTheDocument();
    expect(screen.getByText(/legal identity is not available/i)).toBeInTheDocument();
    expect(screen.getByText("Private")).toBeInTheDocument();
  });

  it("keeps the rest of the Overview when the billing profile fails to load", async () => {
    stubFetch((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001")
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Equinor",
            status: "active",
            type: "business",
            createdAt: "2026-06-01T10:00:00Z",
            updatedAt: "2026-07-01T10:00:00Z",
            identity: null,
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(new Response(null, { status: 500 }));
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.includes("/contacts")) return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderRoute("/customers/1001", "Equinor");

    // The billing card says so itself rather than throwing the whole tab at
    // the route's error component.
    expect(await screen.findByText("Could not load the billing profile.")).toBeInTheDocument();
    expect(screen.getByText("Contact & addresses")).toBeInTheDocument();
    expect(screen.getByText("Timeline")).toBeInTheDocument();
    expect(screen.queryByText("Customer not found")).not.toBeInTheDocument();
  });

  it("shows a not-found state for unknown customers", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    await renderRoute("/customers/999999", "Customer not found");

    expect(await screen.findByText("Customer not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /back to customers/i })).toBeInTheDocument();
  });
});
