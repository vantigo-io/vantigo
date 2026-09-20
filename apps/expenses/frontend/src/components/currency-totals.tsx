import { Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ExpenseCurrencyTotal } from "../api/approvals";
import "../i18n";
import { useExpenseFormat } from "../lib/format";

export interface CurrencyTotalsProps {
  totals: ExpenseCurrencyTotal[];
  /** Left out, both figures are written; a narrow column asks for the owed one only. */
  owedOnly?: boolean;
}

/**
 * A group's figures, one line per currency. Nothing is ever converted
 * (design §4), so somebody owed money in two currencies gets two lines and
 * never a sum that would be in neither.
 */
export const CurrencyTotals = ({ totals, owedOnly = false }: CurrencyTotalsProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  if (totals.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        {t("nothingOwed")}
      </Text>
    );
  }
  return (
    <Stack gap={0}>
      {totals.map((total) => (
        <Text key={total.currency} size="sm">
          {owedOnly
            ? `${t("owedToEmployee")}: ${format.money(total.owedToEmployee, total.currency)}`
            : `${t("grossAmount")}: ${format.money(total.gross, total.currency)} · ${t("owedToEmployee")}: ${format.money(total.owedToEmployee, total.currency)}`}
        </Text>
      ))}
    </Stack>
  );
};
