import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { outlookNavigation } from "../api/outlook";
import { problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { AUTHORIZE_URL, activeOutlook, type EntraServer, outlookConnection, stubEntraApi } from "../test/server";
import { OutlookConnectButton, OutlookConnectionCard, type OutlookOutcome } from "./outlook-connection";

const refusal = (status: number, code: string) => problemResponse(status, `refused: ${code}`, { code });

const openCard = async (server: EntraServer = {}, outcome?: OutlookOutcome) => {
  const fetchMock = stubEntraApi(server);
  const open = vi.spyOn(outlookNavigation, "open").mockImplementation(() => undefined);
  const onReturn = vi.fn();
  renderWithProviders(
    <StrictMode>
      <OutlookConnectionCard outcome={outcome} onReturn={onReturn} />
    </StrictMode>,
  );
  const card = await screen.findByRole("region", { name: "Outlook" });
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

describe("OutlookConnectionCard — the states", () => {
  it("offers Connect Outlook while not connected, and sends the browser to Microsoft", async () => {
    const { fetchMock, open, card } = await openCard();
    expect(await within(card).findByText("Not connected")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Connect Outlook" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    expect(sent(fetchMock, "POST")).toMatchObject({
      url: "/api/v1/entra/outlook/connect",
      body: { feature: "calendar", returnTo: "/settings/connections" },
    });
  });

  it("reads a closing connection as not connected", async () => {
    const { card } = await openCard({ outlook: activeOutlook({ state: "closing" }) });
    expect(await within(card).findByText("Not connected")).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "Connect Outlook" })).toBeInTheDocument();
  });

  it("names the account, the date and the features while connected, and disconnects after asking", async () => {
    const { fetchMock, card } = await openCard({ outlook: activeOutlook() });
    expect(await within(card).findByText("Connected as kari@contoso.no")).toBeInTheDocument();
    expect(within(card).getByText(/^Since .*2026/)).toBeInTheDocument();
    expect(within(card).getByText("Used by: Calendar")).toBeInTheDocument();

    await userEvent.click(within(card).getByRole("button", { name: "Disconnect" }));
    const dialog = await screen.findByRole("dialog", { name: "Disconnect Outlook?" });
    expect(sentAll(fetchMock, "DELETE")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "Disconnect" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/entra/outlook/connection"));
    expect(await within(card).findByText("Not connected")).toBeInTheDocument();
    expect(await screen.findByText("Outlook disconnected")).toBeInTheDocument();
  });

  const reasons = [
    ["expired", "Your sign-in to Microsoft has expired."],
    ["sign_in_again", "Microsoft asks you to sign in again."],
    ["mfa", "Microsoft asks you to complete multi-factor authentication."],
    ["blocked", /Conditional Access blocked the connection/],
    ["consent_missing", /has not consented to a permission the connection needs/],
  ] as const;
  for (const [code, sentence] of reasons)
    it(`says Reconnect needed with the reason ${code}, and offers Reconnect Outlook`, async () => {
      const { open, card } = await openCard({ outlook: activeOutlook({ state: "reconnect", errorCode: code }) });
      expect(await within(card).findByText("Reconnect needed")).toBeInTheDocument();
      expect(within(card).getByText(sentence)).toBeInTheDocument();
      await userEvent.click(within(card).getByRole("button", { name: "Reconnect Outlook" }));
      await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    });

  for (const reason of ["switch_off", "no_account"] as const)
    it(`says there is nothing to connect when not offered (${reason})`, async () => {
      const { card } = await openCard({ outlook: outlookConnection({ offered: false, reason, account: undefined }) });
      expect(await within(card).findByText(/^Nothing to connect/)).toBeInTheDocument();
      expect(within(card).queryByRole("button")).not.toBeInTheDocument();
    });

  it("says a refused connect in its own sentence, and stays", async () => {
    const { open, card } = await openCard({ connect: refusal(409, "account_taken") });
    await userEvent.click(await within(card).findByRole("button", { name: "Connect Outlook" }));
    expect(
      await screen.findByText("Another person in Vantigo has already connected this Microsoft account."),
    ).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });

  it("never leaves for an address that is not https", async () => {
    const { open, card } = await openCard({
      connect: new Response(JSON.stringify({ authorizeUrl: "javascript:alert(1)" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    });
    await userEvent.click(await within(card).findByRole("button", { name: "Connect Outlook" }));
    expect(await screen.findByText("Outlook could not be connected")).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });
});

describe("OutlookConnectionCard — the redirect's outcome", () => {
  it("completes once, under React's double effect, and returns to the answer's returnTo", async () => {
    const { fetchMock, onReturn } = await openCard(
      { complete: { connection: activeOutlook(), returnTo: "/calendar/settings" } },
      { outlook: "complete" },
    );
    await waitFor(() => expect(onReturn).toHaveBeenCalledWith("/calendar/settings"));
    expect(sentAll(fetchMock, "POST").filter((one) => one.url === "/api/v1/entra/outlook/complete")).toHaveLength(1);
    expect(onReturn).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("Outlook connected")).toBeInTheDocument();
  });

  it("sends an unsafe returnTo to the Connections page", async () => {
    const { onReturn } = await openCard(
      { complete: { connection: activeOutlook(), returnTo: "//evil.example/x" } },
      { outlook: "complete" },
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
    [409, "account_taken", "Another person in Vantigo has already connected this Microsoft account."],
    [409, "no_account", /Vantigo knows no Microsoft account of yours that it manages/],
    [409, "switch_off", "The Outlook connection is turned off for this workspace."],
  ] as const;
  for (const [status, code, sentence] of completeRefusals)
    it(`says why complete was refused (${code}) and returns nowhere`, async () => {
      const { onReturn, card } = await openCard({ complete: refusal(status, code) }, { outlook: "complete" });
      expect(await within(card).findByText(sentence)).toBeInTheDocument();
      expect(onReturn).not.toHaveBeenCalled();
    });

  const failures = [
    ["access_denied", "The sign-in to Microsoft was cancelled or refused. Nothing was connected."],
    ["tenant_mismatch", /an account from another organisation/],
    ["expired", "The connect took too long, or was started in another session. Connect again."],
    ["failed", "Microsoft's answer could not be used. Try again in a moment."],
    ["consent_missing", /Ask your administrator to grant admin consent/],
    ["something_new", "Microsoft's answer could not be used. Try again in a moment."],
  ] as const;
  for (const [code, sentence] of failures)
    it(`says the failure ${code} in its sentence, and completes nothing`, async () => {
      const { fetchMock, card } = await openCard({}, { outlook: "failed", code });
      expect(await within(card).findByText(sentence)).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Connect Outlook" })).toBeInTheDocument();
      expect(sentAll(fetchMock, "POST")).toHaveLength(0);
    });
});

describe("OutlookConnectionCard — in Norwegian", () => {
  it("names each state", () =>
    inNorwegian(async () => {
      const { card } = await openCard();
      expect(await within(card).findByText("Ikke tilkoblet")).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Koble til Outlook" })).toBeInTheDocument();
    }));

  it("names the connected account and the feature", () =>
    inNorwegian(async () => {
      const { card } = await openCard({ outlook: activeOutlook() });
      expect(await within(card).findByText("Tilkoblet som kari@contoso.no")).toBeInTheDocument();
      expect(within(card).getByText("Brukes av: Kalender")).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Koble fra" })).toBeInTheDocument();
    }));

  it("asks to reconnect, saying why", () =>
    inNorwegian(async () => {
      const { card } = await openCard({ outlook: activeOutlook({ state: "reconnect", errorCode: "mfa" }) });
      expect(await within(card).findByText("Må kobles til på nytt")).toBeInTheDocument();
      expect(within(card).getByText("Microsoft ber deg fullføre flerfaktorautentisering.")).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Koble til Outlook på nytt" })).toBeInTheDocument();
    }));

  it("says a failure in its sentence", () =>
    inNorwegian(async () => {
      const { card } = await openCard({}, { outlook: "failed", code: "tenant_mismatch" });
      expect(await within(card).findByText(/^Du logget på med en konto fra en annen organisasjon/)).toBeInTheDocument();
    }));

  it("says there is nothing to connect", () =>
    inNorwegian(async () => {
      const { card } = await openCard({ outlook: outlookConnection({ offered: false, reason: "switch_off" }) });
      expect(await within(card).findByText(/^Ingenting å koble til/)).toBeInTheDocument();
    }));
});

describe("OutlookConnectButton", () => {
  const openButton = async (server: EntraServer = {}) => {
    const fetchMock = stubEntraApi(server);
    const open = vi.spyOn(outlookNavigation, "open").mockImplementation(() => undefined);
    renderWithProviders(<OutlookConnectButton feature="calendar" returnTo="/calendar/settings" />);
    await waitFor(() => expect(sentAll(fetchMock, "GET").length).toBeGreaterThan(0));
    return { fetchMock, open };
  };

  it("connects, returning to the card's page", async () => {
    const { fetchMock, open } = await openButton();
    await userEvent.click(await screen.findByRole("button", { name: "Connect Outlook" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    expect(sent(fetchMock, "POST").body).toEqual({ feature: "calendar", returnTo: "/calendar/settings" });
  });

  it("names the account while connected", async () => {
    await openButton({ outlook: activeOutlook() });
    expect(await screen.findByText("Connected as kari@contoso.no")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("offers Connect Outlook when connected for another feature but lacking the requested one", async () => {
    const fetchMock = stubEntraApi({ outlook: activeOutlook({ features: ["calendar"] }) });
    const open = vi.spyOn(outlookNavigation, "open").mockImplementation(() => undefined);
    renderWithProviders(<OutlookConnectButton feature="mail" returnTo="/communications/inbox" />);
    expect(await screen.findByText("Connected as kari@contoso.no")).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Connect Outlook" });
    await userEvent.click(button);
    await waitFor(() => expect(open).toHaveBeenCalledWith(AUTHORIZE_URL));
    expect(sent(fetchMock, "POST").body).toEqual({ feature: "mail", returnTo: "/communications/inbox" });
  });

  it("asks to reconnect, saying why", async () => {
    await openButton({ outlook: activeOutlook({ state: "reconnect", errorCode: "expired" }) });
    expect(await screen.findByText("Reconnect needed")).toBeInTheDocument();
    expect(screen.getByText("Your sign-in to Microsoft has expired.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reconnect Outlook" })).toBeInTheDocument();
  });

  it("renders nothing when nothing can be connected", async () => {
    const { fetchMock } = await openButton({ outlook: outlookConnection({ offered: false, reason: "no_account" }) });
    await waitFor(() => expect(sentAll(fetchMock, "GET")).toHaveLength(1));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByText(/connect/i)).not.toBeInTheDocument();
  });

  it("speaks Norwegian", () =>
    inNorwegian(async () => {
      await openButton();
      expect(await screen.findByRole("button", { name: "Koble til Outlook" })).toBeInTheDocument();
    }));
});
