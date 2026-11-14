import { createFileRoute } from "@tanstack/react-router";
import { VatTermsPage } from "@vantigo/accounting-ui/pages/vat";

/** The VAT terms (accounting design A22). */
export const Route = createFileRoute("/accounting/vat/")({
  component: VatTermsPage,
});
