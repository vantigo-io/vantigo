import { createFileRoute } from "@tanstack/react-router";
import { InboxPage } from "@vantigo/communications-ui/pages/inbox";
export const Route = createFileRoute("/communications/inbox")({
  validateSearch: (s: Record<string, unknown>) => ({
    conversationId: typeof s.conversationId === "string" ? s.conversationId : undefined,
    status: s.status === "open" || s.status === "closed" || s.status === "archived" ? s.status : undefined,
    customerId: Number.isInteger(Number(s.customerId)) && Number(s.customerId) > 0 ? Number(s.customerId) : undefined,
    tagId: typeof s.tagId === "string" ? s.tagId : undefined,
    unreadOnly: s.unreadOnly === true || s.unreadOnly === "true" || undefined,
  }),
  component: InboxPage,
});
