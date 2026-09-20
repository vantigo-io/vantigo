import { Box, SimpleGrid, Stack, Text } from "@mantine/core";
import { KpiCard, useI18n } from "@vantigo/frontend-shell";
import type { ExpenseStats } from "../api/stats";
import "../i18n";

export interface StatusStripProps {
  stats?: ExpenseStats;
  loading?: boolean;
}

/**
 * The three figures above the expense list: what is still a draft, what is
 * waiting for somebody to approve it, and what has been approved but not paid
 * back yet.
 *
 * The first two are counts and the third is money, because that is what the
 * server can say about each: a count of drafts is a count, and what is owed
 * is an amount — one line per currency, never a sum that would be in neither
 * (design §4).
 */
export const StatusStrip = ({ stats, loading = false }: StatusStripProps) => {
  const { t, formatters } = useI18n("expenses");
  const count = (value: number) => (value === 1 ? t("oneExpense") : t("countOfExpenses", { count: value }));
  const owed = stats?.unreimbursed ?? [];

  return (
    <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm" data-testid="expenses-strip">
      <Box data-testid="expenses-drafts">
        <KpiCard
          label={t("inDrafts")}
          value={stats ? stats.draft : ""}
          hint={stats && count(stats.draft)}
          loading={loading}
        />
      </Box>
      <Box data-testid="expenses-submitted">
        <KpiCard
          label={t("awaitingApproval")}
          value={stats ? stats.submitted : ""}
          hint={stats && count(stats.submitted)}
          loading={loading}
        />
      </Box>
      <Box data-testid="expenses-owed">
        <KpiCard
          label={t("owedToYou")}
          loading={loading}
          value={
            owed.length === 0 ? (
              <Text span size="xl" fw={700} c="dimmed">
                {t("nothingOwed")}
              </Text>
            ) : (
              <Stack gap={0}>
                {owed.map((line) => (
                  <Text key={line.currency} size="xl" fw={700}>
                    {formatters.formatCurrency(line.amount, line.currency)}
                  </Text>
                ))}
              </Stack>
            )
          }
        />
      </Box>
    </SimpleGrid>
  );
};
