import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  assistantMessage,
  card,
  cardPart,
  conversation,
  detailOf,
  sseResponse,
  stubAssistantApi,
  userMessage,
} from "../test/server";
import { ConversationView } from "./conversation-view";
import "../i18n";

const origin = { app: "customers", route: "/customers" };
const summary = conversation({ id: "c1", title: "Payment terms" });

/** A conversation whose last run proposed a change that nobody has decided. */
const reloaded = (cardPatch: Parameters<typeof card>[0] = {}) => {
  const proposed = card(cardPatch);
  return {
    proposed,
    detail: detailOf(
      summary,
      [
        userMessage("Set Ola's payment terms to 30 days"),
        assistantMessage("I have proposed the change.", {
          id: "a1",
          parts: [{ type: "text", text: "I have proposed the change." }, cardPart(proposed.id)],
        }),
      ],
      {
        activeRun: {
          id: "run-1",
          conversationId: "c1",
          state: "awaiting_confirmation",
          toolCalls: 1,
          startedAt: "x",
          stopRequested: false,
        },
        pendingActions: [proposed],
      },
    ),
  };
};

const view = (props: Partial<Parameters<typeof ConversationView>[0]> = {}) =>
  renderWithProviders(<ConversationView conversationId="c1" origin={origin} onCreated={() => {}} {...props} />);

const decided = (status: "confirmed" | "cancelled" | "failed", extra = {}) =>
  card({
    status,
    fields: [{ name: "paymentTermsDays" }],
    decidedAt: "2026-11-02T09:00:00Z",
    outcome:
      status === "confirmed"
        ? { ok: true, status: 200 }
        : { ok: false, reason: status === "cancelled" ? "cancelled" : "refused" },
    ...extra,
  });

