import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { AppShellLayout, setLanguagePreference, useSidePanelController } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type UseConversationsShellOptions, useConversationsShell } from "../hooks/useConversationsShell";
import { isConversationsShortcut, shouldIgnoreConversationsShortcut } from "../lib/shortcut";

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  editorText: "",
}));

vi.mock("../api/request", () => ({
  request: mocks.request,
  setAuthStateClearer: () => undefined,
  setUnauthorizedHandler: () => undefined,
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

const participant = {
  id: "p-1",
  channelId: "channel-1",
  address: "customer@example.com",
  displayName: "Customer Example",
  contactId: null,
};
const item = (id: string, subject: string, overrides: Record<string, unknown> = {}) => ({
  id,
  channelId: "channel-1",
  channelType: "email",
  subject,
  status: "open",
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
  ...overrides,
});
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
const detail = (id: string, subject: string, overrides: Record<string, unknown> = {}) => ({
  ...item(id, subject),
  suggestedCustomerConfidence: null,
  suggestedCustomerReasoning: null,
  createdAt: "2026-01-01T00:00:00Z",
  channelAddress: "post@firma.no",
  personalSender: { available: false, reason: "unavailable", errorCode: null, address: null, displayName: null },
  messages: [
    {
      id: "m-1",
      direction: "inbound",
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
  lastReadAt: "2026-01-01T00:00:00Z",
  replyRecipients: { canReply: true, canReplyAll: false, replyTo: participant.address, replyAllCc: [] },
  ...overrides,
});

let lists: Record<string, unknown[]>;
let counts = { unread: 0, assignedToMeUnread: 0 };
const urls = () => mocks.request.mock.calls.map(([url]) => String(url));
const listCalls = () => urls().filter((url) => url.startsWith("/api/v1/communications/conversations?"));

beforeEach(() => {
  setLanguagePreference("en");
  mocks.editorText = "";
  window.localStorage.clear();
  lists = { default: [item("c-1", "First question"), item("c-2", "Second question")] };
  counts = { unread: 0, assignedToMeUnread: 0 };
  mocks.request.mockReset().mockImplementation((url: string) => {
    if (url.startsWith("/api/v1/communications/inbox/counts")) return Promise.resolve(counts);
    if (url.startsWith("/api/v1/communications/tags")) return Promise.resolve([]);
    if (url.startsWith("/api/v1/communications/conversations?")) {
      const params = new URLSearchParams(url.split("?")[1]);
      if (params.get("assignedUserId")) return Promise.resolve(page(lists.mine ?? []));
      if (params.get("unreadOnly")) return Promise.resolve(page(lists.unread ?? lists.default ?? []));
      if (params.get("customerId")) return Promise.resolve(page(lists.customer ?? []));
      return Promise.resolve(page(lists.default ?? []));
    }
    const match = url.match(/conversations\/([^/?]+)$/);
    if (match)
      return Promise.resolve(detail(match[1] as string, match[1] === "c-1" ? "First question" : "Second question"));
    if (url.endsWith("/read")) return Promise.resolve(undefined);
    return Promise.resolve(undefined);
  });
  vi.stubGlobal("crypto", { randomUUID: () => "key" });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
});

const onNavigate = vi.fn();

const Harness = (props: Partial<UseConversationsShellOptions> & { children?: ReactNode }) => {
  const side = useSidePanelController();
  const shell = useConversationsShell({
    side,
    enabled: true,
    userId: "u1",
    canReply: true,
    canManage: true,
    onNavigate,
    ...props,
  });
  return (
    <AppShellLayout
      headerActions={
        <>
          {shell.button}
          <button type="button" onClick={() => side.close()}>
            close panel
          </button>
          <button type="button" onClick={() => side.show("conversations")}>
            show panel
          </button>
        </>
      }
      aside={side.aside(shell.sidePanel ? [shell.sidePanel] : [])}
    >
      <div>page</div>
    </AppShellLayout>
  );
};

const mount = (props: Partial<UseConversationsShellOptions> = {}) =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Harness {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  );

const openPanel = async () => {
  fireEvent.click(screen.getByRole("button", { name: /^Open conversations/ }));
  return screen.findByTestId("conversations-panel");
};

describe("the Conversations panel's list", () => {
  it("defaults to Assigned to me, and to All open when nothing is assigned to the person", async () => {
    lists.mine = [];
    mount();
    await openPanel();
    expect(await screen.findByText("First question")).toBeInTheDocument();
    const calls = listCalls();
    expect(calls[0]).toContain("assignedUserId=u1");
    expect(calls[0]).toContain("status=open");
    expect(calls.at(-1)).not.toContain("assignedUserId");
    expect(screen.getByRole("radio", { name: "All open" })).toBeChecked();
  });

  it("shows what is assigned to the person when something is", async () => {
    lists.mine = [item("c-9", "Mine only")];
    mount();
    await openPanel();
    expect(await screen.findByText("Mine only")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Assigned to me" })).toBeChecked();
    expect(screen.queryByText("First question")).not.toBeInTheDocument();
  });

  it("switches to Unread and All open", async () => {
    lists.mine = [item("c-9", "Mine only")];
    lists.unread = [item("c-5", "An unread one")];
    mount();
    await openPanel();
    await screen.findByText("Mine only");
    fireEvent.click(screen.getByRole("radio", { name: "Unread" }));
    expect(await screen.findByText("An unread one")).toBeInTheDocument();
    expect(listCalls().at(-1)).toContain("unreadOnly=true");
    fireEvent.click(screen.getByRole("radio", { name: "All open" }));
    expect(await screen.findByText("First question")).toBeInTheDocument();
  });

  it("offers This customer on a customer page, and never forces it", async () => {
    lists.mine = [item("c-9", "Mine only")];
    lists.customer = [item("c-7", "About the customer")];
    mount({ customerId: 7 });
    await openPanel();
    expect(await screen.findByText("Mine only")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Assigned to me" })).toBeChecked();
    fireEvent.click(screen.getByRole("radio", { name: "This customer" }));
    expect(await screen.findByText("About the customer")).toBeInTheDocument();
    expect(listCalls().at(-1)).toContain("customerId=7");
  });

  it("does not offer This customer away from a customer page", async () => {
    mount();
    await openPanel();
    await screen.findByText("First question");
    expect(screen.queryByRole("radio", { name: "This customer" })).not.toBeInTheDocument();
  });

  it("searches the conversations already loaded, without asking the server", async () => {
    lists.mine = [];
    mount();
    await openPanel();
    await screen.findByText("First question");
    const before = listCalls().length;
    fireEvent.change(screen.getByRole("searchbox", { name: "Search the conversations shown" }), {
      target: { value: "second" },
    });
    await waitFor(() => expect(screen.queryByText("First question")).not.toBeInTheDocument());
    expect(screen.getByText("Second question")).toBeInTheDocument();
    expect(listCalls().length).toBe(before);
  });
});

describe("a conversation in the panel", () => {
  it("opens with Back, Open in inbox and the customer's link navigating the main page", async () => {
    lists.mine = [];
    mount();
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /First question/ }));
    const panel = await screen.findByTestId("conversations-panel");
    expect(await within(panel).findByText("Could you help me with this?")).toBeInTheDocument();

    fireEvent.click(within(panel).getByRole("link", { name: "Open in inbox" }));
    expect(onNavigate).toHaveBeenCalledWith("/communications/inbox?conversationId=c-1");

    fireEvent.click(within(panel).getByRole("button", { name: "Back" }));
    expect(await screen.findByRole("button", { name: /Second question/ })).toBeInTheDocument();
  });

  it("links the customer", async () => {
    mocks.request.mockImplementation((url: string) =>
      Promise.resolve(
        url.startsWith("/api/v1/communications/conversations?")
          ? page([item("c-1", "First question")])
          : url.endsWith("/conversations/c-1")
            ? detail("c-1", "First question", { customerId: 42 })
            : url.startsWith("/api/v1/communications/inbox/counts")
              ? counts
              : url.startsWith("/api/v1/communications/tags")
                ? []
                : undefined,
      ),
    );
    mount();
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /First question/ }));
    fireEvent.click(await screen.findByRole("link", { name: "Customer #42" }));
    expect(onNavigate).toHaveBeenCalledWith("/customers/42");
    // The panel stays as it is.
    expect(screen.getByTestId("conversations-panel")).toBeInTheDocument();
  });

  it("reads the conversation that is opened, so the badge clears", async () => {
    mocks.request.mockImplementation((url: string) =>
      Promise.resolve(
        url.startsWith("/api/v1/communications/conversations?")
          ? page([item("c-1", "First question")])
          : url.endsWith("/conversations/c-1")
            ? detail("c-1", "First question", { lastReadAt: null })
            : url.startsWith("/api/v1/communications/inbox/counts")
              ? counts
              : [],
      ),
    );
    mount();
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /First question/ }));
    await waitFor(() => expect(urls()).toContain("/api/v1/communications/conversations/c-1/read"));
  });

  it("is read-only without the reply permission, and loses the close and tag controls without manage", async () => {
    lists.mine = [];
    mount({ canReply: false, canManage: false });
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /First question/ }));
    expect(await screen.findByText("You can read this conversation but may not reply to it.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send reply" })).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("Add tag")).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Status" })).toBeDisabled();
  });

  it("keeps a half-written Messenger reply while the panel is closed and opened again", async () => {
    const chat = (overrides: Record<string, unknown> = {}) =>
      detail("c-1", "", {
        subject: null,
        channelType: "messenger",
        replyWindow: { state: "open", closesAt: "2026-01-02T00:00:00Z" },
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
        ...overrides,
      });
    mocks.request.mockImplementation((url: string) =>
      Promise.resolve(
        url.startsWith("/api/v1/communications/conversations?")
          ? page([item("c-1", "", { channelType: "messenger", subject: null })])
          : url.endsWith("/conversations/c-1")
            ? chat()
            : url.startsWith("/api/v1/communications/inbox/counts")
              ? counts
              : [],
      ),
    );
    mount();
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /Preview for/ }));
    const box = await screen.findByRole("textbox", { name: "Message" });
    fireEvent.change(box, { target: { value: "Hello, we are looking into it" } });

    fireEvent.click(screen.getByRole("button", { name: "close panel" }));
    fireEvent.click(screen.getByRole("button", { name: "show panel" }));
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue("Hello, we are looking into it");
  });
});

