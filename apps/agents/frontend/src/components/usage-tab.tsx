import { Alert, Button, Group, Loader, Progress, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { formatDate, formatNumber, useI18n } from "@vantigo/frontend-shell";
import { usageQueryOptions } from "../api/agents";
import type { Agent } from "../api/types";
import { budgetShare, totalsOf } from "../lib/usage";
import { MemorySummary } from "./memory-summary";

const Figure = ({ label, value }: { label: string; value: string }) => (
  <Stack gap={0}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text fw={600} size="lg">
      {value}
    </Text>
  </Stack>
);

/** The agent's last 30 days (V24): runs, tokens, and what it sent, held and had discarded; today against the daily budget. */
export const UsageTab = ({ agent }: { agent: Agent }) => {
  const { t } = useI18n("agents");
  const query = useQuery(usageQueryOptions(agent.id, 30));
  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Alert color="red">
        <Group>
          <Text size="sm">{t("usageLoadFailed")}</Text>
          <Button size="xs" variant="light" onClick={() => void query.refetch()}>
            {t("tryAgain")}
          </Button>
        </Group>
      </Alert>
    );
  const { days, dailyTokenBudget } = query.data;
  const totals = totalsOf(days);
  const today = days.at(-1);
  const share = budgetShare(today, dailyTokenBudget);
  // The newest day first; days with nothing are left out of the table, the totals hold them anyway.
  const rows = days.filter((day) => day.runs > 0 || day.sent > 0 || day.held > 0 || day.discarded > 0).reverse();
  return (
    <Stack gap="md">
      <MemorySummary agent={agent} />
      <SimpleGrid cols={{ base: 2, sm: 5 }} data-testid="usage-totals">
        <Figure label={t("usageRuns")} value={formatNumber(totals.runs)} />
        <Figure label={t("usageTokens")} value={formatNumber(totals.tokens)} />
        <Figure label={t("usageSent")} value={formatNumber(totals.sent)} />
        <Figure label={t("usageHeld")} value={formatNumber(totals.held)} />
        <Figure label={t("usageDiscarded")} value={formatNumber(totals.discarded)} />
      </SimpleGrid>
      {dailyTokenBudget ? (
        <Stack gap={4} data-testid="usage-budget">
          <Text size="sm">
            {t("usageBudgetToday", {
              used: formatNumber((today?.inputTokens ?? 0) + (today?.outputTokens ?? 0)),
              budget: formatNumber(dailyTokenBudget),
            })}
          </Text>
          <Progress
            value={Math.min(100, (share ?? 0) * 100)}
            color={(share ?? 0) >= 1 ? "red" : "blue"}
            aria-label={t("usageBudgetLabel")}
          />
        </Stack>
      ) : (
        <Text size="sm" c="dimmed">
          {t("usageNoBudget")}
        </Text>
      )}
      {rows.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("usageEmpty")}
        </Text>
      ) : (
        <Table striped data-testid="usage-days">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("usageDay")}</Table.Th>
              <Table.Th ta="right">{t("usageRuns")}</Table.Th>
              <Table.Th ta="right">{t("usageTokens")}</Table.Th>
              <Table.Th ta="right">{t("usageSent")}</Table.Th>
              <Table.Th ta="right">{t("usageHeld")}</Table.Th>
              <Table.Th ta="right">{t("usageDiscarded")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((day) => (
              <Table.Tr key={day.day}>
                <Table.Td>{formatDate(`${day.day}T12:00:00`, { dateStyle: "medium" })}</Table.Td>
                <Table.Td ta="right">{formatNumber(day.runs)}</Table.Td>
                <Table.Td ta="right">{formatNumber(day.inputTokens + day.outputTokens)}</Table.Td>
                <Table.Td ta="right">{formatNumber(day.sent)}</Table.Td>
                <Table.Td ta="right">{formatNumber(day.held)}</Table.Td>
                <Table.Td ta="right">{formatNumber(day.discarded)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  );
};
