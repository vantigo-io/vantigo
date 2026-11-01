import type { QueryClient } from "@tanstack/react-query";

/** What a finished run tells the screen, for the seam below. */
export interface AssistantRunOutcome {
  conversationId: string;
  runId?: string;
  state: "done" | "stopped" | "failed";
}

/**
 * THE SEAM FOR DELIVERY B. After an action succeeds, the panel invalidates
 * that module's TanStack queries so the screen the person is on shows the
 * change (design S12). Delivery A has no writes (the assistant reads only), so
 * this does nothing; it is called, once, when a run finishes and again for
 * each `tool` event that reports `done`, so B has one place to map an
 * operation to the query keys it changes without touching the stream code.
 *
 * `operation` is the tool's `name` as the stream reports it.
 */
export const invalidateAfterAssistantWrites = (
  _queryClient: QueryClient,
  _outcome: AssistantRunOutcome,
  _operation?: string,
): void => {
  // Delivery A: the assistant only reads, so nothing changes under the screen.
  void [_queryClient, _outcome, _operation];
};
