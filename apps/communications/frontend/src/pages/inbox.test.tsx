import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InboxPage } from "./inbox";

const mocks = vi.hoisted(() => ({
  fetchConversations: vi.fn(),
  fetchConversation: vi.fn(),
  replyToConversation: vi.fn().mockResolvedValue({ conversationId: "c-1", messageId: "m-2", status: "sent" }),
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

vi.mock("../api/conversations", () => ({
  conversationsQueryOptions: (filters: Record<string, unknown> = {}) => ({
    queryKey: ["conversations", filters],
    queryFn: () => mocks.fetchConversations(filters),
  }),
  conversationQueryOptions: (id: string) => ({
    queryKey: ["conversation", id],
    queryFn: () => mocks.fetchConversation(id),
    enabled: Boolean(id),
  }),
  tagsQueryOptions: () => ({ queryKey: ["communication-tags"], queryFn: () => Promise.resolve([]) }),
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
  stageConversationAttachment: vi.fn(),
  safeHtmlSrcDoc: (html: string) => html,
}));

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
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <InboxPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

afterEach(() => cleanup());

beforeEach(() => {
  mocks.fetchConversations.mockReset();
  mocks.fetchConversation.mockReset();
  mocks.replyToConversation.mockClear();
  mocks.navigate.mockReset();
  mocks.search = {};
  mocks.editorText = "";
  vi.stubGlobal("crypto", { randomUUID: () => "reply-idempotency-key" });
});

describe("inbox", () => {
  it("shows the loading state", () => {
    mocks.fetchConversations.mockReturnValue(new Promise(() => undefined));
    renderPage();
    expect(document.querySelector(".mantine-Loader-root")).toBeTruthy();
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

  it("toggles the unread filter and fetches with unreadOnly", async () => {
    mocks.fetchConversations.mockResolvedValue(page([]));
    renderPage();
    await screen.findByText("No conversations match these filters.");

    mocks.navigate.mockImplementation((options: { search: Record<string, unknown> }) => {
      mocks.search = options.search;
    });
    fireEvent.click(screen.getByRole("button", { name: "Unread" }));
    renderPage();

    await waitFor(() =>
      expect(mocks.fetchConversations).toHaveBeenCalledWith(
        expect.objectContaining({ page: 1, pageSize: 30, unreadOnly: true }),
      ),
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
});
