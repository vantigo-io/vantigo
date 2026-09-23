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

const tagRows = [
  { id: "t1", name: "VIP", color: "grape", customerCount: 2 },
  { id: "t2", name: "Prospect", color: null, customerCount: 0 },
];

const stubFetch = (rows: unknown[] = [defaultRow]) => {
  const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
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
    if (url.startsWith("/api/v1/customers/tags")) return Promise.resolve(jsonResponse(tagRows));
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
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderPage = (props: { canEdit?: boolean } = {}) =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <CustomersPage {...props} />
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

// ownedRow is the wire body for a customer with an owner and one tag —
// literally what the server sends, nothing invented.
const ownedRow = {
  ...defaultRow,
  owner: { userId: "u1", displayName: "Kari Nordmann", active: true },
  tags: [{ id: "t1", name: "VIP", color: "grape" }],
};

describe("CustomersPage, owner and tags", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    router.search = { page: 1, search: "" };
    router.navigate.mockReset();
  });

  it("shows the owner's name and the tag chips on the row", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("VIP")).toBeInTheDocument();
  });

  it("marks an owner the directory says is inactive", async () => {
    stubFetch([{ ...ownedRow, owner: { userId: "u1", displayName: "Kari Nordmann", active: false } }]);
    renderPage();
    await screen.findByText("Kari Nordmann");
    // The name is still shown — nothing is revoked (design D1) — with a hint
    // that the account can no longer act.
    expect(screen.getByTitle("This account is inactive")).toBeInTheDocument();
  });

  it("shows an em dash for an unowned customer", async () => {
    stubFetch([{ ...defaultRow, owner: null, tags: [] }]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByText("Kari Nordmann")).not.toBeInTheDocument();
  });

  it("drives the URL from the Owner filter and sends ownerId to the API", async () => {
    const fetchMock = stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("combobox", { name: "Owner" }));
    await userEvent.click(await screen.findByRole("option", { name: "Mine" }));

    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ ownerId: "me", page: 1 }) }),
    );

    // The request is found by method and URL, never by "the last call": the
    // stats and tags queries land on their own clocks.
    router.search = { page: 1, search: "", ownerId: "me" };
    cleanup();
    renderPage();
    await screen.findByText("Equinor");
    const listCalls = fetchMock.mock.calls
      .map(([url]) => String(url))
      .filter((url) => url.startsWith("/api/v1/customers?"));
    expect(listCalls.some((url) => url.includes("ownerId=me"))).toBe(true);
  });

  it("offers Unassigned as the other Owner filter", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("combobox", { name: "Owner" }));
    await userEvent.click(await screen.findByRole("option", { name: "Unassigned" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ ownerId: "none", page: 1 }) }),
    );
  });

  it("drives the URL from the Tag filter, naming the tags the installation has", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("combobox", { name: "Tag" }));
    await userEvent.click(await screen.findByRole("option", { name: "Prospect" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ tagId: "t2", page: 1 }) }),
    );
  });

  it("opens Manage tags only for a caller who may edit", async () => {
    stubFetch([ownedRow]);
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("button", { name: "Manage tags" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Manage tags");

    cleanup();
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("button", { name: "Manage tags" })).not.toBeInTheDocument();
  });
});
