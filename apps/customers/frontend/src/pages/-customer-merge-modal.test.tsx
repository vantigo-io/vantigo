import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type CustomerResponse,
  customerQueryOptions,
  normalizeCustomer,
  type RawCustomerResponse,
} from "../api/customers";
import { stubFetch } from "../test/fetch";
import { CustomerMergeModal } from "./-customer-merge-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// Literally the body the server sends: nothing unset is on the wire, so
// timelineSummary carries no latestOccurredOn until there is one.
const wire = (overrides: Record<string, unknown> = {}) => ({
  id: 1002,
  customerNumber: 2,
  name: "Acme AS",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 3 },
  revision: 4,
  ...overrides,
});
const survivor = (overrides: Record<string, unknown> = {}): CustomerResponse =>
  normalizeCustomer(wire(overrides) as RawCustomerResponse);
const listOf = (...customers: unknown[]) => ({
  data: customers,
  pagination: {
    page: 1,
    pageSize: 20,
    totalCount: customers.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});
const duplicate = wire({ id: 1005, customerNumber: 5, name: "Acme Norge AS", status: "archived", revision: 7 });

/** The page's own read of the survivor, which the modal sits on: a refetch of it is what a stale revision needs. */
const SurvivorPage = () => {
  useQuery(customerQueryOptions(1002));
  return null;
};

const renderModal = (customer: CustomerResponse, onRequest: (url: string, init?: RequestInit) => Promise<Response>) => {
  const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) =>
    String(url) === "/api/v1/customers/1002" && !init?.method
      ? Promise.resolve(jsonResponse(200, wire()))
      : onRequest(String(url), init),
  );
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <SurvivorPage />
        <CustomerMergeModal customer={customer} opened onClose={vi.fn()} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

/** The GETs of one URL fetch saw, counted by method and URL. */
const reads = (fetchMock: ReturnType<typeof vi.fn>, path: string | RegExp) =>
  fetchMock.mock.calls.filter(
    ([url, init]) =>
      !(init as RequestInit | undefined)?.method &&
      (typeof path === "string" ? String(url) === path : path.test(String(url))),
  ).length;

/** The merge POSTs fetch saw — never "the last fetch": the picker's search runs on its own clock. */
const mergePosts = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls.filter(
    ([url, init]) =>
      String(url) === "/api/v1/customers/1002/merge" && (init as RequestInit | undefined)?.method === "POST",
  );

const pick = async (name: string) => {
  await userEvent.click(screen.getByRole("combobox", { name: /duplicate to merge/i }));
  await userEvent.click(await screen.findByRole("option", { name }));
};

