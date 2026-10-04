import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { AccessPoint } from "../api/access-point";
import type { InvoicesMeta } from "../api/meta";
import type { InvoiceSettings } from "../api/settings";
import { jsonResponse, path, problemResponse, refusal, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { accessPoint, meta, settings } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

/** The caller the page is for: meta's `canManage` (EHF and KID design D1: the e-invoicing settings are manage's). */
const manager = {
  canCreate: true,
  canIssue: true,
  canManage: true,
  canRegisterPayments: false,
  canSend: false,
  canSendEhf: false,
};

interface World {
  settings: InvoiceSettings;
  meta: Partial<InvoicesMeta>;
  /** The stored credentials, as GET and PUT answer them; null when none are stored. */
  accessPoint: AccessPoint | null;
  /** Whether a transmission is queued, submitted or unconfirmed: DELETE is then refused (D7). */
  transmissionsActive: boolean;
  verify: string;
}

const world = (overrides: Partial<World> = {}): World => ({
  settings: settings(),
  meta: {},
  accessPoint: null,
  transmissionsActive: false,
  verify: "ok",
  ...overrides,
});

/**
 * The fetch fake for the e-invoicing settings, a small model of the server's
 * rules (D2, D3, D7): the settings saved at their revision, the KID agreement
 * judged against the next number (`nextNumber` once the series is locked,
 * the request's start before) — refused with a 400 on kidLength when it
 * does not fit, warned `kid_headroom_low` when fewer than two digits are left —
 * the access point read and stored without its key ever coming back — none
 * stored reads `{hasCredentials: false}` — its removal
 * refused while a transmission is active, and Verify answering what the world
 * says.
 */
const server = (state: World = world()) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(200, meta({ capabilities: manager, ...state.meta }));
    }
    if (url === "/api/v1/invoices/settings" && method === "PUT") {
      const { revision, ...rest } = body as InvoiceSettings;
      if (revision !== state.settings.revision) return jsonResponse(409, { title: "Conflict", status: 409 });
      const next = state.settings.seriesLocked ? state.settings.nextNumber : rest.seriesStart;
      const room = rest.kidLength === null ? 99 : rest.kidLength - 1 - String(next).length;
      if (room < 0) return problemResponse(400, "Invalid settings", { kidLength: ["does not fit"] });
      state.settings = {
        ...state.settings,
        ...rest,
        warnings: room < 2 ? ["kid_headroom_low"] : [],
        revision: revision + 1,
      };
      return jsonResponse(200, state.settings);
    }
    if (url === "/api/v1/invoices/settings") return jsonResponse(200, state.settings);
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, []);
    if (url === "/api/v1/invoices/settings/access-point" && method === "GET") {
      return jsonResponse(200, state.accessPoint ?? { hasCredentials: false });
    }
    if (url === "/api/v1/invoices/settings/access-point" && method === "PUT") {
      if (!body.apiKey && !state.accessPoint) {
        return problemResponse(400, "Invalid access point", { apiKey: ["is required"] });
      }
      state.accessPoint = accessPoint({ legalEntityId: body.legalEntityId });
      return jsonResponse(200, state.accessPoint);
    }
    if (url === "/api/v1/invoices/settings/access-point" && method === "DELETE") {
      if (state.transmissionsActive) return refusal(409, "transmissions_active");
      state.accessPoint = null;
      return new Response(null, { status: 204 });
    }
    if (url === "/api/v1/invoices/settings/access-point/verify" && method === "POST") {
      return jsonResponse(200, { result: state.verify });
    }
    return new Response(null, { status: 404 });
  });

const eInvoicingCard = () => screen.findByTestId("e-invoicing-card");
const kidCard = () => screen.findByTestId("kid-card");

const chooseAlgorithm = async (name: string) => {
  await userEvent.click(within(await kidCard()).getByRole("combobox", { name: "Check digit" }));
  await userEvent.click(await screen.findByRole("option", { name }));
};

