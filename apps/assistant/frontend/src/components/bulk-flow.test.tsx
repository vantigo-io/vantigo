import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  assistantMessage,
  bulkCard,
  bulkJob,
  cardPart,
  conversation,
  detailOf,
  sseResponse,
  stubAssistantApi,
  userMessage,
} from "../test/server";
import { ConversationView } from "./conversation-view";
import "../i18n";

const origin = { app: "projects", route: "/projects" };
const summary = conversation({ id: "c1", title: "Notes" });
const jobUrl = "/api/v1/assistant/bulk-jobs/j1000000-0000-4000-8000-000000000001";
const actionUrl = "/api/v1/assistant/pending-actions/p2000000-0000-4000-8000-000000000002";

/** A conversation whose last run proposed a bulk change, with its job where it stands. */
const withBulk = (
  card: ReturnType<typeof bulkCard>,
  run = "awaiting_confirmation" as "awaiting_confirmation" | "running",
) =>
  detailOf(
    summary,
    [
      userMessage("Add the note to every project"),
      assistantMessage("I have proposed it.", {
        id: "a1",
        parts: [{ type: "text", text: "I have proposed it." }, cardPart(card.id)],
      }),
    ],
    {
      activeRun: { id: "run-1", conversationId: "c1", state: run, toolCalls: 1, startedAt: "x", stopRequested: false },
      pendingActions: [card],
    },
  );

const view = (props: Partial<Parameters<typeof ConversationView>[0]> = {}) =>
  renderWithProviders(<ConversationView conversationId="c1" origin={origin} onCreated={() => {}} {...props} />);

afterEach(() => {
  vi.useRealTimers();
});

