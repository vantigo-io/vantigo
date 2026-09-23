import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

// Literally the vocabulary's wire rows: Key accounts has no default, so it has
// no defaultPaymentTermsDays key at all.
const groupRows = [
  { id: "g1", name: "Retail", defaultPaymentTermsDays: 30, customerCount: 2 },
  { id: "g2", name: "Key accounts", customerCount: 0 },
];

const stubFetch = (rows: unknown[] = [defaultRow]) => {
  const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
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
    if (url.startsWith("/api/v1/customers/groups")) return Promise.resolve(jsonResponse(groupRows));
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

/** The table row a cell belongs to, so an assertion about one customer says so. */
const rowOf = (cell: HTMLElement) => cell.closest("tr") as HTMLElement;

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
    const row = rowOf(await screen.findByText("Equinor"));
    expect(within(row).getByText("Kari Nordmann")).toBeInTheDocument();
    // Scoped to the row: a tag name is arbitrary text, and the Tag filter's own
    // options carry the same names elsewhere on the page.
    expect(within(row).getByText("VIP")).toBeInTheDocument();
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
    // `defaultRow` is literally the wire body for a customer with no owner and
    // no tags: the server omits both keys rather than sending null and [], and
    // the row has to read that as "unowned" on its own.
    stubFetch([defaultRow]);
    renderPage();
    const row = rowOf(await screen.findByText("Equinor"));
    expect(within(row).queryByText("Kari Nordmann")).not.toBeInTheDocument();
    expect(within(row).getByText("—")).toBeInTheDocument();
  });

  it("drives the URL from the Owner filter", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("combobox", { name: "Owner" }));
    await userEvent.click(await screen.findByRole("option", { name: "Mine" }));

    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ ownerId: "me", page: 1 }) }),
    );
  });

  it("sends ownerId to the API when the URL carries it", async () => {
    const fetchMock = stubFetch([ownedRow]);
    router.search = { page: 1, search: "", ownerId: "me" };
    renderPage();
    await screen.findByText("Equinor");

    // The request is found by URL, never by "the last call": the stats and tags
    // queries land on their own clocks.
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

  it("resets to the first page when a filter changes, wherever the reader had got to", async () => {
    // Page 3 of the unfiltered list is not page 3 of the filtered one; staying
    // on it lands on an empty table for no reason the reader can see. Every
    // filter goes through the one `filterBy`, so pinning it on Owner pins it.
    stubFetch([ownedRow]);
    router.search = { page: 3, search: "" };
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("combobox", { name: "Owner" }));
    await userEvent.click(await screen.findByRole("option", { name: "Mine" }));

    expect(router.navigate).toHaveBeenCalledWith({
      search: {
        page: 1,
        search: "",
        status: undefined,
        type: undefined,
        ownerId: "me",
        tagId: undefined,
        groupId: undefined,
        sortBy: undefined,
        sortDirection: undefined,
      },
    });
  });

  it("opens Manage tags for a caller who may edit", async () => {
    stubFetch([ownedRow]);
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("button", { name: "Manage tags" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Manage tags");
  });

  it("offers no Manage tags without canEdit", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("button", { name: "Manage tags" })).not.toBeInTheDocument();
  });

  it("drops a tagId the loaded vocabulary does not know", async () => {
    // A link outlives the tag it filters by: someone deleted it in another tab,
    // or a week ago. Left in the URL it narrows the list to nothing while the
    // Tag filter sits blank, so the page shows an empty table and says it is
    // filtering by nothing — the same dead end deleting a filtered tag leaves,
    // arrived at from outside this page.
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", tagId: "6f1a7c3a-0000-0000-0000-000000000000" };
    renderPage();
    await screen.findByText("Equinor");

    await waitFor(() =>
      expect(router.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ search: expect.objectContaining({ tagId: undefined, page: 1 }) }),
      ),
    );
  });

  it("keeps a tagId the vocabulary knows", async () => {
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", tagId: "t1" };
    renderPage();
    await screen.findByText("Equinor");

    // The filter naming the tag is the vocabulary having landed: t1 is in it,
    // and a live filter must survive that.
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Tag" })).toHaveValue("VIP"));
    expect(router.navigate).not.toHaveBeenCalled();
  });

  it("leaves the tagId alone while the vocabulary is unknown", async () => {
    // Until the vocabulary answers, EVERY tag id is one it does not know —
    // dropping the filter then would throw away a perfectly good one on nothing
    // but a slow (or failing) request.
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.startsWith("/api/v1/customers/tags")) {
          return Promise.resolve(new Response(JSON.stringify({ title: "Boom" }), { status: 500 }));
        }
        if (url.startsWith("/api/v1/customers/stats")) return Promise.resolve(jsonResponse({}));
        return Promise.resolve(
          jsonResponse({
            data: [ownedRow],
            pagination: { page: 1, pageSize: 25, totalCount: 1, totalPages: 1, hasNextPage: false },
          }),
        );
      }),
    );
    router.search = { page: 1, search: "", tagId: "t1" };
    renderPage();
    await screen.findByText("Equinor");

    expect(router.navigate).not.toHaveBeenCalled();
  });

  it("drops a deleted tag from the URL when the list was filtered by it", async () => {
    // The tag is gone, so `tagId=t1` would narrow the list to nothing on the
    // next fetch and the Tag filter would sit blank while it did.
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", tagId: "t1" };
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Manage tags" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete VIP" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete tag" }));

    await waitFor(() =>
      expect(router.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ search: expect.objectContaining({ tagId: undefined, page: 1 }) }),
      ),
    );
  });

  it("leaves the URL alone when the deleted tag is not the one filtered by", async () => {
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", tagId: "t2" };
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Manage tags" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete VIP" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete tag" }));

    await waitFor(() => expect(screen.queryByText("Delete tag")).not.toBeInTheDocument());
    expect(router.navigate).not.toHaveBeenCalled();
  });

  it("drives the URL from the Group filter, and offers No group as a filter of its own", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "Retail" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ groupId: "g1", page: 1 }) }),
    );

    router.navigate.mockReset();
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "No group" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ groupId: "none", page: 1 }) }),
    );
  });

  it("asks the API for the group the URL names, by method and URL rather than by call order", async () => {
    const fetchMock = stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "g1" };
    renderPage();
    await screen.findByText("Equinor");
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(([u, init]) => String(u).includes("groupId=g1") && !(init as RequestInit)?.method),
      ).not.toHaveLength(0),
    );
  });

  it("opens Manage groups for a caller who may edit, and offers it to nobody else", async () => {
    stubFetch([ownedRow]);
    const { unmount } = renderPage({ canEdit: true });
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("button", { name: "Manage groups" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Manage groups");
    unmount();

    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("button", { name: "Manage groups" })).not.toBeInTheDocument();
  });

  it("drops a groupId the loaded vocabulary does not know", async () => {
    // The Tag filter's dead end, arrived at through a group: a bookmarked link
    // to a group deleted since would narrow the list to nothing while the Group
    // filter sat blank, since the Select renders nothing for a value none of its
    // options describes.
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "6f1a7c3a-0000-0000-0000-000000000000" };
    renderPage();
    await screen.findByText("Equinor");

    await waitFor(() =>
      expect(router.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ search: expect.objectContaining({ groupId: undefined, page: 1 }) }),
      ),
    );
  });

  it("keeps a known groupId, and No group, once the vocabulary has landed", async () => {
    // `none` is a value the vocabulary never holds, so the repair must let it be.
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "none" };
    const { unmount } = renderPage();
    await screen.findByText("Equinor");
    // The select reading a vocabulary name proves the vocabulary is in.
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    expect(await screen.findByRole("option", { name: "Retail" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("No group");
    expect(router.navigate).not.toHaveBeenCalled();
    unmount();

    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "g1" };
    renderPage();
    await screen.findByText("Equinor");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    expect(router.navigate).not.toHaveBeenCalled();
  });

  it("drops a deleted group from the URL when the list was filtered by it", async () => {
    // The group is gone, so `groupId=g2` would narrow the list by an id no
    // customer can carry while the Group filter sat on a name nobody has.
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "g2" };
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Manage groups" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: "Delete Key accounts" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete group" }));

    await waitFor(() =>
      expect(router.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ search: expect.objectContaining({ groupId: undefined, page: 1 }) }),
      ),
    );
  });

  it("leaves the URL alone when the deleted group is not the one filtered by", async () => {
    stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "none" };
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("button", { name: "Manage groups" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("button", { name: "Delete Key accounts" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete group" }));

    await waitFor(() => expect(screen.queryByText("Delete group")).not.toBeInTheDocument());
    expect(router.navigate).not.toHaveBeenCalled();
  });
});
