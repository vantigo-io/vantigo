import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { connected, type GoogleServer, PERSONAL_REDIRECT_URI, personalSwitch, stubGoogleApi } from "../test/server";
import { GoogleCalendarGmailTab } from "./calendar-gmail-tab";

afterEach(() => setLanguagePreference("auto"));

const openPage = async (server: GoogleServer = {}, title = "Connection for people") => {
  const fetchMock = stubGoogleApi(server);
  renderWithProviders(<GoogleCalendarGmailTab />);
  const card = await screen.findByRole("region", { name: title });
  return { fetchMock, card };
};

const on = (overrides: Parameters<typeof personalSwitch>[0] = {}) =>
  connected({ personalConnection: personalSwitch({ enabled: true, status: "ready", ...overrides }) });

describe("GoogleCalendarGmailTab — Connection for people", () => {
  it("says what it does, the redirect URI to register, and what is still to come", async () => {
    const { card } = await openPage({ connection: connected() });
    expect(
      within(card).getByText(/^Each person connects their own Google account to Vantigo once/),
    ).toBeInTheDocument();
    expect(within(card).getByText(PERSONAL_REDIRECT_URI)).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: `Copy ${PERSONAL_REDIRECT_URI}` })).toBeInTheDocument();
    expect(within(card).getByText(/^Register this redirect URI on the Internal web client/)).toBeInTheDocument();
    expect(within(card).getByRole("status")).toHaveTextContent("Off");
    expect(screen.getByText("Calendar sync and Gmail are coming")).toBeInTheDocument();
  });

  it("turns the switch on for an Owner", async () => {
    const { fetchMock, card } = await openPage({ connection: connected() });
    await userEvent.click(within(card).getByRole("switch", { name: /^Connection for people/ }));
    await waitFor(() =>
      expect(sent(fetchMock, "PUT")).toMatchObject({
        url: "/api/v1/google/connection/personal",
        body: { enabled: true },
      }),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(within(card).getByRole("status")).toHaveTextContent("Ready"));
    expect(within(card).getByRole("switch", { name: /^Connection for people/ })).toBeChecked();
  });

  it("shows the switch read-only to someone who is not an Owner", async () => {
    const { card } = await openPage({ connection: on(), owner: false });
    expect(within(card).getByRole("switch", { name: /^Connection for people/ })).toBeDisabled();
    expect(within(card).getByText(/^An Owner does this step/)).toBeInTheDocument();
  });

  it("asks before turning off while people are connected, and turns off only on yes", async () => {
    const { fetchMock, card } = await openPage({ connection: on({ connected: 2, reconnect: 1 }) });
    const toggle = () => userEvent.click(within(card).getByRole("switch", { name: /^Connection for people/ }));

    await toggle();
    let dialog = await screen.findByRole("dialog", { name: "Turn Connection for people off?" });
    expect(within(dialog).getByText(/^Everyone's Google connection closes/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sentAll(fetchMock, "PUT")).toHaveLength(0);

    await toggle();
    dialog = await screen.findByRole("dialog", { name: "Turn Connection for people off?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Turn off" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT").body).toEqual({ enabled: false });
  });

  it("turns off without asking when nobody is connected", async () => {
    const { fetchMock, card } = await openPage({ connection: on() });
    await userEvent.click(within(card).getByRole("switch", { name: /^Connection for people/ }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT").body).toEqual({ enabled: false });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("counts the people connected and waiting to reconnect", async () => {
    const { card } = await openPage({ connection: on({ connected: 4, reconnect: 1 }) });
    expect(within(card).getByText("People connected: 4. Waiting to reconnect: 1.")).toBeInTheDocument();
  });

  it("says a missing consent", async () => {
    const { card } = await openPage({ connection: on({ status: "consent_missing" }) });
    const status = within(card).getByRole("status");
    expect(within(status).getByText("Consent missing")).toBeInTheDocument();
    expect(within(status).getByText(/^The last connect came back without everything it asked for/)).toBeInTheDocument();
  });

  const reasons = [
    ["no_web_client", "Set up the Internal web client and its secret on the Connection tab."],
    ["not_tested", "Test the connection first, so Vantigo knows your organisation's domains."],
    ["client_refused", "Google refused the web client. Check its ID and secret on the Connection tab."],
  ] as const;
  for (const [reason, sentence] of reasons)
    it(`says why it is not available (${reason})`, async () => {
      const { card } = await openPage({ connection: on({ status: "unavailable", reason }) });
      const status = within(card).getByRole("status");
      expect(within(status).getByText("Not available")).toBeInTheDocument();
      expect(within(status).getByText(sentence)).toBeInTheDocument();
    });

  it("speaks Norwegian", async () => {
    setLanguagePreference("nb");
    const { card } = await openPage(
      { connection: on({ status: "unavailable", reason: "not_tested", connected: 3, reconnect: 0 }) },
      "Tilkobling for personer",
    );
    expect(within(card).getByRole("switch", { name: /^Tilkobling for personer/ })).toBeChecked();
    const status = within(card).getByRole("status");
    expect(within(status).getByText("Ikke tilgjengelig")).toBeInTheDocument();
    expect(within(status).getByText(/^Test tilkoblingen først/)).toBeInTheDocument();
    expect(within(status).getByText("Personer tilkoblet: 3. Venter på ny tilkobling: 0.")).toBeInTheDocument();
    expect(screen.getByText("Kalendersynkronisering og Gmail kommer")).toBeInTheDocument();
  });
});
