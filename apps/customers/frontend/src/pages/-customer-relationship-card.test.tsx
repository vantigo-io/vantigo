import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { syncCustomerRevision } from "../api/customers";
import { CustomerRelationshipCard } from "./-customer-relationship-card";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// customerBody is literally the wire body for a customer with nothing set:
// no owner key at all, no tags key at all (design D1, D2 — both are omitted
// rather than sent as null/[]), which is the shape a component must survive.
const customerBody = {
  id: 1001,
  customerNumber: 5001,
  name: "Equinor",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  identity: null,
  revision: 3,
};

const ownedBody = {
  ...customerBody,
  owner: { userId: "u1", displayName: "Kari Nordmann", active: true },
  tags: [{ id: "t1", name: "VIP", color: "grape" }],
};

const tagRows = [
  { id: "t1", name: "VIP", color: "grape", customerCount: 2 },
  { id: "t2", name: "Prospect", color: null, customerCount: 0 },
];

const stubFetch = (options: { customer?: unknown; tagsFail?: boolean } = {}) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method === "PUT" && url.endsWith("/owner"))
      return Promise.resolve(jsonResponse({ ...ownedBody, revision: 4 }));
    if (init?.method === "PUT" && url.endsWith("/tags"))
      return Promise.resolve(
        jsonResponse({ tags: tagRows.map((tag) => ({ id: tag.id, name: tag.name, color: tag.color })) }),
      );
    if (init?.method === "POST")
      return Promise.resolve(jsonResponse({ id: "t3", name: "Churned", customerCount: 0 }, 201));
    if (url.startsWith("/api/v1/customers/assignable-users")) {
      return Promise.resolve(jsonResponse([{ userId: "u2", displayName: "Ola Nordmann" }]));
    }
    if (url === "/api/v1/customers/tags")
      return Promise.resolve(options.tagsFail ? jsonResponse({ title: "Boom" }, 500) : jsonResponse(tagRows));
    return Promise.resolve(jsonResponse(options.customer ?? customerBody));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

// The client is handed back so a test can do to the cache what a sibling row
// editor's save does to it (`syncCustomerRevision`) while this card is mounted.
const renderCard = (props: { canEdit?: boolean } = {}) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <Suspense fallback={null}>
          <CustomerRelationshipCard customerId={1001} {...props} />
        </Suspense>
      </QueryClientProvider>
    </MantineProvider>,
  );
  return queryClient;
};

/** The pills of a multi-select live in the same list as its search field. */
const pillsOf = (input: HTMLElement) => input.parentElement as HTMLElement;

// putsTo is every write this test made to one URL, found by method and URL —
// never "the last fetch": the tag vocabulary and the debounced user search land
// on their own clocks.
const putsTo = (fetchMock: ReturnType<typeof stubFetch>, url: string) =>
  fetchMock.mock.calls.filter(([u, init]) => String(u) === url && (init as RequestInit)?.method === "PUT");

