import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelsPage } from "./admin.channels";

const mocks = vi.hoisted(() => ({
  fetchChannels: vi.fn(),
  createChannel: vi.fn().mockResolvedValue({ id: "channel-2" }),
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
  settings: { host: "smtp.example.com", port: 587, useSsl: true, username: "support", domain: null, region: null },
};

vi.mock("../api/channels", () => ({
  channelsQueryOptions: () => ({
    queryKey: ["channels"],
    queryFn: () => mocks.fetchChannels(),
  }),
  createChannel: mocks.createChannel,
  updateChannel: vi.fn().mockResolvedValue({}),
  verifyChannel: vi.fn().mockResolvedValue({ ok: true }),
}));

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ChannelsPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

afterEach(() => cleanup());

beforeEach(() => {
  mocks.fetchChannels.mockReset();
  mocks.createChannel.mockClear();
});

describe("admin channels", () => {
  it("shows the loading state", () => {
    mocks.fetchChannels.mockReturnValue(new Promise(() => undefined));
    renderPage();
    expect(document.querySelector(".mantine-Loader-root")).toBeTruthy();
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

  it("renders channel rows", async () => {
    mocks.fetchChannels.mockResolvedValue([channel]);
    renderPage();
    expect(await screen.findByText("Support")).toBeTruthy();
    expect(screen.getByText("support@example.com")).toBeTruthy();
    expect(screen.getByText("smtp")).toBeTruthy();
    expect(screen.getByText("Active")).toBeTruthy();
  });

  it("switches between SMTP and Mailgun provider fields", async () => {
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));
    expect(await screen.findByText("Add email channel")).toBeTruthy();
    expect(await screen.findByRole("textbox", { name: "SMTP host" })).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Mailgun domain" })).toBeNull();

    fireEvent.click(screen.getByRole("combobox", { name: "Provider" }));
    const mailgunOptions = await screen.findAllByText("Mailgun");
    const mailgunOption = mailgunOptions.at(-1);
    if (!mailgunOption) throw new Error("Mailgun option is required");
    fireEvent.click(mailgunOption);
    expect(screen.queryByRole("textbox", { name: "SMTP host" })).toBeNull();
    expect(screen.getByRole("textbox", { name: "Mailgun domain" })).toBeTruthy();
    expect(document.querySelector('input[type="password"]')).toBeTruthy();
    expect(document.querySelectorAll('input[type="password"]')).toHaveLength(2);
  });

  it("creates a Mailgun channel with the expected payload", async () => {
    mocks.fetchChannels.mockResolvedValue([]);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add channel" }));

    fireEvent.change(await screen.findByRole("textbox", { name: "Email address" }), {
      target: { value: "  mail@example.com " },
    });
    fireEvent.change(await screen.findByRole("textbox", { name: "Display name" }), { target: { value: " Mail " } });
    fireEvent.click(screen.getByRole("combobox", { name: "Provider" }));
    const mailgunOptions = await screen.findAllByText("Mailgun");
    const mailgunOption = mailgunOptions.at(-1);
    if (!mailgunOption) throw new Error("Mailgun option is required");
    fireEvent.click(mailgunOption);
    fireEvent.change(await screen.findByRole("textbox", { name: "Mailgun domain" }), {
      target: { value: "mg.example.com" },
    });
    const passwordInputs = document.querySelectorAll<HTMLInputElement>('input[type="password"]');
    const apiKeyInput = passwordInputs.item(0);
    const signingKeyInput = passwordInputs.item(1);
    fireEvent.change(apiKeyInput, { target: { value: "key-123" } });
    fireEvent.change(signingKeyInput, {
      target: { value: "sign-456" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create channel" }));

    await waitFor(() =>
      expect(mocks.createChannel).toHaveBeenCalledWith({
        type: "email",
        address: "mail@example.com",
        displayName: "Mail",
        provider: "mailgun",
        mailgun: { domain: "mg.example.com", region: "us", apiKey: "key-123", inboundSigningKey: "sign-456" },
      }),
    );
  });
});
