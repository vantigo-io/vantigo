import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ContactChannel } from "../api/contacts";
import { ContactMessengerMenu, MessengerMark } from "./-contact-messenger";

const mocks = vi.hoisted(() => ({
  createMessengerLink: vi.fn(),
  writeOnMessenger: vi.fn(),
  unlinkMessengerParticipant: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock("../api/messenger", () => ({
  createMessengerLink: mocks.createMessengerLink,
  writeOnMessenger: mocks.writeOnMessenger,
  unlinkMessengerParticipant: mocks.unlinkMessengerParticipant,
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));

const inAnHour = new Date(Date.now() + 3_600_000).toISOString();
const anHourAgo = new Date(Date.now() - 3_600_000).toISOString();

const linked = (overrides: Partial<ContactChannel> = {}): ContactChannel => ({
  channelId: "channel-1",
  type: "messenger",
  name: "Nordlys",
  linked: true,
  participantId: "participant-1",
  lastInboundAt: anHourAgo,
  replyWindowClosesAt: inAnHour,
  ...overrides,
});
const unlinked = (): ContactChannel => ({ channelId: "channel-2", type: "messenger", name: "Fjord", linked: false });

const renderMenu = (channels: ContactChannel[]) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={client}>
        <ContactMessengerMenu contactId={2001} contactName="Kari Nordmann" channels={channels} customerId={1001} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

const open = async () => userEvent.click(screen.getByRole("button", { name: "Messenger for Kari Nordmann" }));

describe("a contact's Messenger menu", () => {
  beforeEach(() => {
    mocks.createMessengerLink
      .mockReset()
      .mockResolvedValue({ url: "https://m.me/1?ref=abc", expiresAt: "2027-01-01T00:00:00Z" });
    mocks.writeOnMessenger
      .mockReset()
      .mockResolvedValue({ conversationId: "conversation-9", messageId: "m-1", status: "queued" });
    mocks.unlinkMessengerParticipant.mockReset().mockResolvedValue(undefined);
    mocks.navigate.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("writes the first message to a linked person while their window is open, then opens the conversation", async () => {
    renderMenu([linked()]);
    await open();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Write on Messenger" }));
    await userEvent.type(await screen.findByLabelText("Message"), "Hello Kari");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(mocks.writeOnMessenger).toHaveBeenCalledTimes(1));
    const [participant, text, key] = mocks.writeOnMessenger.mock.calls[0] ?? [];
    expect(participant).toBe("participant-1");
    expect(text).toBe("Hello Kari");
    expect(typeof key).toBe("string");
    await waitFor(() =>
      expect(mocks.navigate).toHaveBeenCalledWith({ href: "/communications/inbox?conversationId=conversation-9" }),
    );
  });

  it("disables writing once the window is closed, and says the person must write first", async () => {
    renderMenu([linked({ replyWindowClosesAt: null })]);
    await open();
    const item = await screen.findByRole("menuitem", {
      name: /Kari Nordmann must write to Nordlys first\. Send them your Messenger link/,
    });
    expect(item).toBeDisabled();
    expect(screen.queryByRole("menuitem", { name: "Write on Messenger" })).not.toBeInTheDocument();
  });

  it("offers the Messenger link of a contact who is not linked yet, and no writing or unlinking", async () => {
    renderMenu([unlinked()]);
    await open();
    expect(screen.queryByRole("menuitem", { name: "Write on Messenger" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Unlink from Messenger" })).not.toBeInTheDocument();
    // The package setup put a clipboard on the window.
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    await userEvent.click(await screen.findByRole("menuitem", { name: "Copy Messenger link" }));
    await waitFor(() => expect(mocks.createMessengerLink).toHaveBeenCalledWith("channel-2", 2001));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("https://m.me/1?ref=abc"));
    expect(await screen.findByText(/Send it only to Kari Nordmann/)).toBeInTheDocument();
  });

  it("names each channel when there are several, and unlinks the person of the channel it is on", async () => {
    renderMenu([linked(), unlinked()]);
    await open();
    expect(await screen.findByText("Nordlys")).toBeInTheDocument();
    expect(screen.getByText("Fjord")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: "Unlink from Messenger" }));
    await waitFor(() => expect(mocks.unlinkMessengerParticipant).toHaveBeenCalledWith("participant-1"));
  });

  it("marks a contact whose person is linked, and no other", () => {
    const { rerender } = render(
      <MantineProvider env="test">
        <MessengerMark channels={[linked()]} />
      </MantineProvider>,
    );
    expect(screen.getByRole("img", { name: "On Messenger: Nordlys" })).toBeInTheDocument();
    rerender(
      <MantineProvider env="test">
        <MessengerMark channels={[unlinked()]} />
      </MantineProvider>,
    );
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });
});
