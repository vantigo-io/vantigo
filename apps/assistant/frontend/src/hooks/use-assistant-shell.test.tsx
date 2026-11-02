import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AppShellLayout } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AssistantConversationDetail } from "../api/types";
import type { AssistantChip } from "../lib/context";
import { setAssistantRunStatus } from "../lib/run-status";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  assistantMessage,
  card,
  cardPart,
  controlledStream,
  conversation,
  detailOf,
  sseResponse,
  stubAssistantApi,
  userMessage,
} from "../test/server";
import { useAssistantShell } from "./use-assistant-shell";
import "../i18n";

const customerChip: AssistantChip = {
  app: "customers",
  route: "/customers/$customerId",
  label: "Customer Ola Nordmann",
  params: { customerId: "42" },
};

/** What the host's root layout does with the hook, with nothing else around it. */
const Harness = ({
  context = customerChip,
  onNavigate = () => {},
  onOpenInAssistant = () => {},
}: {
  context?: AssistantChip;
  onNavigate?: (route: string) => void;
  onOpenInAssistant?: (id: string | null) => void;
}) => {
  const shell = useAssistantShell({ enabled: true, context, onNavigate, onOpenInAssistant });
  return (
    <AppShellLayout
      headerActions={
        <>
          {shell.button}
          <button type="button" onClick={() => shell.ask("Where is the invoice for Acme?")}>
            ask from spotlight
          </button>
        </>
      }
      aside={shell.aside}
    >
      <div>the page</div>
      <input aria-label="plain input" />
      <div contentEditable suppressContentEditableWarning data-testid="rich-editor" />
    </AppShellLayout>
  );
};

const panel = () => screen.getByRole("complementary", { name: "Assistant panel", hidden: true });
const isOpen = () => !panel().hasAttribute("inert");
const openButton = () => screen.findByRole("button", { name: "Open the assistant" });

