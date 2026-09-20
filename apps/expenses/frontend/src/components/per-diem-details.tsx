import { Group, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import { mealDeductions, mealLabelKey, type PerDiem, perDiemTypeLabelKey } from "../lib/per-diem";

export interface PerDiemDetailsProps {
  perDiem: PerDiem;
  /** The line's own currency, which a trip abroad takes from the claim. */
  currency: string;
}

/**
 * The figures a per diem day was priced from, read only: the day rate, and
 * each meal's deduction as the rate table priced it *on that day*.
 *
 * A percentage the table never held is **absent, not zero** — `0 %` would be
 * a figure the server never said — so a meal with none is written as "not
 * priced" rather than as a deduction of nothing. The amount itself is the
 * line's `grossAmount` and is never worked out here.
 */
export const PerDiemDetails = ({ perDiem, currency }: PerDiemDetailsProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  return (
    <Stack gap={2}>
      <Text size="sm">{t(perDiemTypeLabelKey(perDiem.type))}</Text>
      <Text size="xs" c="dimmed">
        {`${t("perDiemDayRate")}: ${format.money(perDiem.dayRate, currency)}`}
      </Text>
      <Group gap="xs">
        {mealDeductions(perDiem).map((deduction) => (
          <Text key={deduction.meal} size="xs" c={deduction.covered ? undefined : "dimmed"}>
            {deduction.percent === undefined
              ? t("mealNotPriced", { meal: t(mealLabelKey(deduction.meal)) })
              : deduction.covered
                ? t("mealCoveredDeducts", {
                    meal: t(mealLabelKey(deduction.meal)),
                    percent: format.number(deduction.percent, 0),
                  })
                : t("mealNotCovered", { meal: t(mealLabelKey(deduction.meal)) })}
          </Text>
        ))}
      </Group>
    </Stack>
  );
};
