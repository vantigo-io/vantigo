import { Anchor, Badge, type MantineSize } from "@mantine/core";
import { useI18n, useShellLink } from "@vantigo/frontend-shell";
import "../i18n";
import { type InvoicedBy, type TimeEntryStatus, timeEntryStatusColor, timeEntryStatusLabel } from "../lib/status";

export interface EntryStatusBadgeProps {
  status: TimeEntryStatus;
  size?: MantineSize;
  /** The invoice the Invoices module invoiced the entry on, when it did: the badge names it. */
  invoicedBy?: InvoicedBy | null;
  /**
   * Where one invoice lives in the host's routes. The host passes it when the
   * caller may open invoices; the badge naming an invoice is then a link to
   * it. Without it, words alone — this package knows no route of Invoices.
   */
  invoiceHref?: (invoiceId: number) => string;
}

/**
 * A time entry's status, in the one colour and wording every time view uses
 * for it — an entry the Invoices module invoiced says "Invoiced by invoice n"
 * (invoices work design D18), a link where the host says invoices live.
 */
export const EntryStatusBadge = ({ status, size, invoicedBy, invoiceHref }: EntryStatusBadgeProps) => {
  const { t } = useI18n("time");
  const Link = useShellLink();
  const label = timeEntryStatusLabel(status, invoicedBy ?? undefined);
  const badge = (
    <Badge variant="light" color={timeEntryStatusColor(status)} size={size}>
      {t(label.key, label.values)}
    </Badge>
  );
  if (status !== "invoiced" || !invoicedBy || !invoiceHref) return badge;
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