describe("a bulk change in a conversation", () => {
  it("is live after a reload: the plan waits for Start and the composer waits with it", async () => {
    stubAssistantApi({ details: { c1: withBulk(bulkCard()) } });
    const onWaitingChange = vi.fn();
    view({ onWaitingChange });
    const group = await screen.findByRole("group", { name: "Bulk change" });
    expect(within(group).getByRole("button", { name: "Start" })).toBeEnabled();
    expect(screen.getByRole("textbox", { name: "Message to the assistant" })).toBeDisabled();
    await waitFor(() => expect(onWaitingChange).toHaveBeenCalledWith(true));
  });

  it("starts: Start posts the decision, the stream shows the canary item by item, and the screen is told once the canary is done", async () => {
    const proposed = bulkCard();
    const canaryDone = bulkCard({
      state: "awaiting_continue",
      processed: 5,
      succeeded: 5,
      pauseReason: "canary_done",
      canary: [0, 1, 2, 3, 4].map((seq) => ({ seq, label: `Project ${seq}`, status: "succeeded" as const })),
    });
    const fetchMock = stubAssistantApi({
      details: { c1: withBulk(proposed) },
      handle: (method, path) =>
        method === "GET" && path === jobUrl ? jsonResponse(200, bulkJob(canaryDone.bulk)) : undefined,
      decide: () =>
        sseResponse([
          ["run", { runId: "run-1" }],
          ["confirmation", bulkCard({ state: "canary", processed: 1, succeeded: 1 })],
          ["confirmation", bulkCard({ state: "canary", processed: 3, succeeded: 3 })],
          ["confirmation", canaryDone],
          ["done", { state: "awaiting_confirmation" }],
        ]),
    });
    const { queryClient } = view();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await userEvent.click(await screen.findByRole("button", { name: "Start" }));

    await waitFor(() => expect(sentAll(fetchMock, "POST").map((call) => call.url)).toContain(`${actionUrl}/confirm`));
    await waitFor(() =>
      expect(invalidate.mock.calls.filter(([filter]) => (filter as { predicate?: unknown })?.predicate)).toHaveLength(
        1,
      ),
    );
    const [filter] = invalidate.mock.calls.find(([f]) => (f as { predicate?: unknown })?.predicate) as [
      { predicate: (query: unknown) => boolean },
    ];
    expect(filter.predicate({ queryKey: ["projects", "list"] })).toBe(true);
    expect(filter.predicate({ queryKey: ["assistant", "conversation", "c1"] })).toBe(false);
  });

  it("continues and stops through the job's own endpoints, and shows why one was refused", async () => {
    const waiting = bulkCard({ state: "awaiting_continue", processed: 5, succeeded: 5, pauseReason: "canary_done" });
    const fetchMock = stubAssistantApi({
      details: { c1: withBulk(waiting) },
      handle: (method, path) => {
        if (method === "GET" && path === jobUrl) return jsonResponse(200, bulkJob(waiting.bulk));
        if (method === "POST" && path === `${jobUrl}/continue`)
          return jsonResponse(200, bulkJob({ ...waiting.bulk, state: "running" }));
        if (method === "POST" && path === `${jobUrl}/stop`)
          return new Response(JSON.stringify({ title: "gone", code: "job_not_waiting" }), {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          });
        return undefined;
      },
    });
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await waitFor(() => expect(sentAll(fetchMock, "POST").map((call) => call.url)).toContain(`${jobUrl}/continue`));

    await userEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(sentAll(fetchMock, "POST").map((call) => call.url)).toContain(`${jobUrl}/stop`));
    expect(await screen.findByRole("alert")).toHaveTextContent("This job is not waiting for you any more.");
  });

  it("works while the job runs: the badge reports working, the conversation is polled, and the screen refreshes when it ends", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const running = bulkCard({ state: "running", processed: 40, succeeded: 40 });
    const done = bulkCard({ state: "done", processed: 130, succeeded: 130 }, { outcome: { ok: true } });
    let reads = 0;
    // Two reads while it runs, one when it has ended and the run resumes to answer, then the run is over.
    const ended = detailOf(summary, [userMessage("Add the note"), assistantMessage("Done: all 130.", { id: "a2" })], {
      pendingActions: [done],
      lastRun: { id: "run-1", conversationId: "c1", state: "done", toolCalls: 1, startedAt: "x", stopRequested: false },
    });
    stubAssistantApi({
      handle: (method, path) => {
        if (method === "GET" && path === "/api/v1/assistant/conversations/c1") {
          const n = reads++;
          return jsonResponse(200, n < 2 ? withBulk(running) : n === 2 ? withBulk(done, "running") : ended);
        }
        if (method === "GET" && path === jobUrl)
          return jsonResponse(200, bulkJob(reads < 3 ? running.bulk : done.bulk));
        return undefined;
      },
    });
    const onWorkingChange = vi.fn();
    const onWaitingChange = vi.fn();
    const { queryClient } = view({ onWorkingChange, onWaitingChange });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await screen.findByRole("group", { name: "Bulk change" });
    await waitFor(() => expect(onWorkingChange).toHaveBeenCalledWith(true));
    expect(onWaitingChange).not.toHaveBeenCalledWith(true);

    await vi.advanceTimersByTimeAsync(2100);
    await vi.advanceTimersByTimeAsync(2100);
    await waitFor(() => expect(screen.getByTestId("bulk-summary")).toHaveTextContent("Done: 130 changed"));
    await waitFor(() =>
      expect(invalidate.mock.calls.filter(([filter]) => (filter as { predicate?: unknown })?.predicate)).toHaveLength(
        1,
      ),
    );
    // The composer is for the person again once the job and the run have ended.
    await vi.advanceTimersByTimeAsync(2100);
    await waitFor(() => expect(onWorkingChange).toHaveBeenLastCalledWith(false));
  });

  it("reports waiting, not working, while a job waits for Continue", async () => {
    stubAssistantApi({
      details: { c1: withBulk(bulkCard({ state: "awaiting_continue", processed: 5, succeeded: 5 })) },
      handle: (method, path) =>
        method === "GET" && path === jobUrl
          ? jsonResponse(200, bulkJob({ state: "awaiting_continue", processed: 5, succeeded: 5 }))
          : undefined,
    });
    const onWorkingChange = vi.fn();
    const onWaitingChange = vi.fn();
    view({ onWorkingChange, onWaitingChange });
    await screen.findByRole("group", { name: "Bulk change" });
    await waitFor(() => expect(onWaitingChange).toHaveBeenCalledWith(true));
    expect(onWorkingChange).not.toHaveBeenCalledWith(true);
  });
});
