import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationDetail } from "../api/conversations";
import "../i18n";
import { ChatComposer } from "./ChatComposer";

const mocks = vi.hoisted(() => ({ replyToConversation: vi.fn(), addConversationNote: vi.fn() }));

vi.mock("../api/conversations", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/conversations")>();
  return {
    ...actual,
    replyToConversation: mocks.replyToConversation,
    addConversationNote: mocks.addConversationNote,
    stageConversationAttachment: vi.fn(),
    attachmentUploadStatusQueryOptions: () => ({ queryKey: ["upload"], queryFn: () => undefined, enabled: false }),
  };
});

const conversation = (overrides: Partial<ConversationDetail> = {}): ConversationDetail =>
  ({
    id: "conversation-1",
    channelId: "channel-1",
    channelType: "messenger",
    subject: null,
    status: "open",
    assignedUserId: null,
    customerId: null,
    customerAssociationSource: null,
    suggestedCustomerId: null,
    candidateCustomerIds: [],
    lastActivityAt: "2026-01-01T00:00:00Z",
    previewText: null,
    participants: [],
    unread: false,
    tags: [],
    suggestedCustomerConfidence: null,
    suggestedCustomerReasoning: null,
    createdAt: "2026-01-01T00:00:00Z",
    messages: [],
    lastReadAt: null,
    replyRecipients: { canReply: true, canReplyAll: false, replyTo: "4242", replyAllCc: [] },
    componentLimits: {
      maxText: 2000,
      maxAnswerOptions: 13,
      maxAnswerTitle: 20,
      shareEmail: true,
      maxCards: 10,
      maxCardChoices: 3,
      maxCardTitle: 80,
      maxCardSubtitle: 80,
      maxChoiceTitle: 20,
    },
    replyWindow: { state: "open", closesAt: "2026-01-02T00:00:00Z" },
    ...overrides,
  }) as ConversationDetail;

const renderComposer = (detail: ConversationDetail) => {
  const onSent = vi.fn().mockResolvedValue(undefined);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <ChatComposer conversation={detail} onSent={onSent} />
      </MantineProvider>
    </QueryClientProvider>,
  );
  return { onSent };
};

const send = () => screen.getByRole("button", { name: "Send reply" });
const write = (text: string) => fireEvent.change(screen.getByLabelText("Message"), { target: { value: text } });

