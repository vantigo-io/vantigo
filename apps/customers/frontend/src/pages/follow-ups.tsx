import { Alert, Anchor, Badge, Button, Card, Group, Pagination, Select, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
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
import { markFollowUpDone } from "../api/timeline";
import { isOverdue } from "../lib/follow-up-dates";
import { formatDateOnly } from "../lib/format-date-only";
import "../i18n";

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
  const { data, isPending, isError, error, refetch } = useQuery(followUpsQueryOptions(params));

  const go = (next: Partial<FollowUpsSearch>) => navigate({ search: { ...search, page: 1, ...next } });

  const reload = () => queryClient.invalidateQueries({ queryKey: ["customers"] });
  const tick = useMutation({
    mutationFn: (row: FollowUpRow) => markFollowUpDone(row.customerId, row.entryId),
    onSuccess: reload,
    onError: (error: Error) => {
      // The failure re-reads too, the way the timeline card's own tick does: the
      // likeliest failure is an entry somebody else deleted or un-followed-up,
      // and a row left on screen only offers the same doomed button again.
      reload();
      notifications.show({ color: "red", title: t("couldNotUpdateFollowUp"), message: error.message });
    },
  });

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
            // The message, not just the headline: the nav admits anybody with
            // `customers:timeline-view`, while this endpoint also wants
            // `customers:view` for the customer names it puts in every row — so
            // the likeliest failure here is a 403 whose sentence says exactly
            // that, and "Could not load follow-ups" alone would read as a glitch
            // worth reloading for. The Alert-with-detail shape is the customer
            // list's own (`customers.index.tsx`); Retry is what the timeline
            // card's error state offers, for the failure that IS transient.
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoadFollowUps")}>
              <Stack align="flex-start" gap="xs">
                <Text size="sm">{error.message}</Text>
                <Button variant="light" size="compact-sm" onClick={() => refetch()}>
                  {t("tryAgain")}
                </Button>
              </Stack>
            </Alert>
          ) : data.data.length === 0 ? (
            <EmptyState title={t("noFollowUps")} />
          ) : (
            // Scrollable at a narrow width rather than squeezed: five columns,
            // one of them free-text, is the customer list's situation too.
            <Table.ScrollContainer minWidth={900}>
              <Table highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("dueOn")}</Table.Th>
                    <Table.Th>{t("customer")}</Table.Th>
                    <Table.Th>{t("description")}</Table.Th>
                    <Table.Th>{t("followUpAssignee")}</Table.Th>
                    {/* No column at all for a reader: the only thing in it is the
                        tick, and a header over an empty column reads as a column
                        that failed to load. */}
                    {canManageTimeline && <Table.Th>{t("actions")}</Table.Th>}
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
                            {formatDateOnly(formatters, row.followUp.dueOn)}
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
                        <Anchor
                          renderRoot={(props) => <Link to={`/customers/${row.customerId}` as never} {...props} />}
                        >
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
                      {canManageTimeline && (
                        <Table.Td>
                          {!row.followUp.doneAt && (
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
                      )}
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
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
