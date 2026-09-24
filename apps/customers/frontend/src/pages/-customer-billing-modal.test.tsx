import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerBillingModal } from "./-customer-billing-modal";

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
  peppolId: null,
  gln: null,
  buyerReference: null,
  defaultBillRate: null,
  revision: 3,
  warnings: [],
};

/** The last PUT to the billing-profile endpoint fetch saw — never "the last fetch": a background refetch can land after it. */
const lastBillingPut = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls
    .filter(
      ([url, init]) => String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === "PUT",
    )
    .at(-1);

const renderModal = (
  profile: Record<string, unknown>,
  handlers: { billingPut?: (body: unknown) => unknown; billingGet?: () => unknown } = {},
) => {
  const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT" && handlers.billingPut) {
      return Promise.resolve(handlers.billingPut(JSON.parse(String(init.body))));
    }
    if (path === "/api/v1/customers/1001/billing-profile" && (!init || init.method === undefined)) {
      return Promise.resolve(jsonResponse(200, handlers.billingGet ? handlers.billingGet() : profile));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <CustomerBillingModal opened customerId={1001} profile={profile as never} onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

describe("CustomerBillingModal", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the full eleven-field profile plus revision on save", async () => {
    const fetchMock = renderModal(emptyProfile, {
      billingPut: () => jsonResponse(200, { ...emptyProfile, invoiceEmail: "invoices@acme.test" }),
    });

    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/^invoice email/i), "invoices@acme.test");
    await userEvent.type(within(dialog).getByLabelText(/^reminder email/i), "reminders@acme.test");
    await userEvent.type(within(dialog).getByLabelText(/payment terms/i), "30");
    await userEvent.type(within(dialog).getByLabelText(/^currency/i), "nok");
    await userEvent.type(within(dialog).getByLabelText(/default bill rate/i), "1250.5");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(lastBillingPut(fetchMock)).toBeTruthy());
    const [, init] = lastBillingPut(fetchMock) as [string, RequestInit];
    const body = JSON.parse(String(init.body));
    expect(body).toEqual({
      invoiceEmail: "invoices@acme.test",
      reminderEmail: "reminders@acme.test",
      paymentTermsDays: 30,
      currency: "NOK",
      language: null,
      invoiceDelivery: null,
      reminderDelivery: null,
      peppolId: null,
      gln: null,
      buyerReference: null,
      defaultBillRate: 1250.5,
      revision: 3,
    });
  });

  it("sends a literal 0 for payment terms, and null once the field is cleared", async () => {
    // Zero days is "due on receipt", not "not set": the difference has to
    // survive a field whose empty value is an empty string.
    const fetchMock = renderModal(emptyProfile, {
      billingPut: () => jsonResponse(200, { ...emptyProfile, paymentTermsDays: 0, revision: 4 }),
    });

    const dialog = await screen.findByRole("dialog");
    const paymentTerms = within(dialog).getByLabelText(/payment terms/i);
    await userEvent.type(paymentTerms, "0");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(lastBillingPut(fetchMock)).toBeTruthy());
    expect(JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).paymentTermsDays).toBe(0);

    await userEvent.clear(paymentTerms);
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(
          ([url, init]) =>
            String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === "PUT",
        ),
      ).toHaveLength(2),
    );
    expect(
      JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).paymentTermsDays,
    ).toBeNull();
  });

  it("upper-cases the currency as it is typed", async () => {
    renderModal(emptyProfile);
    const dialog = await screen.findByRole("dialog");
    const currency = within(dialog).getByLabelText(/^currency/i);
    await userEvent.type(currency, "nok");
    expect(currency).toHaveValue("NOK");
  });

  it("keeps a stored default bill rate through a save that never touched it, and sends null once it is cleared", async () => {
    // The PUT is a full replace, and the Use EHF offer builds its body through
    // the same valuesFromProfile/toInput: a rate the form did not carry would be
    // cleared by a save about something else.
    const stored = { ...emptyProfile, currency: "NOK", defaultBillRate: 1250.5 };
    const fetchMock = renderModal(stored, {
      billingPut: () => jsonResponse(200, { ...stored, revision: 4 }),
    });
    const puts = () =>
      fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === "PUT",
      );

    const dialog = await screen.findByRole("dialog");
    const rate = within(dialog).getByLabelText(/default bill rate/i);
    expect(rate).toHaveValue("1250.5");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(puts()).toHaveLength(1));
    expect(JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).defaultBillRate).toBe(
      1250.5,
    );

    await userEvent.clear(rate);
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(puts()).toHaveLength(2));
    expect(JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).defaultBillRate).toBeNull();
  });

  it("shows the server's rate-needs-a-currency error on the rate field", async () => {
    const message = "A default bill rate needs the billing profile's currency to be quoted in";
    renderModal(emptyProfile, {
      billingPut: () =>
        jsonResponse(400, { title: "Invalid billing profile", status: 400, errors: { defaultBillRate: [message] } }),
    });

    const dialog = await screen.findByRole("dialog");
    const rate = within(dialog).getByLabelText(/default bill rate/i);
    await userEvent.type(rate, "1250");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(await within(dialog).findByText(message)).toBeInTheDocument();
    expect(rate).toHaveAttribute("aria-invalid", "true");
  });

  it("sends a typed zero rate as it is, and shows the server's refusal on the rate field", async () => {
    // The input offers no floor above zero: 0 must reach the server as 0 —
    // not clamped, not read as "cleared" — so its refusal is the one the user sees.
    const message = "A default bill rate must be greater than zero, but was 0";
    const fetchMock = renderModal(
      { ...emptyProfile, currency: "NOK" },
      {
        billingPut: () =>
          jsonResponse(400, { title: "Invalid billing profile", status: 400, errors: { defaultBillRate: [message] } }),
      },
    );

    const dialog = await screen.findByRole("dialog");
    const rate = within(dialog).getByLabelText(/default bill rate/i);
    await userEvent.type(rate, "0");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(await within(dialog).findByText(message)).toBeInTheDocument();
    expect(rate).toHaveAttribute("aria-invalid", "true");
    expect(JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).defaultBillRate).toBe(0);
  });

  it("maps field errors from a 400 onto the right inputs", async () => {
    renderModal(emptyProfile, {
      billingPut: () =>
        jsonResponse(400, {
          title: "Invalid billing profile",
          status: 400,
          errors: { currency: ["Currency must be three letters"] },
        }),
    });

    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(await within(dialog).findByText("Currency must be three letters")).toBeInTheDocument();
  });

  it("shows the Reload pattern on a revision conflict, re-seeding values and the revision the next save sends", async () => {
    let reloaded = false;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && (!init || init.method === undefined)) {
        return Promise.resolve(
          jsonResponse(
            200,
            reloaded ? { ...emptyProfile, invoiceEmail: "latest@acme.test", revision: 4 } : emptyProfile,
          ),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        const sent = JSON.parse(String(init.body)) as { revision?: number };
        if (sent.revision !== 4) {
          reloaded = true;
          return Promise.resolve(
            jsonResponse(409, {
              title: "Customer revision conflict",
              detail: "The customer was changed by someone else.",
              status: 409,
            }),
          );
        }
        return Promise.resolve(jsonResponse(200, { ...emptyProfile, invoiceEmail: "latest@acme.test", revision: 4 }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <MantineProvider env="test">
        <Notifications />
        <QueryClientProvider client={queryClient}>
          <CustomerBillingModal opened customerId={1001} profile={emptyProfile as never} onClose={() => {}} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    let dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(
      await within(dialog).findByText("This customer was changed by someone else. Reload to see the latest version."),
    ).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: /reload/i }));

    dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByLabelText(/^invoice email/i)).toHaveValue("latest@acme.test");

    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => {
      const put = lastBillingPut(fetchMock);
      expect(put).toBeTruthy();
      const [, init] = put as [string, RequestInit];
      expect(JSON.parse(String(init.body)).revision).toBe(4);
    });
  });

  it("keeps the conflict banner and the refused revision when the reload itself fails", async () => {
    let conflicted = false;
    const fetchMock = vi.fn().mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile" && init?.method === "PUT") {
        conflicted = true;
        return Promise.resolve(
          jsonResponse(409, {
            title: "Customer revision conflict",
            detail: "The customer was changed by someone else.",
            status: 409,
          }),
        );
      }
      if (path === "/api/v1/customers/1001/billing-profile") {
        return conflicted
          ? Promise.resolve(new Response(null, { status: 500 }))
          : Promise.resolve(jsonResponse(200, emptyProfile));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <MantineProvider env="test">
        <Notifications />
        <QueryClientProvider client={queryClient}>
          <CustomerBillingModal opened customerId={1001} profile={emptyProfile as never} onClose={() => {}} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    const banner = "This customer was changed by someone else. Reload to see the latest version.";
    expect(await within(dialog).findByText(banner)).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: /reload/i }));

    expect(await within(dialog).findByText("Could not reload. Try again.")).toBeInTheDocument();
    expect(within(dialog).getByText(banner)).toBeInTheDocument();

    // Nothing was re-seeded: the next save still sends the revision the
    // server refused, rather than one a failed reload invented.
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(
          ([url, init]) =>
            String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === "PUT",
        ),
      ).toHaveLength(2),
    );
    const [, init] = lastBillingPut(fetchMock) as [string, RequestInit];
    expect(JSON.parse(String(init.body)).revision).toBe(3);
  });
});
