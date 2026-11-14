import { createFileRoute } from "@tanstack/react-router";
import { NeedsAttentionPage } from "@vantigo/accounting-ui/pages/needs-attention";

/** The sales facts the books could not post (accounting design A15). */
export const Route = createFileRoute("/accounting/needs-attention")({
  component: NeedsAttentionPage,
});
