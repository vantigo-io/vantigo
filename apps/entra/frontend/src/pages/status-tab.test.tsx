import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { requiredPermissions } from "../api/connection";
import { sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { connected, type EntraServer, stubEntraApi, unconfigured } from "../test/server";
import { EntraStatusTab } from "./status-tab";

/** The Status tab as the host mounts it, ready once its first card is drawn. */
const openPage = async (server: EntraServer = {}) => {
  const fetchMock = stubEntraApi(server);
  renderWithProviders(<EntraStatusTab />);
  await screen.findByRole("region", { name: "What is turned on" });
  return { fetchMock };
};

const region = (name: string) => screen.getByRole("region", { name });

describe("EntraStatusTab — the Status card", () => {
  it("names each missing permission, the reason in words, the counts and the events", async () => {
    await openPage({
      connection: connected({
        statusReason: "role_missing",
        permissionsPresent: requiredPermissions.filter((name) => name !== "UserAuthMethod-TAP.ReadWrite.All"),
        permissionsMissing: ["UserAuthMethod-TAP.ReadWrite.All"],
      }),
      events: [
        {
          id: 7,
          at: "2026-10-19T11:00:00Z",
          actor: "system:staff-accounts",
          action: "entra.user.activated",
          upn: "ola.nordmann@contoso.no",
          codes: ["licence_unavailable:ENTERPRISEPACK"],
        },
        {
          id: 8,
          at: "2026-10-19T10:00:00Z",
          actor: "system:staff-accounts",
          action: "entra.user.deactivated",
          upn: "kari@contoso.no",
          codes: ["refused:adoption_unconfirmed"],
        },
      ],
    });
    const status = region("Status");
    expect(within(status).getByText("Connected")).toBeInTheDocument();
    expect(within(status).getByText(/most likely lacks the User Administrator role/)).toBeInTheDocument();
    expect(within(status).getByText("Contoso AS")).toBeInTheDocument();
    const missing = within(status).getByRole("list", { name: "Missing permissions" });
    expect(
      within(missing)
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual(["UserAuthMethod-TAP.ReadWrite.All"]);
    expect(within(status).getByText("3 in all: 2 enabled, 1 disabled, 0 deleted")).toBeInTheDocument();
    const events = await within(status).findByRole("list", { name: "Recent events" });
    expect(
      within(events).getByText("An account was enabled · ola.nordmann@contoso.no · by Staff's accounts run"),
    ).toBeInTheDocument();
    expect(within(events).getByText("licence ENTERPRISEPACK has no seat free")).toBeInTheDocument();
    expect(
      within(events).getByText("refused: found in the tenant, not created, and its adoption is not yet confirmed"),
    ).toBeInTheDocument();
  });

  it("words the groups' events and codes", async () => {
    await openPage({
      connection: connected(),
      events: [
        {
          id: 1,
          at: "2026-10-19T11:00:00Z",
          actor: "system:staff-accounts",
          action: "entra.group.retired",
          codes: ["security_kept", "removed:3", "owners_removed:1"],
        },
        {
          id: 2,
          at: "2026-10-19T10:00:00Z",
          actor: "system:staff-accounts",
          action: "entra.group.adopted",
          codes: ["type:security", "refused:adoption_unconfirmed"],
        },
      ],
    });
    const events = await within(region("Status")).findByRole("list", { name: "Recent events" });
    expect(within(events).getByText(/A group was emptied and kept/)).toBeInTheDocument();
    expect(
      within(events).getByText(
        "a security group is never deleted: its people were removed and it was kept; 3 members removed; 1 owners removed",
      ),
    ).toBeInTheDocument();
    expect(within(events).getByText(/A group was found and adopted/)).toBeInTheDocument();
    expect(
      within(events).getByText(
        "type security; refused: found in the tenant, not created, and its adoption is not yet confirmed",
      ),
    ).toBeInTheDocument();
  });
});

describe("EntraStatusTab — an adopted account", () => {
  const adopted = {
    objectId: "33333333-3333-3333-3333-333333333333",
    employeeNumber: "E-4",
    userPrincipalName: "kari@contoso.no",
    state: "enabled" as const,
    createdByUs: false,
    adoptionConfirmed: false,
    hasPass: false,
    reported: ["adoption_unconfirmed"],
    createdAt: "2026-10-19T08:00:00Z",
  };
  const created = {
    ...adopted,
    objectId: "44444444-4444-4444-4444-444444444444",
    employeeNumber: "E-5",
    userPrincipalName: "ola@contoso.no",
    createdByUs: true,
    reported: [],
  };

  it("asks to confirm a found account, naming it and what confirming allows, then confirms it", async () => {
    const { fetchMock } = await openPage({ connection: connected(), accounts: [adopted, created] });
    const table = await within(region("Status")).findByRole("table", { name: "The module's accounts" });
    expect(within(table).getByText("Adopted — confirm")).toBeInTheDocument();
    expect(
      within(table).getByText(
        "found in the tenant, not created: nothing is written to it until its adoption is confirmed",
      ),
    ).toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: "Confirm ola@contoso.no" })).not.toBeInTheDocument();

    await userEvent.click(within(table).getByRole("button", { name: "Confirm kari@contoso.no" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the adoption of kari@contoso.no?" });
    expect(
      within(dialog).getByText(/enable it and issue a Temporary Access Pass, and disable and delete it/),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").map((call) => call.url)).toEqual([
        "/api/v1/entra/accounts/33333333-3333-3333-3333-333333333333/confirm",
      ]),
    );
    expect(await screen.findByText("Adoption confirmed")).toBeInTheDocument();
    await waitFor(() => expect(within(table).queryByText("Adopted — confirm")).not.toBeInTheDocument());
  });

  it("says a refused confirmation in words", async () => {
    await openPage({
      connection: connected(),
      accounts: [adopted],
      handle: (method, path) =>
        method === "POST" && path.endsWith("/confirm")
          ? new Response(JSON.stringify({ title: "Adoption confirmed", status: 409, code: "adoption_confirmed" }), {
              status: 409,
              headers: { "Content-Type": "application/problem+json" },
            })
          : undefined,
    });
    const table = await within(region("Status")).findByRole("table", { name: "The module's accounts" });
    await userEvent.click(within(table).getByRole("button", { name: "Confirm kari@contoso.no" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Confirm the adoption of kari@contoso.no?" })).getByRole(
        "button",
        { name: "Confirm" },
      ),
    );
    expect(
      await screen.findByText("The account is Vantigo's already: its adoption was confirmed, or Vantigo created it."),
    ).toBeInTheDocument();
  });
});

describe("EntraStatusTab — permissions Vantigo does not use", () => {
  it("warns on the Status card, naming each, while the connection stays connected", async () => {
    await openPage({
      connection: connected({
        permissionsPresent: [...requiredPermissions, "Mail.ReadWrite", "Sites.Read.All"],
        permissionsUnexpected: ["Mail.ReadWrite", "Sites.Read.All"],
      }),
    });
    const status = region("Status");
    const list = within(status).getByRole("list", { name: "The application holds permissions Vantigo does not use:" });
    expect(within(list).getByText("Mail.ReadWrite")).toBeInTheDocument();
    expect(within(list).getByText("Sites.Read.All")).toBeInTheDocument();
    expect(
      within(status).getByText(/^They may be left from an earlier setup, or wider than needed\./),
    ).toBeInTheDocument();
    expect(within(status).getByText("Connected")).toBeInTheDocument();
  });

  it("says nothing when the token carries only what Vantigo uses", async () => {
    await openPage({ connection: connected() });
    expect(within(region("Status")).queryByText(/permissions Vantigo does not use/)).not.toBeInTheDocument();
  });

  it("warns in Norwegian", async () => {
    setLanguagePreference("nb");
    try {
      stubEntraApi({ connection: connected({ permissionsUnexpected: ["Mail.ReadWrite"] }) });
      renderWithProviders(<EntraStatusTab />);
      expect(await screen.findByText("Applikasjonen har tillatelser Vantigo ikke bruker:")).toBeInTheDocument();
      expect(screen.getByText(/^De kan være igjen fra et tidligere oppsett/)).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});

/** The Outlook connection switch on, and the Mail channels switch on, as GET /connection answers them. */
const outlookOn = {
  enabled: true,
  status: "ready",
  connected: 0,
  reconnect: 0,
  redirectUri: "https://vantigo.example/api/v1/entra/outlook/callback",
  notifications: { status: "not_configured", subscriptions: 0 },
} as const;

describe("EntraStatusTab — the progress and the switches", () => {
  it("counts the required steps and sends the first open one's reader to the Connection tab", async () => {
    await openPage({ connection: unconfigured() });
    const progress = screen.getByRole("status", { name: "Progress" });
    expect(progress).toHaveTextContent("0 of 4 required steps done — continue in");
    expect(within(progress).getByRole("link", { name: "Connection" })).toHaveAttribute(
      "href",
      "/workspace/entra/connection",
    );
  });

  it("names the Accounts tab once steps 1 to 3 are done and the account rules are not", async () => {
    await openPage({ connection: connected({ settings: { ...connected().settings, upnDomain: undefined } }) });
    const progress = screen.getByRole("status", { name: "Progress" });
    expect(progress).toHaveTextContent("3 of 4 required steps done — continue in");
    expect(within(progress).getByRole("link", { name: "Accounts" })).toHaveAttribute(
      "href",
      "/workspace/entra/accounts",
    );
  });

  it("says nothing of progress once all four are done", async () => {
    await openPage({ connection: connected() });
    expect(screen.queryByRole("status", { name: "Progress" })).not.toBeInTheDocument();
  });

  it("draws a notice rather than the Status card while the connection is unconfigured", async () => {
    await openPage({ connection: unconfigured() });
    expect(screen.getByText("Not connected yet")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Status" })).not.toBeInTheDocument();
  });

  it("gives one line per switch, on or off, each linking to the Outlook and mail tab", async () => {
    await openPage({ connection: connected({ outlookConnect: outlookOn }) });
    const list = screen.getByRole("list", { name: "What is turned on" });
    const [outlook, mail] = within(list).getAllByRole("listitem");
    expect(within(outlook as HTMLElement).getByText("Outlook connection and calendar sync")).toBeInTheDocument();
    expect(within(outlook as HTMLElement).getByText("On")).toBeInTheDocument();
    expect(within(mail as HTMLElement).getByText("Mail channels")).toBeInTheDocument();
    expect(within(mail as HTMLElement).getByText("Off")).toBeInTheDocument();
    for (const line of [outlook, mail])
      expect(within(line as HTMLElement).getByRole("link", { name: "Outlook and mail" })).toHaveAttribute(
        "href",
        "/workspace/entra/outlook",
      );
  });

  it("speaks Norwegian", async () => {
    setLanguagePreference("nb");
    try {
      stubEntraApi({ connection: connected({ outlookConnect: outlookOn }) });
      renderWithProviders(<EntraStatusTab />);
      const list = await screen.findByRole("list", { name: "Hva som er slått på" });
      expect(within(list).getByText("Outlook-tilkobling og kalendersynkronisering")).toBeInTheDocument();
      expect(within(list).getByText("På")).toBeInTheDocument();
      expect(within(list).getByText("Av")).toBeInTheDocument();
      expect(within(list).getAllByRole("link", { name: "Outlook og e-post" })).toHaveLength(2);
    } finally {
      setLanguagePreference("auto");
    }
  });
});
