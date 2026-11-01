import { createFileRoute } from "@tanstack/react-router";

/** `/assistant/$conversationId`: a conversation to resume. The layout route draws the page and reads the id. */
export const Route = createFileRoute("/assistant/$conversationId")({
  component: () => null,
});
