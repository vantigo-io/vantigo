import { createFileRoute } from "@tanstack/react-router";

/** `/assistant` itself: a fresh conversation. The layout route draws the page; this only owns the address. */
export const Route = createFileRoute("/assistant/")({
  component: () => null,
});
