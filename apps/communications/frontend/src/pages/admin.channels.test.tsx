import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelsPage } from "./admin.channels";

const mocks = vi.hoisted(() => ({
  fetchChannels: vi.fn(),
  fetchProviders: vi.fn(),
  createChannel: vi.fn(),
  updateChannel: vi.fn(),
  switchChannelProvider: vi.fn(),
  verifyChannel: vi.fn(),
}));

const channel = {
  id: "channel-1",
  type: "email" as const,
  address: "support@example.com",
  displayName: "Support",
  createdAt: "2026-01-01T00:00:00Z",
  isActive: true,
  provider: "smtp" as const,
  isDefault: true,
  hasCredentials: true,
  mailbox: null,
  settings: { host: "smtp.example.com", port: 587, useSsl: true, username: "support" },
};

const entraChannel = {
  ...channel,
  id: "channel-2",
  address: "post@example.com",
  displayName: "Post",
  provider: "m365" as const,
  isDefault: false,
  hasCredentials: false,
  mailbox: { id: "4f0c1e2a-0000-0000-0000-000000000001" },
  settings: null,
};

const smtpProvider = {
  key: "smtp",
  displayName: "SMTP",
  available: true,
  reason: null,
  warning: null,
  maxMessageBytes: null,
};
const entraProvider = {
  key: "m365",
  displayName: "Microsoft 365",
  available: true,
  reason: null,
  warning: null,
  maxMessageBytes: 3_000_000,
};

const googleProvider = {
  key: "google",
  displayName: "Google Workspace",
  available: true,
  reason: null,
  warning: "tenant_wide",
  maxMessageBytes: 25_000_000,
};

const apiError = (status: number, code: string, message = "server words") =>
  Object.assign(new Error(message), { status, code });

vi.mock("../api/channels", () => ({
  channelsQueryOptions: () => ({ queryKey: ["channels"], queryFn: () => mocks.fetchChannels() }),
  channelProvidersQueryOptions: () => ({ queryKey: ["channel-providers"], queryFn: () => mocks.fetchProviders() }),
  createChannel: mocks.createChannel,
  updateChannel: mocks.updateChannel,
  switchChannelProvider: mocks.switchChannelProvider,
  verifyChannel: mocks.verifyChannel,
}));

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

const renderPage = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
      >
        <ChannelsPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

const chooseProvider = async (label: string) => {
  fireEvent.click(screen.getByRole("combobox", { name: /^(Provider|Leverandør)/ }));
  fireEvent.click(await screen.findByRole("option", { name: label }));
};

const openMicrosoft365Form = async () => {
  fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
  await screen.findByRole("textbox", { name: "SMTP host" });
  await chooseProvider("Microsoft 365");
  fireEvent.change(screen.getByRole("textbox", { name: "Email address" }), { target: { value: " post@example.com " } });
  fireEvent.change(screen.getByRole("textbox", { name: "Display name" }), { target: { value: " Post " } });
};

afterEach(() => {
  cleanup();
  setLanguagePreference("auto");
});

beforeEach(() => {
  mocks.fetchChannels.mockReset();
  mocks.fetchProviders.mockReset().mockResolvedValue([smtpProvider]);
  mocks.createChannel.mockReset().mockResolvedValue({ id: "channel-3" });
  mocks.updateChannel.mockReset().mockResolvedValue({});
  mocks.switchChannelProvider.mockReset().mockResolvedValue({ ...channel, provider: "m365" });
  mocks.verifyChannel.mockReset().mockResolvedValue({ ok: true });
  vi.mocked(notifications.show).mockClear();
});

