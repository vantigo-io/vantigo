import { createFileRoute } from "@tanstack/react-router";
import { PostingRulesPage } from "@vantigo/accounting-ui/pages/posting-rules";

/** The posting rules (accounting design A14). */
export const Route = createFileRoute("/accounting/posting-rules")({
  component: PostingRulesPage,
});