describe("a proposed change in a conversation", () => {
  it("is live after a reload: the card shows, and the composer waits for the decision", async () => {
    const { detail } = reloaded();
    stubAssistantApi({ details: { c1: detail } });
    view();
    const group = await screen.findByRole("group", { name: "Proposed change" });
    expect(within(group).getByRole("button", { name: "Confirm" })).toBeEnabled();
    const box = screen.getByRole("textbox", { name: "Message to the assistant" });
    expect(box).toBeDisabled();
    expect(box).toHaveAttribute("placeholder", "Confirm or cancel the change above first");
  });

  it("confirms from the reloaded conversation, goes on streaming, and refreshes the screen after success", async () => {
    const { proposed, detail } = reloaded();
    const confirmed = decided("confirmed");
    const fetchMock = stubAssistantApi({
      details: { c1: detail },
      decide: () =>
        sseResponse([
          ["run", { runId: "run-1" }],
          ["confirmation", { ...proposed, status: "confirmed" }],
          ["confirmation", confirmed],
          ["delta", { text: "Done. " }],
          ["message", assistantMessage("Done. The terms are 30 days.", { id: "a2" })],
          ["done", { state: "done" }],
        ]),
    });
    const { queryClient } = view();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").map((call) => call.url)).toContain(
        "/api/v1/assistant/pending-actions/p1000000-0000-4000-8000-000000000001/confirm",
      ),
    );
    // The screen is told: every query but the assistant's own is invalidated, once the change succeeded.
    await waitFor(() => {
      const predicates = invalidate.mock.calls.map(([filter]) => filter).filter((filter) => filter?.predicate);
      expect(predicates).toHaveLength(1);
      const { predicate } = predicates[0] as { predicate: (query: unknown) => boolean };
      expect(predicate({ queryKey: ["customers", "42"] })).toBe(true);
      expect(predicate({ queryKey: ["invoices"] })).toBe(true);
      expect(predicate({ queryKey: ["assistant", "conversation", "c1"] })).toBe(false);
    });
  });

  it("does not refresh the screen when the change was cancelled, refused or failed", async () => {
    for (const [verb, outcome] of [
      ["cancel", decided("cancelled")],
      ["confirm", decided("failed", { outcome: { ok: false, status: 412, reason: "changed" } })],
    ] as const) {
      const { proposed, detail } = reloaded();
      stubAssistantApi({
        details: { c1: detail },
        decide: () =>
          sseResponse([
            ["run", { runId: "run-1" }],
            ["confirmation", { ...proposed, status: verb === "cancel" ? "cancelled" : "confirmed" }],
            ["confirmation", outcome],
            ["done", { state: "done" }],
          ]),
      });
      const { queryClient, unmount } = view();
      const invalidate = vi.spyOn(queryClient, "invalidateQueries");
      await userEvent.click(await screen.findByRole("button", { name: verb === "cancel" ? "Cancel" : "Confirm" }));
      await waitFor(() =>
        expect(
          invalidate.mock.calls.some(([filter]) => (filter as { queryKey?: unknown[] })?.queryKey?.[0] === "assistant"),
        ).toBe(true),
      );
      expect(invalidate.mock.calls.filter(([filter]) => (filter as { predicate?: unknown })?.predicate)).toHaveLength(
        0,
      );
      unmount();
    }
  });

  it("says why when the server refuses the decision, and keeps the card to decide", async () => {
    const { detail } = reloaded();
    stubAssistantApi({
      details: { c1: detail },
      decide: () =>
        new Response(JSON.stringify({ status: 409, title: "decided", code: "action_decided" }), {
          status: 409,
          headers: { "Content-Type": "application/problem+json" },
        }),
    });
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Somebody already decided this change.");
  });

  it("shows a decided card from an earlier run as its outcome, and the box is free again", async () => {
    const proposed = decided("confirmed");
    stubAssistantApi({
      details: {
        c1: detailOf(
          summary,
          [
            userMessage("Set Ola's payment terms to 30 days"),
            assistantMessage("Proposed.", {
              id: "a1",
              parts: [{ type: "text", text: "Proposed." }, cardPart(proposed.id)],
            }),
          ],
          { pendingActions: [proposed] },
        ),
      },
    });
    view();
    const group = await screen.findByRole("group", { name: "Proposed change" });
    expect(within(group).getByText("Changed")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Message to the assistant" })).toBeEnabled();
  });

  it("announces a card from the stream, reports waiting, and takes the focus", async () => {
    const proposed = card();
    const proposedMessage = assistantMessage("Proposed.", {
      id: "a1",
      parts: [{ type: "text", text: "Proposed." }, cardPart(proposed.id)],
    });
    const onProposal = vi.fn();
    const onWaitingChange = vi.fn();
    const details: Record<string, ReturnType<typeof detailOf>> = {};
    stubAssistantApi({
      details,
      stream: () => {
        details["c-new"] = detailOf(
          conversation({ id: "c-new", title: "Terms" }),
          [userMessage("Change it"), proposedMessage],
          {
            activeRun: {
              id: "run-1",
              conversationId: "c-new",
              state: "awaiting_confirmation",
              toolCalls: 1,
              startedAt: "x",
              stopRequested: false,
            },
            pendingActions: [proposed],
          },
        );
        return sseResponse([
          ["run", { runId: "run-1" }],
          ["message", proposedMessage],
          ["confirmation", proposed],
          ["done", { state: "awaiting_confirmation" }],
        ]);
      },
    });
    const Host = () => {
      const [id, setId] = useState<string | null>(null);
      return (
        <ConversationView
          conversationId={id}
          origin={origin}
          onCreated={setId}
          onProposal={onProposal}
          onWaitingChange={onWaitingChange}
        />
      );
    };
    renderWithProviders(<Host />);
    await userEvent.type(await screen.findByRole("textbox", { name: "Message to the assistant" }), "Change it{Enter}");

    const group = await screen.findByRole("group", { name: "Proposed change" });
    await waitFor(() => expect(onProposal).toHaveBeenCalledWith(proposed));
    await waitFor(() => expect(onWaitingChange).toHaveBeenLastCalledWith(true));
    await waitFor(() => expect(group).toHaveFocus());
  });
});

describe("the query seam", () => {
  it("refetches only what is on screen", async () => {
    const { invalidateAfterAssistantWrites } = await import("../lib/invalidate");
    const client = new QueryClient();
    const customers = vi.fn().mockResolvedValue("fresh");
    client.setQueryDefaults(["customers"], { queryFn: customers });
    await client.fetchQuery({ queryKey: ["customers", "42"], queryFn: customers });
    customers.mockClear();
    invalidateAfterAssistantWrites(client);
    // Nothing observes it, so it is marked stale and not fetched.
    expect(client.getQueryState(["customers", "42"])?.isInvalidated).toBe(true);
    expect(customers).not.toHaveBeenCalled();
  });
});
