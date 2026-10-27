import { Badge, Group } from "@mantine/core";
import { BILLING_ITEM_STATUSES, type BillingRunCounts } from "../api/billing-runs";
import "../i18n";
import { itemStatusColour, itemStatusWords } from "../lib/billing-runs";
import { useInvoiceFormat } from "../lib/format";

/** A billing run's items counted by status, as badges: those above nought, or all six with `all`. */
export const BillingRunCountBadges = ({ counts, all = false }: { counts: BillingRunCounts; all?: boolean }) => {
  const { t } = useInvoiceFormat();
  return (
    <Group gap={4} data-testid="billing-run-counts">
      {BILLING_ITEM_STATUSES.filter((status) => all || counts[status] > 0).map((status) => (
        <Badge key={status} variant="light" color={itemStatusColour[status]}>
          {t("billingRun.count", { status: itemStatusWords(status, t), count: counts[status] })}
        </Badge>
      ))}
    </Group>
  );
};
