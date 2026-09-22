import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { formatDate } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { customerBillingProfileQueryOptions } from "../api/billing-profile";
import { stubFetch } from "../test/fetch";
import { CustomerPeppolStatus } from "./-customer-peppol-status";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const emptyProfile = {
  invoiceEmail: null,
  reminderEmail: null,
  paymentTermsDays: null,
  currency: null,
  language: null,
  invoiceDelivery: null,
  reminderDelivery: null,
  peppolId: "0192:923609016",
  gln: null,
  buyerReference: null,
  revision: 3,
  warnings: [],
};

/** What the server sends for a customer that has never been checked: no peppolLookup key at all (design D3). */
const neverCheckedProfile = { revision: 3, warnings: [] };

const registeredWithInvoice = {
  status: "registered",
  canReceiveInvoice: true,
  canReceiveCreditNote: true,
  checkedAt: "2026-09-21T10:00:00Z",
  participantId: "0192:923609016",
  smpHost: "smp.example.test",
};

/** The withheld-participantId shape design D3 describes: an explicit lookup answer without the id. */
const registeredWithheldParticipant = {
  status: "registered",
  canReceiveInvoice: true,
  canReceiveCreditNote: true,
  checkedAt: "2026-09-21T10:00:00Z",
  smpHost: "smp.example.test",
};

/** Fetch calls filtered by method + URL — never "the last fetch" (global constraint). */
const callsTo = (fetchMock: ReturnType<typeof vi.fn>, path: string, method?: string) =>
  fetchMock.mock.calls.filter(
    ([url, init]) => String(url) === path && ((init as RequestInit | undefined)?.method ?? "GET") === (method ?? "GET"),
  );

/**
 * Renders `CustomerPeppolStatus` behind a live `useQuery` on the same key
 * the real `CustomerBillingCard` uses, the way it is actually mounted — a
 * refetch triggered by the mutation's own invalidation must reach the
 * component through this observer, not through a prop the test edits by
 * hand.
 */
const Harness = ({ canManageBilling = true }: { canManageBilling?: boolean }) => {
  const { data } = useQuery(customerBillingProfileQueryOptions(1001));
  if (!data) return null;
  return <CustomerPeppolStatus customerId={1001} profile={data} canManageBilling={canManageBilling} />;
};

const renderStatus = (
  fetchMock: ReturnType<typeof vi.fn>,
  { canManageBilling = true, queryClient }: { canManageBilling?: boolean; queryClient?: QueryClient } = {},
) => {
  stubFetch(fetchMock);
  const client =
    queryClient ?? new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={client}>
        <Harness canManageBilling={canManageBilling} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { fetchMock, queryClient: client };
};

