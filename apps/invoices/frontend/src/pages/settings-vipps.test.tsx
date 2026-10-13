import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoicesMeta } from "../api/meta";
import type { PaymentSettings } from "../api/payment-settings";
import type { VippsSettings } from "../api/vipps";
import { invoicesCatalog } from "../i18n";
import { jsonResponse, path, refusal } from "../test/api";
import { stubFetch } from "../test/fetch";
import { meta, settings } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

/** The caller the settings are for: `invoices:manage` (D14: Vipps and the payment settings are manage's). */
const manager = {
  canCreate: true,
  canImportBankFiles: false,
  canIssue: true,
  canManage: true,
  canQuickInvoice: false,
  canRegisterPayments: false,
  canRunReminders: false,
  canSend: false,
  canSendEhf: false,
  canTakeOnSitePayment: false,
};

/** The payment settings as the server answers them: both switches off, no URLs. */
const paymentSettings = (overrides: Partial<PaymentSettings> = {}): PaymentSettings => ({
  referencePrefix: "k3x9q2",
  payLinksEnabled: false,
  onSitePaymentsEnabled: false,
  revision: 1,
  updatedAt: "2026-09-12T08:00:00Z",
  ...overrides,
});

interface World {
  meta: Partial<InvoicesMeta>;
  vipps: VippsSettings;
  payments: PaymentSettings;
  /** Answers by "METHOD url", over the model. */
  answers: Record<string, () => Response>;
}

/**
 * The fetch fake for the Vipps and payment settings (D14): the credentials
 * read without a key ever coming back, a PUT keeping what it was not sent,
 * and the payment settings saved at their revision. Anything else the
 * settings page reads is answered as the other settings tests answer it, or
 * not at all.
 */
const server = (overrides: Partial<World> = {}) => {
  const state: World = {
    meta: {},
    vipps: { hasCredentials: false },
    payments: paymentSettings(),
    answers: {},
    ...overrides,
  };
  const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const answer = state.answers[`${method} ${url}`];
    if (answer) return answer();
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager, ...state.meta }));
    if (url === "/api/v1/invoices/settings" && method === "GET") return jsonResponse(200, settings());
    if (url === "/api/v1/invoices/settings/vipps") {
      if (method === "PUT") {
        state.vipps = { hasCredentials: true, msn: body.msn };
        return jsonResponse(200, state.vipps);
      }
      if (method === "DELETE") {
        state.vipps = { hasCredentials: false };
        return new Response(null, { status: 204 });
      }
      return jsonResponse(200, state.vipps);
    }
    if (url === "/api/v1/invoices/settings/vipps/verify") return jsonResponse(200, { result: "ok" });
    if (url === "/api/v1/invoices/settings/payments") {
      if (method === "PUT") {
        const rest = { ...body };
        delete rest.acknowledgeKontantsalg;
        state.payments = paymentSettings({ ...rest, revision: body.revision + 1 });
        return jsonResponse(200, state.payments);
      }
      return jsonResponse(200, state.payments);
    }
    return new Response(null, { status: 404 });
  });
  return { fetchMock, state };
};

/** The body of the one request sent with this method to this URL, or undefined. */
const bodyOf = (fetchMock: ReturnType<typeof stubFetch>, method: string, url: string) => {
  const call = fetchMock.actualCalls.find(([u, init]) => path(u) === url && (init?.method ?? "GET") === method);
  return call ? JSON.parse(String(call[1]?.body)) : undefined;
};

const vippsCard = async () => screen.findByTestId("vipps-settings");
const paymentCard = async () => screen.findByTestId("payment-settings");

