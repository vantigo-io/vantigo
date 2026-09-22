import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { formatDate } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { customerBillingProfileQueryOptions } from "../api/billing-profile";
import { stubFetch } from "../test/fetch";
import { CustomerEhfOffer, CustomerPeppolStatus, resetPeppolLookupDisabledForSession } from "./-customer-peppol-status";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** The answer line's date, formatted exactly as the block formats it — date and time, so a same-day re-check reads as new. */
const checkedOn = (checkedAt: string) => formatDate(checkedAt, { dateStyle: "medium", timeStyle: "short" });

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
 * Renders both halves of the Peppol/EHF surface behind a live `useQuery` on
 * the same key the real `CustomerBillingCard` uses, in the same order the card
 * mounts them (the offer at the top, the answer inside the Peppol ID row) — a
 * refetch triggered by a mutation's own invalidation must reach them through
 * this observer, not through a prop the test edits by hand.
 */
const Harness = ({ canManageBilling = true }: { canManageBilling?: boolean }) => {
  const { data } = useQuery(customerBillingProfileQueryOptions(1001));
  if (!data) return null;
  return (
    <>
      <CustomerEhfOffer customerId={1001} profile={data} canManageBilling={canManageBilling} />
      <CustomerPeppolStatus customerId={1001} profile={data} canManageBilling={canManageBilling} />
    </>
  );
};

const renderStatus = (
  fetchMock: ReturnType<typeof vi.fn>,
  { canManageBilling = true, queryClient }: { canManageBilling?: boolean; queryClient?: QueryClient } = {},
) => {
  stubFetch(fetchMock);
  const client =
    queryClient ?? new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const { container } = render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={client}>
        {/* A mount point of the test's own: "the block rendered nothing at
            all" has to be assertable without Mantine's injected <style> tags
            counting as content. */}
        <div id="peppol-mount">
          <Harness canManageBilling={canManageBilling} />
        </div>
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { fetchMock, queryClient: client, mount: container.querySelector("#peppol-mount") as HTMLElement };
};

