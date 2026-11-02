import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  conversationQueryKey,
  conversationQueryOptions,
  conversationsQueryKey,
  createConversation,
  stopRun,
} from "../api/assistant";
import { decideAction, type StreamEvent, StreamRefusedError, sendMessage } from "../api/stream";
import type { AssistantMessage, AssistantMessageContext, AssistantPendingAction } from "../api/types";
import { invalidateAfterAssistantWrites } from "../lib/invalidate";

type ErrorKey =
  | "errorGeneric"
  | "errorProvider"
  | "errorUnavailable"
  | "errorRunInProgress"
  | "errorBudget"
  | "errorActionDecided"
  | "errorActionExpired"
  | "errorNotAllowed";

/** The run this tab is listening to: what the stream has said so far. */
interface LiveRun {
  /** The conversation the run belongs to; undefined until a draft has been created. */
  conversationId?: string;
  runId?: string;
  /** The person's message, shown at once and replaced by the persisted one when it arrives. */
  pendingUser?: { text: string; context?: AssistantMessageContext };
  /** Messages the stream has persisted, in order. */
  messages: AssistantMessage[];
  /** The proposed changes the stream has announced, newest state of each. */
  cards: Record<string, AssistantPendingAction>;
  /** The assistant's current text, not yet a message. */
  partial: string;
  tool?: string;
  phase: "working" | "finished";
  finalState?: "done" | "stopped" | "awaiting_confirmation";
  /** A key of the catalog's `error*` family. */
  error?: ErrorKey;
}

export interface UseConversationOptions {
  /** The open conversation; null is a draft that exists once the first message is sent. */
  conversationId: string | null;
  /** Where a conversation started from here began. */
  origin: { app: string; route: string };
  /** A draft became a conversation. */
  onCreated: (id: string) => void;
  /** The conversation is gone (deleted, or never the person's): the host should leave it. */
  onMissing?: () => void;
  /** The run started or ended, for the button's badge. */
  onWorkingChange?: (working: boolean) => void;
  /** A change waits for the person's confirmation, or no longer does, for the button's badge. */
  onWaitingChange?: (waiting: boolean) => void;
  /** A change was proposed while this tab listened: the panel tells the person if it is shut. */
  onProposal?: (card: AssistantPendingAction) => void;
}

const POLL_MS = 2000;

const errorKeyFor = (error: unknown): ErrorKey => {
  if (error instanceof StreamRefusedError) {
    if (error.code === "run_in_progress") return "errorRunInProgress";
    if (error.code === "budget_exhausted") return "errorBudget";
    if (error.code === "action_decided") return "errorActionDecided";
    if (error.code === "action_expired") return "errorActionExpired";
    if (error.code === "operation_unavailable") return "errorNotAllowed";
    if (error.code === "assistant_unavailable" || error.status === 403) return "errorUnavailable";
    if (error.code === "ai_unavailable" || error.status === 503) return "errorProvider";
  }
  return "errorGeneric";
};

const errorKeyForCode = (code: string): ErrorKey =>
  code === "assistant_unavailable"
    ? "errorUnavailable"
    : code === "ai_unavailable" || code === "provider_error" || code === "provider_unavailable"
      ? "errorProvider"
      : code === "run_in_progress"
        ? "errorRunInProgress"
        : code === "budget_exhausted"
          ? "errorBudget"
          : "errorGeneric";

/**
 * One conversation's data and its run, shared by the panel and the app page.
 *
 * Sending, confirming and cancelling are each a `fetch` over a
 * `ReadableStream` (`api/stream.ts`); the stream is only a view of the run,
 * which the server finishes whether anyone listens or not. So a run found
 * `running` on the server (after a reload, from another tab, or after the
 * stream broke) is followed by polling the conversation every two seconds. A
 * run that proposed a change is paused on the server until the person decides:
 * the card is in the conversation, so it is live after a reload or on another
 * device.
 */
