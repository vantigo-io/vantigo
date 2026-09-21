import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerDetailHeader, CustomerOverview } from "./customers.$customerId";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (revision: number, email: string | null) => ({
  id: 1001,
  customerNumber: 5001,
  name: "Equinor",
  status: "active",
  type: "business",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  identity: null,
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision,
  contactInfo: email ? { email } : {},
});

/** A GET that never answers — the round trip a second editor is opened inside. */
const pending = () => new Promise<Response>(() => {});

/** The last PUT to a given path fetch saw — never "the last fetch": other GETs land after it. */
const lastPutTo = (fetchMock: ReturnType<typeof vi.fn>, path: string) =>
  fetchMock.mock.calls
    .filter(([url, init]) => String(url) === path && (init as RequestInit | undefined)?.method === "PUT")
    .at(-1);

const putBody = (call: unknown[]) => JSON.parse(String((call[1] as RequestInit).body));

const renderPage = (fetchMock: ReturnType<typeof vi.fn>) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <Suspense fallback="loading">
            <CustomerDetailHeader customerId={1001} />
            <CustomerOverview customerId={1001} canEdit canManageBilling />
          </Suspense>
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
};

/**
 * One customer row, one revision (design D5), four editors — and two cache
 * entries holding it, since the billing profile's revision *is* the row's
 * (design D4). A save answers the fresh revision; the editor opened straight
 * after it must send that one, not the one the save replaced, even though
 * the refetches the save triggers have not come back yet.
 */
describe("the customer row's revision across its editors", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("carries a contact-info save's new revision into the billing modal opened right after it", async () => {
    let billingGets = 0;
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/contact-info" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(200, customer(4, "new@acme.test")));
      }
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(200, { revision: 5, warnings: [] }));
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        billingGets += 1;
        // Only the first GET answers: the billing profile the modal seeds
        // itself from is then whatever the contact-info save left in the
        // cache, which is the point of the test.
        return billingGets === 1 ? Promise.resolve(jsonResponse(200, { revision: 3, warnings: [] })) : pending();
      }
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, customer(4, "new@acme.test")));
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.includes("/contacts")) return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderPage(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Edit contact details" }));
    const contactDialog = await screen.findByRole("dialog");
    await userEvent.type(within(contactDialog).getByLabelText(/^email/i), "new@acme.test");
    await userEvent.click(within(contactDialog).getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await userEvent.click(await screen.findByLabelText("Edit billing profile"));
    const billingDialog = await screen.findByRole("dialog");
    await userEvent.click(within(billingDialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(lastPutTo(fetchMock, "/api/v1/customers/1001/billing-profile")).toBeTruthy());
    expect(putBody(lastPutTo(fetchMock, "/api/v1/customers/1001/billing-profile") as unknown[]).revision).toBe(4);
  });

  it("carries a billing save's new revision into the customer form opened right after it", async () => {
    let customerGets = 0;
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(200, { revision: 4, warnings: [] }));
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(jsonResponse(200, { revision: 3, warnings: [] }));
      }
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(200, customer(5, null)));
      }
      if (path === "/api/v1/customers/1001") {
        customerGets += 1;
        // As above: the form modal opens on the customer the billing save
        // left in the cache, not on a refetch that has yet to answer.
        return customerGets === 1 ? Promise.resolve(jsonResponse(200, customer(3, null))) : pending();
      }
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.includes("/contacts")) return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderPage(fetchMock);

    await userEvent.click(await screen.findByLabelText("Edit billing profile"));
    const billingDialog = await screen.findByRole("dialog");
    await userEvent.click(within(billingDialog).getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await userEvent.click(await screen.findByRole("button", { name: /edit customer/i }));
    const formDialog = await screen.findByRole("dialog");
    await userEvent.click(within(formDialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(lastPutTo(fetchMock, "/api/v1/customers/1001")).toBeTruthy());
    expect(putBody(lastPutTo(fetchMock, "/api/v1/customers/1001") as unknown[]).revision).toBe(4);
  });
});
