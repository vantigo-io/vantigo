import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { routeTree } from "../routeTree.gen";
import { stubFetch } from "../test/fetch";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

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

  await screen.findByRole("heading", { name: heading });
};

describe("customer details page", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows the customer and its legal identity", async () => {
    stubFetch((url: RequestInfo | URL) => {
      if (String(url) === "/api/v1/customers/1001") {
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Equinor",
            identity: { country: "no", type: "business", id: "923609016", name: "EQUINOR ASA", source: "brreg" },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderRoute("/customers/1001", "Equinor");

    expect(await screen.findByRole("heading", { name: "Equinor" })).toBeInTheDocument();
    expect(screen.getByText("#1001")).toBeInTheDocument();
    expect(screen.getByText("EQUINOR ASA")).toBeInTheDocument();
    expect(screen.getByText("923609016")).toBeInTheDocument();
    expect(screen.getByText("NO")).toBeInTheDocument();
    expect(screen.getByText("Business")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Brønnøysundregistrene" })).toHaveAttribute(
      "href",
      "https://virksomhet.brreg.no/nb/oppslag/enheter/923609016",
    );
    expect(screen.getByRole("button", { name: /edit customer/i })).toBeInTheDocument();
  });

  it("shows no legal identity row when the customer has none", async () => {
    stubFetch((url: RequestInfo | URL) =>
      String(url) === "/api/v1/customers/1002"
        ? Promise.resolve(jsonResponse(200, { id: 1002, name: "Acme", identity: null }))
        : String(url).includes("/timeline")
          ? Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }))
          : Promise.resolve(new Response(null, { status: 404 })),
    );

    await renderRoute("/customers/1002", "Acme");

    expect(await screen.findByRole("heading", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText("#1002")).toBeInTheDocument();
    expect(screen.queryByText(/legal/i)).not.toBeInTheDocument();
  });

  it("shows a not-found state for unknown customers", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    await renderRoute("/customers/999999", "Customer not found");

    expect(await screen.findByText("Customer not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /back to customers/i })).toBeInTheDocument();
  });
});
