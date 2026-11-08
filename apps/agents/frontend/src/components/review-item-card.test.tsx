import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import type { ReviewItem } from "../api/types";
import { ReviewItemCard } from "./review-item-card";

const mocks = vi.hoisted(() => ({ approve: vi.fn(), sendEdited: vi.fn(), discard: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({
  Link: (props: Record<string, unknown>) => {
    // The router's own props are not attributes of an anchor.
    const { children, ...rest } = props;
    for (const key of ["to", "search", "params"]) delete rest[key];
    return (
      <a href="/agents" {...(rest as object)}>
        {children as never}
      </a>
    );
  },
}));
vi.mock("../api/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/agents")>()),
  approveItem: mocks.approve,
  sendEditedItem: mocks.sendEdited,
  discardItem: mocks.discard,
}));

const item = (patch: Partial<ReviewItem> = {}): ReviewItem => ({
  id: "i1",
  runId: "r1",
  agentId: "a1",
  agentName: "Hanna",
  roleId: "ro1",
  roleName: "Front desk",
  conversationId: "c1",
  kind: "reply",
  state: "waiting",
  score: 91,
  modelScore: 95,
  sources: ["getInvoicesById 2026-0931"],
  heldBy: ["S4", "C2"],
  expiresAt: new Date(Date.now() + 5 * 3_600_000).toISOString(),
  windowClosesAt: null,
  createdAt: "2026-11-08T09:00:00Z",
  decidedAt: null,
  decidedByUserId: null,
  discardReason: null,
  discardText: null,
  editRatio: null,
  outcome: null,
  version: 3,
  reply: { text: "Your order ships tomorrow.", disclosure: "«Hanna» is «Hansen Rør»'s virtual assistant (AI)." },
  ...patch,
});

const renderCard = (value: ReviewItem, canReview = true) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <ReviewItemCard item={value} canReview={canReview} />
      </MantineProvider>
    </QueryClientProvider>,
  );
};

describe("a review item", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("says why it waits in words, with its score, its sources and the disclosure locked above the reply", () => {
    renderCard(item());
    const why = within(screen.getByTestId("held-by"));
    expect(why.getByText("A first contact")).toBeInTheDocument();
    expect(why.getByText(/promises a date, a discount, a refund or a price/)).toBeInTheDocument();
    expect(screen.getByText("Score 91")).toBeInTheDocument();
    expect(screen.getByText(/The agent said 95/)).toBeInTheDocument();
    expect(screen.getByText("getInvoicesById 2026-0931")).toBeInTheDocument();
    const disclosure = screen.getByLabelText("Opening line");
    expect(disclosure).toHaveValue("«Hanna» is «Hansen Rør»'s virtual assistant (AI).");
    expect(disclosure).toHaveAttribute("readonly");
    expect(screen.getByTestId("reply-text")).toHaveTextContent("Your order ships tomorrow.");
    expect(screen.getByTestId("countdown")).toHaveTextContent(/Lapses in 4 h/);
  });

  it("shows a conversation the agent would start, to whom and on what subject, with the disclosure locked and no conversation to open", () => {
    renderCard(
      item({
        kind: "start",
        conversationId: "00000000-0000-0000-0000-000000000000",
        start: { channelId: "ch1", to: "kari@example.test", subject: "Følger opp tilbudet" },
        reply: { text: "Hei Kari!", disclosure: "«Hanna» is «Hansen Rør»'s virtual assistant (AI)." },
      }),
    );
    expect(screen.getByText("New conversation")).toBeInTheDocument();
    expect(within(screen.getByTestId("start")).getByText("To: kari@example.test")).toBeInTheDocument();
    expect(screen.getByLabelText("Opening line")).toHaveAttribute("readonly");
    expect(screen.queryByText("Open the conversation")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument();
  });

  it("counts down to a Messenger window's close", () => {
    renderCard(item({ windowClosesAt: new Date(Date.now() + 90 * 60_000).toISOString() }));
    expect(screen.getByTestId("countdown")).toHaveTextContent(/The Messenger window closes in 1 h 29 min/);
  });

  it("sends at the version it read", async () => {
    mocks.approve.mockResolvedValue(item({ state: "sent", version: 4 }));
    renderCard(item());
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(mocks.approve).toHaveBeenCalledTimes(1));
    expect(mocks.approve.mock.calls[0][0]).toMatchObject({ id: "i1", version: 3 });
  });

  it("edits and sends the reviewer's text, the disclosure staying locked above it", async () => {
    mocks.sendEdited.mockResolvedValue(item({ state: "edited_sent", version: 4 }));
    renderCard(item());
    fireEvent.click(screen.getByRole("button", { name: "Edit and send" }));
    const editor = screen.getByLabelText("The reply");
    expect(screen.getByLabelText("Opening line")).toHaveAttribute("readonly");
    fireEvent.change(editor, { target: { value: "It ships on Friday." } });
    fireEvent.click(screen.getByRole("button", { name: "Send the edited reply" }));
    await waitFor(() => expect(mocks.sendEdited).toHaveBeenCalledTimes(1));
    expect(mocks.sendEdited.mock.calls[0][0]).toMatchObject({ id: "i1", version: 3 });
    expect(mocks.sendEdited.mock.calls[0][1]).toBe("It ships on Friday.");
    expect(mocks.approve).not.toHaveBeenCalled();
  });

  it("discards with a reason", async () => {
    mocks.discard.mockResolvedValue(item({ state: "discarded", version: 4 }));
    renderCard(item());
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Reason"), { target: { value: "wrong_tone" } });
    fireEvent.change(within(dialog).getByLabelText("Anything to add (optional)"), { target: { value: "Too cold." } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(mocks.discard).toHaveBeenCalledTimes(1));
    expect(mocks.discard.mock.calls[0].slice(1)).toEqual(["wrong_tone", "Too cold."]);
  });

  it("offers a person without agents:review nothing to decide with", () => {
    renderCard(item(), false);
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Discard" })).toBeNull();
    expect(screen.getByText(/You may not review/)).toBeInTheDocument();
  });

  it("shows a decided item without buttons, and a change with its preview", () => {
    renderCard(item({ state: "discarded", discardReason: "wrong_facts", discardText: "No such order." }));
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
    expect(screen.getByText(/Wrong facts: No such order\./)).toBeInTheDocument();
    cleanup();
    renderCard(
      item({
        kind: "change",
        reply: undefined,
        heldBy: ["CHANGE"],
        change: {
          operationId: "putCustomersByIdContacts",
          summary: "Update a contact",
          explain: "The signature has a new mobile.",
          body: { mobile: "412 33 210" },
        },
      }),
    );
    expect(screen.getByText("Update a contact")).toBeInTheDocument();
    expect(screen.getByText("The signature has a new mobile.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit and send" })).toBeNull();
  });
});
