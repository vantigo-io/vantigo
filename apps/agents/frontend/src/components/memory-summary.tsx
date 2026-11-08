import { Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { formatNumber, useI18n } from "@vantigo/frontend-shell";
import { memoryCountsQueryOptions } from "../api/agents";
import type { Agent } from "../api/types";

/**
 * What the agent remembers (V27), as counts: the notes themselves are shown on each customer's page, where a
 * person can delete them, and are kept for the retention like the runs are.
 */
export const MemorySummary = ({ agent }: { agent: Agent }) => {
  const { t } = useI18n("agents");
  const query = useQuery(memoryCountsQueryOptions(agent.id));
  if (query.isPending || query.isError || !query.data) return null;
  const { customerNotes, conversationNotes, customers } = query.data;
  return (
    <Stack gap={2} data-testid="memory-summary">
      <Text fw={600}>{t("memoryTitle", { agent: agent.name })}</Text>
      {customerNotes + conversationNotes === 0 ? (
        <Text size="sm" c="dimmed">
          {t("memoryNone", { agent: agent.name })}
        </Text>
      ) : (
        <Text size="sm">
          {t("memoryCounts", {
            customerNotes: formatNumber(customerNotes),
            customers: formatNumber(customers),
            conversationNotes: formatNumber(conversationNotes),
          })}
        </Text>
      )}
      <Text size="xs" c="dimmed">
        {t("memoryHelp")}
      </Text>
    </Stack>
  );
};