describe("VippsSettings_KeysWriteOnlyVerifyAndRejected", () => {
  it("omits every key left blank, so the stored ones are kept, and never shows a key", async () => {
    const { fetchMock } = server({ vipps: { hasCredentials: true, msn: "123456" } });
    renderWithProviders(<SettingsPage />);
    const card = await vippsCard();

    await waitFor(() => expect(within(card).getByLabelText("Sales unit (MSN)")).toHaveValue("123456"));
    expect(within(card).getByText("Keys stored")).toBeInTheDocument();
    for (const label of ["Client id", "Client secret", "Subscription key"]) {
      expect(within(card).getByLabelText(label)).toHaveValue("");
    }
    await userEvent.type(within(card).getByLabelText("Client secret"), "the-new-secret");
    await userEvent.click(within(card).getByRole("button", { name: "Save Vipps" }));

    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/settings/vipps")).toEqual({
        msn: "123456",
        clientSecret: "the-new-secret",
      }),
    );
    expect(await screen.findByText("The Vipps keys are saved")).toBeInTheDocument();
    await waitFor(() => expect(within(card).getByLabelText("Client secret")).toHaveValue(""));
  });

  it("sends all three keys the first time", async () => {
    const { fetchMock } = server();
    renderWithProviders(<SettingsPage />);
    const card = await vippsCard();

    await userEvent.type(within(card).getByLabelText("Sales unit (MSN)"), "654321");
    await userEvent.type(within(card).getByLabelText("Client id"), "id-1");
    await userEvent.type(within(card).getByLabelText("Client secret"), "secret-1");
    await userEvent.type(within(card).getByLabelText("Subscription key"), "sub-1");
    await userEvent.click(within(card).getByRole("button", { name: "Save Vipps" }));

    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/settings/vipps")).toEqual({
        msn: "654321",
        clientId: "id-1",
        clientSecret: "secret-1",
        subscriptionKey: "sub-1",
      }),
    );
  });

  it("says what Verify answered, and waits for stored keys", async () => {
    server({
      vipps: { hasCredentials: true, msn: "123456" },
      answers: { "POST /api/v1/invoices/settings/vipps/verify": () => jsonResponse(200, { result: "unauthorized" }) },
    });
    renderWithProviders(<SettingsPage />);
    const card = await vippsCard();

    await userEvent.click(await within(card).findByRole("button", { name: "Verify Vipps" }));
    expect(await within(card).findByText("Vipps refused the keys. Enter them again.")).toBeInTheDocument();
  });

  it("offers no Verify or Remove without stored keys", async () => {
    server();
    renderWithProviders(<SettingsPage />);
    const card = await vippsCard();

    expect(within(card).getByRole("button", { name: "Verify Vipps" })).toBeDisabled();
    expect(within(card).getByRole("button", { name: "Remove the Vipps keys" })).toBeDisabled();
  });

  it("says when Vipps refused the stored keys", async () => {
    server({
      vipps: { hasCredentials: true, msn: "123456", rejectedAt: "2026-09-10T08:00:00Z" },
      meta: { vippsCredentialsRejected: true },
    });
    renderWithProviders(<SettingsPage />);
    const card = await vippsCard();

    expect(await within(card).findByText("Vipps refused the keys")).toBeInTheDocument();
    expect(within(card).getByText(/^Vipps refused the stored keys on Sep 10, 2026/)).toBeInTheDocument();
  });

  it("asks before removing the keys, and says payment_attempts_active in words", async () => {
    const { fetchMock } = server({
      vipps: { hasCredentials: true, msn: "123456" },
      answers: { "DELETE /api/v1/invoices/settings/vipps": () => refusal(409, "payment_attempts_active") },
    });
    renderWithProviders(<SettingsPage />);
    const card = await vippsCard();

    await userEvent.click(await within(card).findByRole("button", { name: "Remove the Vipps keys" }));
    const dialog = await screen.findByRole("dialog");
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove the Vipps keys" }));

    expect(
      await within(card).findByText(
        "A Vipps payment is in flight. The sales unit cannot be changed, nor the keys removed, until it is finished or abandoned. New keys of the same sales unit can be saved.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("names no webhook anywhere: Vipps is polled in this release", async () => {
    server({ vipps: { hasCredentials: true, msn: "123456" } });
    renderWithProviders(<SettingsPage />);
    const cards = [await vippsCard(), await paymentCard()];

    for (const card of cards) expect(card.textContent).not.toMatch(/webhook|notification|varsel/i);
  });
});

