import { Anchor, Badge, Button, Card, Group, Pagination, Select, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import {
  FOLLOW_UP_ASSIGNEES,
  FOLLOW_UP_STATES,
  type FollowUpAssigneeFilter,
  type FollowUpRow,
  type FollowUpState,
  followUpsListParams,
  followUpsQueryOptions,
} from "../api/follow-ups";
import { markFollowUpDone, type TimelineFollowUp } from "../api/timeline";
import "../i18n";

/** Open and past its due date, in UTC — the same calendar the server compares in. */
const utcToday = () => new Date().toISOString().slice(0, 10);
const isOverdue = (followUp: TimelineFollowUp) => !followUp.doneAt && followUp.dueOn < utcToday();

interface FollowUpsSearch {
  page: number;
  assignee: FollowUpAssigneeFilter;
  state: FollowUpState;
  customerId?: number;
}

/**
 * "What is on my plate" (follow-ups design D3): every follow-up the caller
 * asked for, across customers, oldest due date first.
 *
 * Both filters live in the URL rather than in component state, which is what
 * makes a filtered list a link somebody can send — the customer list's own
 * rule. `canManageTimeline` comes from the host's `customers:timeline-manage`
 * check; without it the rows are still readable and the Done tick is not there.
 */
export const FollowUpsPage = ({ canManageTimeline }: { canManageTimeline?: boolean }) => {
  const { t, formatters } = useI18n("customers");
  const search = useSearch({ strict: false }) as FollowUpsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const params = followUpsListParams(search);
  const { data, isPending, isError } = useQuery(followUpsQueryOptions(params));

  const go = (next: Partial<FollowUpsSearch>) => navigate({ search: { ...search, page: 1, ...next } });

  const tick = useMutation({
    mutationFn: (row: FollowUpRow) => markFollowUpDone(row.customerId, row.entryId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["customers"] }),
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("couldNotUpdateFollowUp"), message: error.message }),
  });

  const formatDateOnly = (date: string) =>
    formatters.formatDate(`${date}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" });

  return (
    <Stack gap="lg">
      <PageHeader title={t("followUps")} description={t("followUpsDescription")} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group gap="sm" wrap="wrap">
            <Select
              label={t("followUpAssigneeFilter")}
              data={FOLLOW_UP_ASSIGNEES.map((value) => ({ value, label: t(`followUpAssignee_${value}`) }))}
              value={params.assignee}
              onChange={(value) => value && go({ assignee: value as FollowUpAssigneeFilter })}
              allowDeselect={false}
            />
            <Select
              label={t("followUpStateFilter")}
              data={FOLLOW_UP_STATES.map((value) => ({ value, label: t(`followUpState_${value}`) }))}
              value={params.state}
              onChange={(value) => value && go({ state: value as FollowUpState })}
              allowDeselect={false}
            />
          </Group>
          {isPending ? (
            <ContentSkeleton rows={4} />
          ) : isError ? (
            <Text c="red">{t("couldNotLoadFollowUps")}</Text>
          ) : data.data.length === 0 ? (
            <EmptyState title={t("noFollowUps")} />
          ) : (
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("dueOn")}</Table.Th>
                  <Table.Th>{t("customer")}</Table.Th>
                  <Table.Th>{t("description")}</Table.Th>
                  <Table.Th>{t("followUpAssignee")}</Table.Th>
                  <Table.Th>{t("actions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.data.map((row) => (
                  <Table.Tr key={`${row.customerId}-${row.entryId}`}>
                    <Table.Td>
                      <Group gap="xs">
                        <Text
                          size="sm"
                          c={row.followUp.doneAt ? "dimmed" : isOverdue(row.followUp) ? "red" : undefined}
                        >
                          {formatDateOnly(row.followUp.dueOn)}
                        </Text>
                        {isOverdue(row.followUp) && (
                          <Badge size="xs" color="red" variant="light">
                            {t("followUpOverdue")}
                          </Badge>
                        )}
                        {row.followUp.doneAt && (
                          <Badge size="xs" color="gray" variant="light">
                            {t("followUpDone")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Anchor renderRoot={(props) => <Link to={`/customers/${row.customerId}` as never} {...props} />}>
                        {row.customerName}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" lineClamp={2}>
                        {row.note || t("noAdditionalDetails")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c={row.followUp.assignee ? undefined : "dimmed"}>
                        {row.followUp.assignee
                          ? `${row.followUp.assignee.displayName}${row.followUp.assignee.active ? "" : ` (${t("inactiveUser")})`}`
                          : t("followUpUnassigned")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      {canManageTimeline && !row.followUp.doneAt && (
                        <Button
                          size="compact-xs"
                          variant="light"
                          loading={tick.isPending && tick.variables?.entryId === row.entryId}
                          onClick={() => tick.mutate(row)}
                        >
                          {t("markDone")}
                        </Button>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
          {data && data.pagination.totalPages > 1 && (
            <Group justify="center">
              <Pagination
                total={data.pagination.totalPages}
                value={params.page}
                onChange={(page) => navigate({ search: { ...search, page } })}
              />
            </Group>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
