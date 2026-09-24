import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CustomerResponse } from "../api/customers";
import { stubFetch } from "../test/fetch";
import { CustomerBillingCard } from "./-customer-billing-card";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 1001,
  customerNumber: 5001,
  name: "Acme",
  status: "active",
  type: "business",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  identity: null,
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision: 3,
  contactInfo: { email: null, phone: null, website: null },
  ...overrides,
});

const emptyProfile = {
  invoiceEmail: null,
  reminderEmail: null,
  paymentTermsDays: null,
  currency: null,
  language: null,
  invoiceDelivery: null,
  reminderDelivery: null,
  peppolId: null,
  gln: null,
  buyerReference: null,
  defaultBillRate: null,
  revision: 3,
  warnings: [],
};

/**
 * What the server really sends for a customer that has decided nothing: the
 * eleven optional fields are `omitempty` on the wire (see
 * `apps/server/internal/customers/gen/api.gen.go`), so they are absent, not
 * null. Every other fixture here spells the nulls out, which the wire never
 * does.
 */
const omittedProfile = { revision: 3, warnings: ["no_invoice_address"] };

/** The last PUT to the billing-profile endpoint fetch saw — never "the last fetch". */
const lastBillingPut = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls
    .filter(
      ([url, init]) => String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === "PUT",
    )
    .at(-1);

const renderCard = (
  profile: Record<string, unknown>,
  customerOverrides: Record<string, unknown> = {},
  canManageBilling = true,
) => {
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
      return Promise.resolve(jsonResponse(200, { ...(profile as object), revision: 4 }));
    }
    if (path === "/api/v1/customers/1001/billing-profile") return Promise.resolve(jsonResponse(200, profile));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <CustomerBillingCard
          customerId={1001}
          customer={customer(customerOverrides) as CustomerResponse}
          canManageBilling={canManageBilling}
        />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

