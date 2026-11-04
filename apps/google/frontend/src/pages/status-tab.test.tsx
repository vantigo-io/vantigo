import { screen, within } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import type { GoogleConnection } from "../api/connection";
import { renderWithProviders } from "../test/render";
import { connected, type GoogleServer, SCOPES, stubGoogleApi, unconfigured } from "../test/server";
import { GoogleStatusTab } from "./status-tab";

afterEach(() => setLanguagePreference("auto"));

const openPage = async (server: GoogleServer = {}, title = "Status") => {
  const fetchMock = stubGoogleApi(server);
  renderWithProviders(<GoogleStatusTab />);
  await screen.findByRole("region", { name: title });
  return { fetchMock };
};

const card = () => screen.getByRole("region", { name: "Status" });

const failing = (reason: string): GoogleConnection =>
  connected({ status: "failing", statusReason: reason, scopesGranted: [], scopesMissing: [] });

describe("GoogleStatusTab", () => {
  it("shows the organisation, its domains, the last test and the scopes", async () => {
    await openPage({ connection: connected({ scopesUnexpected: ["https://www.googleapis.com/auth/drive"] }) });
    const status = card();
    expect(within(status).getByText("Connected")).toBeInTheDocument();
    expect(within(status).getByText("Contoso AS")).toBeInTheDocument();
    expect(within(status).getByText("C01abc234")).toBeInTheDocument();
    expect(within(status).getByText("contoso.no")).toBeInTheDocument();
    expect(within(status).getByText("contoso.no, contoso.com")).toBeInTheDocument();
    expect(within(status).getByText(/^Nov 2, 2026/)).toBeInTheDocument();
    expect(within(status).getByText("Every scope Vantigo needs is granted.")).toBeInTheDocument();
    const granted = within(status).getByRole("list", { name: "Granted scopes" });
    for (const scope of SCOPES) expect(within(granted).getByText(scope)).toBeInTheDocument();
    const unexpected = within(status).getByRole("list", { name: "Scopes Vantigo does not use" });
    expect(within(unexpected).getByText("https://www.googleapis.com/auth/drive")).toBeInTheDocument();
    expect(within(status).getByText(/Domain-wide delegation reaches every account/)).toBeInTheDocument();
  });

  it("lists the scopes Google refused", async () => {
    await openPage({ connection: connected({ scopesGranted: [SCOPES[0]], scopesMissing: [SCOPES[1]] }) });
    const missing = within(card()).getByRole("list", { name: "Missing scopes" });
    expect(within(missing).getByText(SCOPES[1])).toBeInTheDocument();
  });

  it.each([
    [
      "pending_delegation",
      "not_delegated",
      "Waiting for delegation",
      "Google has not granted the scopes yet. Domain-wide delegation can take up to 24 hours to apply, and with multi-party approval a second super admin must approve it.",
    ],
    ["failing", "credential_invalid", "Failing", /Google refused every key, or does not know the admin to impersonate/],
    ["failing", "admin_refused", "Failing", /The admin to impersonate may not read the organisation/],
    ["failing", "google_unreachable", "Failing", "Google did not answer. Test the connection again in a while."],
  ] as const)("says %s (%s) in words", async (status, reason, badge, sentence) => {
    await openPage({ connection: { ...failing(reason), status } });
    expect(within(card()).getByText(badge)).toBeInTheDocument();
    expect(within(card()).getByText(sentence)).toBeInTheDocument();
  });

  it("points to the Connection tab while nothing is set up", async () => {
    await openPage({ connection: unconfigured() });
    expect(screen.getByText("Not connected yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Connection" })).toHaveAttribute("href", "/workspace/google/connection");
    expect(within(card()).getByText("Not set up")).toBeInTheDocument();
    expect(within(card()).getByText("The scopes are checked when the connection is tested.")).toBeInTheDocument();
  });

  it("lists the recent events with their action, time and codes", async () => {
    await openPage({
      connection: connected(),
      events: [
        {
          id: 2,
          at: "2026-11-02T10:00:00Z",
          actor: "user-1",
          action: "google.connection.tested",
          codes: ["connected"],
        },
        { id: 1, at: "2026-11-01T08:00:00Z", actor: "system:google", action: "google.credential.refused", codes: [] },
      ],
    });
    const events = await screen.findByRole("list", { name: "Recent events" });
    const items = within(events).getAllByRole("listitem");
    expect(items[0]).toHaveTextContent("The connection was tested");
    expect(items[0]).toHaveTextContent("Connected");
    expect(items[1]).toHaveTextContent("Google refused a key · by Vantigo");
  });

  it("says it all in Norwegian", async () => {
    setLanguagePreference("nb");
    await openPage({ connection: { ...failing("not_delegated"), status: "pending_delegation" } });
    expect(within(card()).getByText("Venter på delegering")).toBeInTheDocument();
    expect(within(card()).getByText(/Domenedekkende delegering kan ta opptil 24 timer/)).toBeInTheDocument();
    expect(within(card()).getByText("Organisasjon")).toBeInTheDocument();
    expect(await screen.findByText("Ingenting har skjedd ennå.")).toBeInTheDocument();
  });
});