/** The kontantsalg notice, word for word, in both languages: its one wording (open question 3). */
const noticeEn = [
  "Payment on site is a cash sale (kontantsalg)",
  "Taking a Vipps payment from a customer standing in front of you is a cash sale (kontantsalg), even though the customer gets an invoice: paying with Vipps counts as cash.",
  "Without a cash register (kassasystem), a cash sale is allowed only under the bookkeeping regulation (bokføringsforskriften) § 5-4-1 third paragraph: for a business that mainly sells on credit to customers it identifies, with a cash invoice (kontantfaktura) for each sale.",
  "A shop or counter sale needs a product-declared cash register (produkterklært kassasystem). Do not turn payment on site on for such sales.",
  "Name the buyer truthfully. Vantigo checks that a name and an address or an organisation number are there, not that they are right.",
  "Showing a customer on site an invoice's pay link or its QR code is the same cash sale. That is why the app shows them on screen only while payment on site is on.",
];
const noticeNb = [
  "Betaling på stedet er kontantsalg",
  "Å ta imot en Vipps-betaling fra en kunde som står foran deg, er kontantsalg, selv om kunden får en faktura: betaling med Vipps regnes som kontant betaling.",
  "Uten kassasystem er kontantsalg bare tillatt etter bokføringsforskriften § 5-4-1 tredje ledd: for en virksomhet som hovedsakelig selger på kreditt til kunder den identifiserer, med en kontantfaktura for hvert salg.",
  "Salg i butikk eller over disk krever et produkterklært kassasystem. Slå ikke på betaling på stedet for slikt salg.",
  "Oppgi kjøperen sannferdig. Vantigo kontrollerer at et navn og en adresse eller et organisasjonsnummer finnes, ikke at de stemmer.",
  "Å vise en kunde på stedet en fakturas betalingslenke eller QR-koden til den er det samme kontantsalget. Derfor viser appen dem på skjermen bare mens betaling på stedet er slått på.",
];
const noticeKeys = [
  "paymentSettings.notice.title",
  "paymentSettings.notice.cashSale",
  "paymentSettings.notice.withoutRegister",
  "paymentSettings.notice.counterSale",
  "paymentSettings.notice.truthfulBuyer",
  "paymentSettings.notice.payLinkOnSite",
];

