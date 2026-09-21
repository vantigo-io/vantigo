import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

const defaultRow = {
  id: 1001,
  customerNumber: 5001,
  name: "Equinor",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  identity: null,
};

const stubFetch = (rows: unknown[] = [defaultRow]) =>
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
          data: rows,
          pagination: {
            page: 1,
            pageSize: 25,
            totalCount: rows.length,
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
    <MantineProvider env="test">
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
      expect.objectContaining({
        search: {
          page: 1,
          search: "",
          status: undefined,
          type: undefined,
          sortBy: undefined,
          sortDirection: undefined,
        },
        replace: true,
      }),
    );
  });

  it("shows the customer number, not the database id", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Equinor");

    expect(screen.getByText("5001")).toBeInTheDocument();
    expect(screen.queryByText("1001")).not.toBeInTheDocument();
  });

  it("says what the search box searches", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Equinor");

    expect(
      screen.getByPlaceholderText("Search by name, number, organisation number, contact or email..."),
    ).toBeInTheDocument();
  });

  it("choosing a status filter navigates with that filter and resets the page to 1", async () => {
    stubFetch();
    router.search = { page: 3, search: "acme" };
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("combobox", { name: "Status" }));
    await userEvent.click(await screen.findByRole("option", { name: "Active" }));

    expect(router.navigate).toHaveBeenCalledWith({
      search: {
        page: 1,
        search: "acme",
        status: "active",
        type: undefined,
        sortBy: undefined,
        sortDirection: undefined,
      },
    });
  });

  it("choosing a type filter navigates with that filter and resets the page to 1", async () => {
    stubFetch();
    router.search = { page: 3, search: "acme" };
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("combobox", { name: "Type" }));
    await userEvent.click(await screen.findByRole("option", { name: "Business" }));

    expect(router.navigate).toHaveBeenCalledWith({
      search: {
        page: 1,
        search: "acme",
        status: undefined,
        type: "business",
        sortBy: undefined,
        sortDirection: undefined,
      },
    });
  });

  it("sorts ascending on the first click of a sortable header", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Number" }));

    expect(router.navigate).toHaveBeenCalledWith({
      search: {
        page: 1,
        search: "",
        status: undefined,
        type: undefined,
        sortBy: "customerNumber",
        sortDirection: "asc",
      },
    });
  });

  it("sorts descending on the second click of the same header", async () => {
    stubFetch();
    router.search = { page: 1, search: "", sortBy: "customerNumber", sortDirection: "asc" };
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Number" }));

    expect(router.navigate).toHaveBeenCalledWith({
      search: {
        page: 1,
        search: "",
        status: undefined,
        type: undefined,
        sortBy: "customerNumber",
        sortDirection: "desc",
      },
    });
  });

  it("clears the sort on a third click of the same header", async () => {
    stubFetch();
    router.search = { page: 1, search: "", sortBy: "customerNumber", sortDirection: "desc" };
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Number" }));

    expect(router.navigate).toHaveBeenCalledWith({
      search: {
        page: 1,
        search: "",
        status: undefined,
        type: undefined,
        sortBy: undefined,
        sortDirection: undefined,
      },
    });
  });

  it("exposes aria-sort on the sortable headers, only the active one set", async () => {
    stubFetch();
    router.search = { page: 1, search: "", sortBy: "name", sortDirection: "desc" };
    renderPage();
    await screen.findByText("Equinor");

    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveAttribute("aria-sort", "descending");
    expect(screen.getByRole("columnheader", { name: "Number" })).toHaveAttribute("aria-sort", "none");
  });

  it("shows the archived badge for a customer returned under status=archived", async () => {
    stubFetch([{ ...defaultRow, status: "archived" }]);
    router.search = { page: 1, search: "", status: "archived" };
    renderPage();

    expect(await screen.findByText("Archived")).toBeInTheDocument();
  });
});
