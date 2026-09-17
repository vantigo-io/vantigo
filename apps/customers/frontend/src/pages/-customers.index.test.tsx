import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomersPage } from "./customers.index";

const router = vi.hoisted(() => ({
  search: { page: 1, search: "" } as Record<string, unknown>,
  navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
  useSearch: () => router.search,
  useNavigate: () => router.navigate,
}));

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

const stubFetch = () =>
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/customers/stats")) {
        return Promise.resolve(
          jsonResponse({
            totalCount: 1,
            activeCount: 1,
            newLast30DaysCount: 0,
            businessCount: null,
            personCount: null,
            missingIdentityCount: null,
            distinctCountryCount: null,
          }),
        );
      }
      if (url.startsWith("/api/v1/customers/lookup")) {
        return Promise.resolve(jsonResponse([]));
      }
      return Promise.resolve(
        jsonResponse({
          data: [
            {
              id: 1001,
              name: "Equinor",
              status: "active",
              createdAt: "2026-06-01T10:00:00Z",
              updatedAt: "2026-07-01T10:00:00Z",
              identity: null,
            },
          ],
          pagination: {
            page: 1,
            pageSize: 25,
            totalCount: 1,
            totalPages: 1,
            hasNextPage: false,
            hasPreviousPage: false,
          },
        }),
      );
    }),
  );

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <CustomersPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("CustomersPage", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    router.search = { page: 1, search: "" };
    router.navigate.mockReset();
  });

  it("keeps the create form closed on an ordinary list URL", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens the create form when the URL asks for it, and drops the intent when the form closes", async () => {
    stubFetch();
    router.search = { page: 1, search: "", create: true };
    renderPage();

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Create new customer");

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: { page: 1, search: "" }, replace: true }),
    );
  });
});
