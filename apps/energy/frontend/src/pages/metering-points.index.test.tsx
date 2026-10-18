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

const renderPage = (props: { canImport?: boolean }) => {
  const fetchMock = stubFetch(async () =>
    json({ data: [], pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0 } }),
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
});
