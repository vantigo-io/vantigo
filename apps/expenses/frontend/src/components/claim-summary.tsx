import { Badge, Group, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ClaimListItem, ClaimSummary } from "../api/claims";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import type { ExpenseStatus } from "../lib/status";
import { CurrencyTotals } from "./currency-totals";
import { ExpenseStatusBadge } from "./expense-status-badge";

/**
 * A trip as every list shows it. Both shapes the server answers a claim in
 * outside its own read — the list's row and the queue's unit — carry this
 * much, so one component draws a trip in "My expenses", in the approval queue
 * and in the payroll list.
 */
export type ClaimHeadline = Pick<ClaimSummary, "id" | "purpose" | "departureAt" | "returnAt" | "lineCount" | "totals"> &
  Partial<Pick<ClaimSummary, "destination" | "project">> & { status?: ExpenseStatus };

export interface ClaimSummaryLineProps {
  claim: ClaimHeadline | ClaimListItem;
  /** The installation's business time zone, from `/meta`. The trip's days are days in it. */
  timeZone: string;
}

/** The trip's dates, written in the installation's zone and never the browser's. */
export const ClaimDates = ({ claim, timeZone }: ClaimSummaryLineProps) => {
  const format = useExpenseFormat();
  return (
    <Text size="sm">
      {`${format.zonedDateTime(claim.departureAt, timeZone)} – ${format.zonedDateTime(claim.returnAt, timeZone)}`}
    </Text>
  );
};

/**
 * One travel claim at a glance: what it was for, where it went, when, what
 * state it is in, how many expenses it holds and what it comes to per
 * currency. Nothing is summed across currencies (design §4).
 */
export const ClaimSummaryLine = ({ claim, timeZone }: ClaimSummaryLineProps) => {
  const { t } = useI18n("expenses");
  return (
    <Stack gap={4}>
      <Group gap="xs" wrap="wrap">
        <Text size="sm" fw={500}>
          {claim.purpose}
        </Text>
        {claim.status && <ExpenseStatusBadge status={claim.status} />}
        {claim.project && <Badge variant="default">{claim.project.code}</Badge>}
      </Group>
      {claim.destination && (
        <Text size="sm" c="dimmed">
          {claim.destination}
        </Text>
      )}
      <ClaimDates claim={claim} timeZone={timeZone} />
      <Text size="xs" c="dimmed">
        {claim.lineCount === 1 ? t("oneExpense") : t("countOfExpenses", { count: claim.lineCount })}
      </Text>
      <CurrencyTotals totals={claim.totals} />
    </Stack>
  );
};
