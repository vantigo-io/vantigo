import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { syncCustomerRevision } from "../api/customers";
import { CustomerRelationshipCard } from "./-customer-relationship-card";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

import { notifications } from "@mantine/notifications";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// customerBody is literally the wire body for a customer with nothing set:
// no owner key, no tags key and no group key at all (owner and tags design D1,
// D2; customer groups design D3 — each is omitted rather than sent as null/[]),
// which is the shape a component must survive.
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
  group: { id: "g1", name: "Retail" },
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

// `hold` names a PUT (by its URL's last segment) that never answers, so a test
// can look at the card while that save is still in flight.
const stubFetch = (
  options: { customer?: unknown; tagsFail?: boolean; groups?: unknown[]; hold?: "owner" | "group" } = {},
) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method === "PUT" && options.hold && url.endsWith(`/${options.hold}`))
      return new Promise<Response>(() => {});
    if (init?.method === "PUT" && url.endsWith("/owner"))
      return Promise.resolve(jsonResponse({ ...ownedBody, revision: 4 }));
    if (init?.method === "PUT" && url.endsWith("/group"))
      return Promise.resolve(jsonResponse({ ...ownedBody, group: { id: "g2", name: "Key accounts" }, revision: 4 }));
    if (init?.method === "PUT" && url.endsWith("/tags"))
      return Promise.resolve(
        jsonResponse({ tags: tagRows.map((tag) => ({ id: tag.id, name: tag.name, color: tag.color })) }),
      );
    if (init?.method === "POST")
      return Promise.resolve(jsonResponse({ id: "t3", name: "Churned", customerCount: 0 }, 201));
    if (url.startsWith("/api/v1/customers/assignable-users")) {
      return Promise.resolve(jsonResponse([{ userId: "u2", displayName: "Ola Nordmann" }]));
    }
    if (url === "/api/v1/customers/groups") return Promise.resolve(jsonResponse(options.groups ?? groupRows));
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
    vi.clearAllMocks();
  });

  it("names the owner and shows the tag chips", async () => {
    stubFetch({ customer: ownedBody });
    renderCard();
    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("VIP")).toBeInTheDocument();
  });

  it("shows em dashes when there is no owner and no tags", async () => {
    // `customerBody` carries neither key at all, which is what the wire sends.
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
    expect(screen.queryByRole("combobox", { name: "Group" })).not.toBeInTheDocument();
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

  it("names the owner the PUT answered without reading the customer again", async () => {
    // The owner PUT answers the WHOLE customer, so the fresh row is already in
    // hand: writing it into this query's cache is what moves the Owner row
    // without a round trip. The customer GET is made to fail from the moment the
    // PUT has answered, so a card that still leaned on a refetch could not show
    // the name at all — and the answered name is deliberately not the label of
    // the option that was picked, so the text on screen can only have come from
    // the response body.
    let putAnswered = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/owner")) {
        putAnswered = true;
        return Promise.resolve(
          jsonResponse({
            ...customerBody,
            revision: 4,
            owner: { userId: "u2", displayName: "Siri Haugen", active: true },
          }),
        );
      }
      if (url.startsWith("/api/v1/customers/assignable-users"))
        return Promise.resolve(jsonResponse([{ userId: "u2", displayName: "Ola Nordmann" }]));
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      return Promise.resolve(putAnswered ? jsonResponse({ title: "Boom" }, 500) : jsonResponse(customerBody));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderCard({ canEdit: true });
    const picker = await screen.findByRole("combobox", { name: "Owner" });
    await userEvent.click(picker);
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));

    expect(await screen.findByText("Siri Haugen")).toBeInTheDocument();
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

  it("shows the tag set the PUT answered without reading the customer again", async () => {
    // The set replace answers the customer's tags, so the chips and the pills
    // can move on that body alone; the customer GET fails from the moment the
    // PUT has answered to prove no refetch is what moved them. The answered set
    // is deliberately not the one the request named — the server's answer is
    // what counts, and a tag renamed a second ago is exactly how they differ.
    let putAnswered = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/tags")) {
        putAnswered = true;
        return Promise.resolve(jsonResponse({ tags: [{ id: "t9", name: "Key account", color: "teal" }] }));
      }
      if (url.startsWith("/api/v1/customers/assignable-users")) return Promise.resolve(jsonResponse([]));
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      return Promise.resolve(putAnswered ? jsonResponse({ title: "Boom" }, 500) : jsonResponse(ownedBody));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("combobox", { name: "Tags" });
    await userEvent.click(tagsInput);
    await userEvent.click(await screen.findByRole("option", { name: "Prospect" }));

    await waitFor(() => expect(within(pillsOf(tagsInput)).getByText("Key account")).toBeInTheDocument());
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

  it("shows no group for a customer that belongs to none, and offers the vocabulary to a caller who may edit", async () => {
    stubFetch();
    renderCard({ canEdit: true });
    // The wire body has no `group` key at all: absent, not null (design D3) — and
    // a Mantine Select's input renders the LABEL of the option matching its
    // value, so an empty value with a "No group" row reads as "No group", never
    // as "" (the owner picker's "keeps the current owner" case asserts the same).
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("No group"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    expect(await screen.findByRole("option", { name: "Retail" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "No group" })).toBeInTheDocument();
  });

  it("saves a group through its own PUT and takes the answered customer into the cache", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    const queryClient = renderCard({ canEdit: true });
    // The billing profile's revision IS the row's, and nothing on this page
    // observes it, so its cached copy only moves if the save carries the
    // answered revision there itself (`syncCustomerRevision`) — the broad
    // invalidation merely marks an unobserved entry stale.
    queryClient.setQueryData(["customers", 1001, "billing-profile"], { revision: 3 });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "Key accounts" }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/group")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/group")[0];
    // The revision is read off the query, never a private copy: a sibling
    // editor's save moves it under this card between renders.
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ groupId: "g2", revision: 3 });
    await waitFor(() =>
      expect((queryClient.getQueryData(["customers", 1001]) as { group: { name: string } }).group.name).toBe(
        "Key accounts",
      ),
    );
    expect((queryClient.getQueryData(["customers", 1001, "billing-profile"]) as { revision: number }).revision).toBe(4);
  });

  it("raises the conflict alert when the group save loses a revision race", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/group"))
        return Promise.resolve(jsonResponse({ title: "Customer revision conflict" }, 409));
      if (url === "/api/v1/customers/groups") return Promise.resolve(jsonResponse(groupRows));
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      if (url.startsWith("/api/v1/customers/assignable-users")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse(ownedBody));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderCard({ canEdit: true });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "No group" }));
    expect(await screen.findByText("Customer changed")).toBeInTheDocument();
  });
  it("removes the customer from its group with null, not the No-group row's empty value", async () => {
    // "No group" is the "" sentinel inside the Select; "" on the wire would be
    // an invalid uuid, so the mapping to null is what this pins.
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "No group" }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/group")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/group")[0];
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ groupId: null, revision: 3 });
  });

  it("keeps the customer's own group on offer when the vocabulary no longer holds it", async () => {
    // Retail is the customer's group but the vocabulary answered without it (a
    // delete racing this read): the field must still name it, not read blank.
    stubFetch({ customer: ownedBody, groups: [groupRows[1]] });
    renderCard({ canEdit: true });
    await userEvent.click(await screen.findByRole("combobox", { name: "Group" }));
    // Key accounts on offer is the vocabulary having landed.
    expect(await screen.findByRole("option", { name: "Key accounts" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Retail" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail");
  });

  it("holds both revision-carrying controls while either save is in flight", async () => {
    // The owner and the group both send the revision read off the query; a
    // second save sent before the first answers carries the revision the first
    // is about to replace, and earns a 409 nobody else caused.
    stubFetch({ customer: ownedBody, hold: "owner" });
    renderCard({ canEdit: true });
    await userEvent.click(await screen.findByLabelText("Clear owner"));
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toBeDisabled());
    cleanup();

    stubFetch({ customer: ownedBody, hold: "group" });
    renderCard({ canEdit: true });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "Key accounts" }));
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Owner" })).toBeDisabled());
  });

  it("names the field's reason when the group is gone by the time it is saved, and reads the vocabulary again", async () => {
    // A group deleted between the vocabulary read and the save is a field error
    // on groupId (the customer exists; the body is what is wrong). Its title is
    // the problem's, so the field message is the one worth showing — and the
    // vocabulary is read again so the vanished group stops being offered.
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/group"))
        return Promise.resolve(
          jsonResponse(
            {
              title: "Invalid customer group",
              status: 400,
              errors: { groupId: ["Customer group g2 does not exist"] },
            },
            400,
          ),
        );
      if (url === "/api/v1/customers/groups") return Promise.resolve(jsonResponse(groupRows));
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      if (url.startsWith("/api/v1/customers/assignable-users")) return Promise.resolve(jsonResponse([]));
      return Promise.resolve(jsonResponse(ownedBody));
    });
    vi.stubGlobal("fetch", fetchMock);
    const vocabularyReads = () =>
      fetchMock.mock.calls.filter(([u, init]) => String(u) === "/api/v1/customers/groups" && !init?.method).length;
    renderCard({ canEdit: true });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "Key accounts" }));

    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({ color: "red", message: "Customer group g2 does not exist" }),
      ),
    );
    await waitFor(() => expect(vocabularyReads()).toBe(2));
  });
});
