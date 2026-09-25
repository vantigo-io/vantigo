import { Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { TimeEntryBilling } from "../api/entries";
import "../i18n";

/**
 * "900 × 150 % = 1 350" (work types design D5): the base rate the chain
 * resolved, the work type's bill multiplier, and what an hour bills at. It is
 * drawn only from a billing block that carries all three — the block is there
 * exactly when the caller may see the rate, and the multiplier exactly when a
 * work type was picked — so an entry of ordinary hours shows nothing new.
 */
export const RateLine = ({ billing }: { billing?: TimeEntryBilling | null }) => {
  const { t, formatters } = useI18n("time");
  if (billing?.billRate == null || billing.multiplierPercent == null || billing.effectiveRate == null) return null;
  return (
    <Text size="xs" c="dimmed" data-testid="rate-line">
      {t("rateTimesMultiplier", {
        rate: formatters.formatNumber(billing.billRate),
        percent: formatters.formatNumber(billing.multiplierPercent),
        effective: formatters.formatNumber(billing.effectiveRate),
      })}
    </Text>
  );
};