describe("a poll that fails", () => {
  const messenger = (id: string) =>
    detail(id, "", {
      subject: null,
      channelType: "messenger",
      replyWindow: { state: "open", closesAt: "2026-01-02T00:00:00Z" },
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
    });

  it("keeps the thread, and the half-written reply, and says so inline", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let failing = false;
    mocks.request.mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/communications/conversations?"))
        return Promise.resolve(page([item("c-1", "", { channelType: "messenger", subject: null })]));
      if (url.endsWith("/conversations/c-1"))
        return failing ? Promise.reject(new Error("offline")) : Promise.resolve(messenger("c-1"));
      if (url.startsWith("/api/v1/communications/inbox/counts")) return Promise.resolve(counts);
      return Promise.resolve([]);
    });
    mount();
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /Preview for/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Message" }), { target: { value: "still typing" } });

    failing = true;
    await act(async () => void (await vi.advanceTimersByTimeAsync(5_200)));
    expect(await screen.findByText("Could not refresh; showing what was loaded last.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue("still typing");
    expect(screen.queryByText("Could not load this conversation.")).not.toBeInTheDocument();
  });

  it("still says it could not load a conversation that never loaded", async () => {
    mocks.request.mockImplementation((url: string) => {
      if (url.startsWith("/api/v1/communications/conversations?"))
        return Promise.resolve(page([item("c-1", "First question")]));
      if (url.endsWith("/conversations/c-1")) return Promise.reject(new Error("offline"));
      if (url.startsWith("/api/v1/communications/inbox/counts")) return Promise.resolve(counts);
      return Promise.resolve([]);
    });
    mount();
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: /First question/ }));
    expect(await screen.findByText("Could not load this conversation.")).toBeInTheDocument();
  });
});

