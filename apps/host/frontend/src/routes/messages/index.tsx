import { createFileRoute } from "@tanstack/react-router";
import { MessagesPage } from "@vantigo/communications-ui/pages/messages.index";
export const Route = createFileRoute("/messages/")({
  validateSearch: (s: Record<string, unknown>) => ({
    page: Math.max(1, Number(s.page) || 1),
    archived: s.archived === true || s.archived === "true" || undefined,
  }),
  component: MessagesPage,
});
