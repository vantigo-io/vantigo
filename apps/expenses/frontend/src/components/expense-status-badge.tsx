import { Badge, type MantineSize } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { type ExpenseStatus, expenseStatusColor, expenseStatusLabelKey } from "../lib/status";

export interface ExpenseStatusBadgeProps {
  status: ExpenseStatus;
  size?: MantineSize;
}

/**
 * An expense's status, in the one colour and wording every expense view uses
 * for it. The word is the answer and the colour only agrees with it: nothing
 * in this package is told by colour alone.
 */
export const ExpenseStatusBadge = ({ status, size }: ExpenseStatusBadgeProps) => {
  const { t } = useI18n("expenses");
  return (
    <Badge variant="light" color={expenseStatusColor(status)} size={size}>
      {t(expenseStatusLabelKey(status))}
    </Badge>
  );
};
