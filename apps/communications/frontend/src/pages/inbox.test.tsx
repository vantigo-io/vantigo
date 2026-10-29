import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InboxPage } from "./inbox";

const mocks = vi.hoisted(() => ({
  fetchConversations: vi.fn(),
  fetchConversation: vi.fn(),
  replyToConversation: vi.fn(),
  stageConversationAttachment: vi.fn(),
  fetchInlineImage: vi.fn(),
  navigate: vi.fn(),
  search: {} as Record<string, unknown>,
  editorText: "",
}));

const page = (data: unknown[]) => ({
  data,
  pagination: {
    page: 1,
    pageSize: 30,
    totalCount: data.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

const participant = {
  id: "p-1",
  channelId: "channel-1",
  address: "customer@example.com",
  displayName: "Customer Example",
  contactId: null,
};

const conversation = (id: string, subject: string) => ({
  id,
  channelId: "channel-1",
  subject,
  status: "open" as const,
  assignedUserId: null,
  customerId: null,
  customerAssociationSource: null,
  suggestedCustomerId: null,
  candidateCustomerIds: [],
  lastActivityAt: "2026-01-01T00:00:00Z",
  previewText: `Preview for ${subject}`,
  participants: [participant],
  unread: true,
  tags: [],
});

const detail = (id: string, subject: string) => ({
  ...conversation(id, subject),
  suggestedCustomerConfidence: null,
  suggestedCustomerReasoning: null,
  createdAt: "2026-01-01T00:00:00Z",
  messages: [
    {
      id: "m-1",
      direction: "inbound" as const,
      participant,
      authorUserId: null,
      subject,
      textBody: "Could you help me with this?",
      htmlBody: null,
      occurredAt: "2026-01-01T00:00:00Z",
      createdAt: "2026-01-01T00:00:00Z",
      attachments: [],
      deliveries: [],
    },
  ],
  lastReadAt: null,
  replyRecipients: { canReply: true, canReplyAll: false, replyTo: participant.address, replyAllCc: [] },
});

vi.mock("../api/conversations", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/conversations")>();
  return {
    conversationsQueryOptions: (filters: Record<string, unknown> = {}) => ({
      queryKey: ["conversations", filters],
      queryFn: () => mocks.fetchConversations(filters),
    }),
    conversationQueryOptions: (id: string) => ({
      queryKey: ["conversation", id],
      queryFn: () => mocks.fetchConversation(id),
      enabled: Boolean(id),
    }),
    tagsQueryOptions: () => ({
      queryKey: ["communication-tags"],
      queryFn: () => Promise.resolve([{ id: "tag-1", name: "Billing", color: null }]),
    }),
    replyToConversation: mocks.replyToConversation,
    markConversationRead: vi.fn().mockResolvedValue(undefined),
    updateConversation: vi.fn().mockResolvedValue(undefined),
    addConversationNote: vi.fn().mockResolvedValue(undefined),
    addTag: vi.fn().mockResolvedValue(undefined),
    removeTag: vi.fn().mockResolvedValue(undefined),
    draftConversationWithAi: vi.fn().mockResolvedValue({ text: "Draft reply" }),
    suggestConversationCustomerWithAi: vi.fn().mockResolvedValue({}),
    attachmentDownloadUrl: (path: string) => path,
    attachmentUploadStatusQueryOptions: (conversationId: string, attachmentId: string) => ({
      queryKey: ["conversation-attachment-upload", conversationId, attachmentId],
      queryFn: () => Promise.resolve(undefined),
      enabled: false,
    }),
    stageConversationAttachment: mocks.stageConversationAttachment,
    safeHtmlSrcDoc: (html: string) => html,
    rawMessageDownloadUrl: (path: string) => path,
    // The real substitution, over a fetch the test answers.
    inlineImages: (html: string, attachments: Parameters<typeof actual.inlineImages>[1]) =>
      actual.inlineImages(html, attachments, mocks.fetchInlineImage),
  };
});

vi.mock("@mantine/tiptap", () => {
  const Wrapper = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const Content = () => <div className="tiptap" contentEditable suppressContentEditableWarning />;
  const RichTextEditor = Object.assign(Wrapper, {
    Toolbar: Wrapper,
    ControlsGroup: Wrapper,
    Bold: () => null,
    Italic: () => null,
    BulletList: () => null,
    Content,
  });
  return { RichTextEditor };
});

vi.mock("@tiptap/react", () => ({
  useEditor: () => ({
    getText: () => mocks.editorText,
    getHTML: () => `<p>${mocks.editorText}</p>`,
    commands: { clearContent: vi.fn(), setContent: vi.fn() },
  }),
}));

vi.mock("@tiptap/starter-kit", () => ({ default: {} }));

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => options,
  useSearch: () => mocks.search,
  useNavigate: () => mocks.navigate,
}));