describe("CustomerPeppolStatus", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows nothing but the action when the customer was never checked", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, neverCheckedProfile));
    renderStatus(fetchMock);

    expect(await screen.findByRole("button", { name: "Check EHF" })).toBeInTheDocument();
    expect(screen.queryByText(/checked/i)).not.toBeInTheDocument();
  });

  it("shows 'Can receive EHF invoices' with the date and the SMP host for registered + canReceiveInvoice", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithInvoice }));
    renderStatus(fetchMock);

    const expectedDate = formatDate(registeredWithInvoice.checkedAt);
    expect(await screen.findByText(`Can receive EHF invoices — checked ${expectedDate}`)).toBeInTheDocument();
    expect(screen.getByText("via SMP smp.example.test")).toBeInTheDocument();
  });

  it("shows 'Registered in Peppol, but not for invoices' when registered without invoice capability", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        ...emptyProfile,
        peppolLookup: { ...registeredWithInvoice, canReceiveInvoice: false },
      }),
    );
    renderStatus(fetchMock);

    const expectedDate = formatDate(registeredWithInvoice.checkedAt);
    expect(
      await screen.findByText(`Registered in Peppol, but not for invoices — checked ${expectedDate}`),
    ).toBeInTheDocument();
  });

  it("shows 'Not registered in Peppol' for a not_registered answer", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        ...emptyProfile,
        peppolLookup: {
          ...registeredWithInvoice,
          status: "not_registered",
          canReceiveInvoice: false,
          canReceiveCreditNote: false,
        },
      }),
    );
    renderStatus(fetchMock);

    const expectedDate = formatDate(registeredWithInvoice.checkedAt);
    expect(await screen.findByText(`Not registered in Peppol — checked ${expectedDate}`)).toBeInTheDocument();
  });

  it("reads a stored answer whose participantId the server withheld (no legal-identity-view permission)", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithheldParticipant }));
    renderStatus(fetchMock);

    const expectedDate = formatDate(registeredWithInvoice.checkedAt);
    expect(await screen.findByText(`Can receive EHF invoices — checked ${expectedDate}`)).toBeInTheDocument();
  });

  it("shows no Check EHF action without canManageBilling", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, neverCheckedProfile));
    renderStatus(fetchMock, { canManageBilling: false });

    await waitFor(() => expect(callsTo(fetchMock, "/api/v1/customers/1001/billing-profile").length).toBe(1));
    expect(screen.queryByRole("button", { name: "Check EHF" })).not.toBeInTheDocument();
  });

  it("POSTs once, disables the action while pending, then shows the refetched answer", async () => {
    let resolvePost!: (value: Response) => void;
    const postPromise = new Promise<Response>((resolve) => {
      resolvePost = resolve;
    });
    let billingGets = 0;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") return postPromise;
      if (path === "/api/v1/customers/1001/billing-profile") {
        billingGets += 1;
        return Promise.resolve(
          jsonResponse(
            200,
            billingGets === 1 ? neverCheckedProfile : { ...emptyProfile, peppolLookup: registeredWithInvoice },
          ),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    const button = await screen.findByRole("button", { name: "Check EHF" });
    await userEvent.click(button);

    expect(button).toBeDisabled();
    expect(callsTo(fetchMock, "/api/v1/customers/1001/peppol-lookup", "POST")).toHaveLength(1);

    resolvePost(jsonResponse(200, registeredWithInvoice));

    const expectedDate = formatDate(registeredWithInvoice.checkedAt);
    expect(await screen.findByText(`Can receive EHF invoices — checked ${expectedDate}`)).toBeInTheDocument();
    expect(callsTo(fetchMock, "/api/v1/customers/1001/peppol-lookup", "POST")).toHaveLength(1);
    expect(callsTo(fetchMock, "/api/v1/customers/1001/billing-profile")).toHaveLength(2);
  });

  it("shows a dimmed note on no_identifier, without asking the billing profile GET again", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(200, {
            status: "no_identifier",
            canReceiveInvoice: false,
            canReceiveCreditNote: false,
            checkedAt: "2026-09-21T10:00:00Z",
          }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, neverCheckedProfile));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Check EHF" }));

    expect(
      await screen.findByText("Nothing to look up — no Peppol ID or Norwegian organisation number"),
    ).toBeInTheDocument();
    expect(callsTo(fetchMock, "/api/v1/customers/1001/billing-profile")).toHaveLength(1);
  });

  it("keeps the card intact and shows a retry message on a 502", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(502, { title: "Peppol lookup unavailable", status: 502, detail: "Could not reach Peppol." }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, neverCheckedProfile));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    const button = await screen.findByRole("button", { name: "Check EHF" });
    await userEvent.click(button);

    expect(await screen.findByText("The Peppol network could not be reached. Try again.")).toBeInTheDocument();
    expect(button).toBeInTheDocument();
    expect(button).not.toBeDisabled();
  });

  it("explains the ehf_available offer with a Use EHF action, and hides it once EHF is switched on", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        const sent = JSON.parse(String(init.body));
        expect(sent).toEqual({
          invoiceEmail: null,
          reminderEmail: null,
          paymentTermsDays: null,
          currency: null,
          language: null,
          invoiceDelivery: "ehf",
          reminderDelivery: null,
          peppolId: "0192:923609016",
          gln: null,
          buyerReference: null,
          revision: 3,
        });
        return Promise.resolve(
          jsonResponse(200, {
            ...emptyProfile,
            invoiceDelivery: "ehf",
            peppolLookup: registeredWithInvoice,
            revision: 4,
            warnings: [],
          }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(
          jsonResponse(200, { ...emptyProfile, warnings: ["ehf_available"], peppolLookup: registeredWithInvoice }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queryClient.setQueryData(["customers", 1001], { id: 1001, revision: 3 });
    renderStatus(fetchMock, { queryClient });

    expect(await screen.findByText("This customer can receive EHF invoices")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Use EHF" }));

    await waitFor(() => expect(queryClient.getQueryData<{ revision: number }>(["customers", 1001])?.revision).toBe(4));
    expect(screen.queryByText("This customer can receive EHF invoices")).not.toBeInTheDocument();
  });

  it("shows the delivery-A conflict wording on a 409, and Reload carries the fresh revision into the next PUT", async () => {
    let conflicted = false;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        const sent = JSON.parse(String(init.body)) as { revision?: number };
        if (sent.revision !== 4) {
          conflicted = true;
          return Promise.resolve(
            jsonResponse(409, {
              title: "Customer revision conflict",
              detail: "The customer was changed by someone else.",
              status: 409,
            }),
          );
        }
        return Promise.resolve(
          jsonResponse(200, {
            ...emptyProfile,
            invoiceDelivery: "ehf",
            peppolLookup: registeredWithInvoice,
            revision: 5,
            warnings: [],
          }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(
          jsonResponse(
            200,
            conflicted
              ? { ...emptyProfile, warnings: ["ehf_available"], peppolLookup: registeredWithInvoice, revision: 4 }
              : { ...emptyProfile, warnings: ["ehf_available"], peppolLookup: registeredWithInvoice },
          ),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queryClient.setQueryData(["customers", 1001], { id: 1001, revision: 3 });
    renderStatus(fetchMock, { queryClient });

    await userEvent.click(await screen.findByRole("button", { name: "Use EHF" }));

    expect(
      await screen.findByText("This customer was changed by someone else. Reload to see the latest version."),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /reload/i }));
    await waitFor(() => expect(screen.queryByText(/changed by someone else/)).not.toBeInTheDocument());

    await userEvent.click(await screen.findByRole("button", { name: "Use EHF" }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === "/api/v1/customers/1001/billing-profile" &&
          (init as RequestInit | undefined)?.method === "PUT",
      );
      expect(puts.length).toBe(2);
      expect(JSON.parse(String((puts.at(-1) as [string, RequestInit])[1].body)).revision).toBe(4);
    });
  });
});

describe("CustomerPeppolStatus — 503 disables the action for the rest of the session", () => {
  afterEach(() => vi.unstubAllGlobals());

  // Last in the file deliberately: the flag this sets is module-level (not
  // component state), on purpose — the installation setting it reflects
  // does not change per customer or per mount, so a fresh card must not
  // offer the action back. That also means it outlives this test.
  it("hides Check EHF and shows a dimmed note after a 503, even for a freshly mounted card", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(503, { title: "Peppol lookup disabled", status: 503, detail: "Peppol lookup is disabled." }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, neverCheckedProfile));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Check EHF" }));

    expect(await screen.findByText("Peppol lookup is switched off on this installation")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Check EHF" })).not.toBeInTheDocument();

    // A fresh mount (e.g. navigating to a different customer's page) must
    // not ask the server again — the flag it set is for the session, not
    // this one component instance.
    const fetchMock2 = vi.fn().mockResolvedValue(jsonResponse(200, neverCheckedProfile));
    renderStatus(fetchMock2);

    await waitFor(() => expect(callsTo(fetchMock2, "/api/v1/customers/1001/billing-profile").length).toBe(1));
    expect(screen.getAllByText("Peppol lookup is switched off on this installation").length).toBeGreaterThan(0);
    expect(callsTo(fetchMock2, "/api/v1/customers/1001/peppol-lookup", "POST")).toHaveLength(0);
  });
});