describe("CustomerRelationshipCard", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders an owner and tags, and em dashes when there are none", async () => {
    stubFetch({ customer: ownedBody });
    renderCard();
    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("VIP")).toBeInTheDocument();

    cleanup();
    stubFetch();
    renderCard();
    await screen.findByText("Owner");
    expect(screen.queryByText("Kari Nordmann")).not.toBeInTheDocument();
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  it("sends the revision the cache holds now, not the one it first read", async () => {
    // This card is mounted beside every other editor of the customer row, and
    // their saves write the fresh revision into this very cache entry
    // (`syncCustomerRevision`). A card keeping its own copy would go on sending
    // the revision the server has already moved past and earn a 409 nobody
    // caused.
    const fetchMock = stubFetch();
    const queryClient = renderCard({ canEdit: true });
    const picker = await screen.findByRole("combobox", { name: "Owner" });

    syncCustomerRevision(queryClient, 1001, 7);

    await userEvent.click(picker);
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/owner")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/owner")[0];
    expect(JSON.parse(String((init as RequestInit).body)).revision).toBe(7);
  });

  it("says when the owner's account is inactive, and still names them", async () => {
    stubFetch({ customer: { ...ownedBody, owner: { userId: "u1", displayName: "Kari Nordmann", active: false } } });
    renderCard();
    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("Inactive")).toBeInTheDocument();
  });

  it("shows no editing affordance without canEdit", async () => {
    stubFetch({ customer: ownedBody });
    renderCard();
    await screen.findByText("Kari Nordmann");
    expect(screen.queryByRole("combobox", { name: "Owner" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Tags" })).not.toBeInTheDocument();
  });

  it("assigns an owner the debounced search found, and sends the revision it read", async () => {
    const fetchMock = stubFetch();
    renderCard({ canEdit: true });
    const picker = await screen.findByRole("combobox", { name: "Owner" });
    await userEvent.click(picker);
    await userEvent.type(picker, "Ola");
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/owner")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/owner")[0];
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ ownerUserId: "u2", revision: 3 });
  });

  it("keeps the current owner on the list even when the search would drop them", async () => {
    // The search answers only Ola; Kari is the owner and must stay selectable,
    // or the picker blanks the name it exists to show (projects' assignee
    // picker settled this).
    stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    const picker = await screen.findByRole("combobox", { name: "Owner" });
    expect(picker).toHaveValue("Kari Nordmann");
    await userEvent.click(picker);
    await userEvent.type(picker, "Ola");
    expect(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Kari Nordmann" })).toBeInTheDocument();
  });

  it("clears the owner with null", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    // Mantine's clear button is aria-hidden (the Select itself is clearable
    // through the keyboard), so it is found by its label, not by role. It
    // exists only once the picker has an owner to clear.
    await userEvent.click(await screen.findByLabelText("Clear owner"));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/owner")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/owner")[0];
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ ownerUserId: null, revision: 3 });
  });

  it("offers Reload after a revision conflict, and re-seeds the revision it will send next", async () => {
    let revision = 3;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/owner")) {
        if (revision === 3) {
          revision = 9;
          return Promise.resolve(jsonResponse({ title: "Customer revision conflict", status: 409 }, 409));
        }
        return Promise.resolve(jsonResponse({ ...customerBody, revision: 10 }));
      }
      if (url.startsWith("/api/v1/customers/assignable-users")) {
        return Promise.resolve(jsonResponse([{ userId: "u2", displayName: "Ola Nordmann" }]));
      }
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      return Promise.resolve(jsonResponse({ ...customerBody, revision }));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderCard({ canEdit: true });
    const picker = await screen.findByRole("combobox", { name: "Owner" });
    await userEvent.click(picker);
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));
    await screen.findByText("Reload");

    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    await userEvent.click(picker);
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(
        ([u, init]) => String(u) === "/api/v1/customers/1001/owner" && (init as RequestInit)?.method === "PUT",
      );
      expect(puts).toHaveLength(2);
      expect(JSON.parse(String((puts[1][1] as RequestInit).body)).revision).toBe(9);
    });
  });

  it("names the customer's own tags on their pills even when the vocabulary never answers", async () => {
    // A MultiSelect shows the raw VALUE of anything its `data` does not
    // describe, and a tag's value is a uuid — so the customer's own tags (which
    // carry name and colour on the customer read this card already made) seed
    // the option list, and the vocabulary only widens it.
    stubFetch({ customer: ownedBody, tagsFail: true });
    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("combobox", { name: "Tags" });

    expect(within(pillsOf(tagsInput)).getByText("VIP")).toBeInTheDocument();
  });

  it("replaces the tag set from the multi-select, sending every id at once", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("combobox", { name: "Tags" });
    await userEvent.click(tagsInput);
    await userEvent.click(await screen.findByRole("option", { name: "Prospect" }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/tags")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/tags")[0];
    // The whole set, not a delta: VIP was already on the customer.
    expect(JSON.parse(String((init as RequestInit).body)).tagIds.sort()).toEqual(["t1", "t2"]);
  });

  it("creates a tag on the fly and then puts it on the customer", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("combobox", { name: "Tags" });
    await userEvent.click(tagsInput);
    await userEvent.type(tagsInput, "Churned");
    await userEvent.click(await screen.findByRole("option", { name: 'Create "Churned"' }));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([u, init]) => String(u) === "/api/v1/customers/tags" && (init as RequestInit)?.method === "POST",
      );
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String((posts[0][1] as RequestInit).body))).toEqual({ name: "Churned", color: null });
    });
    await waitFor(() => {
      const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/tags")[0];
      // The whole set again: the tag just created AND the one the customer
      // already carried, or creating a tag would quietly drop the others.
      expect(JSON.parse(String((init as RequestInit).body)).tagIds.sort()).toEqual(["t1", "t3"]);
    });
  });

  it("attaches the tag that already holds the name when the create is refused", async () => {
    // Two people can type the same new name, and a vocabulary fetched a minute
    // ago need not know a tag that now exists. Either way the person asked for
    // this customer to carry a tag by that name, so the refusal is answered by
    // reading the vocabulary again and attaching the tag that holds it — not by
    // a red notification about a conflict they did not cause.
    let vocabularyReads = 0;
    const churned = { id: "t3", name: "Churned", color: null, customerCount: 4 };
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url === "/api/v1/customers/tags") {
        return Promise.resolve(jsonResponse({ title: "Tag already exists", status: 409, code: "tag_exists" }, 409));
      }
      if (init?.method === "PUT" && url.endsWith("/tags")) return Promise.resolve(jsonResponse({ tags: [] }));
      if (url.startsWith("/api/v1/customers/assignable-users")) return Promise.resolve(jsonResponse([]));
      if (url === "/api/v1/customers/tags") {
        vocabularyReads += 1;
        // The first read is the one the card rendered from — before the tag
        // existed. The re-read is where its id comes from.
        return Promise.resolve(jsonResponse(vocabularyReads === 1 ? tagRows : [...tagRows, churned]));
      }
      return Promise.resolve(jsonResponse(ownedBody));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("combobox", { name: "Tags" });
    await userEvent.click(tagsInput);
    // "churned" in lower case: the name is unique case-insensitively, so the
    // tag that holds it need not be spelled the way it was typed.
    await userEvent.type(tagsInput, "churned");
    await userEvent.click(await screen.findByRole("option", { name: 'Create "churned"' }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/tags")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/tags")[0];
    expect(JSON.parse(String((init as RequestInit).body)).tagIds.sort()).toEqual(["t1", "t3"]);
  });
});
