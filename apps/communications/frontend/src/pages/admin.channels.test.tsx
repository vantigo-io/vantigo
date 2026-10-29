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
  provider: "entra" as const,
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
  key: "entra",
  displayName: "Microsoft 365",
  available: true,
  reason: null,
  warning: null,
  maxMessageBytes: 3_000_000,
};

const apiError = (status: number, code: string, message = "server words") =>
  Object.assign(new Error(message), { status, code });

vi.mock("../api/channels", () => ({
  channelsQueryOptions: () => ({ queryKey: ["channels"], queryFn: () => mocks.fetchChannels() }),
  channelProvidersQueryOptions: () => ({ queryKey: ["channel-providers"], queryFn: () => mocks.fetchProviders() }),
  createChannel: mocks.createChannel,
  updateChannel: vi.fn().mockResolvedValue({}),
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
  mocks.switchChannelProvider.mockReset().mockResolvedValue({ ...channel, provider: "entra" });
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
    expect(screen.queryByText("entra")).toBeNull();
  });

  it("names a Microsoft 365 channel so while its provider is not offered", async () => {
    mocks.fetchChannels.mockResolvedValue([entraChannel]);
    renderPage();
    const [, row] = await screen.findAllByRole("row");
    expect(within(row).getByText("Microsoft 365")).toBeTruthy();
  });

  it("offers SMTP alone, with no Mailgun, while Microsoft 365 is not available", async () => {
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
    expect(options.map((option) => option.textContent)).toEqual(["SMTP"]);
    expect(screen.queryByText(/Mailgun/)).toBeNull();
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
        provider: "entra",
      }),
    );
  });

  it.each([
    [
      "personal_mailbox",
      apiError(422, "personal_mailbox"),
      "This is a personal mailbox. Only a shared mailbox can be a channel.",
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
      "Microsoft 365 mail channels are not available. An Entra administrator turns on Mail channels in the Entra settings.",
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
      await screen.findByText("Dette er en personlig postboks. Bare en delt postboks kan være en kanal."),
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
    await waitFor(() => expect(mocks.switchChannelProvider).toHaveBeenCalledWith("channel-1"));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ title: "Channel switched" })),
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
      await within(dialog).findByText("This is a personal mailbox. Only a shared mailbox can be a channel."),
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
});