describe("the chat composer", () => {
  beforeEach(() => {
    void setLanguagePreference("en");
    mocks.replyToConversation.mockReset().mockResolvedValue({ conversationId: "conversation-1", messageId: "m-1" });
    mocks.addConversationNote.mockReset().mockResolvedValue({});
  });
  afterEach(cleanup);

  it("is plain text: no subject, no Cc and no rich formatting", () => {
    renderComposer(conversation());
    expect(screen.getByLabelText("Message")).toBeInTheDocument();
    expect(screen.queryByLabelText(/subject/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^cc$/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /bold/i })).not.toBeInTheDocument();
  });

  it("sends the text, with no components when there are none", async () => {
    const { onSent } = renderComposer(conversation());
    write("Hello there");
    fireEvent.click(send());
    await waitFor(() => expect(mocks.replyToConversation).toHaveBeenCalledTimes(1));
    const [id, body, key] = mocks.replyToConversation.mock.calls[0] ?? [];
    expect(id).toBe("conversation-1");
    expect(body).toEqual({ textBody: "Hello there", replyMode: "reply", attachmentIds: [] });
    expect(typeof key).toBe("string");
    await waitFor(() => expect(onSent).toHaveBeenCalled());
  });

  describe("the window", () => {
    it("says nothing is wrong while it is open, and until when", () => {
      renderComposer(conversation());
      expect(screen.getByText(/You can answer until/)).toBeInTheDocument();
      write("Hi");
      expect(send()).toBeEnabled();
    });

    it("warns that the reply is tagged once the first 24 hours are over", () => {
      renderComposer(conversation({ replyWindow: { state: "human_agent", closesAt: "2026-01-08T00:00:00Z" } }));
      expect(screen.getByText(/Human Agent tag/)).toBeInTheDocument();
      write("Hi");
      expect(send()).toBeEnabled();
    });

    it("disables sending when it is closed, and says the customer must write again", () => {
      renderComposer(conversation({ replyWindow: { state: "closed", closesAt: "2026-01-02T00:00:00Z" } }));
      expect(screen.getByText("The customer must write again before you can answer here")).toBeInTheDocument();
      write("Hi");
      expect(send()).toBeDisabled();
    });

    it("still lets a note be added when it is closed", async () => {
      renderComposer(conversation({ replyWindow: { state: "closed", closesAt: null } }));
      fireEvent.click(screen.getByRole("button", { name: "Add note" }));
      fireEvent.change(screen.getByLabelText("Internal note"), { target: { value: "Called them" } });
      const add = screen.getAllByRole("button", { name: "Add note" }).at(-1) as HTMLElement;
      expect(add).toBeEnabled();
      fireEvent.click(add);
      await waitFor(() => expect(mocks.addConversationNote).toHaveBeenCalledWith("conversation-1", "Called them"));
    });
  });

  describe("the text limit", () => {
    it("counts characters against the provider's limit and refuses more", () => {
      renderComposer(conversation());
      write("å".repeat(2000));
      expect(screen.getByText("2000 / 2000")).toBeInTheDocument();
      expect(send()).toBeEnabled();
      write("å".repeat(2001));
      expect(screen.getByText("2001 / 2000")).toBeInTheDocument();
      expect(send()).toBeDisabled();
    });
  });

  describe("the answer options", () => {
    it("adds Yes and No in the user's language, in order, and sends them", async () => {
      renderComposer(conversation());
      write("Shall we?");
      fireEvent.click(screen.getByRole("button", { name: "Add Yes / No" }));
      expect(screen.getByLabelText("Answer option 1")).toHaveValue("Yes");
      expect(screen.getByLabelText("Answer option 2")).toHaveValue("No");
      fireEvent.click(send());
      await waitFor(() => expect(mocks.replyToConversation).toHaveBeenCalledTimes(1));
      expect(mocks.replyToConversation.mock.calls[0]?.[1]).toMatchObject({
        textBody: "Shall we?",
        components: {
          answerOptions: [
            { id: "yes", title: "Yes" },
            { id: "no", title: "No" },
          ],
        },
      });
    });

    it("lets the agent rename an option, and counts its characters against 20", () => {
      renderComposer(conversation());
      write("Hi");
      fireEvent.click(screen.getByRole("button", { name: "Add option" }));
      const input = screen.getByLabelText("Answer option 1");
      fireEvent.change(input, { target: { value: "Maybe later" } });
      expect(screen.getByTestId("option-counter")).toHaveTextContent("11/20");
      expect(send()).toBeEnabled();
      fireEvent.change(input, { target: { value: "x".repeat(21) } });
      expect(screen.getByTestId("option-counter")).toHaveTextContent("21/20");
      expect(screen.getByText("At most 20 characters")).toBeInTheDocument();
      expect(send()).toBeDisabled();
    });

    it("cannot send an option with no title", () => {
      renderComposer(conversation());
      write("Hi");
      fireEvent.click(screen.getByRole("button", { name: "Add option" }));
      expect(send()).toBeDisabled();
    });

    it("removes an option with its ×", () => {
      renderComposer(conversation());
      fireEvent.click(screen.getByRole("button", { name: "Add Yes / No" }));
      fireEvent.click(screen.getByRole("button", { name: "Remove option 1" }));
      expect(screen.getAllByLabelText(/Answer option/)).toHaveLength(1);
      expect(screen.getByLabelText("Answer option 1")).toHaveValue("No");
    });

    it("stops adding at the provider's limit of 13", () => {
      renderComposer(conversation());
      const add = screen.getByRole("button", { name: "Add option" });
      for (let i = 0; i < 13; i++) fireEvent.click(add);
      expect(screen.getAllByLabelText(/Answer option/)).toHaveLength(13);
      expect(add).toBeDisabled();
      expect(screen.getByRole("button", { name: "Add Yes / No" })).toBeDisabled();
    });

    it("follows a provider that allows fewer", () => {
      const base = conversation();
      renderComposer({
        ...base,
        componentLimits: { ...(base.componentLimits as NonNullable<typeof base.componentLimits>), maxAnswerOptions: 2 },
      });
      fireEvent.click(screen.getByRole("button", { name: "Add Yes / No" }));
      expect(screen.getByRole("button", { name: "Add option" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "Add Yes / No" })).toBeDisabled();
    });

    it("asks for an e-mail address by sending shareEmail", async () => {
      renderComposer(conversation());
      write("Where can we reach you?");
      fireEvent.click(screen.getByLabelText("Ask for e-mail"));
      fireEvent.click(send());
      await waitFor(() => expect(mocks.replyToConversation).toHaveBeenCalledTimes(1));
      expect(mocks.replyToConversation.mock.calls[0]?.[1]).toMatchObject({ components: { shareEmail: true } });
    });

    it("counts the request for an e-mail address among the 13 and stops the checkbox at the limit", () => {
      renderComposer(conversation());
      fireEvent.click(screen.getByLabelText("Ask for e-mail"));
      const add = screen.getByRole("button", { name: "Add option" });
      for (let i = 0; i < 13; i++) fireEvent.click(add);
      expect(screen.getAllByLabelText(/Answer option/)).toHaveLength(12);
      expect(add).toBeDisabled();
      fireEvent.click(screen.getByLabelText("Ask for e-mail"));
      for (let i = 0; i < 2; i++) fireEvent.click(add);
      expect(screen.getAllByLabelText(/Answer option/)).toHaveLength(13);
      expect(screen.getByLabelText("Ask for e-mail")).toBeDisabled();
    });

    it("hides the request for an e-mail address when the provider has none", () => {
      const base = conversation();
      renderComposer({
        ...base,
        componentLimits: { ...(base.componentLimits as NonNullable<typeof base.componentLimits>), shareEmail: false },
      });
      expect(screen.queryByLabelText("Ask for e-mail")).not.toBeInTheDocument();
    });

    it("shows the options only on a reply, not on a note", () => {
      renderComposer(conversation());
      fireEvent.click(screen.getByRole("button", { name: "Add note" }));
      const footer = screen.getByRole("contentinfo");
      expect(within(footer).queryByText("Answer options")).not.toBeInTheDocument();
    });
  });

  it("names a refusal of the reply in the user's words", async () => {
    mocks.replyToConversation.mockRejectedValue(Object.assign(new Error("422"), { code: "reply_window_closed" }));
    renderComposer(conversation());
    write("Hi");
    fireEvent.click(send());
    await waitFor(() =>
      expect(screen.getAllByText("The customer must write again before you can answer here").length).toBeGreaterThan(0),
    );
  });
});