describe("polling by visibility", () => {
  const tick = (ms: number) => act(async () => void (await vi.advanceTimersByTimeAsync(ms)));

  it("refetches the list every 15 s and the open conversation every 5 s while shown, and stops when hidden", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    lists.mine = [];
    mount();
    await openPanel();
    await screen.findByText("First question");
    fireEvent.click(screen.getByRole("button", { name: /First question/ }));
    await screen.findByText("Could you help me with this?");
    const detailCalls = () => urls().filter((url) => url.endsWith("/conversations/c-1")).length;

    const d0 = detailCalls();
    await tick(5_100);
    expect(detailCalls()).toBeGreaterThan(d0);

    // The panel is closed: nothing more is asked of the list or the conversation.
    fireEvent.click(screen.getByRole("button", { name: "close panel" }));
    await tick(100);
    const d1 = detailCalls();
    const l1 = listCalls().length;
    await tick(40_000);
    expect(detailCalls()).toBe(d1);
    expect(listCalls().length).toBe(l1);

    // Shown again: it resumes with a refetch.
    fireEvent.click(screen.getByRole("button", { name: "show panel" }));
    await tick(100);
    expect(detailCalls()).toBeGreaterThan(d1);

    // The tab is hidden: it stops again.
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    act(() => void document.dispatchEvent(new Event("visibilitychange")));
    await tick(100);
    const d2 = detailCalls();
    await tick(40_000);
    expect(detailCalls()).toBe(d2);

    // Visible again: a refetch.
    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    act(() => void document.dispatchEvent(new Event("visibilitychange")));
    await tick(100);
    expect(detailCalls()).toBeGreaterThan(d2);
  });

  it("refreshes the list every 15 s while the panel is shown", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    lists.mine = [];
    mount();
    await openPanel();
    await screen.findByText("First question");
    const before = listCalls().length;
    await tick(15_100);
    expect(listCalls().length).toBeGreaterThan(before);
  });
});

