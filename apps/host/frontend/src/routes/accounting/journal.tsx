import { createFileRoute } from "@tanstack/react-router";
import { validateJournalSearch } from "@vantigo/accounting-ui/lib/search";
import { JournalEntryPage } from "@vantigo/accounting-ui/pages/journal";

/** A new manual journal, from a copied voucher when `copy` names one (accounting design A6, R13). */
export const Route = createFileRoute("/accounting/journal")({
  validateSearch: validateJournalSearch,
  component: JournalEntryPage,
});
