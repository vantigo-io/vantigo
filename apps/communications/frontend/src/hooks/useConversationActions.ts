import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  addTag,
  type ConversationStatus,
  markConversationRead,
  removeTag,
  suggestConversationCustomerWithAi,
  updateConversation,
} from "../api/conversations";

/**
 * The mutations on one conversation and the invalidations that follow them: the
 * inbox page and the side panel's Conversations panel share it, so both behave
 * the same (Messenger design N25). Every change refreshes the lists, the
 * conversation and the badge's counts.
 */
export const useConversationActions = (conversationId: string) => {
  const client = useQueryClient();
  const invalidate = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: ["conversations"] }),
      client.invalidateQueries({ queryKey: ["conversation", conversationId] }),
      client.invalidateQueries({ queryKey: ["inbox-counts"] }),
    ]);
  const markRead = useMutation({ mutationFn: () => markConversationRead(conversationId), onSuccess: invalidate });
  const workflow = useMutation({
    mutationFn: (body: { status?: ConversationStatus; assignedUserId?: string | null; customerId?: number | null }) =>
      updateConversation(conversationId, body),
    onSuccess: invalidate,
  });
  const tagMutation = useMutation({
    mutationFn: ({ tagId, remove }: { tagId: string; remove?: boolean }) =>
      remove ? removeTag(conversationId, tagId) : addTag(conversationId, tagId),
    onSuccess: invalidate,
  });
  const suggest = useMutation({
    mutationFn: () => suggestConversationCustomerWithAi(conversationId),
    onSuccess: invalidate,
  });
  return { invalidate, markRead, workflow, tagMutation, suggest };
};
