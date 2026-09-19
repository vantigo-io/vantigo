import { Alert, Anchor, Badge, Box, Card, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconClock } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { NotFoundError } from "../api/request";
import { projectTimeSummaryQueryOptions } from "../api/stats";
import "../i18n";
import { useHoursFormat } from "../lib/hours";
import { timeEntryStatusColor, timeEntryStatuses, timeEntryStatusLabelKey } from "../lib/status";

export interface ProjectTimePanelProps {
  projectId: number;
}

/**
 * The project page's Time tab (design §8): the hours logged on one project,
 * by status, by billing line and by person, and what they bill when the
 * caller may see the project's money (D8). The summary is a bare 404 for a
 * project this caller cannot see, which reads as nothing to show rather than
 * as a failure — the project page has already said what it can.
 */
export const ProjectTimePanel = ({ projectId }: ProjectTimePanelProps) => {
  const { t, formatters } = useI18n("time");
  const hours = useHoursFormat();
  const Link = useShellLink();
  const { data, isPending, isError, error } = useQuery(projectTimeSummaryQueryOptions(projectId));
  const missing = error instanceof NotFoundError;

  if (isError) {
    // A bare 404 is a project this caller cannot see, or one with no time at
    // all: nothing to show, which is not a failure.
    return missing ? (
      <Box mt="md">
        <EmptyState icon={IconClock} title={t("noProjectTime")} description={t("noProjectTimeDescription")} />
      </Box>
    ) : (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProjectTime")} mt="md">
        {error.message}
      </Alert>
    );
  }
  if (isPending || !data) {
    return (
      <Box mt="md">
        <ContentSkeleton rows={4} rowHeight={48} />
      </Box>
    );
  }

  const logTime = "/time";
  const byStatus = data.hours.byStatus;
  const billing = data.billing;

  return (
    <Stack gap="md" mt="md">
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group justify="space-between" wrap="wrap">
            <Stack gap={2}>
              <Text fw={600} component="h3">
                {t("projectTime")}
              </Text>
              <Text size="sm" c="dimmed">
                {t("projectTimeDescription")}
              </Text>
            </Stack>
            {Link ? (
              <Anchor size="sm" renderRoot={(props) => <Link to={logTime} {...props} />}>
                {t("logTime")}
              </Anchor>
            ) : (
              <Anchor size="sm" href={logTime}>
                {t("logTime")}
              </Anchor>
            )}
          </Group>

          <Group gap="xl" wrap="wrap">
            <Stack gap={0}>
              <Text size="xs" c="dimmed">
                {t("totalHours")}
              </Text>
              <Text fw={700} size="xl" data-testid="project-time-total">
                {t("hoursShort", { hours: hours.display(data.hours.total) })}
              </Text>
            </Stack>
            {/* A status with nothing in it is left out: a row of zeroes says less than the statuses in play. */}
            <Group gap="xs" wrap="wrap" data-testid="project-time-statuses">
              {timeEntryStatuses
                .filter((status) => byStatus[status] > 0)
                .map((status) => (
                  <Badge key={status} variant="light" color={timeEntryStatusColor(status)}>
                    {t(timeEntryStatusLabelKey(status))}
                    {": "}
                    {hours.display(byStatus[status])}
                  </Badge>
                ))}
            </Group>
          </Group>

          {billing && (
            <Group gap="xl" wrap="wrap" data-testid="project-time-billing">
              <Stack gap={0}>
                <Text size="xs" c="dimmed">
                  {t("billAmount")}
                </Text>
                <Text fw={700}>
                  {billing.currency
                    ? formatters.formatCurrency(billing.amount, billing.currency)
                    : formatters.formatNumber(billing.amount, { maximumFractionDigits: 2 })}
                </Text>
              </Stack>
              {billing.unpricedHours > 0 && (
                <Text size="sm" c="dimmed">
                  {t("unpricedHours", { hours: hours.display(billing.unpricedHours) })}
                </Text>
              )}
            </Group>
          )}
        </Stack>
      </Card>

      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
        <Card withBorder padding="lg" radius="md">
          <Stack gap="sm">
            <Text fw={600} component="h3">
              {t("byLine")}
            </Text>
            {data.hours.byLine.length === 0 ? (
              <EmptyState title={t("noProjectTime")} size="sm" />
            ) : (
              <Table verticalSpacing="xs" data-testid="project-time-by-line">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("billingLine")}</Table.Th>
                    <Table.Th ta="right">{t("hours")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {data.hours.byLine.map((line) => (
                    <Table.Tr key={line.billingLineId ?? "none"}>
                      <Table.Td>{line.trackableCode ?? line.billingLineCode ?? t("noBillingLine")}</Table.Td>
                      <Table.Td ta="right">{hours.display(line.hours)}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </Stack>
        </Card>

        <Card withBorder padding="lg" radius="md">
          <Stack gap="sm">
            <Text fw={600} component="h3">
              {t("byPerson")}
            </Text>
            {data.hours.byPerson.length === 0 ? (
              <EmptyState title={t("noProjectTime")} size="sm" />
            ) : (
              <Table verticalSpacing="xs" data-testid="project-time-by-person">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("person")}</Table.Th>
                    <Table.Th ta="right">{t("hours")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {data.hours.byPerson.map((person) => (
                    <Table.Tr key={person.userId}>
                      <Table.Td>{person.displayName}</Table.Td>
                      <Table.Td ta="right">{hours.display(person.hours)}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </Stack>
        </Card>
      </SimpleGrid>
    </Stack>
  );
};
