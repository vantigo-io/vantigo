import { createFileRoute } from "@tanstack/react-router";
import { validateVatPostingsSearch } from "@vantigo/accounting-ui/lib/search";
import { VatPostingsPage } from "@vantigo/accounting-ui/pages/vat";

/** The postings behind a VAT figure: a period, and a tax code and a part, or the unreconciled ones (accounting design A21). */
export const Route = createFileRoute("/accounting/vat/postings")({
  validateSearch: validateVatPostingsSearch,
  component: VatPostingsPage,
});
