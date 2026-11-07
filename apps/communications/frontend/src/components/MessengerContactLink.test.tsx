import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MessengerLink } from "../api/contact-links";
import "../i18n";
import { MessengerContactLink } from "./MessengerContactLink";

const mocks = vi.hoisted(() => ({
  link: vi.fn(),
  unlink: vi.fn(),
  options: vi.fn(),
  name: vi.fn(),
}));

vi.mock("../api/contact-links", () => ({
  linkParticipantContact: mocks.link,
  unlinkParticipantContact: mocks.unlink,
  customerContactOptionsQueryOptions: (customerId: number | null) => ({
    queryKey: ["options", customerId],
    queryFn: () => mocks.options(customerId),
    enabled: customerId != null,
  }),
  contactNameQueryOptions: (id: number | null) => ({
    queryKey: ["name", id],
    queryFn: () => mocks.name(id),
    enabled: id != null,
  }),
}));

const link = (overrides: Partial<MessengerLink> = {}): MessengerLink => ({
  participantId: "participant-1",
  contactId: null,
  suggestedContactId: null,
  referralContactId: null,
  ...overrides,
});

const renderLink = (value: MessengerLink, customerId: number | null = 1001) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <MessengerContactLink conversationId="conversation-1" customerId={customerId} link={value} />
      </MantineProvider>
    </QueryClientProvider>,
  );
};

describe("the Messenger contact link in the thread header", () => {
  beforeEach(() => {
    void setLanguagePreference("en");
    mocks.link.mockReset().mockResolvedValue({});
    mocks.unlink.mockReset().mockResolvedValue(undefined);
    mocks.options.mockReset().mockResolvedValue([
      { id: 2001, name: "Kari Nordmann" },
      { id: 2002, name: "Ola Nordmann" },
    ]);
    mocks.name
      .mockReset()
      .mockImplementation((id: number) => Promise.resolve(id === 2001 ? "Kari Nordmann" : "Ola Nordmann"));
  });
  afterEach(cleanup);

  it("offers a picker of the customer's contacts, and links the one picked from this conversation", async () => {
    renderLink(link());
    const picker = await screen.findByPlaceholderText("Choose a contact");
    fireEvent.click(picker);
    fireEvent.click(await screen.findByText("Ola Nordmann"));
    fireEvent.click(screen.getByRole("button", { name: "Link to contact" }));
    await waitFor(() => expect(mocks.link).toHaveBeenCalledWith("participant-1", 2002, "conversation-1"));
  });

  it("offers no picker while the conversation has no customer", () => {
    renderLink(link(), null);
    expect(screen.queryByLabelText("Choose a contact")).not.toBeInTheDocument();
  });

  it("shows the contact's name once linked, and Unlink clears it", async () => {
    renderLink(link({ contactId: 2001 }));
    expect(await screen.findByText("Linked to Kari Nordmann")).toBeInTheDocument();
    expect(screen.queryByLabelText("Choose a contact")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Unlink" }));
    await waitFor(() => expect(mocks.unlink).toHaveBeenCalledWith("participant-1"));
  });

  it("offers the contact a shared e-mail address names, and links nothing until it is clicked", async () => {
    renderLink(link({ suggestedContactId: 2001 }), null);
    const button = await screen.findByRole("button", { name: "Link to Kari Nordmann" });
    expect(mocks.link).not.toHaveBeenCalled();
    fireEvent.click(button);
    await waitFor(() => expect(mocks.link).toHaveBeenCalledWith("participant-1", 2001, "conversation-1"));
  });

  it("says whose Messenger link the person used when they are linked to another contact", async () => {
    renderLink(link({ contactId: 2002, referralContactId: 2001 }));
    expect(await screen.findByText("This person used Kari Nordmann's Messenger link")).toBeInTheDocument();
    expect(screen.getByText("Linked to Ola Nordmann")).toBeInTheDocument();
  });

  it("falls back to the contact's number when Customers will not name it", async () => {
    mocks.name.mockResolvedValue(null);
    renderLink(link({ contactId: 2001 }));
    expect(await screen.findByText("Linked to Contact #2001")).toBeInTheDocument();
  });
});
