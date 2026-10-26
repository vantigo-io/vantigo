import { Alert, Anchor, Card, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { planningMetaQueryOptions } from "../api/meta";
import { unbookedQueryOptions } from "../api/unbooked";
import { usePlanningFormat } from "../lib/format";
import { personTimelineLinkOptions } from "../lib/routes";

/**
 * The unbooked list (design S13, §7): the people whose confirmed load over
 * the horizon is under the threshold, those free now first, then by the day
 * they roll off (amendment 9). A view, not a digest. `planning:view`'s.
 */
export const UnbookedPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const unbooked = useQuery(unbookedQueryOptions());
  const meta = useQuery(planningMetaQueryOptions());
  const settings = meta.data?.settings;
  const horizon = unbooked.data?.horizon;

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("unbookedTitle")}
        description={
          horizon
            ? settings
              ? t("unbookedDescriptionWithThreshold", {
                  range: format.range(horizon.from, horizon.to),
                  threshold: format.percent(settings.unbookedThresholdPercent),
                })
              : t("unbookedDescription", { range: format.range(horizon.from, horizon.to) })
            : undefined
        }
      />
      {meta.data?.canView && meta.data.workforceVisible === false ? (
        <Card withBorder padding="lg" radius="md">
          <EmptyState title={t("workforceHiddenTitle")} description={t("workforceHiddenDescription")} />
        </Card>
      ) : (
        <Card withBorder padding="sm" radius="md">
          {unbooked.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadUnbooked")}>
              {unbooked.error.message}
            </Alert>
          )}
          {unbooked.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
          {unbooked.data && unbooked.data.people.length === 0 && (
            <EmptyState title={t("nobodyUnbooked")} description={t("nobodyUnbookedDescription")} />
          )}
          {unbooked.data && unbooked.data.people.length > 0 && (
            <Table.ScrollContainer minWidth={560}>
              <Table aria-label={t("unbookedTitle")} verticalSpacing="xs" highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("person")}</Table.Th>
                    <Table.Th>{t("department")}</Table.Th>
                    <Table.Th ta="right">{t("loadOverHorizon")}</Table.Th>
                    <Table.Th ta="right">{t("freeHours")}</Table.Th>
                    <Table.Th>{t("rollOff")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {unbooked.data.people.map((entry) => (
                    <Table.Tr key={entry.person.id}>
                      <Table.Td>
                        <Anchor
                          size="sm"
                          fw={500}
                          renderRoot={(props) => <Link {...personTimelineLinkOptions(entry.person.id)} {...props} />}
                        >
                          {entry.person.displayName}
                        </Anchor>
                        <Text size="xs" c="dimmed">
                          {entry.person.employeeNumber}
                        </Text>
                      </Table.Td>
                      <Table.Td>{entry.person.department?.name ?? ""}</Table.Td>
                      <Table.Td ta="right">{format.percent(entry.loadPercent)}</Table.Td>
                      <Table.Td ta="right">{format.hours(entry.freeHours)}</Table.Td>
                      <Table.Td>{entry.rollOff ? format.date(entry.rollOff) : t("freeNow")}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Card>
      )}
    </Stack>
  );
};