describe("the E-invoicing card", () => {
  it("says the seller's Peppol id on its own readiness line, never in what issuing needs", async () => {
    server(world({ settings: settings({ peppolId: "0192:974760673" }) }));
    renderWithProviders(<SettingsPage />);

    const readiness = await screen.findByRole("list", { name: "What e-invoicing needs" });
    expect(within(readiness).getByText("Peppol id 0192:974760673")).toBeInTheDocument();
    const issuing = screen.getByRole("list", { name: "What issuing needs" });
    expect(within(issuing).queryByText(/Peppol/)).not.toBeInTheDocument();
    expect(await eInvoicingCard()).toContainElement(readiness);
  });

  it("says a missing Peppol id", async () => {
    server(world({ settings: settings({ peppolId: null, organisationNumber: "" }) }));
    renderWithProviders(<SettingsPage />);

    const readiness = await screen.findByRole("list", { name: "What e-invoicing needs" });
    expect(
      within(readiness).getByText(
        "The seller's Peppol id is missing: enter it, or the organisation number it is derived from.",
      ),
    ).toBeInTheDocument();
  });

  it("shows the Peppol id derived from the organisation number as the placeholder, following the number", async () => {
    server(world({ settings: settings({ peppolId: "0192:974760673" }) }));
    renderWithProviders(<SettingsPage />);

    const peppol = await screen.findByRole("textbox", { name: "Peppol id" });
    expect(peppol).toHaveValue("");
    expect(peppol).toHaveAttribute("placeholder", "0192:974760673");
    const number = screen.getByRole("textbox", { name: "Organisation number" });
    await userEvent.clear(number);
    await userEvent.type(number, "923609016");
    expect(peppol).toHaveAttribute("placeholder", "0192:923609016");
  });

  it("saves a Peppol id typed in, with the settings", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    await userEvent.type(await screen.findByRole("textbox", { name: "Peppol id" }), "9908:974760673");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ peppolId: "9908:974760673" });
  });

  it("puts a refused Peppol id on its input, in words", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = path(input);
      if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager }));
      if (url === "/api/v1/invoices/settings" && init?.method === "PUT") {
        return problemResponse(400, "Invalid settings", { peppolId: ["is not a participant id"] });
      }
      if (url === "/api/v1/invoices/settings") return jsonResponse(200, settings());
      return new Response(null, { status: 404 });
    });
    renderWithProviders(<SettingsPage />);

    await userEvent.type(await screen.findByRole("textbox", { name: "Peppol id" }), "0192:1");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText(
        "A four-digit scheme, a colon and an identifier, such as 0192:974760673; a 0192 id is the seller's own organisation number.",
      ),
    ).toBeInTheDocument();
  });

  it.each([
    [true, "Sending as EHF is available"],
    [
      false,
      "Sending as EHF is not available. It needs e-invoicing switched on by the operator, the Peppol lookup enabled, the access point's credentials and the seller's Peppol id.",
    ],
  ])("says whether sending as EHF is available (%s)", async (ehfAvailable, words) => {
    server(world({ meta: { ehfAvailable } }));
    renderWithProviders(<SettingsPage />);

    expect(within(await eInvoicingCard()).getByText(words)).toBeInTheDocument();
  });

  it("says the provider refused the stored key, and when", async () => {
    server(
      world({
        accessPoint: accessPoint({ rejectedAt: "2026-09-12T08:15:00Z" }),
        meta: { accessPointCredentialsRejected: true },
      }),
    );
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    expect(await within(card).findByText("The access point refused the key")).toBeInTheDocument();
    expect(
      within(card).getByText(
        /^The provider refused the stored API key on Sep 12, 2026.*\. Documents wait in the queue until a valid key is saved\.$/,
      ),
    ).toBeInTheDocument();
    expect(within(card).getByText("Key stored")).toBeInTheDocument();
  });
});