describe("the button and its badge", () => {
  it("shows the unread assigned to the person as a count, and names the counts", async () => {
    counts = { unread: 5, assignedToMeUnread: 2 };
    mount();
    expect(
      await screen.findByRole("button", {
        name: "Open conversations. Unread conversations assigned to you: 2. Unread conversations: 5",
      }),
    ).toBeInTheDocument();
  });

  it("shows a dot with the unread count named when nothing unread is assigned to the person", async () => {
    counts = { unread: 3, assignedToMeUnread: 0 };
    mount();
    expect(
      await screen.findByRole("button", { name: "Open conversations. Unread conversations: 3" }),
    ).toBeInTheDocument();
  });

  it("shows nothing when nothing is unread", async () => {
    mount();
    expect(await screen.findByRole("button", { name: "Open conversations" })).toBeInTheDocument();
    await waitFor(() => expect(urls()).toContain("/api/v1/communications/inbox/counts"));
  });

  it("reads the counts every 30 s while the tab is visible", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mount();
    await screen.findByRole("button", { name: "Open conversations" });
    const countCalls = () => urls().filter((url) => url.includes("/inbox/counts")).length;
    const before = countCalls();
    await act(async () => void (await vi.advanceTimersByTimeAsync(30_100)));
    expect(countCalls()).toBeGreaterThan(before);
  });

  it("has no button for a person who cannot use it, and none on the inbox page", () => {
    const { unmount } = mount({ enabled: false });
    expect(screen.queryByRole("button", { name: /^Open conversations/ })).not.toBeInTheDocument();
    unmount();
    mount({ onInboxPage: true });
    expect(screen.queryByRole("button", { name: /^Open conversations/ })).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: "u", ctrlKey: true, shiftKey: true });
    expect(screen.queryByTestId("conversations-panel")).not.toBeInTheDocument();
  });

  it("opens and closes with Ctrl+Shift+U and Cmd+Shift+U", async () => {
    mount();
    await screen.findByRole("button", { name: "Open conversations" });
    fireEvent.keyDown(window, { key: "U", ctrlKey: true, shiftKey: true });
    expect(await screen.findByTestId("conversations-panel")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close conversations" })).toHaveAttribute("aria-pressed", "true");
    fireEvent.keyDown(window, { key: "u", metaKey: true, shiftKey: true });
    expect(screen.getByRole("button", { name: "Open conversations" })).toHaveAttribute("aria-pressed", "false");
  });
});

describe("the shortcut's rules", () => {
  const key = (init: Partial<KeyboardEventInit>) => new KeyboardEvent("keydown", { key: "u", ...init });

  it("is Cmd or Ctrl with Shift and U, and nothing else", () => {
    expect(isConversationsShortcut(key({ ctrlKey: true, shiftKey: true }))).toBe(true);
    expect(isConversationsShortcut(key({ metaKey: true, shiftKey: true }))).toBe(true);
    expect(isConversationsShortcut(key({ ctrlKey: true }))).toBe(false);
    expect(isConversationsShortcut(key({ shiftKey: true }))).toBe(false);
    expect(isConversationsShortcut(key({ ctrlKey: true, shiftKey: true, altKey: true }))).toBe(false);
  });

  it("is left to code editors and to a handler that took the key", () => {
    const inside = (html: string) => {
      document.body.innerHTML = html;
      return document.querySelector("[data-target]") as Element;
    };
    expect(shouldIgnoreConversationsShortcut({ defaultPrevented: true, target: document.body })).toBe(true);
    expect(
      shouldIgnoreConversationsShortcut({
        defaultPrevented: false,
        target: inside('<div class="cm-editor"><div data-target></div></div>'),
      }),
    ).toBe(true);
    expect(
      shouldIgnoreConversationsShortcut({
        defaultPrevented: false,
        target: inside('<div contenteditable="true"><div data-target></div></div>'),
      }),
    ).toBe(true);
    // The panel's own composer is a rich-text editor too, and the key works there.
    expect(
      shouldIgnoreConversationsShortcut({
        defaultPrevented: false,
        target: inside('<div data-side-panel><div contenteditable="true" data-target></div></div>'),
      }),
    ).toBe(false);
    expect(
      shouldIgnoreConversationsShortcut({ defaultPrevented: false, target: inside("<input data-target />") }),
    ).toBe(false);
    document.body.innerHTML = "";
  });
});
