import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  conversationQueryKey,
  conversationQueryOptions,
  conversationsQueryKey,
  createConversation,
  stopRun,
} from "../api/assistant";
import { StreamRefusedError, sendMessage } from "../api/stream";
import type { AssistantMessage, AssistantMessageContext } from "../api/types";
import { invalidateAfterAssistantWrites } from "../lib/invalidate";

/** The run this tab is listening to: what the stream has said so far. */
interface LiveRun {
  /** The conversation the run belongs to; undefined until a draft has been created. */
  conversationId?: string;
  runId?: string;
  /** The person's message, shown at once and replaced by the persisted one when it arrives. */
  pendingUser?: { text: string; context?: AssistantMessageContext };
  /** Messages the stream has persisted, in order. */
  messages: AssistantMessage[];
  /** The assistant's current text, not yet a message. */
  partial: string;
  tool?: string;
  phase: "working" | "finished";
  finalState?: "done" | "stopped";
  /** A key of the catalog's `error*` family. */
  error?: "errorGeneric" | "errorProvider" | "errorUnavailable" | "errorRunInProgress";
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
}

const POLL_MS = 2000;

const errorKeyFor = (error: unknown): NonNullable<LiveRun["error"]> => {
  if (error instanceof StreamRefusedError) {
    if (error.code === "run_in_progress") return "errorRunInProgress";
    if (error.code === "assistant_unavailable" || error.status === 403) return "errorUnavailable";
    if (error.code === "ai_unavailable" || error.status === 503) return "errorProvider";
  }
  return "errorGeneric";
};

const errorKeyForCode = (code: string): NonNullable<LiveRun["error"]> =>
  code === "assistant_unavailable"
    ? "errorUnavailable"
    : code === "ai_unavailable" || code === "provider_error" || code === "provider_unavailable"
      ? "errorProvider"
      : code === "run_in_progress"
        ? "errorRunInProgress"
        : "errorGeneric";

/**
 * One conversation's data and its run, shared by the panel and the app page.
 *
 * Sending is a `fetch` over a `ReadableStream` (`api/stream.ts`); the stream
 * is only a view of the run, which the server finishes whether anyone listens
 * or not. So a run found `running` on the server (after a reload, from
 * another tab, or after the stream broke) is followed by polling the
 * conversation every two seconds.
 */
export const useConversation = ({
  conversationId,
  origin,
  onCreated,
  onMissing,
  onWorkingChange,
}: UseConversationOptions) => {
  const queryClient = useQueryClient();
  const [live, setLive] = useState<LiveRun | null>(null);
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
  }, [conversationId]);

  useEffect(() => () => abort.current?.abort(), []);

  const missing = (detail.error as { status?: number } | null)?.status === 404;
  useEffect(() => {
    if (missing) onMissing?.();
  }, [missing, onMissing]);

  const serverRunning = detail.data?.activeRun?.state === "running" || detail.data?.conversation.running === true;
  const working = live?.phase === "working" || serverRunning;

  const wasWorking = useRef(false);
  useEffect(() => {
    if (working !== wasWorking.current) {
      wasWorking.current = working;
      onWorkingChange?.(working);
      // A run another tab started has ended: the list shows its new title and order.
      if (!working) void queryClient.invalidateQueries({ queryKey: conversationsQueryKey });
    }
  }, [working, onWorkingChange, queryClient]);

  const messages = useMemo(() => {
    const persisted = detail.data?.messages ?? [];
    const known = new Set(persisted.map((message) => message.id));
    return [...persisted, ...(live?.messages ?? []).filter((message) => !known.has(message.id))];
  }, [detail.data, live?.messages]);

  const hasUserMessageFromStream = live?.messages.some((message) => message.role === "user") === true;
  const pendingUser = live?.pendingUser && !hasUserMessageFromStream ? live.pendingUser : undefined;

  const finish = useCallback(
    async (id: string | undefined, runId: string | undefined, state: "done" | "stopped" | "failed") => {
      if (id) {
        await queryClient.invalidateQueries({ queryKey: conversationQueryKey(id) });
        invalidateAfterAssistantWrites(queryClient, { conversationId: id, runId, state });
      }
      void queryClient.invalidateQueries({ queryKey: conversationsQueryKey });
    },
    [queryClient],
  );

  const send = useCallback(
    async (text: string, context?: AssistantMessageContext) => {
      if (liveRef.current?.phase === "working" || serverRunning) return;
      const controller = new AbortController();
      abort.current?.abort();
      abort.current = controller;
      setLive({ pendingUser: { text, context }, messages: [], partial: "", phase: "working" });

      let id = conversationId ?? undefined;
      let runId: string | undefined;
      let ended: "done" | "stopped" | undefined;
      let failed: LiveRun["error"];
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
        for await (const event of sendMessage(id, { text, context }, controller.signal)) {
          switch (event.event) {
            case "run":
              runId = event.data.runId;
              setLive((current) => current && { ...current, runId });
              break;
            case "message": {
              const message = event.data;
              setLive(
                (current) =>
                  current && {
                    ...current,
                    messages: current.messages.some((one) => one.id === message.id)
                      ? current.messages
                      : [...current.messages, message],
                    partial: message.role === "assistant" ? "" : current.partial,
                  },
              );
              break;
            }
            case "delta":
              setLive((current) => current && { ...current, partial: current.partial + event.data.text });
              break;
            case "tool":
              setLive(
                (current) =>
                  current && { ...current, tool: event.data.state === "started" ? event.data.label : undefined },
              );
              if (event.data.state === "done")
                invalidateAfterAssistantWrites(
                  queryClient,
                  { conversationId: conversationKey, runId, state: "done" },
                  event.data.name,
                );
              break;
            case "done":
              ended = event.data.state;
              setLive(
                (current) =>
                  current && {
                    ...current,
                    phase: "finished",
                    finalState: event.data.state,
                    tool: undefined,
                    partial: "",
                  },
              );
              break;
            case "error":
              failed = errorKeyForCode(event.data.code);
              setLive(
                (current) => current && { ...current, phase: "finished", tool: undefined, partial: "", error: failed },
              );
              break;
          }
        }
        if (ended || failed) {
          // What the stream persisted is on the server now; the conversation is the truth from here.
          await finish(conversationKey, runId, failed ? "failed" : (ended ?? "done"));
          setLive((current) => current && { ...current, messages: [], pendingUser: undefined });
        } else {
          // The connection ended without a verdict: the run may still be going. Polling takes over.
          setLive(null);
          await queryClient.invalidateQueries({ queryKey: conversationQueryKey(conversationKey) });
        }
      } catch (error) {
        if (controller.signal.aborted) return;
        const key = errorKeyFor(error);
        setLive((current) => ({
          messages: current?.messages ?? [],
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
    [conversationId, serverRunning, origin, onCreated, queryClient, finish],
  );

  const stop = useCallback(async () => {
    const runId = liveRef.current?.runId ?? detail.data?.activeRun?.id;
    if (!runId) return;
    try {
      await stopRun(runId);
    } catch {
      setLive((current) => current && { ...current, error: "errorGeneric" });
    }
  }, [detail.data?.activeRun?.id]);

  /** Dismisses an error line once the person has read it. */
  const clearError = useCallback(() => setLive((current) => (current?.error ? null : current)), []);

  return {
    detail,
    messages,
    pendingUser,
    partial: live?.partial ?? "",
    tool: live?.tool,
    working,
    stopped: live?.finalState === "stopped",
    error: live?.error,
    send,
    stop,
    clearError,
  };
};

export type ConversationController = ReturnType<typeof useConversation>;