describe("the access point", () => {
  it("stores the key and never shows it again", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    expect(await within(card).findByRole("textbox", { name: "Provider" })).toHaveValue("Storecove");
    expect(within(card).queryByText("Key stored")).not.toBeInTheDocument();
    await userEvent.type(within(card).getByRole("textbox", { name: "Legal entity id" }), "4711");
    const key = within(card).getByLabelText("API key");
    await userEvent.type(key, "sk-secret-123");
    await userEvent.click(within(card).getByRole("button", { name: "Save access point" }));
    expect(await screen.findByText("The access point is saved")).toBeInTheDocument();
    expect(requestBody(fetchMock, "PUT", "/api/v1/invoices/settings/access-point")).toEqual({
      provider: "storecove",
      legalEntityId: 4711,
      apiKey: "sk-secret-123",
    });
    await waitFor(() => expect(within(card).getByLabelText("API key")).toHaveValue(""));
    expect(within(card).getByText("Key stored")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("sk-secret-123")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain("sk-secret-123");
    expect(
      within(card).getByText("A key is stored. It is never shown; enter a new one to replace it."),
    ).toBeInTheDocument();
  });

  it("keeps the stored key when none is typed", async () => {
    const fetchMock = server(world({ accessPoint: accessPoint(), meta: { ehfAvailable: true } }));
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    expect(await within(card).findByText("Key stored")).toBeInTheDocument();
    const entity = within(card).getByRole("textbox", { name: "Legal entity id" });
    expect(entity).toHaveValue("4711");
    await userEvent.clear(entity);
    await userEvent.type(entity, "4712");
    await userEvent.click(within(card).getByRole("button", { name: "Save access point" }));
    expect(await screen.findByText("The access point is saved")).toBeInTheDocument();
    expect(requestBody(fetchMock, "PUT", "/api/v1/invoices/settings/access-point")).toEqual({
      provider: "storecove",
      legalEntityId: 4712,
    });
  });

  it("puts a missing first key on its input, in words", async () => {
    server();
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    await userEvent.type(await within(card).findByRole("textbox", { name: "Legal entity id" }), "4711");
    await userEvent.click(within(card).getByRole("button", { name: "Save access point" }));
    expect(
      await within(card).findByText("Enter the key: it is needed the first time, and cannot be blank."),
    ).toBeInTheDocument();
  });

  it.each([
    ["ok", "The access point accepted the key."],
    ["unauthorized", "The access point refused the key. Check it and save it again."],
    [
      "unreachable",
      "The access point could not be reached, or the key does not reach this legal entity. Check the legal entity id, or try again later.",
    ],
  ])("says Verify's %s", async (result, words) => {
    server(world({ verify: result, accessPoint: accessPoint(), meta: { ehfAvailable: true } }));
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    await userEvent.click(await within(card).findByRole("button", { name: "Verify" }));
    expect(await within(card).findByText(words)).toBeInTheDocument();
  });

  it("says a removal refused while a transmission is active in the 409's words, and keeps the key", async () => {
    server(world({ transmissionsActive: true, accessPoint: accessPoint(), meta: { ehfAvailable: true } }));
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    await removeAndConfirm(card);
    expect(await within(card).findByText("Could not remove the credentials")).toBeInTheDocument();
    expect(
      await within(card).findByText(
        "A document is still on its way through this access point. Wait until it is delivered or failed before removing or switching the credentials.",
      ),
    ).toBeInTheDocument();
    expect(within(card).getByText("Key stored")).toBeInTheDocument();
  });

  it("asks before removing the credentials, and removes nothing when the person cancels", async () => {
    const fetchMock = server(world({ accessPoint: accessPoint(), meta: { ehfAvailable: true } }));
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    await userEvent.click(await within(card).findByRole("button", { name: "Remove the credentials" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove the access point's credentials?" });
    expect(
      within(dialog).getByText(
        "The stored API key is deleted and cannot be shown again, and nothing can be sent as EHF until a key is saved again.",
      ),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(requestBody(fetchMock, "DELETE", "/api/v1/invoices/settings/access-point")).toBeUndefined();
    expect(within(card).getByText("Key stored")).toBeInTheDocument();
  });

  it("removes the credentials once confirmed", async () => {
    server(world({ accessPoint: accessPoint(), meta: { ehfAvailable: true } }));
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    await removeAndConfirm(card);
    expect(await screen.findByText("The access point's credentials are removed")).toBeInTheDocument();
    await waitFor(() => expect(within(card).queryByText("Key stored")).not.toBeInTheDocument());
  });

  it("draws an empty form with nothing stored: Verify and Remove wait for a key", async () => {
    server();
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    expect(await within(card).findByRole("textbox", { name: "Legal entity id" })).toHaveValue("");
    expect(within(card).queryByText("Key stored")).not.toBeInTheDocument();
    expect(
      within(card).getByText("The key from Storecove. It is stored encrypted and never shown again."),
    ).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Verify" })).toBeDisabled();
    expect(within(card).getByRole("button", { name: "Remove the credentials" })).toBeDisabled();
  });

  it("prefills the stored legal entity, and says a key is stored without showing it", async () => {
    server(world({ accessPoint: accessPoint({ legalEntityId: 9001 }) }));
    renderWithProviders(<SettingsPage />);

    const card = await eInvoicingCard();
    expect(await within(card).findByRole("textbox", { name: "Legal entity id" })).toHaveValue("9001");
    expect(within(card).getByText("Key stored")).toBeInTheDocument();
    expect(within(card).getByLabelText("API key")).toHaveValue("");
    expect(within(card).getByRole("button", { name: "Verify" })).toBeEnabled();
  });
});

/** Clicks Remove on the access point and confirms it in the dialog that asks. */
const removeAndConfirm = async (card: HTMLElement) => {
  await userEvent.click(await within(card).findByRole("button", { name: "Remove the credentials" }));
  const dialog = await screen.findByRole("dialog", { name: "Remove the access point's credentials?" });
  await userEvent.click(within(dialog).getByRole("button", { name: "Remove the credentials" }));
};

describe("the KID card", () => {
  it("says there is no agreement until one is chosen", async () => {
    server();
    renderWithProviders(<SettingsPage />);

    expect(
      within(await kidCard()).getByText("No KID agreement: invoices carry their number as the payment reference."),
    ).toBeInTheDocument();
  });

  it("previews the next KID from the series start before the first issue, under either algorithm", async () => {
    server(world({ settings: settings({ seriesLocked: false, seriesStart: 1000 }) }));
    renderWithProviders(<SettingsPage />);

    const card = await kidCard();
    await userEvent.type(within(card).getByRole("textbox", { name: "KID length" }), "7");
    await chooseAlgorithm("MOD10 (recommended)");
    expect(await within(card).findByText("Next KID: 0010009 (invoice 1000)")).toBeInTheDocument();
    await chooseAlgorithm("MOD11");
    expect(await within(card).findByText("Next KID: 0010006 (invoice 1000)")).toBeInTheDocument();
  });

  it("previews the next KID from the server's next number once the series is locked", async () => {
    server(world({ settings: settings({ nextNumber: 1003, kidLength: 7, kidAlgorithm: "mod10" }) }));
    renderWithProviders(<SettingsPage />);

    expect(await within(await kidCard()).findByText("Next KID: 0010033 (invoice 1003)")).toBeInTheDocument();
  });

  it("asks for both halves of the agreement", async () => {
    server();
    renderWithProviders(<SettingsPage />);

    const card = await kidCard();
    await userEvent.type(within(card).getByRole("textbox", { name: "KID length" }), "7");
    expect(within(card).getByText("Choose both the length and the check digit, or neither.")).toBeInTheDocument();
  });

  it("says a length the next number does not fit, live and from the server's 400", async () => {
    const fetchMock = server(world({ settings: settings({ seriesLocked: false, seriesStart: 1000 }) }));
    renderWithProviders(<SettingsPage />);

    const card = await kidCard();
    await userEvent.type(within(card).getByRole("textbox", { name: "KID length" }), "4");
    await chooseAlgorithm("MOD10 (recommended)");
    expect(
      await within(card).findByText(
        "Invoice 1000, the next to be issued, does not fit in 4 characters with its check digit. Choose a longer KID.",
      ),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").body).toMatchObject({ kidLength: 4, kidAlgorithm: "mod10" }));
    expect(
      await within(card).findByText(
        "4 to 25, given together with the check digit, and long enough for the next invoice number and its check digit.",
      ),
    ).toBeInTheDocument();
  });

  it("says the headroom warning the save answered while the agreement is as saved", async () => {
    server(
      world({
        settings: settings({ nextNumber: 1003, kidLength: 6, kidAlgorithm: "mod10", warnings: ["kid_headroom_low"] }),
      }),
    );
    renderWithProviders(<SettingsPage />);

    expect(within(await kidCard()).getByText(headroomWords)).toBeInTheDocument();
  });

  it("judges the headroom live while the agreement is edited, and the save agrees", async () => {
    server(world({ settings: settings({ seriesLocked: false, seriesStart: 1000 }) }));
    renderWithProviders(<SettingsPage />);

    const card = await kidCard();
    const length = within(card).getByRole("textbox", { name: "KID length" });
    await userEvent.type(length, "6");
    await chooseAlgorithm("MOD10 (recommended)");
    expect(await within(card).findByText(headroomWords)).toBeInTheDocument();
    await userEvent.clear(length);
    await userEvent.type(length, "7");
    await waitFor(() => expect(within(card).queryByText(headroomWords)).not.toBeInTheDocument());
    await userEvent.clear(length);
    await userEvent.type(length, "6");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(within(await kidCard()).getByText(headroomWords)).toBeInTheDocument();
  });

  it("drops the saved warning once the agreement is lengthened", async () => {
    server(
      world({
        settings: settings({ nextNumber: 1003, kidLength: 6, kidAlgorithm: "mod10", warnings: ["kid_headroom_low"] }),
      }),
    );
    renderWithProviders(<SettingsPage />);

    const card = await kidCard();
    const length = within(card).getByRole("textbox", { name: "KID length" });
    await userEvent.clear(length);
    await userEvent.type(length, "9");
    expect(within(card).queryByText(headroomWords)).not.toBeInTheDocument();
  });

  it("warns that issued invoices keep their KIDs when a set agreement changes", async () => {
    server(world({ settings: settings({ nextNumber: 1003, kidLength: 7, kidAlgorithm: "mod10" }) }));
    renderWithProviders(<SettingsPage />);

    const card = await kidCard();
    const warning =
      "Issued invoices keep the KIDs computed under the agreement they were issued with. Ask the bank to keep the old length valid until they are paid.";
    expect(within(card).queryByText(warning)).not.toBeInTheDocument();
    const length = within(card).getByRole("textbox", { name: "KID length" });
    await userEvent.clear(length);
    await userEvent.type(length, "9");
    expect(within(card).getByText(warning)).toBeInTheDocument();
  });
});

const headroomWords =
  "The next invoice number leaves fewer than two digits of room in the KID's length. Ask the bank for a longer KID before the numbers outgrow it.";

/** The parsed body of the first request to `url` with `method`. */
const requestBody = (fetchMock: ReturnType<typeof stubFetch>, method: string, url: string) => {
  const call = fetchMock.actualCalls.find(([u, init]) => path(u) === url && (init?.method ?? "GET") === method);
  return call ? JSON.parse(String(call[1]?.body ?? "{}")) : undefined;
};
