import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { type EntraOutlookConnect, requiredPermissions } from "../api/connection";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { CLIENT_ID, connected, type EntraServer, stubEntraApi } from "../test/server";
import { EntraOutlookTab } from "./outlook-tab";

// The change notifications' state every Outlook connection fixture answers (calendar Outlook sync design O11.7).
const notifications = { status: "not_configured", subscriptions: 0 } as const;

/** The Outlook connection switch on, as GET /connection answers it, ready unless overridden. */
const outlookConnect = (overrides: Partial<EntraOutlookConnect> = {}): EntraOutlookConnect => ({
  enabled: true,
  status: "ready",
  connected: 0,
  reconnect: 0,
  redirectUri: "https://vantigo.example/api/v1/entra/outlook/callback",
  notifications,
  ...overrides,
});

/** The Outlook and mail tab as the host mounts it, ready once its first card is drawn. */
const openPage = async (server: EntraServer = {}) => {
  const fetchMock = stubEntraApi(server);
  renderWithProviders(<EntraOutlookTab />);
  await screen.findByRole("region", { name: "Outlook and mail" });
  return { fetchMock };
};

const region = (name: string) => screen.getByRole("region", { name });

describe("EntraOutlookTab — the Outlook connection", () => {
  it("is off by default and says nothing of its state", async () => {
    await openPage({ connection: connected() });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    const toggle = within(form).getByRole("switch", {
      name: /^Outlook connection: let people connect their Outlook to Vantigo/,
    });
    expect(toggle).not.toBeChecked();
    expect(
      within(form).getByText(/^Each person connects their own Outlook under Settings › Connections/),
    ).toBeInTheDocument();
    expect(within(form).queryByText(/application permission/)).not.toBeInTheDocument();
    expect(within(form).queryByRole("status")).not.toBeInTheDocument();
  });

  it("is sent as outlookConnect alone", async () => {
    const { fetchMock } = await openPage({ connection: connected() });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    await userEvent.click(within(form).getByRole("switch", { name: /^Outlook connection: let people connect/ }));
    await userEvent.click(within(form).getByRole("button", { name: "Save the Outlook and mail settings" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/entra/connection/settings",
      headers: { "if-match": "4" },
      body: { outlookConnect: true },
    });
    // Only the switches: the account rules are left as they are.
    expect(Object.keys(sent(fetchMock, "PUT").body as object).sort()).toEqual(["mailChannels", "outlookConnect"]);
    expect(await within(form).findByText(/^Nobody has connected yet: connect your own Outlook/)).toBeInTheDocument();
    expect(within(form).getByRole("switch", { name: /^Outlook connection: let people/ })).toBeChecked();
  });

  it("reads Ready once someone has connected", async () => {
    await openPage({ connection: connected({ outlookConnect: outlookConnect() }) });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    expect(within(form).getByRole("status")).toHaveTextContent("Ready");
  });

  it("waits for the connection while it is failing, saying why", async () => {
    await openPage({
      connection: connected({
        status: "failing",
        statusReason: "credential_invalid",
        outlookConnect: outlookConnect({ status: "unavailable", reason: "credential_invalid" }),
      }),
    });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    const status = within(form).getByRole("status");
    expect(within(status).getByText("Waiting for the connection")).toBeInTheDocument();
    expect(within(status).getByText(/The tenant refused the client secret/)).toBeInTheDocument();
  });

  it("says nothing of change notifications while the switch is off", async () => {
    await openPage({ connection: connected() });
    expect(screen.queryByText("Change notifications")).not.toBeInTheDocument();
  });

  const stateCases = [
    {
      name: "active",
      notifications: { status: "active", subscriptions: 3, lastNotificationAt: "2026-10-28T09:30:00Z" },
      en: [/On, watching 3 mailboxes\. Last notification: .*2026/],
      nb: [/På, overvåker 3 postbokser\. Siste varsel: .*2026/],
    },
    {
      name: "active before any notification",
      notifications: { status: "active", subscriptions: 1 },
      en: [/On, watching 1 mailbox\. Last notification: none yet\./],
      nb: [/På, overvåker 1 postboks\. Siste varsel: ingen ennå\./],
    },
    {
      name: "off",
      notifications: { status: "off", subscriptions: 0 },
      en: [/Turned off by the operator \(CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED\)/],
      nb: [/Slått av av driftsansvarlig \(CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED\)/],
    },
    {
      name: "not configured",
      notifications: { status: "not_configured", subscriptions: 0 },
      en: [/Needs PUBLIC_BASE_URL, an address Microsoft can reach\..*every 15 minutes/],
      nb: [/Krever PUBLIC_BASE_URL, en adresse Microsoft kan nå\..*hvert 15\. minutt/],
    },
    {
      name: "unreachable",
      notifications: { status: "unreachable", subscriptions: 2, unreachableAt: "2026-10-28T08:00:00Z" },
      en: [/Microsoft could not reach the address since .*2026.*tries again within the hour/],
      nb: [/Microsoft har ikke nådd adressen siden .*2026.*prøver igjen innen en time/],
    },
  ] as const;

  for (const lang of ["en", "nb"] as const) {
    for (const c of stateCases) {
      it(`shows the change notifications as ${c.name} in ${lang}`, async () => {
        if (lang === "nb") setLanguagePreference("nb");
        try {
          stubEntraApi({
            connection: connected({ outlookConnect: outlookConnect({ notifications: c.notifications }) }),
          });
          renderWithProviders(<EntraOutlookTab />);
          const title = lang === "en" ? "Change notifications" : "Endringsvarsler";
          const status = await screen.findByRole("group", { name: title });
          expect(within(status).getByText(title)).toBeInTheDocument();
          expect(within(status).getByText(c[lang][0])).toBeInTheDocument();
        } finally {
          setLanguagePreference("auto");
        }
      });
    }
  }

  it("speaks Norwegian", async () => {
    setLanguagePreference("nb");
    try {
      stubEntraApi({ connection: connected({ outlookConnect: outlookConnect() }) });
      renderWithProviders(<EntraOutlookTab />);
      expect(
        await screen.findByRole("switch", { name: /^Outlook-tilkobling: la folk koble Outlook til Vantigo/ }),
      ).toBeChecked();
      expect(screen.getByText("Outlook-tilkobling")).toBeInTheDocument();
      expect(screen.getByText("Klar")).toBeInTheDocument();
      expect(screen.getByText("Omdirigerings-URI")).toBeInTheDocument();
      expect(screen.getByText("Personer tilkoblet: 0. Venter på ny tilkobling: 0.")).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });

  it("says in Norwegian that the last connect lacked a delegated permission", async () => {
    setLanguagePreference("nb");
    try {
      stubEntraApi({ connection: connected({ outlookConnect: outlookConnect({ status: "consent_missing" }) }) });
      renderWithProviders(<EntraOutlookTab />);
      expect(
        await screen.findByText(/^Den siste tilkoblingen manglet en delegert tillatelse: gi administratorsamtykke/),
      ).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });

  it("shows the redirect URI whether or not the switch is on, with a button that copies it", async () => {
    // setup() gives the test a clipboard to read back.
    const user = userEvent.setup();
    await openPage({ connection: connected() });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    const uri = "https://vantigo.example/api/v1/entra/outlook/callback";
    expect(within(form).getByText("Redirect URI")).toBeInTheDocument();
    expect(within(form).getByText(uri)).toBeInTheDocument();
    await user.click(within(form).getByRole("button", { name: `Copy ${uri}` }));
    expect(await navigator.clipboard.readText()).toBe(uri);
  });

  const statuses = [
    ["ready", "Ready"],
    ["untested", /^Nobody has connected yet: connect your own Outlook under Settings › Connections/],
    ["consent_missing", /^The last connect lacked a delegated permission: grant admin consent/],
    ["unavailable", "Waiting for the connection"],
  ] as const;
  for (const [status, text] of statuses)
    it(`reads the status ${status} with the counts`, async () => {
      await openPage({
        connection: connected({ outlookConnect: outlookConnect({ status, connected: 4, reconnect: 1 }) }),
      });
      const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
      const line = within(form).getByRole("status");
      expect(within(line).getByText(text)).toBeInTheDocument();
      expect(within(line).getByText("People connected: 4. Waiting to reconnect: 1.")).toBeInTheDocument();
    });

  it("asks before turning the switch off while people are connected, and saves only on yes", async () => {
    const { fetchMock } = await openPage({
      connection: connected({ outlookConnect: outlookConnect({ connected: 2, reconnect: 1 }) }),
    });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    await userEvent.click(within(form).getByRole("switch", { name: /^Outlook connection: let people connect/ }));
    await userEvent.click(within(form).getByRole("button", { name: "Save the Outlook and mail settings" }));

    let dialog = await screen.findByRole("dialog", { name: "Turn the Outlook connection off?" });
    expect(within(dialog).getByText(/^Everyone will have to connect Outlook again\./)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sentAll(fetchMock, "PUT")).toHaveLength(0);

    await userEvent.click(within(form).getByRole("button", { name: "Save the Outlook and mail settings" }));
    dialog = await screen.findByRole("dialog", { name: "Turn the Outlook connection off?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Turn off and save" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ outlookConnect: false });
  });

  it("turns the switch off without asking when nobody is connected", async () => {
    const { fetchMock } = await openPage({ connection: connected({ outlookConnect: outlookConnect() }) });
    const form = within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });
    await userEvent.click(within(form).getByRole("switch", { name: /^Outlook connection: let people connect/ }));
    await userEvent.click(within(form).getByRole("button", { name: "Save the Outlook and mail settings" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("EntraOutlookTab — the Mail channels", () => {
  const SERVICE_PRINCIPAL_ID = "55555555-5555-4555-8555-555555555555";

  /** The six commands as the page shows them, with the client id and the object id it was given. */
  const commandsWith = (clientId: string, objectId: string) =>
    [
      `New-ServicePrincipal -AppId ${clientId} -ObjectId ${objectId} -DisplayName "Vantigo"`,
      `Set-Mailbox post@firma.no -CustomAttribute10 "Vantigo"`,
      `New-ManagementScope -Name "Vantigo mail channels" -RecipientRestrictionFilter "CustomAttribute10 -eq 'Vantigo'"`,
      `New-ManagementRoleAssignment -App ${clientId} -Role "Application Mail Full Access" -CustomResourceScope "Vantigo mail channels"`,
      `New-ManagementRoleAssignment -App ${clientId} -Role "Application MailboxSettings.Read" -CustomResourceScope "Vantigo mail channels"`,
      `Test-ServicePrincipalAuthorization -Identity ${clientId} -Resource post@firma.no`,
    ].join("\n");

  const form = () => within(region("Outlook and mail")).getByRole("form", { name: "Outlook and mail settings" });

  it("is off by default, with no status and no commands", async () => {
    await openPage({ connection: connected() });
    const toggle = within(form()).getByRole("switch", {
      name: /^Mail channels: let Communications send and receive through Exchange Online shared mailboxes/,
    });
    expect(toggle).not.toBeChecked();
    expect(within(form()).getByText("Mail channels")).toBeInTheDocument();
    expect(within(form()).queryByRole("status", { name: "Mail channels" })).not.toBeInTheDocument();
    expect(within(form()).queryByText(/New-ServicePrincipal/)).not.toBeInTheDocument();
  });

  it("is sent as mailChannels alone, then reads Ready with the commands", async () => {
    const { fetchMock } = await openPage({
      connection: connected({
        mailChannels: { enabled: false, status: "off", servicePrincipalId: SERVICE_PRINCIPAL_ID },
      }),
    });
    await userEvent.click(within(form()).getByRole("switch", { name: /^Mail channels: let Communications/ }));
    // The commands follow the switch at once, before it is saved.
    expect(within(form()).getByText(/^New-ServicePrincipal/)).toBeInTheDocument();
    await userEvent.click(within(form()).getByRole("button", { name: "Save the Outlook and mail settings" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/entra/connection/settings",
      headers: { "if-match": "4" },
      body: { mailChannels: true, outlookConnect: false },
    });
    const status = await within(form()).findByRole("status", { name: "Mail channels" });
    expect(
      within(status).getByText(/^Ready: at the last test the application held no Microsoft Graph mail permission/),
    ).toBeInTheDocument();
    expect(within(form()).getByRole("switch", { name: /^Mail channels: let/ })).toBeChecked();
  });

  it("warns once saved when the token carries a tenant-wide Mail role", async () => {
    await openPage({ connection: connected({ permissionsPresent: [...requiredPermissions, "Mail.Send"] }) });
    await userEvent.click(within(form()).getByRole("switch", { name: /^Mail channels: let Communications/ }));
    await userEvent.click(within(form()).getByRole("button", { name: "Save the Outlook and mail settings" }));
    const status = await within(form()).findByRole("status", { name: "Mail channels" });
    expect(within(status).getByText(/^Mail access is granted to every mailbox in the tenant\./)).toBeInTheDocument();
  });

  it("turns off without asking", async () => {
    const { fetchMock } = await openPage({
      connection: connected({ mailChannels: { enabled: true, status: "ready" } }),
    });
    await userEvent.click(within(form()).getByRole("switch", { name: /^Mail channels: let/ }));
    expect(within(form()).queryByText(/New-ServicePrincipal/)).not.toBeInTheDocument();
    await userEvent.click(within(form()).getByRole("button", { name: "Save the Outlook and mail settings" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(sent(fetchMock, "PUT").body).toMatchObject({ mailChannels: false });
    await waitFor(() =>
      expect(within(form()).queryByRole("status", { name: "Mail channels" })).not.toBeInTheDocument(),
    );
  });

  const statuses = [
    {
      status: "ready",
      colour: "teal",
      text: "Ready: at the last test the application held no Microsoft Graph mail permission for the whole tenant. Access granted in Exchange is not visible here — check it with Test-ServicePrincipalAuthorization.",
    },
    {
      status: "tenant_wide",
      colour: "orange",
      text: "Mail access is granted to every mailbox in the tenant. Limit it with RBAC for Applications and remove the tenant-wide consent. While this shows, anyone who manages channels in Communications can send as any shared mailbox in the tenant.",
    },
    { status: "unavailable", colour: "dimmed", text: "Waiting for the connection" },
  ] as const;
  for (const c of statuses)
    it(`reads the status ${c.status} in its colour`, async () => {
      await openPage({ connection: connected({ mailChannels: { enabled: true, status: c.status } }) });
      const status = within(form()).getByRole("status", { name: "Mail channels" });
      const line = within(status).getByText(c.text);
      expect(line.getAttribute("style")).toContain(`--mantine-color-${c.colour}`);
    });

  it("waits for the connection while it is failing, saying why", async () => {
    await openPage({
      connection: connected({
        status: "failing",
        statusReason: "credential_invalid",
        mailChannels: { enabled: true, status: "unavailable", reason: "credential_invalid" },
      }),
    });
    const status = within(form()).getByRole("status", { name: "Mail channels" });
    expect(within(status).getByText("Waiting for the connection")).toBeInTheDocument();
    expect(within(status).getByText(/The tenant refused the client secret/)).toBeInTheDocument();
  });

  it("fills the commands with the client id and the service principal's object id, and copies them", async () => {
    const user = userEvent.setup();
    await openPage({
      connection: connected({
        mailChannels: { enabled: true, status: "ready", servicePrincipalId: SERVICE_PRINCIPAL_ID },
      }),
    });
    const commands = within(form()).getByRole("group", { name: "Commands for Exchange Online PowerShell" });
    const expected = commandsWith(CLIENT_ID, SERVICE_PRINCIPAL_ID);
    expect(within(commands).getByText(/^New-ServicePrincipal/).textContent).toBe(expected);
    expect(within(commands).queryByText(/^Test the connection first/)).not.toBeInTheDocument();
    expect(within(commands).getByText(/^Run them as an Exchange Administrator\./)).toBeInTheDocument();
    await user.click(within(commands).getByRole("button", { name: "Copy Commands for Exchange Online PowerShell" }));
    expect(await navigator.clipboard.readText()).toBe(expected);
  });

  it("leaves a placeholder for the object id until a test has read it, and says to test first", async () => {
    await openPage({ connection: connected({ mailChannels: { enabled: true, status: "ready" } }) });
    const commands = within(form()).getByRole("group", { name: "Commands for Exchange Online PowerShell" });
    expect(within(commands).getByText(/^New-ServicePrincipal/).textContent).toBe(
      commandsWith(CLIENT_ID, "<object id>"),
    );
    expect(
      within(commands).getByText(
        "Test the connection first: the test reads the application's object id under Enterprise applications and fills it in here.",
      ),
    ).toBeInTheDocument();
  });

  it("speaks Norwegian: the switch, the status, the commands' placeholder and the hint", async () => {
    setLanguagePreference("nb");
    try {
      stubEntraApi({ connection: connected({ mailChannels: { enabled: true, status: "ready" } }) });
      renderWithProviders(<EntraOutlookTab />);
      expect(
        await screen.findByRole("switch", {
          name: /^E-postkanaler: la Kommunikasjon sende og motta gjennom delte postbokser i Exchange Online/,
        }),
      ).toBeChecked();
      expect(screen.getByText("E-postkanaler")).toBeInTheDocument();
      const status = screen.getByRole("status", { name: "E-postkanaler" });
      expect(
        within(status).getByText(
          /^Klar: ved siste test hadde applikasjonen ingen e-posttillatelse i Microsoft Graph for hele leietakeren\./,
        ),
      ).toBeInTheDocument();
      const commands = screen.getByRole("group", { name: "Kommandoer for Exchange Online PowerShell" });
      expect(within(commands).getByText(/^New-ServicePrincipal/).textContent).toBe(
        commandsWith(CLIENT_ID, "<objekt-ID>"),
      );
      expect(within(commands).getByText(/^Test tilkoblingen først:/)).toBeInTheDocument();
      expect(within(commands).getByText(/^Kjør dem som Exchange-administrator\./)).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });

  it("warns in Norwegian that every mailbox is reachable", async () => {
    setLanguagePreference("nb");
    try {
      stubEntraApi({ connection: connected({ mailChannels: { enabled: true, status: "tenant_wide" } }) });
      renderWithProviders(<EntraOutlookTab />);
      const status = await screen.findByRole("status", { name: "E-postkanaler" });
      expect(
        within(status).getByText(
          "Applikasjonen har tilgang til alle postboksene i leietakeren. Begrens den med RBAC for Applications, og fjern samtykket som gjelder hele leietakeren. Så lenge denne advarselen vises, kan alle som administrerer kanaler i Kommunikasjon, sende som hvilken som helst delt postboks i leietakeren.",
        ),
      ).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});

describe("EntraOutlookTab — what the tab holds", () => {
  it("holds the Outlook and mail card alone", async () => {
    await openPage({ connection: connected() });
    for (const name of ["Status", "Register the application", "Set the account rules", "Groups"])
      expect(screen.queryByRole("region", { name })).not.toBeInTheDocument();
    expect(
      within(region("Outlook and mail")).getByRole("switch", { name: /^Outlook connection: let people connect/ }),
    ).toBeInTheDocument();
    expect(
      within(region("Outlook and mail")).getByRole("switch", { name: /^Mail channels: let Communications/ }),
    ).toBeInTheDocument();
  });

  it("links to Communications' Channels once Mail channels is on, and not before", async () => {
    await openPage({ connection: connected({ mailChannels: { enabled: false, status: "off" } }) });
    expect(screen.queryByRole("link", { name: "Open Channels" })).not.toBeInTheDocument();
    cleanup();
    await openPage({ connection: connected({ mailChannels: { enabled: true, status: "ready" } }) });
    expect(screen.getByRole("link", { name: "Open Channels" })).toHaveAttribute("href", "/communications/channels");
  });
});
