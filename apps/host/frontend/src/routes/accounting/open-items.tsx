import { createFileRoute } from "@tanstack/react-router";
import { OpenItemsPage } from "@vantigo/accounting-ui/pages/open-items";

/** The open items and their settlements (accounting design A10, R5). */
export const Route = createFileRoute("/accounting/open-items")({
  component: OpenItemsPage,
});
