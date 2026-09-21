import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
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
  revision: 3,
  warnings: [],
};

const renderCard = (
  profile: Record<string, unknown>,
  customerOverrides: Record<string, unknown> = {},
  canManageBilling = true,
) => {
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL) => {
    const path = String(url);
    if (path === "/api/v1/customers/1001/billing-profile") return Promise.resolve(jsonResponse(200, profile));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <Suspense fallback="loading">
          <CustomerBillingCard
            customerId={1001}
            customer={customer(customerOverrides) as CustomerResponse}
            canManageBilling={canManageBilling}
          />
        </Suspense>
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

describe("CustomerBillingCard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("titles the card 'Billing'", async () => {
    renderCard(emptyProfile);
    expect(await screen.findByText("Billing")).toBeInTheDocument();
  });

  it("shows 'Not set — the invoicing default applies' for every null field", async () => {
    renderCard(emptyProfile);
    await screen.findByText("Billing");
    // Ten billing fields, all null in emptyProfile.
    expect(screen.getAllByText("Not set — the invoicing default applies")).toHaveLength(10);
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
    expect(await screen.findByText("EHF recipient 0192:923609016 (from the organisation number)")).toBeInTheDocument();
  });

  it("shows no derived-recipient hint when the identity is absent (viewer may not see it)", async () => {
    renderCard(emptyProfile, { type: "business", identity: null });
    await screen.findByText("Billing");
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
    await screen.findByText("Billing");
    expect(screen.queryByLabelText("Edit billing profile")).not.toBeInTheDocument();
  });
});