describe("CustomerBillingCard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("titles the card with a heading, not a bold line", async () => {
    renderCard(emptyProfile);
    expect(await screen.findByRole("heading", { name: "Billing", level: 3 })).toBeInTheDocument();
  });

  it("shows 'Not set — the invoicing default applies' for every null field", async () => {
    renderCard(emptyProfile);
    // Eleven billing fields, all null in emptyProfile — ten say the invoicing default
    // applies; the rate has its own words (asserted below).
    expect(await screen.findAllByText("Not set — the invoicing default applies")).toHaveLength(10);
    // The rate's own words: when it is not set, the chain goes on to the
    // project's or the person's rate — not to an invoicing default.
    expect(screen.getByText("Not set — the project's or the person's rate applies")).toBeInTheDocument();
  });

  it("reads a profile whose unset fields the server left out entirely", async () => {
    renderCard(omittedProfile);

    expect(await screen.findAllByText("Not set — the invoicing default applies")).toHaveLength(10);
    expect(screen.getByText("Not set — the project's or the person's rate applies")).toBeInTheDocument();
    // "Payment terms" would otherwise render the raw catalog key: i18next
    // cannot pluralise a count of undefined.
    expect(screen.queryByText(/paymentTermsDaysValue/)).not.toBeInTheDocument();
    expect(
      screen.getByText("There is no invoice address — add one so invoices have somewhere to be sent."),
    ).toBeInTheDocument();
  });

  it("still resolves the recipient hints when the fields behind them were left out", async () => {
    renderCard(omittedProfile, { contactInfo: { email: "hello@acme.test" } });

    expect(await screen.findByText("Invoices go to hello@acme.test")).toBeInTheDocument();
    expect(screen.getByText("Reminders go to hello@acme.test")).toBeInTheDocument();
  });

  it("keeps the modal's selects controlled, so an untouched one is saved as an explicit null", async () => {
    // A field the server left out reaches the modal as null, not undefined —
    // `JSON.stringify` drops an undefined value, which would turn the full
    // replace PUT into a partial one.
    const fetchMock = renderCard(omittedProfile);
    await userEvent.click(await screen.findByLabelText("Edit billing profile"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(lastBillingPut(fetchMock)).toBeTruthy());
    const [, init] = lastBillingPut(fetchMock) as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      invoiceEmail: null,
      reminderEmail: null,
      paymentTermsDays: null,
      currency: null,
      language: null,
      invoiceDelivery: null,
      reminderDelivery: null,
      peppolId: null,
      gln: null,
      buyerReference: null,
      defaultBillRate: null,
      revision: 3,
    });
  });

  it("saves without asking again for the profile the server just handed back", async () => {
    // The 200 body is written into this query's own cache, so the broad
    // ["customers"] invalidation that follows deliberately skips it.
    const fetchMock = renderCard(emptyProfile);
    await userEvent.click(await screen.findByLabelText("Edit billing profile"));
    const dialog = await screen.findByRole("dialog");
    const billingGets = () =>
      fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === undefined,
      );
    const getsBeforeSave = billingGets().length;

    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    // The save's invalidation runs before the modal closes, so by the time
    // the dialog is gone any extra GET it asked for would be on record.
    await waitFor(() => expect(lastBillingPut(fetchMock)).toBeTruthy());
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(billingGets().length).toBe(getsBeforeSave);
  });

  it("renders each set field through its own human label", async () => {
    renderCard({
      ...emptyProfile,
      invoiceEmail: "invoices@acme.test",
      reminderEmail: "reminders@acme.test",
      paymentTermsDays: 30,
      currency: "NOK",
      language: "nb",
      invoiceDelivery: "ehf",
      reminderDelivery: "paper",
      peppolId: "0192:923609016",
      gln: "7040110000005",
      buyerReference: "PO-42",
      defaultBillRate: 1250,
    });

    expect(await screen.findByText("invoices@acme.test")).toBeInTheDocument();
    expect(screen.getByText("reminders@acme.test")).toBeInTheDocument();
    expect(screen.getByText("30 days")).toBeInTheDocument();
    expect(screen.getByText("NOK")).toBeInTheDocument();
    expect(screen.getByText("Norwegian (Bokmål)")).toBeInTheDocument();
    expect(screen.getByText("EHF")).toBeInTheDocument();
    expect(screen.getByText("Paper")).toBeInTheDocument();
    expect(screen.getByText("0192:923609016")).toBeInTheDocument();
    expect(screen.getByText("7040110000005")).toBeInTheDocument();
    expect(screen.getByText("PO-42")).toBeInTheDocument();
    expect(screen.getByText("1,250.00 NOK per hour")).toBeInTheDocument();
    expect(screen.getByText("Default bill rate")).toBeInTheDocument();
  });

  it("shows a rate that somehow arrived without a currency as the amount alone", async () => {
    // Impossible through the API (a rate needs a currency), but the row must
    // not render a gap where the code would be. textContent, not the matcher's
    // own text: getByText collapses the double space an empty code would leave.
    renderCard({ ...emptyProfile, defaultBillRate: 1250 });
    expect((await screen.findByText(/^1,250\.00/)).textContent).toBe("1,250.00 per hour");
  });

  it("uses the singular form for a single day", async () => {
    renderCard({ ...emptyProfile, paymentTermsDays: 1 });
    expect(await screen.findByText("1 day")).toBeInTheDocument();
  });

  it("explains each known warning code and ignores an unknown one", async () => {
    renderCard({ ...emptyProfile, warnings: ["no_invoice_address", "something_new"] });

    expect(
      await screen.findByText("There is no invoice address — add one so invoices have somewhere to be sent."),
    ).toBeInTheDocument();
    expect(screen.queryByText("something_new")).not.toBeInTheDocument();
  });

  it("explains ehf_recipient_not_registered in the yellow warnings list (design D4)", async () => {
    renderCard({ ...emptyProfile, warnings: ["ehf_recipient_not_registered"] });

    expect(
      await screen.findByText(
        "EHF is the invoice delivery method, but the last Peppol check says this customer cannot receive EHF invoices.",
      ),
    ).toBeInTheDocument();
  });

  it("renders the Peppol/EHF answer and its action inside the Peppol ID row, wired to this customer", async () => {
    // The detailed behaviour (Check EHF, Use EHF, every status and error) is
    // covered by -customer-peppol-status.test.tsx; this only proves the
    // card actually mounts it with the right customerId and profile, in the
    // row whose value the answer is about.
    renderCard({
      ...emptyProfile,
      peppolLookup: {
        status: "registered",
        canReceiveInvoice: true,
        canReceiveCreditNote: true,
        checkedAt: "2026-09-21T10:00:00Z",
        participantId: "0192:923609016",
        smpHost: null,
      },
    });

    const peppolRow = (await screen.findByText("Peppol ID")).closest("div") as HTMLElement;
    expect(within(peppolRow).getByRole("button", { name: "Check EHF" })).toBeInTheDocument();
    expect(within(peppolRow).getByText(/^Can receive EHF invoices — checked /)).toBeInTheDocument();
  });

  it("leaves nothing behind in the Peppol ID row when there is no answer and nothing to click", async () => {
    renderCard(emptyProfile, {}, false);

    const peppolRow = (await screen.findByText("Peppol ID")).closest("div") as HTMLElement;
    // The row's right-hand side holds its value and nothing else: never
    // checked and no Check EHF to offer means not even an empty stack.
    expect((peppolRow.lastElementChild as HTMLElement).childElementCount).toBe(1);
  });

  it("puts the ehf_available offer at the top of the card, beside the warnings", async () => {
    renderCard({
      ...emptyProfile,
      warnings: ["ehf_available", "no_invoice_address"],
      peppolLookup: {
        status: "registered",
        canReceiveInvoice: true,
        canReceiveCreditNote: true,
        checkedAt: "2026-09-21T10:00:00Z",
        participantId: "0192:923609016",
        smpHost: null,
      },
    });

    // An offer, not a problem (design D4): its own teal alert, above the
    // fields rather than buried in the middle of them.
    const offer = await screen.findByText("This customer can receive EHF invoices");
    const peppolRow = screen.getByText("Peppol ID");
    expect(offer.compareDocumentPosition(peppolRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByText(/ehf_available/)).not.toBeInTheDocument();
  });

  it("hints that invoices resolve to the contact email when invoiceEmail is unset", async () => {
    renderCard(emptyProfile, { contactInfo: { email: "hello@acme.test", phone: null, website: null } });
    expect(await screen.findByText("Invoices go to hello@acme.test")).toBeInTheDocument();
  });

  it("hints that reminders fall back to the resolved invoice email when reminderEmail is unset", async () => {
    renderCard({ ...emptyProfile, invoiceEmail: "invoices@acme.test" });
    expect(await screen.findByText("Reminders go to invoices@acme.test")).toBeInTheDocument();
  });

  it("hints the EHF recipient derived from a Norwegian business's organisation number", async () => {
    renderCard(emptyProfile, { type: "business", identity: { country: "no", type: "business", id: "923609016" } });
    expect(
      await screen.findByText("Would use 0192:923609016 as the EHF recipient (from the organisation number)"),
    ).toBeInTheDocument();
  });

  it("promises no EHF recipient the server could not derive from a malformed legacy id", async () => {
    renderCard(emptyProfile, { type: "business", identity: { country: "no", type: "business", id: "923609017" } });
    await screen.findByText("Peppol ID");
    expect(screen.queryByText(/EHF recipient/)).not.toBeInTheDocument();
  });

  it("shows no derived-recipient hint when the identity is absent (viewer may not see it)", async () => {
    renderCard(emptyProfile, { type: "business", identity: null });
    await screen.findByText("Peppol ID");
    expect(screen.queryByText(/EHF recipient/)).not.toBeInTheDocument();
  });

  it("shows an edit action when canManageBilling, opening the edit modal", async () => {
    renderCard(emptyProfile, {}, true);
    const button = await screen.findByLabelText("Edit billing profile");
    await userEvent.click(button);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("shows no edit affordance without canManageBilling", async () => {
    renderCard(emptyProfile, {}, false);
    await screen.findByText("Peppol ID");
    expect(screen.queryByLabelText("Edit billing profile")).not.toBeInTheDocument();
  });

  it("says where an unset payment term comes from, that an own one overrides it, and nothing when the group decides nothing", async () => {
    // Inherited: the profile decided nothing, the group gives 30.
    renderCard({ ...emptyProfile, groupDefault: { group: { id: "g1", name: "Retail" }, paymentTermsDays: 30 } });
    expect(await screen.findByText("Inherits 30 days from Retail")).toBeInTheDocument();
    cleanup();

    // Overridden: both present, and the card says which one applies.
    renderCard({
      ...emptyProfile,
      paymentTermsDays: 14,
      groupDefault: { group: { id: "g1", name: "Retail" }, paymentTermsDays: 30 },
    });
    expect(await screen.findByText("Group default 30 days — overridden here")).toBeInTheDocument();
    cleanup();

    // In a group that decides nothing: nothing to inherit and nothing to say.
    // The block is still present on the wire, with no paymentTermsDays key at
    // all, which is the trap.
    renderCard({ ...emptyProfile, groupDefault: { group: { id: "g2", name: "Key accounts" } } });
    await screen.findByText("Payment terms");
    expect(screen.queryByText(/Inherits/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Group default/)).not.toBeInTheDocument();
    cleanup();

    // In no group: no groupDefault key at all — the file's own wire fixture,
    // which must not throw.
    renderCard(omittedProfile);
    await screen.findByText("Payment terms");
    expect(screen.queryByText(/Inherits/)).not.toBeInTheDocument();
    cleanup();

    // A group whose default is 0 days is still a real default: due on receipt.
    renderCard({ ...emptyProfile, groupDefault: { group: { id: "g3", name: "Cash only" }, paymentTermsDays: 0 } });
    expect(await screen.findByText("Inherits 0 days from Cash only")).toBeInTheDocument();
  });
});
