import { Alert, Button, Group, Loader, SegmentedControl, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { usageQueryOptions } from "../api/assistant";
import "../i18n";

const RANGES = [7, 30, 90] as const;
type Range = (typeof RANGES)[number];

/** The calendar date `days - 1` days before today in the browser's zone, as `YYYY-MM-DD`. */
const fromDate = (days: number, now = new Date()) => {
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate() - (days - 1));
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${start.getFullYear()}-${pad(start.getMonth() + 1)}-${pad(start.getDate())}`;
};

/**
 * The Owner's count view (design S9): per person and business day, the
 * conversations they began, the runs, the model tokens and the changes that
 * were confirmed and that failed. Counts only — the server holds no content for
 * it to show, and no title.
 */
export const UsageTable = () => {
  const { t, i18n } = useI18n("assistant");
  const [range, setRange] = useState<Range>(30);
  const query = useQuery(usageQueryOptions(fromDate(range)));
  const number = new Intl.NumberFormat(i18n.language);

  return (
    <PageSection data-testid="usage">
      <Stack gap="md">
        <Stack gap={4}>
          <Title order={4}>{t("usageTitle")}</Title>
          <Text size="sm" c="dimmed">
            {t("usageIntro")}
          </Text>
        </Stack>
        <SegmentedControl
          aria-label={t("usageTitle")}
          value={String(range)}
          onChange={(value) => setRange(Number(value) as Range)}
          data={RANGES.map((days) => ({ value: String(days), label: t(`usageRange${days}`) }))}
        />
        {query.isPending && <Loader size="sm" />}
        {query.isError && (
          <Alert color="red">
            <Group>
              <Text size="sm">{t("usageFailedToLoad")}</Text>
              <Button size="xs" variant="light" onClick={() => void query.refetch()}>
                {t("tryAgain")}
              </Button>
            </Group>
          </Alert>
        )}
        {query.isSuccess && query.data.rows.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("usageEmpty")}
          </Text>
        )}
        {query.isSuccess && query.data.rows.length > 0 && (
          <Table.ScrollContainer minWidth={640}>
            <Table fz="sm" verticalSpacing={4} highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("usagePerson")}</Table.Th>
                  <Table.Th>{t("usageDay")}</Table.Th>
                  <Table.Th ta="right">{t("usageConversations")}</Table.Th>
                  <Table.Th ta="right">{t("usageRuns")}</Table.Th>
                  <Table.Th ta="right">{t("usageTokensIn")}</Table.Th>
                  <Table.Th ta="right">{t("usageTokensOut")}</Table.Th>
                  <Table.Th ta="right">{t("usageConfirmed")}</Table.Th>
                  <Table.Th ta="right">{t("usageFailed")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {query.data.rows.map((row) => (
                  <Table.Tr key={`${row.userId}-${row.day}`}>
                    <Table.Td>{row.name || "—"}</Table.Td>
                    <Table.Td>{row.day}</Table.Td>
                    <Table.Td ta="right">{number.format(row.conversations)}</Table.Td>
                    <Table.Td ta="right">{number.format(row.runs)}</Table.Td>
                    <Table.Td ta="right">{number.format(row.inputTokens)}</Table.Td>
                    <Table.Td ta="right">{number.format(row.outputTokens)}</Table.Td>
                    <Table.Td ta="right">{number.format(row.confirmedChanges)}</Table.Td>
                    <Table.Td ta="right">{number.format(row.failedChanges)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </PageSection>
  );
};
