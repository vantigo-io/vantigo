import { Badge } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { invoicesCatalog } from "../i18n";

/** The colour each EHF state is shown in (EHF and KID design D10); a state a newer server added is grey. */
const colours: Record<string, string> = {
  not_sent: "gray",
  queued: "blue",
  submitted: "blue",
  delivered: "green",
  failed: "red",
  unconfirmed: "orange",
  cancelled: "gray",
};

/**
 * A document's or a transmission's EHF state as a badge: the short words in
 * a list (`ehfBadge.*`), the honest long ones on the E-invoice card
 * (`ehfStatus.*`). The server judges the state; this only words and colours it.
 */
export const EhfBadge = ({ status, long = false, testId }: { status: string; long?: boolean; testId?: string }) => {
  const { t } = useI18n("invoices");
  const key = `${long ? "ehfStatus" : "ehfBadge"}.${status}`;
  return (
    <Badge variant="light" color={colours[status] ?? "gray"} data-testid={testId} data-status={status}>
      {key in invoicesCatalog.en ? t(key) : status}
    </Badge>
  );
};
