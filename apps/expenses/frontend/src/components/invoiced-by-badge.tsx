import { Anchor, Badge } from "@mantine/core";
import { useI18n, useShellLink } from "@vantigo/frontend-shell";
import type { Expense } from "../api/entries";
import "../i18n";

export interface InvoicedByBadgeProps {
  /** The invoice the Invoices module issued the line on (invoices work design D1). */
  invoicedBy: NonNullable<NonNullable<NonNullable<Expense["billing"]>["invoice"]>["invoicedBy"]>;
  /**
   * Where one invoice lives in the host's routes. The host hands it over when
   * the caller may open invoices; without it the badge is words alone — this
   * package knows no route of the Invoices app.
   */
  invoiceHref?: (invoiceId: number) => string;
}

/**
 * "Invoiced by invoice n" (invoices work design D18): a line the Invoices
 * module issued, which only a credit note returning its line takes back — so
 * it carries no manual undo. A link to the invoice where the host passes one.
 */
export const InvoicedByBadge = ({ invoicedBy, invoiceHref }: InvoicedByBadgeProps) => {
  const { t } = useI18n("expenses");
  const Link = useShellLink();
  const badge = (
    <Badge variant="light" color="violet" data-testid="invoiced-by-invoice">
      {t("invoicedByInvoice", { number: invoicedBy.number })}
    </Badge>
  );
  if (!invoiceHref) return badge;
  const href = invoiceHref(invoicedBy.invoiceId);
  return Link ? (
    <Anchor underline="never" renderRoot={(props) => <Link to={href} {...props} />}>
      {badge}
    </Anchor>
  ) : (
    <Anchor underline="never" href={href}>
      {badge}
    </Anchor>
  );
};