describe("CustomerMergeModal", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("says what will happen, merges with this customer's revision, and shows what moved", async () => {
    const fetchMock = renderModal(survivor(), (_url, init) =>
      Promise.resolve(
        init?.method === "POST"
          ? jsonResponse(200, {
              customer: wire({ revision: 5 }),
              moved: [
                { kind: "customers.contacts", count: 3 },
                { kind: "customers.addresses", count: 0 },
                { kind: "customers.timelineEntries", count: 14 },
                { kind: "customers.tags", count: 0 },
                { kind: "projects.projects", count: 2 },
                { kind: "somewhere.else", count: 1 },
              ],
            })
          : jsonResponse(200, listOf(duplicate)),
      ),
    );

    await pick("#5 Acme Norge AS · Archived");
    expect(
      screen.getByText(/^The contacts, addresses, timeline and tags of #5 Acme Norge AS, and what other modules hold/),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    await waitFor(() => expect(mergePosts(fetchMock)).toHaveLength(1));
    expect(mergePosts(fetchMock)[0][1]).toEqual({
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sourceId: 1005, revision: 4 }),
    });
    expect(await screen.findByText("#5 Acme Norge AS was merged into this customer.")).toBeInTheDocument();
    for (const moved of ["3 contacts", "14 timeline entries", "2 projects", "1 × somewhere.else"]) {
      expect(screen.getByText(moved)).toBeInTheDocument();
    }
    expect(screen.queryByText(/^0 /)).not.toBeInTheDocument();
  });

  it("warns, and will not merge, a customer of the other type", async () => {
    const person = wire({ id: 1006, customerNumber: 6, name: "Kari Nordmann", type: "person" });
    const fetchMock = renderModal(survivor(), () => Promise.resolve(jsonResponse(200, listOf(person))));

    await pick("#6 Kari Nordmann");

    expect(
      screen.getByText(/^#6 Kari Nordmann is a private person and this customer is a business\./),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
    expect(mergePosts(fetchMock)).toHaveLength(0);
  });

  it("will not merge into an archived customer", async () => {
    renderModal(survivor({ status: "archived" }), () => Promise.resolve(jsonResponse(200, listOf(duplicate))));

    expect(
      screen.getByText("This customer is archived. Restore it before merging another customer into it."),
    ).toBeInTheDocument();
    await pick("#5 Acme Norge AS · Archived");
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
  });

  it.each([
    ["merge_self", "Customer merged into itself", "A customer cannot be merged into itself."],
    [
      "merge_type_mismatch",
      "Customer types differ",
      "The two customers are of different types. Change one of their types first.",
    ],
    [
      "merge_into_archived",
      "Customer is archived",
      "This customer is archived. Restore it before merging another customer into it.",
    ],
    ["customer_merged", "Customer was merged", "This customer was merged into another"],
  ])("names the server's %s refusal in its own words, and refetches what the page shows", async (code, title, said) => {
    const fetchMock = renderModal(survivor(), (_url, init) =>
      Promise.resolve(
        init?.method === "POST"
          ? jsonResponse(409, { title, status: 409, code, detail: "The server's own English detail." })
          : jsonResponse(200, listOf(duplicate)),
      ),
    );

    await pick("#5 Acme Norge AS · Archived");
    await waitFor(() => expect(reads(fetchMock, "/api/v1/customers/1002")).toBe(1));
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The customers could not be merged");
    expect(alert).toHaveTextContent(said);
    expect(alert).not.toHaveTextContent("The server's own English detail.");
    await waitFor(() => expect(reads(fetchMock, "/api/v1/customers/1002")).toBe(2));
  });

  it("names a pick merged away meanwhile, drops it, and refetches the list that offered it", async () => {
    const fetchMock = renderModal(survivor(), (_url, init) =>
      Promise.resolve(
        init?.method === "POST"
          ? jsonResponse(409, {
              title: "Customer already merged",
              status: 409,
              code: "merge_already_merged",
              detail: "#5 Acme Norge AS was already merged into #3 Acme Holding AS.",
            })
          : jsonResponse(200, listOf(duplicate)),
      ),
    );

    await pick("#5 Acme Norge AS · Archived");
    const listReads = reads(fetchMock, /^\/api\/v1\/customers\?/);
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    expect(await screen.findByText("That customer has already been merged into another one.")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: /duplicate to merge/i })).toHaveValue("");
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
    await waitFor(() => expect(reads(fetchMock, /^\/api\/v1\/customers\?/)).toBeGreaterThan(listReads));
  });

  it("answers a stale revision as the page's other writes do: refetch, and say to press Merge again", async () => {
    const fetchMock = renderModal(survivor(), (_url, init) =>
      Promise.resolve(
        init?.method === "POST"
          ? jsonResponse(409, { title: "Customer revision conflict", status: 409, detail: "Revision 4 is stale." })
          : jsonResponse(200, listOf(duplicate)),
      ),
    );

    await pick("#5 Acme Norge AS · Archived");
    await waitFor(() => expect(reads(fetchMock, "/api/v1/customers/1002")).toBe(1));
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Customer changed");
    expect(alert).toHaveTextContent("Check the page — it now shows the latest version — and press Merge again.");
    expect(alert).not.toHaveTextContent("The customers could not be merged");
    await waitFor(() => expect(reads(fetchMock, "/api/v1/customers/1002")).toBe(2));
    // The pick stays: nothing about it was wrong.
    expect(screen.getByRole("button", { name: "Merge" })).toBeEnabled();
  });
});