export const useConversation = ({
  conversationId,
  origin,
  onCreated,
  onMissing,
  onWorkingChange,
  onWaitingChange,
  onProposal,
}: UseConversationOptions) => {
  const queryClient = useQueryClient();
  const [live, setLive] = useState<LiveRun | null>(null);
  /** The card the person is deciding now, and what the server answered when it refused a decision. */
  const [deciding, setDeciding] = useState<string | undefined>();
  const [decisionErrors, setDecisionErrors] = useState<Record<string, ErrorKey>>({});
  /** The card that arrived while this tab listened; the first one to take the focus. */
  const [freshCard, setFreshCard] = useState<string | undefined>();
  const abort = useRef<AbortController | null>(null);
  /** The id of a draft this hook created, so the switch to it does not reset the run being streamed. */
  const created = useRef<string | null>(null);
  const liveRef = useRef<LiveRun | null>(null);
  useLayoutEffect(() => {
    liveRef.current = live;
  });

  const detail = useQuery({
    ...conversationQueryOptions(conversationId ?? ""),
    enabled: conversationId !== null,
    refetchInterval: (query) => {
      if (liveRef.current?.phase === "working") return false;
      const data = query.state.data;
      return data?.activeRun?.state === "running" || data?.conversation.running ? POLL_MS : false;
    },
  });

  // Leaving for another conversation drops the stream we listened to; its run goes on.
  useEffect(() => {
    if (conversationId !== null && conversationId === created.current) {
      created.current = null;
      return;
    }
    abort.current?.abort();
    abort.current = null;
    setLive(null);
    setDeciding(undefined);
    setDecisionErrors({});
    setFreshCard(undefined);
  }, [conversationId]);

  useEffect(() => () => abort.current?.abort(), []);

  const missing = (detail.error as { status?: number } | null)?.status === 404;
  useEffect(() => {
    if (missing) onMissing?.();
  }, [missing, onMissing]);

  const serverRunning = detail.data?.activeRun?.state === "running" || detail.data?.conversation.running === true;
  const working = live?.phase === "working" || serverRunning;

  // The proposed changes: the conversation's, with what the stream has said since laid over them.
  const cards = useMemo(() => {
    const byId: Record<string, AssistantPendingAction> = {};
    for (const card of detail.data?.pendingActions ?? []) byId[card.id] = card;
    for (const card of Object.values(live?.cards ?? {})) byId[card.id] = card;
    return byId;
  }, [detail.data?.pendingActions, live?.cards]);
  const waiting = Object.values(cards).some((card) => card.status === "pending");

  const wasWorking = useRef(false);
  useEffect(() => {
    if (working !== wasWorking.current) {
      wasWorking.current = working;
      onWorkingChange?.(working);
      // A run another tab started has ended: the list shows its new title and order.
      if (!working) void queryClient.invalidateQueries({ queryKey: conversationsQueryKey });
    }
  }, [working, onWorkingChange, queryClient]);

  const wasWaiting = useRef(false);
  useEffect(() => {
    if (waiting !== wasWaiting.current) {
      wasWaiting.current = waiting;
      onWaitingChange?.(waiting);
    }
  }, [waiting, onWaitingChange]);

  const messages = useMemo(() => {
    const persisted = detail.data?.messages ?? [];
    const known = new Set(persisted.map((message) => message.id));
    // A message the stream re-announced (a tool result settled by a decision) replaces the persisted copy.
    const announced = new Map((live?.messages ?? []).map((message) => [message.id, message]));
    return [
      ...persisted.map((message) => announced.get(message.id) ?? message),
      ...(live?.messages ?? []).filter((message) => !known.has(message.id)),
    ];
  }, [detail.data, live?.messages]);

  const hasUserMessageFromStream = live?.messages.some((message) => message.role === "user") === true;
  const pendingUser = live?.pendingUser && !hasUserMessageFromStream ? live.pendingUser : undefined;

  const finish = useCallback(
    async (id: string | undefined) => {
      if (id) await queryClient.invalidateQueries({ queryKey: conversationQueryKey(id) });
      void queryClient.invalidateQueries({ queryKey: conversationsQueryKey });
    },
    [queryClient],
  );

  /** Follows one run's stream into the live state; answers how it ended. */
  const follow = useCallback(
    async (conversationKey: string, source: AsyncGenerator<StreamEvent, void, void>) => {
      let runId: string | undefined;
      let ended: "done" | "stopped" | "awaiting_confirmation" | undefined;
      let failed: ErrorKey | undefined;
      const update = (change: (current: LiveRun) => LiveRun) =>
        setLive((current) => (current ? change(current) : current));
      for await (const event of source) {
        switch (event.event) {
          case "run":
            runId = event.data.runId;
            update((current) => ({ ...current, runId }));
            break;
          case "message": {
            const message = event.data;
            update((current) => ({
              ...current,
              messages: current.messages.some((one) => one.id === message.id)
                ? current.messages.map((one) => (one.id === message.id ? message : one))
                : [...current.messages, message],
              partial: message.role === "assistant" ? "" : current.partial,
            }));
            break;
          }
          case "delta":
            update((current) => ({ ...current, partial: current.partial + event.data.text }));
            break;
          case "tool":
            update((current) => ({ ...current, tool: event.data.state === "started" ? event.data.label : undefined }));
            break;
          case "confirmation": {
            const card = event.data;
            update((current) => ({ ...current, cards: { ...current.cards, [card.id]: card } }));
            if (card.status === "pending") {
              setFreshCard(card.id);
              onProposal?.(card);
            }
            // The change was made: the screen the person is on must show it.
            if (card.status === "confirmed" && card.outcome?.ok === true) invalidateAfterAssistantWrites(queryClient);
            break;
          }
          case "done":
            ended = event.data.state;
            update((current) => ({
              ...current,
              phase: "finished",
              finalState: event.data.state,
              tool: undefined,
              partial: "",
            }));
            break;
          case "error":
            failed = errorKeyForCode(event.data.code);
            update((current) => ({ ...current, phase: "finished", tool: undefined, partial: "", error: failed }));
            break;
        }
      }
      if (ended || failed) {
        // What the stream persisted is on the server now; the conversation is the truth from here.
        await finish(conversationKey);
        setLive((current) => current && { ...current, messages: [], cards: {}, pendingUser: undefined });
      } else {
        // The connection ended without a verdict: the run may still be going. Polling takes over.
        setLive(null);
        await queryClient.invalidateQueries({ queryKey: conversationQueryKey(conversationKey) });
      }
      return runId;
    },
    [finish, onProposal, queryClient],
  );

  const send = useCallback(
    async (text: string, context?: AssistantMessageContext) => {
      if (liveRef.current?.phase === "working" || serverRunning || waiting) return;
      const controller = new AbortController();
      abort.current?.abort();
      abort.current = controller;
      setLive({ pendingUser: { text, context }, messages: [], cards: {}, partial: "", phase: "working" });

      let id = conversationId ?? undefined;
      try {
        if (!id) {
          const conversation = await createConversation(origin);
          id = conversation.id;
          created.current = id;
          onCreated(id);
          void queryClient.invalidateQueries({ queryKey: conversationsQueryKey });
        }
        const conversationKey = id;
        setLive((current) => current && { ...current, conversationId: conversationKey });
        await follow(conversationKey, sendMessage(id, { text, context }, controller.signal));
      } catch (error) {
        if (controller.signal.aborted) return;
        const key = errorKeyFor(error);
        setLive((current) => ({
          messages: current?.messages ?? [],
          cards: current?.cards ?? {},
          partial: "",
          phase: "finished",
          pendingUser: key === "errorRunInProgress" ? undefined : current?.pendingUser,
          error: key,
        }));
        if (id) {
          await queryClient.invalidateQueries({ queryKey: conversationQueryKey(id) });
          if (key === "errorRunInProgress")
            setLive((current) => (current ? { ...current, pendingUser: undefined } : current));
        }
      }
    },
    [conversationId, serverRunning, waiting, origin, onCreated, queryClient, follow],
  );

  /** The person's decision on a proposed change: the run goes on and streams as a message's does. */
  const decide = useCallback(
    async (card: AssistantPendingAction, decision: "confirm" | "cancel") => {
      if (deciding || !conversationId) return;
      const controller = new AbortController();
      abort.current?.abort();
      abort.current = controller;
      setDeciding(card.id);
      setFreshCard(undefined);
      setDecisionErrors((current) => Object.fromEntries(Object.entries(current).filter(([id]) => id !== card.id)));
      setLive({ conversationId, messages: [], cards: {}, partial: "", phase: "working" });
      try {
        await follow(conversationId, decideAction(card.id, decision, controller.signal));
      } catch (error) {
        if (controller.signal.aborted) return;
        const key = errorKeyFor(error);
        // The server refused before anything ran: the card says why, and the conversation shows what is true.
        setDecisionErrors((current) => ({ ...current, [card.id]: key }));
        setLive(null);
        await queryClient.invalidateQueries({ queryKey: conversationQueryKey(conversationId) });
      } finally {
        setDeciding(undefined);
      }
    },
    [deciding, conversationId, follow, queryClient],
  );

  const stop = useCallback(async () => {
    const runId = liveRef.current?.runId ?? detail.data?.activeRun?.id;
    if (!runId) return;
    try {
      await stopRun(runId);
      if (waiting && conversationId)
        await queryClient.invalidateQueries({ queryKey: conversationQueryKey(conversationId) });
    } catch {
      setLive((current) => current && { ...current, error: "errorGeneric" });
    }
  }, [detail.data?.activeRun?.id, waiting, conversationId, queryClient]);

  /** Dismisses an error line once the person has read it. */
  const clearError = useCallback(() => setLive((current) => (current?.error ? null : current)), []);

  return {
    detail,
    messages,
    pendingUser,
    partial: live?.partial ?? "",
    tool: live?.tool,
    working,
    waiting,
    cards,
    freshCard,
    deciding,
    decisionErrors,
    stopped: live?.finalState === "stopped",
    error: live?.error,
    send,
    confirm: (card: AssistantPendingAction) => decide(card, "confirm"),
    cancel: (card: AssistantPendingAction) => decide(card, "cancel"),
    stop,
    clearError,
  };
};

export type ConversationController = ReturnType<typeof useConversation>;
