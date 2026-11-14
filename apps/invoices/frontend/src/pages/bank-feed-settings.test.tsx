import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { BankFeed, BankFeedOffer } from "../api/bank-feed";
import { jsonResponse, path, refusal } from "../test/api";
import { bankServer } from "../test/bank-server";
import { renderWithProviders } from "../test/render";
import { BankFeedSettingsPage } from "./bank-feed-settings";

/** A connection as the server answers it: Folio, the key ending in 2f9a, ok. */
const connected = (overrides: Partial<BankFeed> = {}): BankFeed => ({
  connected: true,
  enabledOnInstance: true,
  provider: "folio",
  keyLastFour: "2f9a",
  status: "ok",
  connectedBy: "00000000-0000-4000-8000-000000000001",
  connectedAt: "2026-09-10T08:00:00Z",
  accounts: [],
  ...overrides,
});

/** The bank's accounts: the seller's, a card and a stranger's. */
const offers: BankFeedOffer[] = [
  { account: "86011117947", name: "Drift", kind: "operational", canEnable: true },
  { account: "97100000001", name: "Kort", kind: "card", canEnable: false, whyNot: "card" },
  { account: "15032080119", name: "Skatt", kind: "tax", canEnable: false, whyNot: "not_sellers_account" },
];

/** The body of the one request sent with this method to this URL, or undefined. */
const bodyOf = (fetchMock: ReturnType<typeof bankServer>, method: string, url: string) => {
  const call = fetchMock.actualCalls.find(([u, init]) => path(u) === url && (init?.method ?? "GET") === method);
  return call ? JSON.parse(String(call[1]?.body)) : undefined;
};

