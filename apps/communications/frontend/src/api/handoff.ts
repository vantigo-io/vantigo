import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/** Whether an agent handed a conversation to the people (virtual employees design V13), as the Agents contract answers it. */
export type ConversationHandOff = {
  handedOff: boolean;
  at: string | null;
  reason: string | null;
  roleId: string | null;
};

const path = (conversationId: string) => `/api/v1/agents/conversations/${encodeURIComponent(conversationId)}/hand-off`;

export const handOffQueryKey = (conversationId: string) => ["communications-hand-off", conversationId] as const;

/** The marker, or none: an installation without the Agents module answers 404, which reads as not handed off. */
export const handOffQueryOptions = (conversationId: string, enabled: boolean) =>
  queryOptions({
    queryKey: handOffQueryKey(conversationId),
    queryFn: async ({ signal }): Promise<ConversationHandOff> => {
      try {
        return await request<ConversationHandOff>(path(conversationId), { signal });
      } catch (error) {
        if ((error as { status?: number }).status === 404)
          return { handedOff: false, at: null, reason: null, roleId: null };
        throw error;
      }
    },
    enabled,
    retry: false,
  });

/** Give back to the agent: the marker is cleared, so the agent answers the next message. */
export const giveBack = (conversationId: string) => request<void>(path(conversationId), { method: "DELETE" });
