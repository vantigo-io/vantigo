import { createFileRoute } from "@tanstack/react-router";
import { WishesPage } from "@vantigo/planning-ui/pages/wishes";

/**
 * The wishes (planning design S21): what the people have asked of their next
 * assignment, `planning:manage`'s. The module access guard gates the path by
 * the sidebar entry's permission, as it does Planning settings.
 */
export const Route = createFileRoute("/planning/wishes")({
  component: WishesPage,
});
