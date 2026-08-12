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

  it("shows basic customer data and separately fetched legal identity", async () => {
    stubFetch((url: RequestInfo | URL) => {
      if (String(url) === "/api/v1/customers/1001") {
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Equinor",
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      }
      if (String(url) === "/api/v1/customers/1001/legal-identity")
        return Promise.resolve(
          jsonResponse(200, { country: "no", type: "business", id: "923609016", name: "EQUINOR ASA", source: "brreg" }),
        );
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderRoute("/customers/1001", "Equinor");

    expect(await screen.findByRole("heading", { name: "Equinor" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Legal identity" })).toBeInTheDocument();
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

    await userEvent.click(screen.getByRole("button", { name: /edit customer/i }));
    expect(await screen.findByLabelText(/^name/i)).toHaveValue("Equinor");
  });

  it("clearly explains when legal identity is unavailable", async () => {
    stubFetch((url: RequestInfo | URL) =>
      String(url) === "/api/v1/customers/1002"
        ? Promise.resolve(
            jsonResponse(200, { id: 1002, name: "Acme", timelineSummary: { entryCount: 0, latestOccurredOn: null } }),
          )
        : String(url) === "/api/v1/customers/1002/legal-identity"
          ? Promise.resolve(new Response(null, { status: 403 }))
          : String(url).includes("/timeline")
            ? Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }))
            : Promise.resolve(new Response(null, { status: 404 })),
    );

    await renderRoute("/customers/1002", "Acme");

    expect(await screen.findByRole("heading", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText("#1002")).toBeInTheDocument();
    expect(screen.getByText(/legal identity is not available/i)).toBeInTheDocument();
  });

  it("shows a not-found state for unknown customers", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    await renderRoute("/customers/999999", "Customer not found");

    expect(await screen.findByText("Customer not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /back to customers/i })).toBeInTheDocument();
  });
});
