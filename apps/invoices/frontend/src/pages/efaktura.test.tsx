import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse, refusal } from "../test/api";
import { stubFetch } from "../test/fetch";
import { issued } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { EfakturaSettingsCard } from "./-efaktura-settings";
import { SendEfakturaDialog } from "./-send-efaktura-dialog";

const SEND = "/api/v1/invoices/1001/send-efaktura";
const PROVIDER = "/api/v1/invoices/settings/consumer-provider";

const provider = (overrides: Record<string, unknown> = {}) => ({
  hasCredentials: true,
  hasMandateCertificate: false,
  operatorReady: true,
  avtalegiroEnabled: true,
  provider: "mps",
  efakturaAgreement: "active",
  avtalegiroAgreement: "active",
  creditorAccount: "86011117947",
  brandName: "Selger AS",
  abbreviatedName: "SELGER",
  sentPerMonth: [{ month: "2026-09", count: 3 }],
  ...overrides,
});

describe("Send as eFaktura (invoices phase 6 design I31, I33)", () => {
  it("queues the invoice and says AvtaleGiro went with it", async () => {
    const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === SEND && init?.method === "POST") {
        return jsonResponse(200, { invoice: issued(), avtalegiro: { included: true } });
      }
      return new Response(null, { status: 404 });
    });
    renderWithProviders(<SendEfakturaDialog document={issued()} onClose={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name: "Send as eFaktura" }));
    expect(await screen.findByText("Queued for sending as eFaktura. AvtaleGiro goes with it.")).toBeInTheDocument();
    expect(fetchMock.calls.some(([u]) => String(u) === SEND)).toBe(true);
  });

  it("says why AvtaleGiro did not go", async () => {
    stubFetch(() =>
      jsonResponse(200, { invoice: issued(), avtalegiro: { included: false, reason: "avtalegiro_limit" } }),
    );
    renderWithProviders(<SendEfakturaDialog document={issued()} onClose={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name: "Send as eFaktura" }));
    expect(
      await screen.findByText(/AvtaleGiro does not go with it: the amount is above the customer's AvtaleGiro limit\./),
    ).toBeInTheDocument();
  });

  it("names a refusal in words, with why the person cannot be sent to", async () => {
    stubFetch(() => refusal(409, "efaktura_not_found", { efakturaReason: "blocked" }));
    renderWithProviders(<SendEfakturaDialog document={issued()} onClose={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name: "Send as eFaktura" }));
    expect(
      await screen.findByText("The customer cannot be sent eFaktura. The customer has blocked eFaktura from you."),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("offers no send for a credit note, and says to send it by e-mail or post", async () => {
    stubFetch(() => new Response(null, { status: 404 }));
    renderWithProviders(<SendEfakturaDialog document={issued({ kind: "credit_note" })} onClose={() => {}} />);
    expect(
      await screen.findByText("Credit notes cannot be sent as eFaktura. Send it by e-mail or post."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send as eFaktura" })).not.toBeInTheDocument();
  });
});

describe("the eFaktura and AvtaleGiro settings (I29, I32, I35)", () => {
  it("saves the agreements, shows the count per month, and assigns an unresolved mandate", async () => {
    const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === PROVIDER) return jsonResponse(200, provider());
      if (url === "/api/v1/invoices/avtalegiro/mandates?unresolved=true") {
        return jsonResponse(200, {
          items: [
            {
              id: 7,
              kid: "9000012344",
              status: "active",
              notifyByMail: false,
              providerSeenAt: "2026-09-01T00:00:00Z",
              updatedAt: "2026-09-01T00:00:00Z",
            },
          ],
        });
      }
      if (url === "/api/v1/invoices/avtalegiro/mandates/7/resolve" && method === "POST") {
        return jsonResponse(200, { id: 7, kid: "9000012344", status: "active", customerId: 2002 });
      }
      return new Response(null, { status: 404 });
    });
    renderWithProviders(<EfakturaSettingsCard />);
    expect(await screen.findByText("2026-09: 3")).toBeInTheDocument();
    expect(await screen.findByText("9000012344")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Customer id" }), "2002");
    await userEvent.click(screen.getByRole("button", { name: "Assign to customer" }));
    await waitFor(() =>
      expect(
        fetchMock.calls.some(
          ([u, init]) =>
            String(u) === "/api/v1/invoices/avtalegiro/mandates/7/resolve" &&
            String(init?.body).includes('"customerId":2002'),
        ),
      ).toBe(true),
    );
  });

  it("says the AvtaleGiro switch's refusal in words", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === PROVIDER) return jsonResponse(200, provider({ avtalegiroEnabled: false }));
      if (url === "/api/v1/invoices/settings/avtalegiro" && init?.method === "PUT") {
        return refusal(409, "avtalegiro_kid_space_taken");
      }
      return new Response(null, { status: 404 });
    });
    renderWithProviders(<EfakturaSettingsCard />);
    await userEvent.click(await screen.findByRole("switch", { name: /AvtaleGiro/ }));
    expect(
      await screen.findByText(
        "The KID has no room for the AvtaleGiro customer KIDs. Agree a longer KID with the bank.",
      ),
    ).toBeInTheDocument();
  });
});