beforeEach(() => {
  setAssistantRunStatus("idle");
  window.localStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("the assistant in the shell", () => {
  it("shows no button and no panel when the assistant is not available", async () => {
    const fetchMock = stubAssistantApi({ availability: { available: false, local: true, reason: "not_enabled" } });
    renderWithProviders(<Harness />);
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => String(url) === "/api/v1/assistant/availability")).toBe(true),
    );
    expect(screen.queryByRole("button", { name: /assistant/i })).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    expect(screen.queryByRole("complementary", { name: "Assistant panel", hidden: true })).not.toBeInTheDocument();
  });

  it("starts closed, opens from the button and closes from the panel's Close", async () => {
    stubAssistantApi();
    renderWithProviders(<Harness />);
    const button = await openButton();
    expect(screen.queryByRole("complementary", { hidden: true })).not.toBeInTheDocument();

    await userEvent.click(button);
    await waitFor(() => expect(isOpen()).toBe(true));
    expect(screen.getByRole("button", { name: "Close the assistant" })).toHaveAttribute("aria-pressed", "true");

    await userEvent.click(within(panel()).getByRole("button", { name: "Close" }));
    expect(isOpen()).toBe(false);
  });

  it("toggles with Ctrl+J and Cmd+J, but leaves the key to an editor that uses it", async () => {
    stubAssistantApi();
    renderWithProviders(<Harness />);
    await openButton();

    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    await waitFor(() => expect(isOpen()).toBe(true));
    fireEvent.keyDown(window, { key: "J", metaKey: true });
    await waitFor(() => expect(isOpen()).toBe(false));

    // A plain input does not use the key: the shortcut works there.
    fireEvent.keyDown(screen.getByLabelText("plain input"), { key: "j", ctrlKey: true });
    await waitFor(() => expect(isOpen()).toBe(true));
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    await waitFor(() => expect(isOpen()).toBe(false));

    // A contenteditable editor keeps it, and so does a handler that took the key itself.
    fireEvent.keyDown(screen.getByTestId("rich-editor"), { key: "j", ctrlKey: true });
    const input = screen.getByLabelText("plain input");
    input.addEventListener("keydown", (event) => event.preventDefault());
    fireEvent.keyDown(input, { key: "j", ctrlKey: true });
    expect(isOpen()).toBe(false);

    // Other chords are not ours.
    fireEvent.keyDown(window, { key: "j", ctrlKey: true, shiftKey: true });
    fireEvent.keyDown(window, { key: "j" });
    expect(isOpen()).toBe(false);
  });

  it("reopens the last conversation the person had open, from the server", async () => {
    const last = conversation({ id: "c-last", title: "Metering point steps" });
    stubAssistantApi({
      me: { lastConversationId: "c-last" },
      conversations: [last],
      details: {
        "c-last": detailOf(last, [
          userMessage("How do I add a metering point?"),
          assistantMessage("Open Energy, then choose Metering points."),
        ]),
      },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());

    expect(await screen.findByText("How do I add a metering point?")).toBeInTheDocument();
    expect(screen.getByText("Open Energy, then choose Metering points.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("panel-title")).toHaveTextContent("Metering point steps"));
  });

  it("opens a fresh conversation when the person has none, and New conversation leaves the open one", async () => {
    const last = conversation({ id: "c-last" });
    stubAssistantApi({
      me: { lastConversationId: "c-last" },
      conversations: [last],
      details: { "c-last": detailOf(last, [userMessage("Earlier question")]) },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    expect(await screen.findByText("Earlier question")).toBeInTheDocument();

    await userEvent.click(within(panel()).getByRole("button", { name: "New conversation" }));
    expect(await screen.findByText("How can I help?")).toBeInTheDocument();
    expect(screen.queryByText("Earlier question")).not.toBeInTheDocument();
  });

  it("opens a conversation from the recent menu and remembers it on the server", async () => {
    const first = conversation({ id: "c1", title: "First" });
    const second = conversation({ id: "c2", title: "Second" });
    const fetchMock = stubAssistantApi({
      conversations: [first, second],
      details: { c1: detailOf(first, [userMessage("one")]), c2: detailOf(second, [userMessage("two")]) },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    await userEvent.click(await within(panel()).findByRole("button", { name: "Recent conversations" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Second" }));

    expect(await screen.findByText("two")).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "PUT").body).toEqual({ lastConversationId: "c2" }));
  });

  it("opens in the Assistant app on the open conversation, and closes the panel", async () => {
    const last = conversation({ id: "c-last" });
    stubAssistantApi({ me: { lastConversationId: "c-last" }, details: { "c-last": detailOf(last, []) } });
    const onOpenInAssistant = vi.fn();
    renderWithProviders(<Harness onOpenInAssistant={onOpenInAssistant} />);
    await userEvent.click(await openButton());
    await waitFor(() => expect(screen.getByRole("button", { name: "Open in Assistant" })).toBeEnabled());
    await userEvent.click(screen.getByRole("button", { name: "Open in Assistant" }));
    expect(onOpenInAssistant).toHaveBeenCalledWith("c-last");
    expect(isOpen()).toBe(false);
  });

  it("keeps the panel's width in localStorage, and works when storage throws", async () => {
    stubAssistantApi();
    window.localStorage.setItem("vantigo.assistant.panelWidth", "520");
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    await waitFor(() => expect(isOpen()).toBe(true));
    const handle = within(panel()).getByRole("separator", { name: "Resize the assistant panel" });
    expect(handle).toHaveAttribute("aria-valuenow", "520");
    fireEvent.keyDown(handle, { key: "ArrowLeft" });
    expect(window.localStorage.getItem("vantigo.assistant.panelWidth")).toBe("544");
  });

  it("falls back to the default width when storage cannot be read", async () => {
    stubAssistantApi();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    const handle = await within(panel()).findByRole("separator", { name: "Resize the assistant panel" });
    expect(handle).toHaveAttribute("aria-valuenow", "400");
    fireEvent.keyDown(handle, { key: "ArrowLeft" });
    expect(handle).toHaveAttribute("aria-valuenow", "424");
  });
});

describe("the context chip", () => {
  const sendFrom = async (text: string) => {
    await userEvent.click(await openButton());
    const box = await within(panel()).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, `${text}{Enter}`);
  };

  it("shows the screen or record, sends it with the message, and can be removed for the next one", async () => {
    const fetchMock = stubAssistantApi({ stream: () => sseResponse([["done", { state: "done" }]]) });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    const chip = await within(panel()).findByTestId("context-chip");
    expect(chip).toHaveTextContent("Viewing: Customer Ola Nordmann");

    // Sent with the chip.
    const box = within(panel()).getByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "Who is this?{Enter}");
    await waitFor(() => expect(sentAll(fetchMock, "POST").some((call) => call.url.endsWith("/messages"))).toBe(true));
    const first = sentAll(fetchMock, "POST").find((call) => call.url.endsWith("/messages"));
    expect(first?.body).toEqual({ text: "Who is this?", context: customerChip });

    // Removed: the next message goes without it.
    await userEvent.click(await within(panel()).findByRole("button", { name: "Do not send what I am viewing" }));
    expect(within(panel()).queryByTestId("context-chip")).not.toBeInTheDocument();
    await userEvent.type(box, "And without context{Enter}");
    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").filter((call) => call.url.endsWith("/messages"))).toHaveLength(2),
    );
    const second = sentAll(fetchMock, "POST").filter((call) => call.url.endsWith("/messages"))[1];
    expect(second.body).toEqual({ text: "And without context" });
  });

  it("follows the person to the next screen, bringing a removed chip back", async () => {
    stubAssistantApi();
    const { rerenderWithProviders } = renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    await userEvent.click(await within(panel()).findByRole("button", { name: "Do not send what I am viewing" }));
    expect(within(panel()).queryByTestId("context-chip")).not.toBeInTheDocument();

    const next: AssistantChip = { app: "invoices", route: "/invoices", label: "Invoices" };
    rerenderWithProviders(<Harness context={next} />);
    expect(await within(panel()).findByTestId("context-chip")).toHaveTextContent("Viewing: Invoices");
  });

  it("starts a conversation that remembers where it began", async () => {
    const fetchMock = stubAssistantApi({ stream: () => sseResponse([["done", { state: "done" }]]) });
    renderWithProviders(<Harness />);
    await sendFrom("Hello");
    await waitFor(() => expect(sentAll(fetchMock, "POST").length).toBeGreaterThanOrEqual(2));
    const created = sentAll(fetchMock, "POST").find((call) => call.url === "/api/v1/assistant/conversations");
    expect(created?.body).toEqual({ origin: { app: "customers", route: "/customers/$customerId" } });
  });
});

describe("a run", () => {
  it("renders streamed text as it arrives, the tool in use, then the persisted messages", async () => {
    const stream = controlledStream();
    const details: Record<string, AssistantConversationDetail> = {};
    const fetchMock = stubAssistantApi({ stream: () => stream.response, details });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    const box = await within(panel()).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "How do I add a metering point?{Enter}");

    // The person's own words show at once.
    expect(await screen.findByText("How do I add a metering point?")).toBeInTheDocument();
    await waitFor(() => expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/messages"))).toBe(true));
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();

    stream.push("run", { runId: "run-1" });
    stream.push("message", userMessage("How do I add a metering point?", { id: "u1", runId: "run-1" }));
    stream.push("tool", { callId: "t1", name: "search_docs", label: "Searching the documentation", state: "started" });
    expect(await screen.findByText("Searching the documentation")).toBeInTheDocument();

    stream.push("tool", { callId: "t1", name: "search_docs", label: "Searching the documentation", state: "done" });
    // Split in the middle of an event, as a network would.
    stream.pushRaw('event: delta\ndata: {"text":"Open **Ene');
    stream.pushRaw('rgy**, then "}\n\nevent: delta\ndata: {"text":"Metering points."}\n\n');
    const streaming = await screen.findByTestId("streaming");
    await waitFor(() => expect(streaming).toHaveTextContent("Open Energy, then Metering points."));
    expect(within(streaming).getByText("Energy").tagName).toBe("STRONG");

    const answer = assistantMessage("Open **Energy**, then Metering points.", {
      id: "a1",
      runId: "run-1",
      parts: [
        { type: "tool_call", callId: "t1", name: "search_docs", label: "Searching the documentation" },
        { type: "text", text: "Open **Energy**, then Metering points." },
        { type: "link", label: "Take me there", route: "/energy/metering-points" },
      ],
    });
    // The server has persisted both messages by the time it says done.
    details["c-new"] = detailOf(conversation({ id: "c-new", title: "How do I add a metering point?" }), [
      userMessage("How do I add a metering point?", { id: "u1", runId: "run-1" }),
      answer,
    ]);
    stream.push("message", answer);
    stream.push("done", { state: "done" });
    stream.close();

    await waitFor(() => expect(screen.queryByTestId("streaming")).not.toBeInTheDocument());
    expect(screen.getByText("Searching the documentation")).toBeInTheDocument();
    expect(screen.getByText("Energy").tagName).toBe("STRONG");
    expect(screen.getByRole("button", { name: "Take me there" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument());
  });

  it("stops the run on Stop", async () => {
    const stream = controlledStream();
    const fetchMock = stubAssistantApi({ stream: () => stream.response });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    await userEvent.type(
      await within(panel()).findByRole("textbox", { name: "Message to the assistant" }),
      "Long question{Enter}",
    );
    stream.push("run", { runId: "run-9" });
    await userEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").some((call) => call.url === "/api/v1/assistant/runs/run-9/stop")).toBe(true),
    );
    stream.push("done", { state: "stopped" });
    stream.close();
    expect(await screen.findByText("Stopped.")).toBeInTheDocument();
  });

  it("says so when a run is already in progress, and follows it by polling", async () => {
    const last = conversation({ id: "c-busy", running: false });
    // A run another tab started: the conversation reads as idle until the send is refused.
    let busyReads = -1;
    const fetchMock = stubAssistantApi({
      me: { lastConversationId: "c-busy" },
      details: { "c-busy": detailOf(last, [userMessage("First")]) },
      handle: (method, path) => {
        if (method === "POST" && path.endsWith("/c-busy/messages")) {
          busyReads = 0;
          return new Response(JSON.stringify({ title: "busy", status: 409, code: "run_in_progress" }), {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          });
        }
        if (method === "GET" && path === "/api/v1/assistant/conversations/c-busy") {
          if (busyReads >= 0) busyReads += 1;
          const running = busyReads >= 1 && busyReads < 3;
          return new Response(
            JSON.stringify(
              detailOf(conversation({ id: "c-busy", running }), [userMessage("First")], {
                activeRun: running
                  ? {
                      id: "run-b",
                      conversationId: "c-busy",
                      state: "running",
                      toolCalls: 0,
                      startedAt: "2026-11-01T08:00:00Z",
                      stopRequested: false,
                    }
                  : undefined,
              }),
            ),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return undefined;
      },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    await screen.findByText("First");
    await userEvent.type(within(panel()).getByRole("textbox", { name: "Message to the assistant" }), "Second{Enter}");

    expect(await screen.findByText("The assistant is still answering your last message.")).toBeInTheDocument();
    // The conversation now reports a running run: the view works, polling every two seconds until it ends.
    await waitFor(() => expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument());
    const polls = () =>
      fetchMock.actualCalls.filter(([url]) => String(url) === "/api/v1/assistant/conversations/c-busy").length;
    const before = polls();
    await waitFor(() => expect(polls()).toBeGreaterThan(before), { timeout: 4500 });
    await waitFor(() => expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument(), { timeout: 8000 });
  }, 15000);

  it("sends a Spotlight question into a fresh conversation", async () => {
    const last = conversation({ id: "c-last" });
    const fetchMock = stubAssistantApi({
      me: { lastConversationId: "c-last" },
      details: { "c-last": detailOf(last, [userMessage("Older chat")]) },
      stream: () => sseResponse([["done", { state: "done" }]]),
    });
    renderWithProviders(<Harness />);
    await screen.findByRole("button", { name: "Open the assistant" });
    await userEvent.click(screen.getByRole("button", { name: "ask from spotlight" }));
    await waitFor(() => expect(isOpen()).toBe(true));
    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").some((call) => call.url === "/api/v1/assistant/conversations")).toBe(true),
    );
    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").find((call) => call.url.endsWith("/messages"))?.body).toMatchObject({
        text: "Where is the invoice for Acme?",
      }),
    );
  });

  it("badges the button while a run goes and when it has finished with the panel shut", async () => {
    const stream = controlledStream();
    stubAssistantApi({ stream: () => stream.response });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    await userEvent.type(
      await within(panel()).findByRole("textbox", { name: "Message to the assistant" }),
      "Hi{Enter}",
    );
    stream.push("run", { runId: "r" });
    await userEvent.click(within(panel()).getByRole("button", { name: "Close" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Open the assistant" })).toHaveAttribute("data-status", "working"),
    );
    stream.push("done", { state: "done" });
    stream.close();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Open the assistant" })).toHaveAttribute("data-status", "done"),
    );
    // Looking at it clears the badge.
    await userEvent.click(screen.getByRole("button", { name: "Open the assistant" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Close the assistant" })).toHaveAttribute("data-status", "idle"),
    );
  });
});

describe("a change that waits for the person", () => {
  const button = () => screen.getByRole("button", { name: /the assistant/i, pressed: undefined });

  it("shows waiting on the button while the panel is shut, and clears it when the card is decided", async () => {
    const proposed = card({ conversationId: "c1" });
    const summary = conversation({ id: "c1", title: "Payment terms" });
    const details: Record<string, AssistantConversationDetail> = {
      c1: detailOf(
        summary,
        [
          userMessage("Change it"),
          assistantMessage("Proposed.", {
            id: "a1",
            parts: [{ type: "text", text: "Proposed." }, cardPart(proposed.id)],
          }),
        ],
        { pendingActions: [proposed] },
      ),
    };
    stubAssistantApi({
      me: { lastConversationId: "c1" },
      details,
      decide: () => {
        details.c1 = detailOf(summary, details.c1.messages, {
          pendingActions: [
            {
              ...proposed,
              status: "cancelled",
              fields: [{ name: "paymentTermsDays" }],
              outcome: { ok: false, reason: "cancelled" },
            },
          ],
        });
        return sseResponse([
          ["run", { runId: "run-1" }],
          ["done", { state: "done" }],
        ]);
      },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    const group = await within(panel()).findByRole("group", { name: "Proposed change" });

    await userEvent.click(within(panel()).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(button()).toHaveAttribute("data-status", "waiting"));
    expect(button()).toHaveAttribute("title", "The assistant is waiting for you to confirm a change");

    // Decided, it is no longer waited for.
    await userEvent.click(await openButton());
    await userEvent.click(within(group).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Open the assistant", hidden: true })).toBeNull());
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Close the assistant" })).toHaveAttribute("data-status", "idle"),
    );
  });

  it("notifies the person when a card arrives with the panel shut", async () => {
    const stream = controlledStream();
    const details: Record<string, AssistantConversationDetail> = {};
    stubAssistantApi({
      details,
      stream: () => {
        // The server has the card by the time it says the run is waiting.
        details["c-new"] = detailOf(conversation({ id: "c-new" }), [userMessage("Change the payment terms")], {
          pendingActions: [card({ conversationId: "c-new" })],
        });
        return stream.response;
      },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    const box = await within(panel()).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "Change the payment terms{Enter}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument());

    await userEvent.click(within(panel()).getByRole("button", { name: "Close" }));
    stream.push("run", { runId: "run-1" });
    stream.push("confirmation", card());
    stream.push("done", { state: "awaiting_confirmation" });
    stream.close();

    expect(await screen.findByText("The assistant is waiting for you")).toBeInTheDocument();
    expect(screen.getByText("Set Ola Nordmann's payment terms to 30 days")).toBeInTheDocument();
    await waitFor(() => expect(button()).toHaveAttribute("data-status", "waiting"));
  });

  it("does not notify while the panel is open", async () => {
    const stream = controlledStream();
    stubAssistantApi({ stream: () => stream.response });
    renderWithProviders(<Harness />);
    await userEvent.click(await openButton());
    const box = await within(panel()).findByRole("textbox", { name: "Message to the assistant" });
    await userEvent.type(box, "Change the payment terms{Enter}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument());
    stream.push("confirmation", card());
    stream.push("done", { state: "awaiting_confirmation" });
    stream.close();
    await waitFor(() => expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument());
    expect(screen.queryByText("The assistant is waiting for you")).not.toBeInTheDocument();
  });
});