describe("BankFeedSettings_ConnectAndAccounts", () => {
  it("connects with the key typed, says what a key can and cannot do, and never shows the key", async () => {
    const fetchMock = bankServer({
      answers: { "PUT /api/v1/invoices/bank-feed": () => jsonResponse(200, connected()) },
    });
    renderWithProviders(<BankFeedSettingsPage />);
    const card = await screen.findByTestId("bank-feed-connection");

    expect(within(card).getByText(/It cannot move money/)).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Connect" })).toBeDisabled();
    await userEvent.type(within(card).getByLabelText("API key"), "  folio-key-123456  ");
    await userEvent.click(within(card).getByRole("button", { name: "Connect" }));

    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/bank-feed")).toEqual({
        provider: "folio",
        apiKey: "folio-key-123456",
      }),
    );
    expect(await screen.findByText("The bank is connected")).toBeInTheDocument();
    await waitFor(() => expect(within(card).getByLabelText("API key")).toHaveValue(""));
  });

  it("says a refused key in words, not the server's English", async () => {
    bankServer({
      answers: { "PUT /api/v1/invoices/bank-feed": () => refusal(422, "bank_feed_credential_rejected") },
    });
    renderWithProviders(<BankFeedSettingsPage />);
    const card = await screen.findByTestId("bank-feed-connection");

    await userEvent.type(within(card).getByLabelText("API key"), "folio-key-123456");
    await userEvent.click(within(card).getByRole("button", { name: "Connect" }));
    expect(
      await within(card).findByText("The bank refused the API key. Create a new key in the bank and save it."),
    ).toBeInTheDocument();
    expect(within(card).queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("says when the installation allows no bank feed, and offers no connect", async () => {
    bankServer({ bankFeed: { connected: false, enabledOnInstance: false, accounts: [] } });
    renderWithProviders(<BankFeedSettingsPage />);

    expect(await screen.findByText("The bank feed is switched off")).toBeInTheDocument();
    const card = screen.getByTestId("bank-feed-connection");
    await userEvent.type(within(card).getByLabelText("API key"), "folio-key-123456");
    expect(within(card).getByRole("button", { name: "Connect" })).toBeDisabled();
    expect(screen.queryByTestId("bank-feed-accounts")).not.toBeInTheDocument();
  });

  it("shows the last four and a refusal on record, and asks before disconnecting", async () => {
    const fetchMock = bankServer({
      bankFeed: connected({ status: "unauthorized", rejectedAt: "2026-09-11T08:00:00Z" }),
      offers,
      answers: { "DELETE /api/v1/invoices/bank-feed": () => new Response(null, { status: 204 }) },
    });
    renderWithProviders(<BankFeedSettingsPage />);
    const card = await screen.findByTestId("bank-feed-connection");

    expect(within(card).getByTestId("bank-feed-key")).toHaveTextContent("the key ending 2f9a");
    expect(within(card).getByText("The bank refused the key")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Disconnect" }));
    const dialog = await screen.findByRole("dialog");
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Disconnect" }));
    expect(await screen.findByText("The bank is disconnected")).toBeInTheDocument();
  });

  it("lists the bank's accounts, says why one cannot be switched on, and switches one on from its start day", async () => {
    const fetchMock = bankServer({
      bankFeed: connected(),
      offers,
      answers: {
        "PUT /api/v1/invoices/bank-feed/accounts/86011117947": () =>
          jsonResponse(200, { account: "86011117947", enabled: true, startDay: "2026-09-12", status: "ok" }),
      },
    });
    renderWithProviders(<BankFeedSettingsPage />);
    const accounts = await screen.findByTestId("bank-feed-accounts");

    const card = await within(accounts).findByTestId("bank-feed-account-97100000001");
    expect(within(card).getByText("A card account is never read.")).toBeInTheDocument();
    expect(within(card).getByRole("switch")).toBeDisabled();
    const stranger = within(accounts).getByTestId("bank-feed-account-15032080119");
    expect(
      within(stranger).getByText("Neither the seller's bank account nor one an issued invoice printed."),
    ).toBeInTheDocument();

    const seller = within(accounts).getByTestId("bank-feed-account-86011117947");
    expect(within(seller).getByText("8601.11.17947")).toBeInTheDocument();
    await userEvent.click(within(seller).getByRole("switch", { name: "Bank feed for 8601.11.17947 · Drift" }));
    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/bank-feed/accounts/86011117947")).toEqual({
        enabled: true,
        startDay: "2026-09-12",
      }),
    );
    expect(await screen.findByText("The bank feed reads the account")).toBeInTheDocument();
  });

  it("switches an account the feed reads off, and shows its status", async () => {
    const fetchMock = bankServer({
      bankFeed: connected({
        accounts: [
          {
            account: "86011117947",
            enabled: true,
            startDay: "2026-09-01",
            status: "ok",
            lastSyncedAt: "2026-09-12T07:00:00Z",
          },
        ],
      }),
      offers,
      answers: {
        "PUT /api/v1/invoices/bank-feed/accounts/86011117947": () =>
          jsonResponse(200, { account: "86011117947", enabled: false, startDay: "2026-09-01", status: "ok" }),
      },
    });
    renderWithProviders(<BankFeedSettingsPage />);
    const seller = await screen.findByTestId("bank-feed-account-86011117947");

    expect(within(seller).getByText("OK")).toBeInTheDocument();
    expect(within(seller).getByText(/^Last read/)).toBeInTheDocument();
    await userEvent.click(within(seller).getByRole("switch"));
    await waitFor(() =>
      expect(bodyOf(fetchMock, "PUT", "/api/v1/invoices/bank-feed/accounts/86011117947")).toEqual({ enabled: false }),
    );
  });

  it("says a key the bank refuses when the accounts are read", async () => {
    bankServer({
      bankFeed: connected(),
      answers: { "GET /api/v1/invoices/bank-feed/accounts": () => refusal(422, "bank_feed_credential_rejected") },
    });
    renderWithProviders(<BankFeedSettingsPage />);
    const accounts = await screen.findByTestId("bank-feed-accounts");
    expect(await within(accounts).findByText("Could not read the bank's accounts")).toBeInTheDocument();
    expect(
      within(accounts).getByText("The bank refused the API key. Create a new key in the bank and save it."),
    ).toBeInTheDocument();
  });
});