describe("admin channels", () => {
  it("shows the loading state", () => {
    mocks.fetchChannels.mockReturnValue(new Promise(() => undefined));
    renderPage();
    expect(screen.getByTestId("content-skeleton")).toBeTruthy();
  });

  it("renders an empty channel table", async () => {
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    expect(await screen.findByRole("columnheader", { name: "Address" })).toBeTruthy();
    expect(screen.getAllByRole("row")).toHaveLength(1);
  });

  it("shows the loading error", async () => {
    mocks.fetchChannels.mockRejectedValue(new Error("channel service unavailable"));
    renderPage();
    expect(await screen.findByText(/Could not load channels: channel service unavailable/)).toBeTruthy();
  });

  it("renders channel rows with the provider's display name, never its key", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([channel, entraChannel]);
    renderPage();
    expect(await screen.findByText("Support")).toBeTruthy();
    expect(screen.getByText("support@example.com")).toBeTruthy();
    expect(screen.getAllByText("Active")).toHaveLength(2);
    const [, smtpRow, entraRow] = screen.getAllByRole("row");
    expect(within(smtpRow).getByText("SMTP")).toBeTruthy();
    expect(within(entraRow).getByText("Microsoft 365")).toBeTruthy();
    expect(screen.queryByText("smtp")).toBeNull();
    expect(screen.queryByText("m365")).toBeNull();
  });

  it("marks a channel that cannot send Unavailable, and says why", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider]);
    mocks.fetchChannels.mockResolvedValue([
      { ...channel, available: true },
      { ...channel, id: "channel-4", address: "faktura@example.com", displayName: "Faktura", available: false },
      { ...entraChannel, available: false },
    ]);
    renderPage();
    const [, readyRow, noCredentialsRow, providerOffRow] = await screen.findAllByRole("row");
    expect(within(readyRow).queryByText("Unavailable")).toBeNull();
    expect(within(noCredentialsRow).getByText("Unavailable")).toBeTruthy();
    expect(
      within(noCredentialsRow).getByText("This channel has no stored credentials, so it sends nothing."),
    ).toBeTruthy();
    expect(within(providerOffRow).getByText("Unavailable")).toBeTruthy();
    expect(
      within(providerOffRow).getByText("Mail channels through m365 are not available, so this channel sends nothing."),
    ).toBeTruthy();
  });

  it("says Utilgjengelig in Norwegian", async () => {
    setLanguagePreference("nb");
    mocks.fetchChannels.mockResolvedValue([{ ...channel, available: false }]);
    renderPage();
    expect(await screen.findByText("Utilgjengelig")).toBeTruthy();
    expect(screen.getByText("Kanalen har ingen lagret legitimasjon og sender derfor ingenting.")).toBeTruthy();
  });

  it("names a mailbox channel by its provider's key while its provider is not listed", async () => {
    mocks.fetchChannels.mockResolvedValue([entraChannel]);
    renderPage();
    const [, row] = await screen.findAllByRole("row");
    expect(within(row).getByText("m365")).toBeTruthy();
  });

  it("lists every mailbox provider the server lists, in its order, and switches to the one chosen", async () => {
    const googleProvider = {
      ...entraProvider,
      key: "google",
      displayName: "Google Workspace",
      maxMessageBytes: 25_000_000,
    };
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider, googleProvider]);
    mocks.fetchChannels.mockResolvedValue([channel]);
    renderPage();
    await screen.findByText("Support");
    expect(screen.getByRole("button", { name: "Switch to Microsoft 365" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Switch to Google Workspace" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(mocks.switchChannelProvider).toHaveBeenCalledWith("channel-1", "google"));
    fireEvent.click(screen.getByRole("button", { name: "Add channel" }));
    await screen.findByRole("textbox", { name: "SMTP host" });
    fireEvent.click(screen.getByRole("combobox", { name: /^Provider/ }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "SMTP",
      "Microsoft 365",
      "Google Workspace",
      "Vantigo hosted mailNot yet available",
    ]);
  });

  it("lists a mailbox provider that is not available disabled, with its reason, and no Mailgun", async () => {
    mocks.fetchProviders.mockResolvedValue([
      smtpProvider,
      { ...entraProvider, available: false, reason: "switch_off" },
    ]);
    mocks.fetchChannels.mockResolvedValue([channel]);
    renderPage();
    await screen.findByText("Support");
    expect(screen.queryByRole("button", { name: "Switch to Microsoft 365" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Add channel" }));
    expect(await screen.findByRole("textbox", { name: "SMTP host" })).toBeTruthy();
    fireEvent.click(screen.getByRole("combobox", { name: /^Provider/ }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "SMTP",
      "Microsoft 365Not available now (switch_off)",
      "Vantigo hosted mailNot yet available",
    ]);
    expect(options[1].getAttribute("aria-disabled")).toBe("true");
    expect(screen.queryByText(/Mailgun/)).toBeNull();
  });

  it("lists Vantigo hosted mail greyed below the offered providers, and choosing it changes nothing", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
    await screen.findByRole("textbox", { name: "SMTP host" });
    fireEvent.click(screen.getByRole("combobox", { name: /^Provider/ }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "SMTP",
      "Microsoft 365",
      "Vantigo hosted mailNot yet available",
    ]);
    const hosted = options[2];
    expect(hosted.getAttribute("aria-disabled")).toBe("true");
    expect(within(hosted).getByText("Not yet available")).toBeTruthy();
    fireEvent.click(hosted);
    expect(screen.getByRole("textbox", { name: "SMTP host" })).toBeTruthy();
    expect((screen.getByRole("combobox", { name: /^Provider/ }) as HTMLInputElement).value).toBe("SMTP");
    expect(screen.queryByRole("textbox", { name: "Mailbox folder" })).toBeNull();
    expect(mocks.createChannel).not.toHaveBeenCalled();
  });

  it("names Vantigo hosted mail in Norwegian", async () => {
    setLanguagePreference("nb");
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Legg til kanal" }));
    await screen.findByRole("textbox", { name: "SMTP-vert" });
    fireEvent.click(screen.getByRole("combobox", { name: /^Leverandør/ }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toContain("Vantigo-driftet e-postIkke tilgjengelig ennå");
  });

  it("creates an SMTP channel with the expected payload", async () => {
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Email address" }), {
      target: { value: " mail@example.com " },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "SMTP host" }), { target: { value: "smtp.example.com" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Username" }), { target: { value: "mail" } });
    const password = document.querySelector<HTMLInputElement>('input[type="password"]');
    if (!password) throw new Error("the password input is required");
    fireEvent.change(password, { target: { value: "secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
    await waitFor(() =>
      expect(mocks.createChannel).toHaveBeenCalledWith({
        type: "email",
        address: "mail@example.com",
        provider: "smtp",
        smtp: { host: "smtp.example.com", port: 587, useSsl: true, username: "mail", password: "secret" },
      }),
    );
  });

  it("adds a Microsoft 365 shared mailbox by its address and display name alone", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    await openMicrosoft365Form();
    expect(screen.queryByRole("textbox", { name: "SMTP host" })).toBeNull();
    expect(document.querySelector('input[type="password"]')).toBeNull();
    expect(screen.getByText("The mailbox must be a shared mailbox in the application's scope.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
    await waitFor(() =>
      expect(mocks.createChannel).toHaveBeenCalledWith({
        type: "email",
        address: "post@example.com",
        displayName: "Post",
        provider: "m365",
      }),
    );
  });

  it("creates a Microsoft 365 channel with an import window and move-to-folder on close", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    await openMicrosoft365Form();
    fireEvent.click(screen.getByRole("combobox", { name: "Import history" }));
    fireEvent.click(await screen.findByRole("option", { name: "Last 30 days" }));
    fireEvent.click(screen.getByRole("combobox", { name: "When a conversation is closed" }));
    fireEvent.click(await screen.findByRole("option", { name: "Move to a folder in the mailbox" }));
    const folderInput = await screen.findByRole("textbox", { name: "Mailbox folder" });
    expect((folderInput as HTMLInputElement).value).toBe("Handled in Vantigo");
    fireEvent.change(folderInput, { target: { value: " Archive / Done " } });
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
    await waitFor(() =>
      expect(mocks.createChannel).toHaveBeenCalledWith({
        type: "email",
        address: "post@example.com",
        displayName: "Post",
        provider: "m365",
        importWindow: "last_30_days",
        onClose: "move",
        folder: "Archive / Done",
      }),
    );
  });

  it("edits the on-close settings of an existing mailbox channel", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([
      {
        ...entraChannel,
        mailbox: { id: "4f0c1e2a-0000-0000-0000-000000000001", onClose: "read" as const, folder: null },
      },
    ]);
    renderPage();
    expect(await screen.findByText("On close: mark as read")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Mailbox on close" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("combobox", { name: "When a conversation is closed" }));
    fireEvent.click(await screen.findByRole("option", { name: "Move to a folder in the mailbox" }));
    expect(await within(dialog).findByRole("textbox", { name: "Mailbox folder" })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mocks.updateChannel).toHaveBeenCalledWith("channel-2", {
        onClose: "move",
        folder: "Handled in Vantigo",
      }),
    );
  });

  it.each([
    [
      "personal_mailbox",
      apiError(422, "personal_mailbox"),
      "This is a person's own mailbox. Only a shared mailbox can be a channel.",
    ],
    [
      "group_address",
      apiError(422, "group_address"),
      "The address is a group. A group has no mailbox Vantigo can read; use a licensed mailbox.",
    ],
    [
      "mailbox_denied",
      apiError(422, "mailbox_denied"),
      "The mailbox is outside the application's scope. Give the application access to it in Exchange Online.",
    ],
    ["mailbox_unknown", apiError(422, "mailbox_unknown"), "Microsoft 365 has no mailbox with this address."],
    [
      "mail_channels_off",
      apiError(409, "mail_channels_off"),
      "Mail channels through Microsoft 365 are not available. An administrator turns them on under Integrations.",
    ],
    ["mailbox_exists", apiError(409, "mailbox_exists"), "Another channel already uses this mailbox."],
    ["channel_exists", apiError(409, "channel_exists"), "A channel with this address already exists."],
    [
      "provider_unreachable",
      apiError(503, "provider_unreachable"),
      "Microsoft 365 did not answer. Try again in a moment.",
    ],
    ["rate_limited", apiError(429, "rate_limited"), "Too many attempts. Wait a minute and try again."],
    ["something_new", apiError(500, "something_new"), "The channel could not be saved. Try again."],
  ])("shows %s's message, never the code", async (_code, error, words) => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    mocks.createChannel.mockRejectedValue(error);
    renderPage();
    await openMicrosoft365Form();
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
    expect(await screen.findByText(words)).toBeTruthy();
    expect(screen.queryByText(new RegExp(error.code))).toBeNull();
    expect(screen.queryByText(/server words/)).toBeNull();
  });

  it("links a mailbox outside the scope to the guide", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    mocks.createChannel.mockRejectedValue(apiError(422, "mailbox_denied"));
    renderPage();
    await openMicrosoft365Form();
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
    const link = await screen.findByRole("link", { name: "How to give the application access" });
    expect(link.getAttribute("href")).toMatch(/\/docs\/en\/admin\/entra\/#mail-channels$/);
  });

  describe("a Google Workspace channel", () => {
    const openGoogleForm = async () => {
      fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
      await screen.findByRole("textbox", { name: "SMTP host" });
      await chooseProvider("Google Workspace");
      fireEvent.change(screen.getByRole("textbox", { name: "Email address" }), {
        target: { value: "post@example.com" },
      });
    };

    it("warns that the delegation reaches every mailbox in the domain, beside Microsoft 365's own warning", async () => {
      mocks.fetchProviders.mockResolvedValue([
        smtpProvider,
        { ...entraProvider, warning: "tenant_wide" },
        googleProvider,
      ]);
      mocks.fetchChannels.mockResolvedValue([]);
      renderPage();
      expect(
        await screen.findByText(
          "Mail access through Google Workspace reaches every mailbox in the domain: domain-wide delegation cannot be limited to some mailboxes. While this shows, anyone who manages channels in Communications can send as any mailbox in the domain that is no person's own.",
        ),
      ).toBeTruthy();
      expect(screen.getByText(/Limit it with RBAC for Applications/)).toBeTruthy();
      expect(screen.getAllByText(/RBAC for Applications/)).toHaveLength(1);
    });

    it("warns about the domain-wide delegation in Norwegian", async () => {
      setLanguagePreference("nb");
      mocks.fetchProviders.mockResolvedValue([smtpProvider, googleProvider]);
      mocks.fetchChannels.mockResolvedValue([]);
      renderPage();
      expect(
        await screen.findByText(/^E-posttilgangen gjennom Google Workspace gjelder alle postboksene i domenet/),
      ).toBeTruthy();
      expect(screen.queryByText(/RBAC for Applications/)).toBeNull();
    });

    it("says which mailbox can be a channel, and refuses a person's own in Google's words", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, googleProvider]);
      mocks.fetchChannels.mockResolvedValue([]);
      mocks.createChannel.mockRejectedValue(apiError(422, "personal_mailbox"));
      renderPage();
      await openGoogleForm();
      expect(
        screen.getByText(/^The mailbox must be a licensed user of the organisation in Google Workspace/),
      ).toBeTruthy();
      expect(screen.queryByText(/shared mailbox in the application's scope/)).toBeNull();
      fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
      expect(
        await screen.findByText(
          "This is a person's own mailbox in Google Workspace. Only a mailbox that is no person's own, such as post@, can be a channel.",
        ),
      ).toBeTruthy();
    });

    it("links a refused mailbox to the Google Workspace guide's mail channels", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, googleProvider]);
      mocks.fetchChannels.mockResolvedValue([]);
      mocks.createChannel.mockRejectedValue(apiError(422, "mailbox_denied"));
      renderPage();
      await openGoogleForm();
      fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
      const link = await screen.findByRole("link", { name: "Mail channels in Google Workspace" });
      expect(link.getAttribute("href")).toMatch(/\/docs\/en\/admin\/google-workspace\/#mail-channels$/);
      expect(screen.getByText(/Check the user in the Google Admin console/)).toBeTruthy();
      expect(screen.queryByText(/Exchange Online/)).toBeNull();
    });

    it("says a refused mailbox's sync in Google's words", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, googleProvider]);
      mocks.fetchChannels.mockResolvedValue([
        {
          ...entraChannel,
          provider: "google",
          address: "denied@example.com",
          displayName: null,
          sync: { state: "failed", error: "mailbox_denied", lastSyncedAt: null },
        },
      ]);
      renderPage();
      expect(
        await screen.findByText(
          "Failed: Google Workspace refuses the mailbox. Check the user in the Google Admin console.",
        ),
      ).toBeTruthy();
    });
  });

  it("says an SMTP channel's address already exists", async () => {
    mocks.fetchChannels.mockResolvedValue([]);
    mocks.createChannel.mockRejectedValue(apiError(409, "channel_exists"));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Email address" }), {
      target: { value: "mail@example.com" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "SMTP host" }), { target: { value: "smtp.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
    expect(await screen.findByText("A channel with this address already exists.")).toBeTruthy();
  });

  it("shows the Microsoft 365 form and its refusal in Norwegian", async () => {
    setLanguagePreference("nb");
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    mocks.createChannel.mockRejectedValue(apiError(422, "personal_mailbox"));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Legg til kanal" }));
    await screen.findByRole("textbox", { name: "SMTP-vert" });
    await chooseProvider("Microsoft 365");
    expect(screen.getByText("Postboksen må være en delt postboks som applikasjonen har tilgang til.")).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "E-postadresse" }), { target: { value: "post@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Opprett kanal" }));
    expect(
      await screen.findByText("Dette er en persons egen postboks. Bare en delt postboks kan være en kanal."),
    ).toBeTruthy();
  });

  it("switches an SMTP channel to Microsoft 365 after a confirm that says the SMTP settings are deleted", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([channel, entraChannel]);
    renderPage();
    await screen.findByText("Support");
    const switches = screen.getAllByRole("button", { name: "Switch to Microsoft 365" });
    expect(switches).toHaveLength(1);
    fireEvent.click(switches[0]);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("The SMTP settings are deleted. To go back you enter them again.")).toBeTruthy();
    expect(mocks.switchChannelProvider).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(mocks.switchChannelProvider).toHaveBeenCalledWith("channel-1", "m365"));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ title: "Channel switched" })),
    );
  });

  it("passes import window and on-close options when switching to Microsoft 365", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([channel]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Switch to Microsoft 365" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("combobox", { name: "Import history" }));
    fireEvent.click(await screen.findByRole("option", { name: "Last 90 days" }));
    fireEvent.click(within(dialog).getByRole("combobox", { name: "When a conversation is closed" }));
    fireEvent.click(await screen.findByRole("option", { name: "Mark as read in the mailbox" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Switch" }));
    await waitFor(() =>
      expect(mocks.switchChannelProvider).toHaveBeenCalledWith("channel-1", "m365", {
        importWindow: "last_90_days",
        onClose: "read",
      }),
    );
  });

  it("shows why a switch was refused", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([channel]);
    mocks.switchChannelProvider.mockRejectedValue(apiError(422, "personal_mailbox"));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Switch to Microsoft 365" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Switch" }));
    expect(
      await within(dialog).findByText("This is a person's own mailbox. Only a shared mailbox can be a channel."),
    ).toBeTruthy();
  });

  it("asks before the switch in Norwegian", async () => {
    setLanguagePreference("nb");
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([channel]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Bytt til Microsoft 365" }));
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText("SMTP-innstillingene slettes. For å gå tilbake må du legge dem inn på nytt."),
    ).toBeTruthy();
  });

  it("warns while the mail grant reaches every mailbox in the tenant", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, { ...entraProvider, warning: "tenant_wide" }]);
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    expect(
      await screen.findByText(
        "Mail access is granted to every mailbox in the tenant. Limit it with RBAC for Applications and remove the tenant-wide consent. While this shows, anyone who manages channels in Communications can send as any shared mailbox in the tenant.",
      ),
    ).toBeTruthy();
  });

  it("warns about the tenant-wide grant in Norwegian", async () => {
    setLanguagePreference("nb");
    mocks.fetchProviders.mockResolvedValue([smtpProvider, { ...entraProvider, warning: "tenant_wide" }]);
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    expect(
      await screen.findByText(
        "Applikasjonen har tilgang til alle postboksene i leietakeren. Begrens den med RBAC for Applications, og fjern samtykket som gjelder hele leietakeren. Så lenge denne advarselen vises, kan alle som administrerer kanaler i Kommunikasjon, sende som hvilken som helst delt postboks i leietakeren.",
      ),
    ).toBeTruthy();
  });

  it("shows no warning while the grant is limited", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    await screen.findByRole("columnheader", { name: "Address" });
    expect(screen.queryByText(/every mailbox in the tenant/)).toBeNull();
  });

  it("verifies one row at a time and says why a verify failed", async () => {
    mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
    mocks.fetchChannels.mockResolvedValue([channel, entraChannel]);
    let reject: (error: unknown) => void = () => undefined;
    mocks.verifyChannel.mockReturnValue(
      new Promise((_, fail) => {
        reject = fail;
      }),
    );
    renderPage();
    await screen.findByText("Support");
    const [, smtpRow, entraRow] = screen.getAllByRole("row");
    fireEvent.click(within(entraRow).getByRole("button", { name: "Verify" }));
    await waitFor(() =>
      expect(within(entraRow).getByRole("button", { name: "Verify" })).toHaveAttribute("data-loading"),
    );
    expect(within(smtpRow).getByRole("button", { name: "Verify" })).not.toHaveAttribute("data-loading");
    reject(apiError(422, "mailbox_changed"));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({
          color: "red",
          title: "Channel not verified",
          message: "The address now belongs to another mailbox in Microsoft 365.",
        }),
      ),
    );
  });

  describe("the receive status", () => {
    const withSync = (id: string, address: string, sync: unknown) => ({
      ...entraChannel,
      id,
      address,
      displayName: null,
      sync,
    });

    it("says whether each mailbox channel receives, and that SMTP sends only", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
      mocks.fetchChannels.mockResolvedValue([
        { ...channel, sync: null },
        withSync("c-2", "post@example.com", { state: "active", error: null, lastSyncedAt: "2026-10-29T08:00:00Z" }),
        withSync("c-3", "new@example.com", { state: "pending", error: null, lastSyncedAt: null }),
        withSync("c-4", "denied@example.com", { state: "failed", error: "mailbox_denied", lastSyncedAt: null }),
        withSync("c-5", "flaky@example.com", { state: "active", error: "sync_failed", lastSyncedAt: null }),
      ]);
      renderPage();
      expect(await screen.findByRole("columnheader", { name: "Receive status" })).toBeTruthy();
      const row = (address: string) => {
        const cell = screen.getByText(address).closest("tr");
        if (!cell) throw new Error(`no row for ${address}`);
        return within(cell);
      };
      expect(row("Support").getByText("Sends only")).toBeTruthy();
      expect(row("post@example.com").getByText("Receiving")).toBeTruthy();
      expect(row("post@example.com").getByText(/^Last synced /)).toBeTruthy();
      expect(row("new@example.com").getByText("Waiting for first sync")).toBeTruthy();
      expect(
        row("denied@example.com").getByText(
          "Failed: The application can no longer read the mailbox. Give it access in Exchange Online.",
        ),
      ).toBeTruthy();
      expect(
        row("flaky@example.com").getByText("Last sync failed: Microsoft 365 did not answer. Vantigo tries again."),
      ).toBeTruthy();
    });

    it("shows the receive status in Norwegian", async () => {
      setLanguagePreference("nb");
      mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider]);
      mocks.fetchChannels.mockResolvedValue([
        { ...channel, sync: null },
        withSync("c-2", "post@example.com", { state: "active", error: null, lastSyncedAt: "2026-10-29T08:00:00Z" }),
        withSync("c-3", "new@example.com", { state: "pending", error: null, lastSyncedAt: null }),
        withSync("c-4", "gone@example.com", { state: "failed", error: "mailbox_gone", lastSyncedAt: null }),
      ]);
      renderPage();
      expect(await screen.findByRole("columnheader", { name: "Mottaksstatus" })).toBeTruthy();
      expect(screen.getByText("Sender bare")).toBeTruthy();
      expect(screen.getByText("Mottar")).toBeTruthy();
      expect(screen.getByText(/^Sist synkronisert /)).toBeTruthy();
      expect(screen.getByText("Venter på første synkronisering")).toBeTruthy();
      expect(screen.getByText("Feilet: Postboksen finnes ikke lenger i Microsoft 365.")).toBeTruthy();
    });
  });

  describe("Messenger channels", () => {
    const metaProvider = {
      key: "meta",
      displayName: "Facebook Messenger",
      type: "messenger" as const,
      available: true,
      reason: null,
      warning: null,
      maxMessageBytes: null,
      endpoints: [
        { address: "1001", displayName: "Acme Page", available: true, reason: null },
        { address: "1002", displayName: "Old Page", available: false, reason: "token_rejected" },
      ],
    };
    const messengerChannel = {
      ...channel,
      id: "channel-9",
      type: "messenger" as const,
      address: "1001",
      displayName: "Acme Page",
      provider: "meta" as const,
      isDefault: true,
      hasCredentials: false,
      settings: null,
      sync: { state: "active", error: null, lastSyncedAt: "2026-11-05T08:00:00Z" },
    };

    it("offers the type choice only while a messenger provider is available", async () => {
      mocks.fetchChannels.mockResolvedValue([]);
      renderPage();
      fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
      await screen.findByRole("textbox", { name: "SMTP host" });
      expect(screen.queryByText("Facebook Messenger")).toBeNull();
    });

    it("creates a Messenger channel on a Page picked from the provider's endpoints", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, metaProvider]);
      mocks.fetchChannels.mockResolvedValue([]);
      renderPage();
      fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
      fireEvent.click(await screen.findByText("Facebook Messenger"));
      fireEvent.click(await screen.findByRole("combobox", { name: /Facebook Page/ }));
      // A Page the provider cannot use is listed, disabled, with its reason.
      expect(await screen.findByText("Not available now (token_rejected)")).toBeTruthy();
      fireEvent.click(await screen.findByRole("option", { name: /Acme Page/ }));
      fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
      await waitFor(() =>
        expect(mocks.createChannel).toHaveBeenCalledWith({
          type: "messenger",
          address: "1001",
          displayName: undefined,
          provider: "meta",
        }),
      );
    });

    it("lists a Messenger channel by its Page, without the mailbox or SMTP actions", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider, metaProvider]);
      mocks.fetchChannels.mockResolvedValue([messengerChannel]);
      renderPage();
      expect(await screen.findByText("Acme Page")).toBeTruthy();
      expect(screen.getByText("Facebook Messenger")).toBeTruthy();
      expect(screen.getByText("Receiving")).toBeTruthy();
      expect(screen.queryByRole("button", { name: /Switch to/ })).toBeNull();
      expect(screen.queryByRole("button", { name: /Mailbox on close|on close/i })).toBeNull();
      expect(screen.getByRole("button", { name: "Verify" })).toBeTruthy();
    });

    it("says an unavailable Messenger channel sends and receives nothing", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, metaProvider]);
      mocks.fetchChannels.mockResolvedValue([{ ...messengerChannel, available: false }]);
      renderPage();
      expect(
        await screen.findByText(
          "Messenger channels through Facebook Messenger are not available, so this channel sends and receives nothing.",
        ),
      ).toBeTruthy();
    });

    it("never offers a messenger provider to switch an SMTP channel to", async () => {
      mocks.fetchProviders.mockResolvedValue([smtpProvider, entraProvider, metaProvider]);
      mocks.fetchChannels.mockResolvedValue([channel]);
      renderPage();
      expect(await screen.findByRole("button", { name: "Switch to Microsoft 365" })).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Switch to Facebook Messenger" })).toBeNull();
    });
  });
});
