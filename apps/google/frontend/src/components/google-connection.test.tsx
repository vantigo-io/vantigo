import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { googleNavigation } from "../api/personal";
import { jsonResponse, problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { AUTHORIZE_URL, activePersonal, type GoogleServer, personalConnection, stubGoogleApi } from "../test/server";
import { GoogleConnectButton, GoogleConnectionCard, type GoogleConnectionOutcome } from "./google-connection";

const refusal = (status: number, code: string) => problemResponse(status, `refused: ${code}`, { code });

const openCard = async (server: GoogleServer = {}, outcome?: GoogleConnectionOutcome) => {
  const fetchMock = stubGoogleApi(server);
  const open = vi.spyOn(googleNavigation, "open").mockImplementation(() => undefined);
  const onReturn = vi.fn();
  renderWithProviders(
    <StrictMode>
      <GoogleConnectionCard outcome={outcome} onReturn={onReturn} />
    </StrictMode>,
  );
  const card = await screen.findByRole("region", { name: "Google" });
  return { fetchMock, open, onReturn, card };
};

const inNorwegian = async (body: () => Promise<void>) => {
  setLanguagePreference("nb");
  try {
    await body();
  } finally {
    setLanguagePreference("auto");
  }
};

afterEach(() => setLanguagePreference("auto"));

describe("GoogleConnectionCard — the states", () => {
  it("offers Connect Google while not connected, and sends the browser to Google", async () => {
    const { fetchMock, open, card } = await openCard();
    expect(await within(card).findByText("Not connected")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Connect Google" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    expect(sent(fetchMock, "POST")).toMatchObject({
      url: "/api/v1/google/connect",
      body: { feature: "calendar", returnTo: "/settings/connections" },
    });
  });

  it("reads a closing connection as not connected", async () => {
    const { card } = await openCard({ personal: activePersonal({ state: "closing" }) });
    expect(await within(card).findByText("Not connected")).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Connect Google" })).toBeInTheDocument();
  });

  it("names the account, the date and the features while connected, and disconnects after asking", async () => {
    const { fetchMock, card } = await openCard({ personal: activePersonal({ features: ["calendar", "mail"] }) });
    expect(await within(card).findByText("Connected as kari.nordmann@contoso.no")).toBeInTheDocument();
    expect(within(card).getByText(/^Since .*2026/)).toBeInTheDocument();
    expect(within(card).getByText("Used by: Calendar, Gmail")).toBeInTheDocument();

    await userEvent.click(within(card).getByRole("button", { name: "Disconnect" }));
    const dialog = await screen.findByRole("dialog", { name: "Disconnect Google?" });
    expect(sentAll(fetchMock, "DELETE")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "Disconnect" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/google/connection/me"));
    expect(await within(card).findByText("Not connected")).toBeInTheDocument();
    expect(await screen.findByText("Google disconnected")).toBeInTheDocument();
  });

  const reasons = [
    ["expired", /^Google no longer accepts the connection: it was revoked/],
    ["blocked", "Your organisation's Google policy blocks the connection. Ask your Google Workspace administrator."],
    ["consent_missing", /^Google did not grant everything Vantigo asked for/],
    ["sign_in_again", "Sign in to Google again to keep the connection."],
    ["something_new", "Sign in to Google again to keep the connection."],
  ] as const;
  for (const [code, sentence] of reasons)
    it(`says Reconnect needed with the reason ${code}, and offers Reconnect Google`, async () => {
      const { open, card } = await openCard({ personal: activePersonal({ state: "reconnect", errorCode: code }) });
      expect(await within(card).findByText("Reconnect needed")).toBeInTheDocument();
      expect(within(card).getByText(sentence)).toBeInTheDocument();
      await userEvent.click(within(card).getByRole("button", { name: "Reconnect Google" }));
      await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    });

  for (const reason of ["switch_off", "no_account"] as const)
    it(`renders nothing and says it offers nothing when not offered (${reason})`, async () => {
      stubGoogleApi({ personal: personalConnection({ offered: false, reason, account: undefined }) });
      const onOffered = vi.fn();
      renderWithProviders(<GoogleConnectionCard onReturn={vi.fn()} onOffered={onOffered} />);
      await waitFor(() => expect(onOffered).toHaveBeenCalledWith(false));
      expect(screen.queryByRole("region", { name: "Google" })).not.toBeInTheDocument();
      expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

  it("says it offers something once the connection is read", async () => {
    stubGoogleApi();
    const onOffered = vi.fn();
    renderWithProviders(<GoogleConnectionCard onReturn={vi.fn()} onOffered={onOffered} />);
    await waitFor(() => expect(onOffered).toHaveBeenCalledWith(true));
    expect(await screen.findByRole("region", { name: "Google" })).toBeInTheDocument();
  });

  it("still says a failed callback when nothing is offered", async () => {
    const { card } = await openCard(
      { personal: personalConnection({ offered: false, reason: "switch_off", account: undefined }) },
      { outcome: "failed", code: "domain_mismatch" },
    );
    expect(
      await within(card).findByText("You signed in with an account outside your organisation's Google Workspace."),
    ).toBeInTheDocument();
  });

  it("says a refused connect in its own sentence, and stays", async () => {
    const { open, card } = await openCard({ connect: refusal(409, "account_taken") });
    await userEvent.click(await within(card).findByRole("button", { name: "Connect Google" }));
    expect(
      await screen.findByText("Another person in Vantigo has already connected this Google account."),
    ).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });

  it("never leaves for an address that is not https", async () => {
    const { open, card } = await openCard({ connect: jsonResponse(200, { authorizeUrl: "javascript:alert(1)" }) });
    await userEvent.click(await within(card).findByRole("button", { name: "Connect Google" }));
    expect(await screen.findByText("Google could not be connected")).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });
});

describe("GoogleConnectionCard — the redirect's outcome", () => {
  it("completes once, under React's double effect, and returns to the answer's returnTo", async () => {
    const { fetchMock, onReturn } = await openCard(
      { complete: { connection: activePersonal(), returnTo: "/calendar/settings" } },
      { outcome: "complete" },
    );
    await waitFor(() => expect(onReturn).toHaveBeenCalledWith("/calendar/settings"));
    expect(sentAll(fetchMock, "POST").filter((one) => one.url === "/api/v1/google/complete")).toHaveLength(1);
    expect(onReturn).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("Google connected")).toBeInTheDocument();
  });

  it("sends an unsafe returnTo to the Connections page", async () => {
    const { onReturn } = await openCard(
      { complete: { connection: activePersonal(), returnTo: "//evil.example/x" } },
      { outcome: "complete" },
    );
    await waitFor(() => expect(onReturn).toHaveBeenCalledWith("/settings/connections"));
  });

  it("does not complete without the outcome", async () => {
    const { fetchMock, card } = await openCard();
    await within(card).findByText("Not connected");
    expect(sentAll(fetchMock, "POST")).toHaveLength(0);
  });

  const completeRefusals = [
    [410, "expired", "The connect took too long, or was started in another session. Connect again."],
    [409, "account_mismatch", /another account than the one Vantigo knows you by/],
    [409, "account_taken", "Another person in Vantigo has already connected this Google account."],
    [409, "no_account", /^Vantigo knows no Google account of yours that it manages/],
    [409, "switch_off", "Connecting Google is turned off for this workspace."],
  ] as const;
  for (const [status, code, sentence] of completeRefusals)
    it(`says why complete was refused (${code}) and returns nowhere`, async () => {
      const { onReturn, card } = await openCard({ complete: refusal(status, code) }, { outcome: "complete" });
      expect(await within(card).findByText(sentence)).toBeInTheDocument();
      expect(onReturn).not.toHaveBeenCalled();
    });

  const failures = [
    ["access_denied", "You cancelled on Google's page."],
    ["domain_mismatch", /an account outside your organisation's Google Workspace/],
    ["expired", "The connect took too long, or was started in another session. Connect again."],
    ["consent_missing", /^Google did not grant everything Vantigo asked for/],
    ["failed", "Google's answer could not be used. Try again in a moment."],
    ["something_new", "Google's answer could not be used. Try again in a moment."],
  ] as const;
  for (const [code, sentence] of failures)
    it(`says the failure ${code} in its sentence, and completes nothing`, async () => {
      const { fetchMock, card } = await openCard({}, { outcome: "failed", code });
      expect(await within(card).findByText(sentence)).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Connect Google" })).toBeInTheDocument();
      expect(sentAll(fetchMock, "POST")).toHaveLength(0);
    });
});

describe("GoogleConnectionCard — in Norwegian", () => {
  it("names each state", () =>
    inNorwegian(async () => {
      const { card } = await openCard();
      expect(await within(card).findByText("Ikke tilkoblet")).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Koble til Google" })).toBeInTheDocument();
    }));

  it("names the connected account and the features", () =>
    inNorwegian(async () => {
      const { card } = await openCard({ personal: activePersonal({ features: ["calendar", "mail"] }) });
      expect(await within(card).findByText("Tilkoblet som kari.nordmann@contoso.no")).toBeInTheDocument();
      expect(within(card).getByText("Brukes av: Kalender, Gmail")).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Koble fra" })).toBeInTheDocument();
    }));

  it("asks to reconnect, saying why", () =>
    inNorwegian(async () => {
      const { card } = await openCard({ personal: activePersonal({ state: "reconnect", errorCode: "blocked" }) });
      expect(await within(card).findByText("Må kobles til på nytt")).toBeInTheDocument();
      expect(within(card).getByText(/^Organisasjonens Google-regler blokkerer tilkoblingen/)).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Koble til Google på nytt" })).toBeInTheDocument();
    }));

  it("says a failure in its sentence", () =>
    inNorwegian(async () => {
      const { card } = await openCard({}, { outcome: "failed", code: "access_denied" });
      expect(await within(card).findByText("Du avbrøt på Googles side.")).toBeInTheDocument();
      expect(within(card).getByText("Google kunne ikke kobles til")).toBeInTheDocument();
    }));
});

describe("GoogleConnectButton", () => {
  const openButton = async (server: GoogleServer = {}, feature: "calendar" | "mail" = "calendar") => {
    const fetchMock = stubGoogleApi(server);
    const open = vi.spyOn(googleNavigation, "open").mockImplementation(() => undefined);
    renderWithProviders(<GoogleConnectButton feature={feature} returnTo="/calendar/settings" />);
    await waitFor(() => expect(sentAll(fetchMock, "GET").length).toBeGreaterThan(0));
    return { fetchMock, open };
  };

  it("connects, returning to the card's page", async () => {
    const { fetchMock, open } = await openButton();
    await userEvent.click(await screen.findByRole("button", { name: "Connect Google" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    expect(sent(fetchMock, "POST")).toMatchObject({
      url: "/api/v1/google/connect",
      body: { feature: "calendar", returnTo: "/calendar/settings" },
    });
  });

  it("never leaves for an address that is not https", async () => {
    const { open } = await openButton({ connect: jsonResponse(200, { authorizeUrl: "http://accounts.google.com/x" }) });
    await userEvent.click(await screen.findByRole("button", { name: "Connect Google" }));
    expect(await screen.findByText("Google could not be connected")).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });

  it("names the account alone while connected with the feature", async () => {
    await openButton({ personal: activePersonal() });
    expect(await screen.findByText("Connected as kari.nordmann@contoso.no")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("offers Connect Google when connected without the feature asked", async () => {
    const { fetchMock, open } = await openButton({ personal: activePersonal({ features: ["calendar"] }) }, "mail");
    expect(await screen.findByText("Connected as kari.nordmann@contoso.no")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Connect Google" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    expect(sent(fetchMock, "POST").body).toEqual({ feature: "mail", returnTo: "/calendar/settings" });
  });

  it("asks to reconnect, saying why", async () => {
    await openButton({ personal: activePersonal({ state: "reconnect", errorCode: "expired" }) });
    expect(await screen.findByText("Reconnect needed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reconnect Google" })).toBeInTheDocument();
  });

  it("renders nothing when nothing can be connected", async () => {
    const { fetchMock } = await openButton({ personal: personalConnection({ offered: false, reason: "no_account" }) });
    await waitFor(() => expect(sentAll(fetchMock, "GET")).toHaveLength(1));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByText(/connect/i)).not.toBeInTheDocument();
  });

  it("speaks Norwegian", () =>
    inNorwegian(async () => {
      await openButton();
      expect(await screen.findByRole("button", { name: "Koble til Google" })).toBeInTheDocument();
    }));
});