describe("CustomerPeppolStatus", () => {
  // The 503 note below is remembered for the whole session on purpose
  // (module-level, not component state), which means it also outlives the
  // test that sets it — every test here starts from "not disabled".
  beforeEach(resetPeppolLookupDisabledForSession);
  afterEach(() => vi.unstubAllGlobals());

  it("shows nothing but the action when the customer was never checked", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, neverCheckedProfile));
    renderStatus(fetchMock);

    expect(await screen.findByRole("button", { name: "Check EHF" })).toBeInTheDocument();
    expect(screen.queryByText(/checked/i)).not.toBeInTheDocument();
  });

  it("shows 'Can receive EHF invoices' with the date and time, the identifier and the SMP host", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithInvoice }));
    renderStatus(fetchMock);

    expect(
      await screen.findByText(`Can receive EHF invoices — checked ${checkedOn(registeredWithInvoice.checkedAt)}`),
    ).toBeInTheDocument();
    expect(screen.getByText("Looked up 0192:923609016")).toBeInTheDocument();
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

    expect(
      await screen.findByText(
        `Registered in Peppol, but not for invoices — checked ${checkedOn(registeredWithInvoice.checkedAt)}`,
      ),
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

    expect(
      await screen.findByText(`Not registered in Peppol — checked ${checkedOn(registeredWithInvoice.checkedAt)}`),
    ).toBeInTheDocument();
  });

  it("claims nothing about a status this version does not know", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        ...emptyProfile,
        peppolLookup: { ...registeredWithInvoice, status: "something_new", canReceiveInvoice: false },
      }),
    );
    renderStatus(fetchMock);

    // The action is still there, so the block rendered — it simply says
    // nothing about an answer it cannot put into words (never "not registered").
    expect(await screen.findByRole("button", { name: "Check EHF" })).toBeInTheDocument();
    expect(screen.queryByText(/registered/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/checked/i)).not.toBeInTheDocument();
  });

  it("reads a stored answer whose participantId the server withheld (no legal-identity-view permission)", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithheldParticipant }));
    renderStatus(fetchMock);

    expect(
      await screen.findByText(`Can receive EHF invoices — checked ${checkedOn(registeredWithInvoice.checkedAt)}`),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Looked up/)).not.toBeInTheDocument();
  });

  it("shows no Check EHF action without canManageBilling", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithInvoice }));
    renderStatus(fetchMock, { canManageBilling: false });

    // A fixture with a stored answer, so there is something to wait for
    // before concluding the button is absent rather than merely not drawn yet.
    expect(
      await screen.findByText(`Can receive EHF invoices — checked ${checkedOn(registeredWithInvoice.checkedAt)}`),
    ).toBeInTheDocument();
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

    expect(
      await screen.findByText(`Can receive EHF invoices — checked ${checkedOn(registeredWithInvoice.checkedAt)}`),
    ).toBeInTheDocument();
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

    await screen.findByText("The Peppol network could not be reached. Try again.");
    // Announced, not just drawn: the note lives in the block's live region.
    expect(
      within(screen.getByRole("status")).getByText("The Peppol network could not be reached. Try again."),
    ).toBeInTheDocument();
    expect(button).toBeInTheDocument();
    expect(button).not.toBeDisabled();
  });

  it("explains the ehf_available offer with a Use EHF action, and hides it once EHF is switched on", async () => {
    // The body is captured and asserted after the interaction, never inside
    // the mock: an expectation that throws in there would surface as a failed
    // request rather than as the mismatch it is.
    let sentBody: unknown;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        sentBody = JSON.parse(String(init.body));
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
    expect(sentBody).toEqual({
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

  it("forgets a failed reload, so an old 'Could not reload' does not haunt the next conflict", async () => {
    let billingGets = 0;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        return Promise.resolve(
          jsonResponse(409, {
            title: "Customer revision conflict",
            detail: "The customer was changed by someone else.",
            status: 409,
          }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        billingGets += 1;
        // The reload's own fetch (the second GET) fails; the card keeps the
        // data it already had.
        if (billingGets === 2) return Promise.resolve(jsonResponse(500, { title: "Boom", status: 500 }));
        return Promise.resolve(
          jsonResponse(200, { ...emptyProfile, warnings: ["ehf_available"], peppolLookup: registeredWithInvoice }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Use EHF" }));
    await screen.findByText("This customer was changed by someone else. Reload to see the latest version.");

    await userEvent.click(screen.getByRole("button", { name: /reload/i }));
    expect(await screen.findByText("Could not reload. Try again.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Use EHF" }));

    // The new conflict is the new conflict: the failure of a reload nobody
    // asked for yet must not be part of it.
    await waitFor(() => {
      expect(
        screen.getByText("This customer was changed by someone else. Reload to see the latest version."),
      ).toBeInTheDocument();
      expect(screen.queryByText("Could not reload. Try again.")).not.toBeInTheDocument();
    });
  });

  it("drops the no-identifier note once the profile has a Peppol ID to look up", async () => {
    let billingGets = 0;
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
      if (path === "/api/v1/customers/1001/billing-profile") {
        billingGets += 1;
        // Somebody fills the Peppol ID in — the note's reason is gone.
        return Promise.resolve(jsonResponse(200, billingGets === 1 ? neverCheckedProfile : emptyProfile));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const { queryClient } = renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Check EHF" }));
    expect(
      await screen.findByText("Nothing to look up — no Peppol ID or Norwegian organisation number"),
    ).toBeInTheDocument();

    await queryClient.invalidateQueries({ queryKey: ["customers", 1001, "billing-profile"] });

    await waitFor(() =>
      expect(
        screen.queryByText("Nothing to look up — no Peppol ID or Norwegian organisation number"),
      ).not.toBeInTheDocument(),
    );
  });

  it("refreshes the timeline when the answer changed, and never the customer itself", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(jsonResponse(200, registeredWithInvoice));
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(
          jsonResponse(200, {
            ...emptyProfile,
            peppolLookup: { ...registeredWithInvoice, status: "not_registered", canReceiveInvoice: false },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queryClient.setQueryData(["customers", 1001, "timeline", {}], { entries: [] });
    queryClient.setQueryData(["customers", 1001], { id: 1001, revision: 3 });
    renderStatus(fetchMock, { queryClient });

    await userEvent.click(await screen.findByRole("button", { name: "Check EHF" }));

    // The server records `customer.peppol_lookup` when the answer moved, so
    // the timeline on this very page is stale until it is asked again.
    await waitFor(() =>
      expect(queryClient.getQueryState(["customers", 1001, "timeline", {}])?.isInvalidated).toBe(true),
    );
    // The customer row is untouched by a lookup (design D3): its revision did
    // not move, so nothing that reads it needs refetching.
    expect(queryClient.getQueryState(["customers", 1001])?.isInvalidated).toBe(false);
  });

  it("leaves the timeline alone when the answer did not change", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(jsonResponse(200, registeredWithInvoice));
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithInvoice }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queryClient.setQueryData(["customers", 1001, "timeline", {}], { entries: [] });
    renderStatus(fetchMock, { queryClient });

    await userEvent.click(await screen.findByRole("button", { name: "Check EHF" }));

    // The profile refetch proves the success path ran; re-checking the same
    // answer records no timeline event, so nothing to refresh.
    await waitFor(() => expect(callsTo(fetchMock, "/api/v1/customers/1001/billing-profile").length).toBe(2));
    expect(queryClient.getQueryState(["customers", 1001, "timeline", {}])?.isInvalidated).toBe(false);
  });

  it("keeps the action pending until the refetched answer has landed", async () => {
    let resolveSecondGet!: (value: Response) => void;
    const secondGet = new Promise<Response>((resolve) => {
      resolveSecondGet = resolve;
    });
    let billingGets = 0;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(jsonResponse(200, registeredWithInvoice));
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        billingGets += 1;
        return billingGets === 1 ? Promise.resolve(jsonResponse(200, neverCheckedProfile)) : secondGet;
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    const button = await screen.findByRole("button", { name: "Check EHF" });
    await userEvent.click(button);

    // The POST has answered and the refetch is in flight: the answer on screen
    // is still the old one, so the action must still read as working.
    await waitFor(() => expect(callsTo(fetchMock, "/api/v1/customers/1001/billing-profile").length).toBe(2));
    expect(button).toBeDisabled();

    resolveSecondGet(jsonResponse(200, { ...emptyProfile, peppolLookup: registeredWithInvoice }));

    await screen.findByText(`Can receive EHF invoices — checked ${checkedOn(registeredWithInvoice.checkedAt)}`);
    expect(button).not.toBeDisabled();
  });

  it("shows a 400's field error inside the offer", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        return Promise.resolve(
          jsonResponse(400, {
            title: "Invalid billing profile",
            status: 400,
            errors: { invoiceDelivery: ["EHF needs a Peppol ID"] },
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
    renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Use EHF" }));

    // Beside the action that caused it, inside the offer — not as a toast that
    // scrolls away from the button it belongs to.
    const offer = await screen.findByRole("alert");
    expect(await within(offer).findByText("EHF needs a Peppol ID")).toBeInTheDocument();
  });

  it("reports a Check EHF that failed for some other reason as a toast", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/peppol-lookup" && init?.method === "POST") {
        return Promise.resolve(jsonResponse(500, { title: "Something went wrong", status: 500, detail: "Boom." }));
      }
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, neverCheckedProfile));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Check EHF" }));

    expect(await screen.findByText("Could not check whether this customer can receive EHF")).toBeInTheDocument();
  });

  it("reports a Use EHF that failed for some other reason as a toast", async () => {
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(500, { title: "Something went wrong", status: 500, detail: "Boom." }));
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(
          jsonResponse(200, { ...emptyProfile, warnings: ["ehf_available"], peppolLookup: registeredWithInvoice }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderStatus(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Use EHF" }));

    expect(await screen.findByText("Could not switch to EHF")).toBeInTheDocument();
  });
});

describe("CustomerPeppolStatus — 503 disables the action for the rest of the session", () => {
  beforeEach(resetPeppolLookupDisabledForSession);
  afterEach(() => vi.unstubAllGlobals());

  // The flag this sets is module-level (not component state), on purpose —
  // the installation setting it reflects does not change per customer or per
  // mount, so a fresh card must not offer the action back. That also means it
  // outlives this test, which is why every test here resets it first.
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
    // this one component instance. The first render is unmounted first, so
    // what follows can only be satisfied by the second one.
    cleanup();
    const fetchMock2 = vi.fn().mockResolvedValue(jsonResponse(200, neverCheckedProfile));
    const { mount } = renderStatus(fetchMock2);

    await waitFor(() => expect(callsTo(fetchMock2, "/api/v1/customers/1001/billing-profile").length).toBe(1));
    expect(await within(mount).findByText("Peppol lookup is switched off on this installation")).toBeInTheDocument();
    expect(within(mount).queryByRole("button", { name: "Check EHF" })).not.toBeInTheDocument();
    expect(callsTo(fetchMock2, "/api/v1/customers/1001/peppol-lookup", "POST")).toHaveLength(0);
  });
});