const renderPage = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <InboxPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

afterEach(() => {
  cleanup();
  setLanguagePreference("auto");
});

beforeEach(() => {
  mocks.fetchConversations.mockReset();
  mocks.fetchConversation.mockReset();
  mocks.replyToConversation.mockReset().mockResolvedValue({ conversationId: "c-1", messageId: "m-2", status: "sent" });
  mocks.stageConversationAttachment.mockReset();
  mocks.fetchInlineImage.mockReset().mockResolvedValue(null);
  mocks.navigate.mockReset();
  mocks.search = {};
  mocks.editorText = "";
  vi.stubGlobal("crypto", { randomUUID: () => "reply-idempotency-key" });
});

describe("inbox", () => {
  it("shows the loading state", () => {
    mocks.fetchConversations.mockReturnValue(new Promise(() => undefined));
    renderPage();
    expect(screen.getByTestId("content-skeleton")).toBeTruthy();
  });

  it("shows the conversation list error state", async () => {
    mocks.fetchConversations.mockRejectedValue(new Error("service unavailable"));
    renderPage();
    expect(await screen.findByText(/Could not load conversations: service unavailable/)).toBeTruthy();
  });

  it("shows the empty state", async () => {
    mocks.fetchConversations.mockResolvedValue(page([]));
    renderPage();
    expect(await screen.findByText("No conversations match these filters.")).toBeTruthy();
  });

  it("renders conversations and shows the selected thread", async () => {
    const first = conversation("c-1", "First question");
    const second = conversation("c-2", "Second question");
    mocks.fetchConversations.mockResolvedValue(page([first, second]));
    mocks.fetchConversation.mockImplementation((id: string) =>
      Promise.resolve(detail(id, id === "c-1" ? first.subject : second.subject)),
    );
    renderPage();

    expect(await screen.findByText("First question")).toBeTruthy();
    expect(screen.getByText("Second question")).toBeTruthy();
    expect(await screen.findByText("Could you help me with this?")).toBeTruthy();

    mocks.navigate.mockImplementation((options: { search: Record<string, unknown> }) => {
      mocks.search = options.search;
    });
    fireEvent.click(screen.getByRole("button", { name: /Second question/ }));
    renderPage();
    expect(await screen.findByRole("heading", { name: "Second question" })).toBeTruthy();
  });

  it("puts the filters in the toolbar, with no side column and no mobile filters toggle", async () => {
    mocks.fetchConversations.mockResolvedValue(page([]));
    const { container } = renderPage();
    await screen.findByText("No conversations match these filters.");

    const toolbar = container.querySelector(".inbox-toolbar");
    expect(toolbar).toContainElement(screen.getByRole("radio", { name: "Open" }));
    expect(toolbar).toContainElement(screen.getByRole("checkbox", { name: "Unread" }));
    expect(toolbar).toContainElement(screen.getByRole("combobox", { name: "Tag" }));
    expect(container.querySelector(".inbox-filters")).toBeNull();
    expect(screen.queryByRole("button", { name: "Filters" })).not.toBeInTheDocument();
  });

  it("filters by status from the segmented control and clears the selected conversation", async () => {
    mocks.search = { conversationId: "c-1" };
    mocks.fetchConversations.mockResolvedValue(page([]));
    renderPage();
    await screen.findByText("No conversations match these filters.");
    expect(screen.getByRole("radio", { name: "All" })).toBeChecked();

    mocks.navigate.mockImplementation((options: { search: Record<string, unknown> }) => {
      mocks.search = options.search;
    });
    fireEvent.click(screen.getByRole("radio", { name: "Closed" }));

    expect(mocks.navigate).toHaveBeenCalledWith({ search: { status: "closed", conversationId: undefined } });
    renderPage();
    await waitFor(() =>
      expect(mocks.fetchConversations).toHaveBeenCalledWith(expect.objectContaining({ status: "closed" })),
    );
    expect(screen.getAllByRole("radio", { name: "Closed" }).at(-1)).toBeChecked();
  });

  it("toggles the unread filter and fetches with unreadOnly", async () => {
    mocks.fetchConversations.mockResolvedValue(page([]));
    renderPage();
    await screen.findByText("No conversations match these filters.");

    mocks.navigate.mockImplementation((options: { search: Record<string, unknown> }) => {
      mocks.search = options.search;
    });
    fireEvent.click(screen.getByRole("checkbox", { name: "Unread" }));
    renderPage();

    await waitFor(() =>
      expect(mocks.fetchConversations).toHaveBeenCalledWith(
        expect.objectContaining({ page: 1, pageSize: 30, unreadOnly: true }),
      ),
    );
    expect(screen.getAllByRole("checkbox", { name: "Unread" }).at(-1)).toBeChecked();
  });

  it("filters by tag from the tag select", async () => {
    mocks.fetchConversations.mockResolvedValue(page([]));
    renderPage();
    await screen.findByText("No conversations match these filters.");

    mocks.navigate.mockImplementation((options: { search: Record<string, unknown> }) => {
      mocks.search = options.search;
    });
    fireEvent.click(screen.getByRole("combobox", { name: "Tag" }));
    fireEvent.click(await screen.findByRole("option", { name: "Billing" }));

    expect(mocks.navigate).toHaveBeenCalledWith({ search: { tagId: "tag-1", conversationId: undefined } });
    renderPage();
    await waitFor(() =>
      expect(mocks.fetchConversations).toHaveBeenCalledWith(expect.objectContaining({ tagId: "tag-1" })),
    );
  });

  it("submits a reply through the composer API", async () => {
    const item = conversation("c-1", "Question");
    mocks.fetchConversations.mockResolvedValue(page([item]));
    mocks.fetchConversation.mockResolvedValue(detail(item.id, item.subject));
    mocks.editorText = "Here is the answer.";
    renderPage();
    await screen.findByText("Could you help me with this?");

    const editor = document.querySelector<HTMLElement>(".tiptap[contenteditable='true']");
    expect(editor).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Send reply" }));

    await waitFor(() =>
      expect(mocks.replyToConversation).toHaveBeenCalledWith(
        "c-1",
        expect.objectContaining({ textBody: "Here is the answer.", replyMode: "reply", attachmentIds: [] }),
        "reply-idempotency-key",
      ),
    );
  });

  describe("the composer on a channel with a size limit", () => {
    const apiError = (status: number, code: string) => Object.assign(new Error("server words"), { status, code });

    // The detail answers the budget of MIME and the attachments it takes; the composer shows the latter only.
    const openConversation = async (maxMessageBytes: number | null) => {
      const item = conversation("c-1", "Question");
      const maxAttachmentBytes = maxMessageBytes === null ? null : 2_100_000;
      mocks.fetchConversations.mockResolvedValue(page([item]));
      mocks.fetchConversation.mockResolvedValue({
        ...detail(item.id, item.subject),
        maxMessageBytes,
        maxAttachmentBytes,
      });
      renderPage();
      await screen.findByText("Could you help me with this?");
    };

    const attach = () => {
      const input = document.querySelector<HTMLInputElement>('input[type="file"]');
      if (!input) throw new Error("the file input is required");
      fireEvent.change(input, { target: { files: [new File(["x"], "big.pdf", { type: "application/pdf" })] } });
    };

    it("shows the channel's limit beside the attachments", async () => {
      await openConversation(3_000_000);
      expect(screen.getByText("Attachments up to about 2.1 MB in total on this channel")).toBeTruthy();
    });

    it("shows no limit on an SMTP channel", async () => {
      await openConversation(null);
      expect(screen.queryByText(/in total on this channel/)).toBeNull();
    });

    it("says an attachment is larger than the channel can send", async () => {
      mocks.stageConversationAttachment.mockRejectedValue(apiError(413, "attachment_too_large"));
      await openConversation(3_000_000);
      attach();
      expect(
        await screen.findByText("The attachment is larger than this channel can send (about 2.1 MB)."),
      ).toBeTruthy();
      expect(screen.queryByText(/server words|attachment_too_large/)).toBeNull();
    });

    it("says an attachment is larger than the configured limit on an SMTP channel", async () => {
      mocks.stageConversationAttachment.mockRejectedValue(apiError(413, "attachment_too_large"));
      await openConversation(null);
      attach();
      expect(await screen.findByText("The attachment is larger than the allowed size.")).toBeTruthy();
    });

    it("says the reply is larger than the channel can send", async () => {
      mocks.replyToConversation.mockRejectedValue(apiError(422, "too_large"));
      mocks.editorText = "Here is the answer.";
      await openConversation(3_000_000);
      fireEvent.click(screen.getByRole("button", { name: "Send reply" }));
      expect(
        await screen.findByText("The message and its attachments are larger than this channel can send."),
      ).toBeTruthy();
      expect(screen.queryByText(/server words|too_large/)).toBeNull();
    });

    it("says a reply was not sent for any other refusal", async () => {
      mocks.replyToConversation.mockRejectedValue(apiError(500, "internal"));
      mocks.editorText = "Here is the answer.";
      await openConversation(null);
      fireEvent.click(screen.getByRole("button", { name: "Send reply" }));
      expect(await screen.findByText("The reply was not sent. Try again.")).toBeTruthy();
    });

    it("shows the limit and the refusals in Norwegian", async () => {
      setLanguagePreference("nb");
      mocks.stageConversationAttachment.mockRejectedValue(apiError(413, "attachment_too_large"));
      mocks.replyToConversation.mockRejectedValue(apiError(422, "too_large"));
      mocks.editorText = "Her er svaret.";
      await openConversation(3_000_000);
      expect(screen.getByText("Vedlegg på opptil omtrent 2,1 MB til sammen på denne kanalen")).toBeTruthy();
      attach();
      expect(await screen.findByText("Vedlegget er større enn denne kanalen kan sende (omtrent 2,1 MB).")).toBeTruthy();
      fireEvent.click(screen.getByRole("button", { name: "Send svar" }));
      expect(await screen.findByText("Meldingen og vedleggene er større enn denne kanalen kan sende.")).toBeTruthy();
    });
  });

  describe("receiving", () => {
    const attachment = (overrides: Record<string, unknown> = {}) => ({
      id: "a-1",
      fileName: "report.pdf",
      contentType: "application/pdf",
      sizeBytes: 1200,
      contentId: null,
      scanStatus: "unscanned",
      isInline: false,
      createdAt: "2026-01-01T00:00:00Z",
      downloadAvailable: true,
      downloadPath: "/api/v1/communications/attachments/a-1/download",
      ...overrides,
    });
    const inbound = (overrides: Record<string, unknown> = {}) => ({
      ...detail("c-1", "Question").messages[0],
      autoSubmitted: false,
      unreadable: false,
      rawDownloadPath: "/api/v1/communications/messages/m-1/raw",
      ...overrides,
    });
    const open = async (
      messages: unknown[],
      replyRecipients: unknown = { canReply: true, canReplyAll: false, replyTo: participant.address, replyAllCc: [] },
    ) => {
      const item = conversation("c-1", "Question");
      mocks.fetchConversations.mockResolvedValue(page([item]));
      mocks.fetchConversation.mockResolvedValue({ ...detail(item.id, item.subject), messages, replyRecipients });
      renderPage();
      await screen.findByRole("heading", { name: "Question" });
    };
    const frame = () => document.querySelector<HTMLIFrameElement>("iframe.message-html");

    it("shows an inbound message's HTML even with a text body, and the text on Show as text", async () => {
      await open([inbound({ textBody: "Plain words", htmlBody: "<p>Formatted <b>words</b></p>" })]);
      await waitFor(() => expect(frame()?.getAttribute("srcdoc")).toContain("<b>words</b>"));
      expect(frame()).toHaveAttribute("sandbox", "");
      expect(screen.queryByText("Plain words")).toBeNull();

      fireEvent.click(screen.getByRole("button", { name: "Show as text" }));
      expect(screen.getByText("Plain words")).toBeTruthy();
      expect(frame()).toBeNull();
      fireEvent.click(screen.getByRole("button", { name: "Show formatted" }));
      expect(frame()).toBeTruthy();
    });

    it("shows the text without a toggle when the message has no HTML", async () => {
      await open([inbound()]);
      expect(await screen.findByText("Could you help me with this?")).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Show as text" })).toBeNull();
      expect(frame()).toBeNull();
    });

    it("turns the message's own inline image into a data: URI and fetches nothing else", async () => {
      const inline = attachment({
        id: "img-1",
        fileName: "logo.png",
        contentType: "image/png",
        contentId: "logo@x",
        isInline: true,
        downloadPath: "/api/v1/communications/attachments/img-1/download",
      });
      const elsewhere = attachment({ id: "img-2", fileName: "other.png", contentType: "image/png", isInline: true });
      mocks.fetchInlineImage.mockResolvedValue("data:image/png;base64,AAAA");
      await open([
        inbound({
          htmlBody: '<p>Hi</p><img src="/api/v1/communications/attachments/img-1/download" alt="logo">',
          attachments: [inline, elsewhere],
        }),
      ]);
      await waitFor(() => expect(frame()?.getAttribute("srcdoc")).toContain('src="data:image/png;base64,AAAA"'));
      expect(frame()?.getAttribute("srcdoc")).not.toContain("/api/v1/communications/attachments/img-1/download");
      expect(mocks.fetchInlineImage).toHaveBeenCalledTimes(1);
      expect(mocks.fetchInlineImage).toHaveBeenCalledWith("/api/v1/communications/attachments/img-1/download");
    });

    it("marks an auto-reply", async () => {
      await open([inbound({ autoSubmitted: true })]);
      expect(await screen.findByText("Auto-reply")).toBeTruthy();
    });

    it("says an unreadable message could not be read and offers its original", async () => {
      await open([inbound({ unreadable: true, textBody: "Vantigo could not read this message.", htmlBody: null })]);
      expect(await screen.findByText("Vantigo could not read this message.")).toBeTruthy();
      expect(screen.getByRole("link", { name: "Download original" })).toHaveAttribute(
        "href",
        "/api/v1/communications/messages/m-1/raw",
      );
    });

    it("offers every inbound message's original in its menu", async () => {
      await open([inbound()]);
      fireEvent.click(await screen.findByRole("button", { name: "Message actions" }));
      expect(await screen.findByRole("menuitem", { name: "Download original" })).toHaveAttribute(
        "href",
        "/api/v1/communications/messages/m-1/raw",
      );
    });

    it("offers an unscanned attachment with a warning", async () => {
      await open([inbound({ attachments: [attachment()] })]);
      const link = await screen.findByRole("link", { name: "report.pdf" });
      expect(link).toHaveAttribute("href", "/api/v1/communications/attachments/a-1/download");
      expect(link).toHaveAttribute("download", "report.pdf");
      expect(screen.getByText("Not scanned. Open it only if you trust the sender.")).toBeTruthy();
    });

    it("disables Send reply when there is no message to reply to", async () => {
      mocks.editorText = "Hello";
      await open([], { canReply: false, canReplyAll: false, replyTo: null, replyAllCc: [] });
      expect(screen.getByText("There is no message from the customer to reply to yet.")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Send reply" })).toBeDisabled();
    });

    it("replies to all from the latest message's Cc", async () => {
      mocks.editorText = "Answer for everyone.";
      const cc = (id: string, address: string) => ({ ...participant, id, address, displayName: null });
      await open([inbound()], {
        canReply: true,
        canReplyAll: true,
        replyTo: participant.address,
        replyAllCc: [cc("p-2", "anna@example.com"), cc("p-3", "ola@example.com")],
      });
      fireEvent.click(screen.getByRole("combobox", { name: "Reply mode" }));
      fireEvent.click(await screen.findByRole("option", { name: "Reply all · 3" }));
      expect(screen.getByText("Replying to customer@example.com and 2 additional recipients")).toBeTruthy();
      fireEvent.click(screen.getByRole("button", { name: "Send reply" }));
      await waitFor(() =>
        expect(mocks.replyToConversation).toHaveBeenCalledWith(
          "c-1",
          expect.objectContaining({ replyMode: "reply_all" }),
          "reply-idempotency-key",
        ),
      );
    });

    it("shows receiving in Norwegian", async () => {
      setLanguagePreference("nb");
      await open([
        inbound({ autoSubmitted: true, htmlBody: "<p>Hei</p>", attachments: [attachment()] }),
        inbound({ id: "m-2", unreadable: true, htmlBody: null }),
      ]);
      expect(await screen.findByText("Autosvar")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Vis som tekst" })).toBeTruthy();
      expect(screen.getByText("Vantigo kunne ikke lese denne meldingen.")).toBeTruthy();
      expect(screen.getByRole("link", { name: "Last ned originalen" })).toBeTruthy();
      expect(screen.getByText("Ikke skannet. Åpne det bare hvis du stoler på avsenderen.")).toBeTruthy();
    });

    it("says there is no message to reply to in Norwegian", async () => {
      setLanguagePreference("nb");
      await open([], { canReply: false, canReplyAll: false, replyTo: null, replyAllCc: [] });
      expect(screen.getByText("Det finnes ingen melding fra kunden å svare på ennå.")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Send svar" })).toBeDisabled();
    });
  });
});