describe("PaymentSettings_NoticeAndAcknowledgement", () => {
  it("words the notice in full, in English and in Norwegian", () => {
    expect(noticeKeys.map((key) => invoicesCatalog.en[key as keyof typeof invoicesCatalog.en])).toEqual(noticeEn);
    expect(noticeKeys.map((key) => invoicesCatalog.nb[key as keyof typeof invoicesCatalog.nb])).toEqual(noticeNb);
  });

  it("shows the notice in full when payment on site turns on, and refuses to save it unacknowledged", async () => {
    const { fetchMock } = server();
    renderWithProviders(<SettingsPage />);
    const card = await paymentCard();

    expect(within(card).queryByTestId("kontantsalg-notice")).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("switch", { name: /^Payment on site/ }));
    const notice = within(card).getByTestId("kontantsalg-notice");
    for (const words of noticeEn) expect(within(notice).getByText(words)).toBeInTheDocument();

    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));
    expect(await within(card).findByText("Acknowledge the notice to turn payment on site on.")).toBeInTheDocument();
    expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/settings/payments")).toBeUndefined();

    await userEvent.click(
      within(card).getByRole("checkbox", {
        name: "I have read the notice, and payment on site will be used only as it allows",
      }),
    );
    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));
    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/settings/payments")).toEqual({
        payLinksEnabled: false,
        termsUrl: null,
        privacyUrl: null,
        onSitePaymentsEnabled: true,
        revision: 1,
        acknowledgeKontantsalg: true,
      }),
    );
    expect(await screen.findByText("The payment settings are saved")).toBeInTheDocument();
  });

  it("shows the notice when pay links turn on too, without asking for the acknowledgement", async () => {
    const { fetchMock } = server();
    renderWithProviders(<SettingsPage />);
    const card = await paymentCard();

    await userEvent.click(within(card).getByRole("switch", { name: /^Pay links/ }));
    expect(within(card).getByTestId("kontantsalg-notice")).toBeInTheDocument();
    expect(within(card).queryByRole("checkbox")).not.toBeInTheDocument();
    await userEvent.type(within(card).getByLabelText("Sales terms address"), "https://example.com/terms");
    await userEvent.type(within(card).getByLabelText("Privacy notice address"), "https://example.com/privacy");
    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));

    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/settings/payments")).toEqual({
        payLinksEnabled: true,
        termsUrl: "https://example.com/terms",
        privacyUrl: "https://example.com/privacy",
        onSitePaymentsEnabled: false,
        revision: 1,
      }),
    );
  });

  it("says who acknowledged the notice once on, and does not ask again", async () => {
    const { fetchMock } = server({
      payments: paymentSettings({
        onSitePaymentsEnabled: true,
        kontantsalgAcknowledgedAt: "2026-09-01T08:00:00Z",
        kontantsalgAcknowledgedBy: "00000000-0000-4000-8000-000000000002",
      }),
    });
    renderWithProviders(<SettingsPage />);
    const card = await paymentCard();

    expect(await within(card).findByText(/^The notice was acknowledged on Sep 1, 2026/)).toBeInTheDocument();
    expect(within(card).queryByRole("checkbox")).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));
    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/settings/payments")).not.toHaveProperty(
        "acknowledgeKontantsalg",
      ),
    );
  });

  it("says a save lost to someone else's in words, and shows the settings as they are now", async () => {
    let lost = true;
    const { fetchMock, state } = server({
      answers: {
        "PUT /api/v1/invoices/settings/payments": () => {
          if (!lost) {
            state.payments = paymentSettings({ revision: 3 });
            return jsonResponse(200, state.payments);
          }
          lost = false;
          // Someone else saved meanwhile: pay links on, with their addresses.
          state.payments = paymentSettings({
            revision: 2,
            payLinksEnabled: true,
            termsUrl: "https://other.example/terms",
            privacyUrl: "https://other.example/privacy",
          });
          return jsonResponse(409, { title: "Conflict", status: 409, detail: "Revision 1 is stale; 2 is current." });
        },
      },
    });
    renderWithProviders(<SettingsPage />);
    let card = await paymentCard();

    await userEvent.type(within(card).getByLabelText("Sales terms address"), "https://mine.example/terms");
    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));

    expect(
      await screen.findByText(
        "Your changes were not saved: someone else changed the payment settings meanwhile. The settings are shown as they are now; make your change again.",
      ),
    ).toBeInTheDocument();
    card = await paymentCard();
    await waitFor(() =>
      expect(within(card).getByLabelText("Sales terms address")).toHaveValue("https://other.example/terms"),
    );
    expect(within(card).getByRole("switch", { name: /^Pay links/ })).toBeChecked();

    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));
    expect(await screen.findByText("The payment settings are saved")).toBeInTheDocument();
    const puts = fetchMock.actualCalls.filter(
      ([u, init]) => path(u) === "/api/v1/invoices/settings/payments" && init?.method === "PUT",
    );
    expect(JSON.parse(String(puts[1][1]?.body)).revision).toBe(2);
    await waitFor(() => expect(screen.queryByText(/^Your changes were not saved/)).not.toBeInTheDocument());
  });

  it.each([
    [
      "public_url_missing",
      "This installation has no public address (PUBLIC_BASE_URL), so pay links cannot be used. Your operator sets it.",
    ],
    [
      "payments_unavailable",
      "Vipps payment is not available: it is switched off, not set up, or its stored keys cannot be read. Ask whoever manages Invoices.",
    ],
  ])("says %s in words", async (code, words) => {
    server({ answers: { "PUT /api/v1/invoices/settings/payments": () => refusal(409, code) } });
    renderWithProviders(<SettingsPage />);
    const card = await paymentCard();

    await userEvent.click(within(card).getByRole("switch", { name: /^Pay links/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save payment settings" }));
    expect(await within(card).findByText(words)).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("explains pay links switched on but not available, PUBLIC_BASE_URL among the reasons", async () => {
    server({
      payments: paymentSettings({
        payLinksEnabled: true,
        termsUrl: "https://example.com/terms",
        privacyUrl: "https://example.com/privacy",
      }),
      meta: { payLinksAvailable: false },
    });
    renderWithProviders(<SettingsPage />);
    const card = await paymentCard();

    expect(
      await within(card).findByText(
        "Pay links are on but not available now: Vipps may be switched off on this installation, its keys missing or refused, or the installation may have no public address (PUBLIC_BASE_URL), which your operator sets.",
      ),
    ).toBeInTheDocument();
  });
});
