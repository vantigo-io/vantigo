import type { AssistantConversation } from "../api/types";

export type ConversationGroupKey = "pinned" | "today" | "yesterday" | "week" | "older";

export interface ConversationGroup {
  key: ConversationGroupKey;
  conversations: AssistantConversation[];
}

const startOfDay = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();

/**
 * Splits the list, which the server already ordered (pinned first, then by
 * last activity), into the sidebar's groups: Pinned, Today, Yesterday, Last
 * 7 days and Older. Empty groups are left out and the order is kept.
 */
export const groupConversations = (conversations: readonly AssistantConversation[], now = new Date()) => {
  const today = startOfDay(now);
  const day = 24 * 60 * 60 * 1000;
  const buckets: Record<ConversationGroupKey, AssistantConversation[]> = {
    pinned: [],
    today: [],
    yesterday: [],
    week: [],
    older: [],
  };
  for (const conversation of conversations) {
    if (conversation.pinned) {
      buckets.pinned.push(conversation);
      continue;
    }
    const at = startOfDay(new Date(conversation.lastActivityAt));
    if (at >= today) buckets.today.push(conversation);
    else if (at >= today - day) buckets.yesterday.push(conversation);
    else if (at >= today - 7 * day) buckets.week.push(conversation);
    else buckets.older.push(conversation);
  }
  return (["pinned", "today", "yesterday", "week", "older"] as const)
    .filter((key) => buckets[key].length > 0)
    .map((key): ConversationGroup => ({ key, conversations: buckets[key] }));
};
