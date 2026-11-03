import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { MeteringPointsPage } from "./metering-points.index";

const router = vi.hoisted(() => ({
  navigate: vi.fn(),
  search: { page: 1, search: "" } as Record<string, unknown>,
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  useNavigate: () => router.navigate,
  useSearch: () => router.search,
}));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

const point = (id: number, currentCustomer?: { id: number; name?: string | null }) => ({
  id,
  gsrn: `70705759900000000${id}`,
  meterNumber: `M-${id}`,
  address: { streetAddress: "Testgata 1", postalCode: "0001", city: "Oslo", countryCode: "NO" },
  priceArea: "NO1",
  connectionStatus: "Connected",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...(currentCustomer ? { currentCustomer } : {}),
});

const renderPage = (
  props: { canImport?: boolean; canViewCustomers?: boolean; canOpenCustomers?: boolean },
  points: unknown[] = [],
) => {
  const fetchMock = stubFetch(async (input) =>
    String(input).startsWith("/api/v1/customers")
      ? json({
          data: [{ id: 1001, name: "Aurora Kraft AS" }],
          pagination: { page: 1, pageSize: 50, totalCount: 1, totalPages: 1 },
        })
      : json({ data: points, pagination: { page: 1, pageSize: 25, totalCount: points.length, totalPages: 1 } }),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <MeteringPointsPage {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

afterEach(() => {
  router.navigate.mockReset();
  router.search = { page: 1, search: "" };
});

describe("MeteringPointsPage", () => {
  it("offers Import only when the host says the caller may import", async () => {
    renderPage({ canImport: true });
    expect(await screen.findByRole("link", { name: "Import" })).toHaveAttribute(
      "href",
      "/energy/metering-points/import",
    );
  });

  it("hides Import otherwise", async () => {
    renderPage({});
    expect(await screen.findByText("No metering points found.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Import" })).not.toBeInTheDocument();
  });

  it("asks for the points without a customer when the filter is on, and turns it off from the switch", async () => {
    router.search = { page: 1, search: "", withoutCustomer: true };
    const fetchMock = renderPage({});
    await waitFor(() =>
      expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
        "/api/v1/energy/metering-points?page=1&pageSize=25&withoutCustomer=true",
      ),
    );
    expect(screen.getByRole("switch", { name: "Without customer" })).toBeChecked();
    await userEvent.click(screen.getByRole("switch", { name: "Without customer" }));
    expect(router.navigate).toHaveBeenCalledWith({ search: { page: 1, search: "" } });
  });

  it("turns the filter on from the switch", async () => {
    renderPage({});
    await userEvent.click(await screen.findByRole("switch", { name: "Without customer" }));
    expect(router.navigate).toHaveBeenCalledWith({ search: { page: 1, search: "", withoutCustomer: true } });
  });

  it("sorts by a column from its header: ascending, then descending, then not at all", async () => {
    renderPage({});
    await userEvent.click(await screen.findByRole("button", { name: "GSRN" }));
    expect(router.navigate).toHaveBeenLastCalledWith({
      search: { page: 1, search: "", sortBy: "gsrn", sortDirection: "asc" },
    });
  });

  it("marks the sorted column and asks the server for its order and the filters", async () => {
    router.search = { page: 1, search: "", sortBy: "meterNumber", sortDirection: "desc", priceArea: "NO4" };
    const fetchMock = renderPage({});
    await waitFor(() =>
      expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
        "/api/v1/energy/metering-points?page=1&pageSize=25&priceArea=NO4&sortBy=meterNumber&sortDirection=desc",
      ),
    );
    expect(await screen.findByRole("columnheader", { name: "Meter number" })).toHaveAttribute(
      "aria-sort",
      "descending",
    );
    await userEvent.click(screen.getByRole("button", { name: "Meter number" }));
    expect(router.navigate).toHaveBeenLastCalledWith({ search: { page: 1, search: "", priceArea: "NO4" } });
  });

  it("puts the customer now first, linked to the customer, and says when there is none", async () => {
    renderPage({ canViewCustomers: true, canOpenCustomers: true }, [
      point(1, { id: 1001, name: "Aurora Kraft AS" }),
      point(2),
    ]);
    expect(await screen.findByRole("link", { name: "Aurora Kraft AS" })).toHaveAttribute("href", "/customers/1001");
    expect(screen.getAllByRole("columnheader")[0]).toHaveTextContent("Customer");
    expect(screen.getByText("No customer")).toBeInTheDocument();
  });

  it("names the customer without a link when the caller cannot open customers", async () => {
    renderPage({ canViewCustomers: true }, [point(1, { id: 1001, name: "Aurora Kraft AS" })]);
    expect(await screen.findByRole("cell", { name: "Aurora Kraft AS" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Aurora Kraft AS" })).not.toBeInTheDocument();
  });

  it("leaves the customer column and filter out without supply-periods-view, and never sends the filter", async () => {
    router.search = { page: 1, search: "", customerId: 1001 };
    const fetchMock = renderPage({}, [point(1)]);
    await waitFor(() =>
      expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/energy/metering-points?page=1&pageSize=25"),
    );
    expect(screen.queryByRole("columnheader", { name: "Customer" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Customer" })).not.toBeInTheDocument();
  });
});
